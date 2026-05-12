package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// knownLmaoboxBuiltinGlobals are special userdata/function objects exposed by the
// Lmaobox runtime that do NOT respond to Lua type() checks, field lookups, or
// existence probes. They must be called directly — no existence-check guards.
// NOTE: pcall() does NOT protect against Lmaobox API crashes; it only catches Lua
// errors (error()/assert). Never wrap Lmaobox API calls in pcall to "validate" them.
var knownLmaoboxBuiltinGlobals = map[string]bool{
	"http":      true,
	"entities":  true,
	"callbacks": true,
	"engine":    true,
	"draw":      true,
	"input":     true,
	"profiler":  true,
	"warp":      true,
	"math":      true, // Lua stdlib but should not be checked
	"table":     true, // Lua stdlib but should not be checked
	"string":    true, // Lua stdlib but should not be checked
}

type LboxMutationPolicy struct {
	RequireDepthZeroRegister            bool
	RequireDepthZeroUnregister          bool
	RequireKillSwitchOrder              bool
	ForbidRuntimeUnregister             bool
	ForbidCollectGarbage                bool
	ForbidRequireInFunction             bool
	ForbidGlobalTable                   bool
	ForbidCreateFontInFunction          bool
	ForbidLegacyBitLibrary              bool
	ForbidDeprecatedCallbacks           bool
	ForbidAllowListener                 bool
	ForbidForceFullUpdate               bool
	RequireDrawTextFontSetup            bool
	RequireDrawColorSetup               bool
	ForbidWarpOutsideCreateMove         bool
	ForbidSuspiciousSetupBones          bool
	ForbidIpairsOnFindByClass           bool
	RequireIsValidOnCachedEntity        bool
	ForbidBuiltinGlobalValidationChecks bool
}

type luaPolicyViolation struct {
	Line    int
	Message string
}

type luaPolicyBlockKind int

type luaFontBindingKind int

const (
	luaBlockGeneric luaPolicyBlockKind = iota
	luaBlockFunction
	luaBlockRepeat
)

const (
	luaFontBindingUnknown luaFontBindingKind = iota
	luaFontBindingValid
	luaFontBindingInvalid
)

type luaFontBinding struct {
	Line int
	Kind luaFontBindingKind
}

type luaDrawState struct {
	hasColor bool
	hasFont  bool
}

type luaDrawAnalysisContext struct {
	policy       LboxMutationPolicy
	fontBindings map[string][]luaFontBinding
	functions    map[string][]luaToken
	callerCounts map[string]int
	visiting     map[string]bool
}

var defaultLboxMutationPolicy = LboxMutationPolicy{
	RequireDepthZeroRegister:            true,
	RequireDepthZeroUnregister:          true,
	RequireKillSwitchOrder:              true,
	ForbidRuntimeUnregister:             true,
	ForbidCollectGarbage:                true,
	ForbidRequireInFunction:             true,
	ForbidGlobalTable:                   true,
	ForbidCreateFontInFunction:          true,
	ForbidLegacyBitLibrary:              true,
	ForbidDeprecatedCallbacks:           true,
	ForbidAllowListener:                 true,
	ForbidForceFullUpdate:               true,
	RequireDrawTextFontSetup:            true,
	RequireDrawColorSetup:               true,
	ForbidWarpOutsideCreateMove:         true,
	ForbidSuspiciousSetupBones:          true,
	ForbidIpairsOnFindByClass:           true,
	RequireIsValidOnCachedEntity:        true,
	ForbidBuiltinGlobalValidationChecks: true,
}

// bundleMutationPolicy is used for the final validation pass on the merged
// bundle file.  It is identical to defaultLboxMutationPolicy except that
// ForbidRequireInFunction is disabled: the bundler wraps each module inside a
// closure, so any top-level require() calls for global (non-bundled) modules
// appear to the policy scanner as if they are inside a function -- which is
// expected and harmless in the bundle context.  All other rules remain active.
var bundleMutationPolicy = func() LboxMutationPolicy {
	p := defaultLboxMutationPolicy
	p.ForbidRequireInFunction = false // module wrappers make top-level requires appear in a function
	return p
}()

func checkLuaCallbackMutationPolicy(filePath string, policy LboxMutationPolicy) ([]luaPolicyViolation, error) {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file for policy scan: %v", err)
	}
	content := string(contentBytes)

	tokens, err := tokenizeLua(content)
	if err != nil {
		return nil, err
	}

	violations := make([]luaPolicyViolation, 0)
	functionDepthByLine := buildFunctionDepthByLine(tokens)
	unregisteredAtDepthZero := make(map[string]bool)

	// Identify callback handler functions (those passed to callbacks.Register)
	callbackHandlers := identifyCallbackHandlerFunctions(tokens)
	callbackHandlerRanges := buildCallbackHandlerLineRanges(tokens, callbackHandlers)

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		if method, args, endIndex, ok := extractCallbacksCall(tokens, i); ok {
			line := tok.Line
			lineDepth := functionDepthByLine[line]
			eventName := stringArgValue(args, 0)
			uniqueID := stringArgValue(args, 1)
			isInCallbackHandler := isLineInCallbackHandler(line, callbackHandlerRanges)

			if strings.EqualFold(method, "register") {
				if policy.ForbidDeprecatedCallbacks && strings.EqualFold(eventName, "PostPropUpdate") {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage — use FrameStageNotify instead"})
				}
				if policy.RequireDepthZeroRegister && lineDepth > 0 && isInCallbackHandler {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: callbacks.Register inside a callback handler function is forbidden — register all callbacks at module load time, never from within a running callback"})
				}
				if policy.RequireKillSwitchOrder && lineDepth == 0 && eventName != "" {
					killSwitchKeyExact := strings.ToLower(eventName + "|" + uniqueID)
					killSwitchKeyEvent := strings.ToLower(eventName + "|")
					hasExactMatch := unregisteredAtDepthZero[killSwitchKeyExact]
					hasEventMatch := unregisteredAtDepthZero[killSwitchKeyEvent]
					if uniqueID != "" && !hasExactMatch && !hasEventMatch {
						violations = append(violations, luaPolicyViolation{Line: line, Message: fmt.Sprintf("CRITICAL: Kill-Switch violation for id '%s' on event '%s': callbacks.Unregister must appear before callbacks.Register at depth 0", uniqueID, eventName)})
					}
				}
			}

			if strings.EqualFold(method, "unregister") {
				if policy.ForbidDeprecatedCallbacks && strings.EqualFold(eventName, "PostPropUpdate") {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage — use FrameStageNotify instead"})
				}
				reportedRuntimeUnregister := false
				if policy.ForbidRuntimeUnregister && lineDepth > 0 {
					if isInCallbackHandler {
						violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: Illegal Unregister inside a callback handler function — runtime callback table mutation is forbidden"})
					} else {
						violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: Illegal Unregister inside function scope (including Unload). Runtime callback table mutation is forbidden"})
					}
					reportedRuntimeUnregister = true
				}
				if policy.RequireDepthZeroUnregister && lineDepth > 0 && !reportedRuntimeUnregister {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: callbacks.Unregister must be declared at depth 0 (global scope only)"})
				}
				if lineDepth == 0 && eventName != "" {
					if uniqueID != "" {
						unregisteredAtDepthZero[strings.ToLower(eventName+"|"+uniqueID)] = true
					} else {
						unregisteredAtDepthZero[strings.ToLower(eventName+"|")] = true
					}
				}
			}

			if policy.ForbidWarpOutsideCreateMove && strings.EqualFold(method, "register") {
				if callbackLine := findQualifiedCallLineInArgs(args, "warp", []string{"TriggerWarp", "TriggerDoubleTap", "TriggerCharge"}); callbackLine > 0 && !strings.EqualFold(eventName, "CreateMove") {
					violations = append(violations, luaPolicyViolation{Line: callbackLine, Message: "CRITICAL: warp trigger calls must be made from a CreateMove callback only"})
				}
			}

			// Scan nested callback calls inside argument tokens (e.g. inline
			// function handlers) while skipping the root call at i to avoid
			// duplicate reports.
			for j := i + 1; j < endIndex; j++ {
				t := tokens[j]
				if t.Kind != "ident" || !strings.EqualFold(t.Text, "callbacks") {
					continue
				}
				nestedMethod, nestedArgs, _, nestedOk := extractCallbacksCall(tokens, j)
				if !nestedOk {
					continue
				}

				nestedLine := t.Line
				nestedDepth := functionDepthByLine[nestedLine]
				nestedEvent := stringArgValue(nestedArgs, 0)

				if strings.EqualFold(nestedMethod, "register") {
					if policy.ForbidDeprecatedCallbacks && strings.EqualFold(nestedEvent, "PostPropUpdate") {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage — use FrameStageNotify instead"})
					}
					if policy.RequireDepthZeroRegister && nestedDepth > 0 {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: callbacks.Register inside a callback handler is forbidden — register all callbacks at module load time, never from within a running callback"})
					}
				}

				if strings.EqualFold(nestedMethod, "unregister") {
					if policy.ForbidDeprecatedCallbacks && strings.EqualFold(nestedEvent, "PostPropUpdate") {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage — use FrameStageNotify instead"})
					}
					reportedRuntimeUnregister := false
					if policy.ForbidRuntimeUnregister && nestedDepth > 0 {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: Illegal Unregister inside function scope (including Unload). Runtime callback table mutation is forbidden"})
						reportedRuntimeUnregister = true
					}
					if policy.RequireDepthZeroUnregister && nestedDepth > 0 && !reportedRuntimeUnregister {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: callbacks.Unregister must be declared at depth 0 (global scope only)"})
					}
				}
			}

			i = endIndex
			continue
		}
	}

	functionDepth2 := 0
	blockStack2 := make([]luaPolicyBlockKind, 0)
	pendingLoopDo2 := 0
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok.Kind == "keyword" {
			switch tok.Text {
			case "function":
				blockStack2 = append(blockStack2, luaBlockFunction)
				functionDepth2++
			case "for", "while":
				blockStack2 = append(blockStack2, luaBlockGeneric)
				pendingLoopDo2++
			case "do":
				if pendingLoopDo2 > 0 {
					pendingLoopDo2--
					break
				}
				blockStack2 = append(blockStack2, luaBlockGeneric)
			case "if":
				blockStack2 = append(blockStack2, luaBlockGeneric)
			case "repeat":
				blockStack2 = append(blockStack2, luaBlockRepeat)
			case "end":
				if len(blockStack2) > 0 {
					for j := len(blockStack2) - 1; j >= 0; j-- {
						if blockStack2[j] == luaBlockRepeat {
							continue
						}
						if blockStack2[j] == luaBlockFunction && functionDepth2 > 0 {
							functionDepth2--
						}
						blockStack2 = append(blockStack2[:j], blockStack2[j+1:]...)
						break
					}
				}
			case "until":
				if len(blockStack2) > 0 {
					for j := len(blockStack2) - 1; j >= 0; j-- {
						if blockStack2[j] == luaBlockRepeat {
							blockStack2 = append(blockStack2[:j], blockStack2[j+1:]...)
							break
						}
					}
				}
			}
			continue
		}
		if tok.Kind != "ident" {
			continue
		}
		if policy.ForbidCollectGarbage && tok.Text == "collectgarbage" && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: collectgarbage() is forbidden — it masks memory leaks instead of fixing them"})
		}
		if policy.ForbidRequireInFunction && tok.Text == "require" && functionDepth2 > 0 && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: require() inside a function causes memory leaks — move all require() calls to the top of the file"})
		}
		if policy.ForbidGlobalTable && tok.Text == "_G" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: _G usage is forbidden — use the G module for shared state instead"})
		}
		if policy.ForbidCreateFontInFunction && strings.EqualFold(tok.Text, "draw") && functionDepth2 > 0 && i+3 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "." && strings.EqualFold(tokens[i+2].Text, "CreateFont") && tokens[i+3].Kind == "symbol" && tokens[i+3].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: draw.CreateFont inside a function creates a permanent irremovable font on every call — move it to module-level (depth 0) and cache the handle"})
		}
		if policy.ForbidLegacyBitLibrary && strings.EqualFold(tok.Text, "bit") && i+3 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "." && tokens[i+2].Kind == "ident" && isLegacyBitMethod(tokens[i+2].Text) && tokens[i+3].Kind == "symbol" && tokens[i+3].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: fmt.Sprintf("CRITICAL: bit.%s() does not exist in Lua 5.4 — use native bitwise operators instead", tokens[i+2].Text)})
		}
		if policy.ForbidAllowListener && tok.Text == "AllowListener" && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: AllowListener() is deprecated and does nothing — remove the call"})
		}
		if policy.ForbidForceFullUpdate && tok.Text == "ForceFullUpdate" && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: ForceFullUpdate() is dangerous and should be avoided — it can lag or crash the game if misused"})
		}
		if policy.ForbidWarpOutsideCreateMove && strings.EqualFold(tok.Text, "warp") && functionDepth2 == 0 && i+3 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "." && tokens[i+2].Kind == "ident" && isWarpTriggerMethod(tokens[i+2].Text) && tokens[i+3].Kind == "symbol" && tokens[i+3].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: warp trigger calls must be made from a CreateMove callback only"})
		}
		if policy.ForbidBuiltinGlobalValidationChecks {
			violations = append(violations, findBuiltinGlobalValidationViolations(tokens, i)...)
		}
	}

	if policy.RequireDrawTextFontSetup || policy.RequireDrawColorSetup {
		violations = append(violations, findDrawStateViolations(content, tokens, policy)...)
	}
	if policy.ForbidSuspiciousSetupBones {
		violations = append(violations, findSuspiciousSetupBonesViolations(content, tokens)...)
	}
	if policy.ForbidIpairsOnFindByClass {
		violations = append(violations, findIpairsOnFindByClassViolations(content)...)
	}
	if policy.RequireIsValidOnCachedEntity {
		violations = append(violations, findCachedEntityWithoutIsValidViolations(content, tokens)...)
	}

	return dedupeLuaPolicyViolations(violations), nil
}

func findDrawStateViolations(content string, tokens []luaToken, policy LboxMutationPolicy) []luaPolicyViolation {
	violations := make([]luaPolicyViolation, 0)
	ctx := buildLuaDrawAnalysisContext(content, tokens, policy)

	for i := 0; i < len(tokens); i++ {
		method, args, _, ok := extractCallbacksCall(tokens, i)
		if !ok || !strings.EqualFold(method, "register") {
			continue
		}

		eventName := stringArgValue(args, 0)
		if !strings.EqualFold(eventName, "Draw") {
			continue
		}

		handlerArg := callbackHandlerArg(args)
		handlerTokens, ok := resolveCallbackHandlerTokens(tokens, handlerArg)
		if !ok {
			continue
		}

		_, handlerViolations := analyzeLuaDrawFunctionTokens(handlerTokens, "", luaDrawState{}, false, ctx)
		violations = append(violations, handlerViolations...)
	}

	return dedupeLuaPolicyViolations(violations)
}

func buildLuaDrawAnalysisContext(content string, tokens []luaToken, policy LboxMutationPolicy) *luaDrawAnalysisContext {
	functions := collectNamedLuaFunctions(tokens)
	callerCounts := collectLuaFunctionCallerCounts(tokens, functions)

	return &luaDrawAnalysisContext{
		policy:       policy,
		fontBindings: collectLuaFontBindings(content),
		functions:    functions,
		callerCounts: callerCounts,
		visiting:     make(map[string]bool),
	}
}

func collectNamedLuaFunctions(tokens []luaToken) map[string][]luaToken {
	functions := make(map[string][]luaToken)
	for i := 0; i+1 < len(tokens); i++ {
		functionIndex := -1
		nameIndex := -1

		if tokens[i].Kind == "keyword" && tokens[i].Text == "function" && i+1 < len(tokens) && tokens[i+1].Kind == "ident" {
			functionIndex = i
			nameIndex = i + 1
		} else if tokens[i].Kind == "keyword" && tokens[i].Text == "local" && i+2 < len(tokens) && tokens[i+1].Kind == "keyword" && tokens[i+1].Text == "function" && tokens[i+2].Kind == "ident" {
			functionIndex = i + 1
			nameIndex = i + 2
		}

		if functionIndex < 0 || nameIndex < 0 {
			continue
		}
		endIndex, ok := findFunctionEndIndex(tokens, functionIndex)
		if !ok {
			continue
		}
		name := strings.ToLower(tokens[nameIndex].Text)
		if _, exists := functions[name]; !exists {
			functions[name] = tokens[functionIndex : endIndex+1]
		}
		i = endIndex
	}
	return functions
}

func collectLuaFunctionCallerCounts(tokens []luaToken, functions map[string][]luaToken) map[string]int {
	callerCounts := make(map[string]int)
	for i := 0; i < len(tokens); i++ {
		method, args, _, ok := extractCallbacksCall(tokens, i)
		if !ok || !strings.EqualFold(method, "register") {
			continue
		}
		handlerArg := callbackHandlerArg(args)
		if len(handlerArg) == 1 && handlerArg[0].Kind == "ident" {
			name := strings.ToLower(handlerArg[0].Text)
			if _, exists := functions[name]; exists {
				callerCounts[name]++
			}
		}
	}

	for name, functionTokens := range functions {
		_ = name
		for _, calleeName := range collectDirectLuaFunctionCalls(functionTokens, functions) {
			callerCounts[calleeName]++
		}
	}

	return callerCounts
}

func collectDirectLuaFunctionCalls(tokens []luaToken, functions map[string][]luaToken) []string {
	callNames := make([]string, 0)
	bodyStart, bodyEnd, ok := findLuaFunctionBodyBounds(tokens)
	if !ok {
		return callNames
	}

	for i := bodyStart; i < bodyEnd; i++ {
		if tokens[i].Kind == "keyword" && tokens[i].Text == "function" {
			endIndex, ok := findFunctionEndIndex(tokens, i)
			if ok {
				i = endIndex
				continue
			}
		}
		calleeName, _, endIndex, ok := extractSimpleLuaFunctionCall(tokens, i, functions)
		if !ok {
			continue
		}
		callNames = append(callNames, calleeName)
		i = endIndex
	}

	return dedupeStrings(callNames)
}

func analyzeLuaDrawFunctionTokens(tokens []luaToken, functionName string, incomingState luaDrawState, resetAtEntry bool, ctx *luaDrawAnalysisContext) (luaDrawState, []luaPolicyViolation) {
	state := incomingState
	if resetAtEntry {
		state = luaDrawState{}
	}

	lowerName := strings.ToLower(functionName)
	if lowerName != "" {
		if ctx.visiting[lowerName] {
			return state, nil
		}
		ctx.visiting[lowerName] = true
		defer delete(ctx.visiting, lowerName)
	}

	violations := make([]luaPolicyViolation, 0)
	bodyStart, bodyEnd, ok := findLuaFunctionBodyBounds(tokens)
	if !ok {
		return state, violations
	}

	for i := bodyStart; i < bodyEnd; i++ {
		if tokens[i].Kind == "keyword" && tokens[i].Text == "function" {
			endIndex, ok := findFunctionEndIndex(tokens, i)
			if ok {
				i = endIndex
				continue
			}
		}

		if line, methodName, endIndex, ok := extractQualifiedDrawCall(tokens, i); ok {
			switch {
			case strings.EqualFold(methodName, "Color"):
				state.hasColor = true
			case strings.EqualFold(methodName, "SetFont"):
				bindingKind, knownInvalid := classifySetFontSource(tokens, i+3, ctx.fontBindings)
				if knownInvalid {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: draw.SetFont() uses a font handle that is statically known invalid here — bind it from draw.CreateFont(...) or a known font source before using draw.Text()"})
				} else {
					state.hasFont = true
				}
				_ = bindingKind
			default:
				if ctx.policy.RequireDrawTextFontSetup && isDrawTextMethod(methodName) && !state.hasFont {
					violations = append(violations, luaPolicyViolation{Line: line, Message: fmt.Sprintf("CRITICAL: draw.%s() requires draw.SetFont() earlier in the same Draw callback chain — font state is not reliable across frames and missing setup can render gibberish", methodName)})
				}
				if ctx.policy.RequireDrawColorSetup && isDrawRenderableMethod(methodName) && !state.hasColor {
					violations = append(violations, luaPolicyViolation{Line: line, Message: fmt.Sprintf("CRITICAL: draw.%s() requires draw.Color() earlier in the same Draw callback chain — render state resets like a palette and missing color leaves output invisible", methodName)})
				}
			}
			i = endIndex
			continue
		}

		calleeName, calleeTokens, endIndex, ok := extractSimpleLuaFunctionCall(tokens, i, ctx.functions)
		if !ok {
			continue
		}
		calleeState, calleeViolations := analyzeLuaDrawFunctionTokens(calleeTokens, calleeName, state, ctx.callerCounts[calleeName] > 1, ctx)
		state = calleeState
		violations = append(violations, calleeViolations...)
		i = endIndex
	}

	return state, dedupeLuaPolicyViolations(violations)
}

func findLuaFunctionBodyBounds(tokens []luaToken) (int, int, bool) {
	functionIndex := -1
	for i := 0; i < len(tokens); i++ {
		if tokens[i].Kind == "keyword" && tokens[i].Text == "function" {
			functionIndex = i
			break
		}
	}
	if functionIndex < 0 {
		return 0, 0, false
	}

	openParenIndex := -1
	for i := functionIndex + 1; i < len(tokens); i++ {
		if tokens[i].Kind == "symbol" && tokens[i].Text == "(" {
			openParenIndex = i
			break
		}
	}
	if openParenIndex < 0 {
		return 0, 0, false
	}

	_, closeParenIndex := collectLuaCallArgs(tokens, openParenIndex)
	if closeParenIndex <= openParenIndex {
		return 0, 0, false
	}
	return closeParenIndex + 1, len(tokens) - 1, true
}

func extractQualifiedDrawCall(tokens []luaToken, start int) (int, string, int, bool) {
	if start+3 >= len(tokens) {
		return 0, "", start, false
	}
	if !(tokens[start].Kind == "ident" && strings.EqualFold(tokens[start].Text, "draw")) {
		return 0, "", start, false
	}
	if tokens[start+1].Kind != "symbol" || tokens[start+1].Text != "." || tokens[start+2].Kind != "ident" || tokens[start+3].Kind != "symbol" || tokens[start+3].Text != "(" {
		return 0, "", start, false
	}
	_, endIndex := collectLuaCallArgs(tokens, start+3)
	if endIndex <= start+3 {
		return 0, "", start, false
	}
	return tokens[start].Line, tokens[start+2].Text, endIndex, true
}

func extractSimpleLuaFunctionCall(tokens []luaToken, start int, functions map[string][]luaToken) (string, []luaToken, int, bool) {
	if start+1 >= len(tokens) {
		return "", nil, start, false
	}
	if tokens[start].Kind != "ident" {
		return "", nil, start, false
	}
	if tokens[start+1].Kind != "symbol" || tokens[start+1].Text != "(" {
		return "", nil, start, false
	}
	if start > 0 {
		prev := tokens[start-1]
		if prev.Kind == "symbol" && prev.Text == "." {
			return "", nil, start, false
		}
		if prev.Kind == "keyword" && prev.Text == "function" {
			return "", nil, start, false
		}
	}

	name := strings.ToLower(tokens[start].Text)
	functionTokens, exists := functions[name]
	if !exists {
		return "", nil, start, false
	}
	_, endIndex := collectLuaCallArgs(tokens, start+1)
	if endIndex <= start+1 {
		return "", nil, start, false
	}
	return name, functionTokens, endIndex, true
}

func collectLuaFontBindings(content string) map[string][]luaFontBinding {
	lines := strings.Split(content, "\n")
	bindings := make(map[string][]luaFontBinding)
	validPatterns := []*regexp.Regexp{
		regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*draw\s*\.\s*CreateFont\s*\(`),
		regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*Fonts\s*\.\s*[A-Za-z_][A-Za-z0-9_]*\b`),
	}
	invalidPatterns := []*regexp.Regexp{
		regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*nil\s*$`),
		regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*false\s*$`),
		regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"[^"]*"\s*$`),
		regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*'[^']*'\s*$`),
	}

	for index, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}

		for _, pattern := range validPatterns {
			if matches := pattern.FindStringSubmatch(line); len(matches) == 2 {
				bindings[matches[1]] = append(bindings[matches[1]], luaFontBinding{Line: index + 1, Kind: luaFontBindingValid})
				goto nextLine
			}
		}
		for _, pattern := range invalidPatterns {
			if matches := pattern.FindStringSubmatch(line); len(matches) == 2 {
				bindings[matches[1]] = append(bindings[matches[1]], luaFontBinding{Line: index + 1, Kind: luaFontBindingInvalid})
				goto nextLine
			}
		}

	nextLine:
	}

	return bindings
}

func classifySetFontSource(tokens []luaToken, openParenIndex int, bindings map[string][]luaFontBinding) (luaFontBindingKind, bool) {
	args, _ := collectLuaCallArgs(tokens, openParenIndex)
	if len(args) == 0 {
		return luaFontBindingUnknown, false
	}
	arg := trimLuaArgTokens(args[0])
	if len(arg) == 0 {
		return luaFontBindingUnknown, false
	}

	if isCreateFontExpression(arg) || isKnownFontExpression(arg) {
		return luaFontBindingValid, false
	}
	if isStaticallyInvalidFontExpression(arg) {
		return luaFontBindingInvalid, true
	}
	if len(arg) == 1 && arg[0].Kind == "ident" {
		bindingKind := resolveLuaFontBinding(bindings, arg[0].Text, arg[0].Line)
		if bindingKind == luaFontBindingInvalid {
			return bindingKind, true
		}
		if bindingKind == luaFontBindingValid {
			return bindingKind, false
		}
	}

	return luaFontBindingUnknown, false
}

func resolveLuaFontBinding(bindings map[string][]luaFontBinding, name string, line int) luaFontBindingKind {
	entries := bindings[name]
	resolvedKind := luaFontBindingUnknown
	for _, entry := range entries {
		if entry.Line > line {
			break
		}
		resolvedKind = entry.Kind
	}
	return resolvedKind
}

func isCreateFontExpression(tokens []luaToken) bool {
	return tokenSliceStartsWithQualifiedCall(tokens, "draw", "CreateFont")
}

func isKnownFontExpression(tokens []luaToken) bool {
	if len(tokens) == 3 && tokens[0].Kind == "ident" && strings.EqualFold(tokens[0].Text, "Fonts") && tokens[1].Kind == "symbol" && tokens[1].Text == "." && tokens[2].Kind == "ident" {
		return true
	}
	return false
}

func isStaticallyInvalidFontExpression(tokens []luaToken) bool {
	if len(tokens) != 1 {
		return false
	}
	if tokens[0].Kind == "string" {
		return true
	}
	if tokens[0].Kind == "keyword" {
		return tokens[0].Text == "nil" || tokens[0].Text == "false"
	}
	return false
}

func tokenSliceStartsWithQualifiedCall(tokens []luaToken, root string, method string) bool {
	if len(tokens) < 4 {
		return false
	}
	return tokens[0].Kind == "ident" && strings.EqualFold(tokens[0].Text, root) &&
		tokens[1].Kind == "symbol" && tokens[1].Text == "." &&
		tokens[2].Kind == "ident" && strings.EqualFold(tokens[2].Text, method) &&
		tokens[3].Kind == "symbol" && tokens[3].Text == "("
}

func callbackHandlerArg(args [][]luaToken) []luaToken {
	if len(args) >= 3 {
		return trimLuaArgTokens(args[2])
	}
	if len(args) >= 2 {
		return trimLuaArgTokens(args[1])
	}
	return nil
}

func resolveCallbackHandlerTokens(tokens []luaToken, handlerArg []luaToken) ([]luaToken, bool) {
	if len(handlerArg) == 0 {
		return nil, false
	}
	if handlerArg[0].Kind == "keyword" && handlerArg[0].Text == "function" {
		return handlerArg, true
	}
	if len(handlerArg) == 1 && handlerArg[0].Kind == "ident" {
		return findNamedFunctionTokens(tokens, handlerArg[0].Text)
	}
	return nil, false
}

func findNamedFunctionTokens(tokens []luaToken, functionName string) ([]luaToken, bool) {
	for i := 0; i+2 < len(tokens); i++ {
		if tokens[i].Kind != "keyword" || tokens[i].Text != "function" {
			continue
		}
		if tokens[i+1].Kind != "ident" || !strings.EqualFold(tokens[i+1].Text, functionName) {
			continue
		}
		endIndex, ok := findFunctionEndIndex(tokens, i)
		if !ok {
			return nil, false
		}
		return tokens[i : endIndex+1], true
	}
	return nil, false
}

func findFunctionEndIndex(tokens []luaToken, functionIndex int) (int, bool) {
	if functionIndex < 0 || functionIndex >= len(tokens) {
		return 0, false
	}

	blockStack := []luaPolicyBlockKind{luaBlockFunction}
	pendingLoopDo := 0
	for i := functionIndex + 1; i < len(tokens); i++ {
		tok := tokens[i]
		if tok.Kind != "keyword" {
			continue
		}

		switch tok.Text {
		case "function":
			blockStack = append(blockStack, luaBlockFunction)
		case "for", "while":
			blockStack = append(blockStack, luaBlockGeneric)
			pendingLoopDo++
		case "do":
			if pendingLoopDo > 0 {
				pendingLoopDo--
				break
			}
			blockStack = append(blockStack, luaBlockGeneric)
		case "if":
			blockStack = append(blockStack, luaBlockGeneric)
		case "repeat":
			blockStack = append(blockStack, luaBlockRepeat)
		case "until":
			for j := len(blockStack) - 1; j >= 0; j-- {
				if blockStack[j] == luaBlockRepeat {
					blockStack = append(blockStack[:j], blockStack[j+1:]...)
					break
				}
			}
		case "end":
			for j := len(blockStack) - 1; j >= 0; j-- {
				if blockStack[j] == luaBlockRepeat {
					continue
				}
				endedBlock := blockStack[j]
				blockStack = append(blockStack[:j], blockStack[j+1:]...)
				if endedBlock == luaBlockFunction && len(blockStack) == 0 {
					return i, true
				}
				break
			}
		}
	}

	return 0, false
}

func isDrawTextMethod(name string) bool {
	switch strings.ToLower(name) {
	case "text", "textshadow":
		return true
	default:
		return false
	}
}

func isDrawRenderableMethod(name string) bool {
	switch strings.ToLower(name) {
	case "text", "textshadow", "line", "filledrect", "outlinedrect", "filledrectfade", "filledrectfastfade", "circle", "filledcircle", "coloredcircle", "texturedrect", "texturedpolygon":
		return true
	default:
		return false
	}
}

func hasQualifiedCall(tokens []luaToken, root string, method string) bool {
	return findQualifiedCallLine(tokens, root, []string{method}) > 0
}

func findQualifiedCallLine(tokens []luaToken, root string, methods []string) int {
	for i := 0; i+3 < len(tokens); i++ {
		if !(tokens[i].Kind == "ident" && strings.EqualFold(tokens[i].Text, root)) {
			continue
		}
		if tokens[i+1].Kind != "symbol" || tokens[i+1].Text != "." || tokens[i+2].Kind != "ident" {
			continue
		}
		matched := false
		for _, method := range methods {
			if strings.EqualFold(tokens[i+2].Text, method) {
				matched = true
				break
			}
		}
		if matched && tokens[i+3].Kind == "symbol" && tokens[i+3].Text == "(" {
			return tokens[i].Line
		}
	}
	return 0
}

func findQualifiedCallLineInArgs(args [][]luaToken, root string, methods []string) int {
	for _, arg := range args {
		if line := findQualifiedCallLine(arg, root, methods); line > 0 {
			return line
		}
	}
	return 0
}

func isLegacyBitMethod(name string) bool {
	switch strings.ToLower(name) {
	case "band", "bor", "bnot", "bxor", "lshift", "rshift", "arshift":
		return true
	default:
		return false
	}
}

func isWarpTriggerMethod(name string) bool {
	switch strings.ToLower(name) {
	case "triggerwarp", "triggerdoubletap", "triggercharge":
		return true
	default:
		return false
	}
}

func findSuspiciousSetupBonesViolations(content string, tokens []luaToken) []luaPolicyViolation {
	patterns := []struct {
		re      *regexp.Regexp
		message string
	}{
		{re: regexp.MustCompile(`(?m):SetupBones\s*\(\s*0\s*[,)]`), message: "CRITICAL: SetupBones with mask 0 returns no useful bones for hitbox work — use 0x7ff00"},
		{re: regexp.MustCompile(`(?m):SetupBones\s*\(\s*(?:255|0x0*ff)\s*[,)]`), message: "CRITICAL: SetupBones with mask 255 is too narrow for hitbox work — use 0x7ff00"},
	}

	violations := make([]luaPolicyViolation, 0)
	for _, pattern := range patterns {
		matches := pattern.re.FindAllStringIndex(content, -1)
		for _, match := range matches {
			violations = append(violations, luaPolicyViolation{Line: lineNumberForOffset(content, match[0]), Message: pattern.message})
		}
	}

	for i := 0; i+1 < len(tokens); i++ {
		if !(tokens[i].Kind == "ident" && strings.EqualFold(tokens[i].Text, "SetupBones")) {
			continue
		}
		if tokens[i+1].Kind != "symbol" || tokens[i+1].Text != "(" {
			continue
		}
		args, _ := collectLuaCallArgs(tokens, i+1)
		if len(args) == 0 {
			violations = append(violations, luaPolicyViolation{Line: tokens[i].Line, Message: "CRITICAL: SetupBones() should pass bone mask 0x7ff00 and globals.CurTime() — omitting both commonly returns incomplete or wrong data"})
			continue
		}
		if !argContainsQualifiedCall(args, "globals", "CurTime") {
			violations = append(violations, luaPolicyViolation{Line: tokens[i].Line, Message: "CRITICAL: SetupBones should use globals.CurTime() as the time argument — passing 0 or omitting time gives incorrect positions"})
		}
	}

	return dedupeLuaPolicyViolations(violations)
}

func argContainsQualifiedCall(args [][]luaToken, root string, method string) bool {
	return findQualifiedCallLineInArgs(args, root, []string{method}) > 0
}

func findBuiltinGlobalValidationViolations(tokens []luaToken, startIdx int) []luaPolicyViolation {
	violations := make([]luaPolicyViolation, 0)
	if startIdx >= len(tokens) {
		return violations
	}

	tok := tokens[startIdx]

	// Check if this is a known Lmaobox builtin global
	globalName := strings.ToLower(tok.Text)
	if !knownLmaoboxBuiltinGlobals[globalName] {
		return violations
	}

	// Helper to find the previous non-whitespace token
	getPrevNonWhitespace := func(idx int) int {
		for i := idx - 1; i >= 0; i-- {
			if tokens[i].Kind != "whitespace" {
				return i
			}
		}
		return -1
	}

	// Pattern 1: "if http then" or "if entities then"
	prevIdx := getPrevNonWhitespace(startIdx)
	if prevIdx >= 0 && tokens[prevIdx].Kind == "keyword" && tokens[prevIdx].Text == "if" {
		violations = append(violations, luaPolicyViolation{
			Line:    tok.Line,
			Message: fmt.Sprintf("CRITICAL: Lmaobox built-in '%s' must NOT be validated with 'if' checks — these globals always exist at runtime. Remove the guard and call %s.SomeCall(...) directly", globalName, globalName),
		})
		return violations
	}

	// Pattern 2: "http ~= nil" or "entities == nil"
	if startIdx+2 < len(tokens) && tokens[startIdx+1].Kind == "symbol" {
		op := tokens[startIdx+1].Text
		if (op == "~=" || op == "==" || op == "===" || op == "!=") && tokens[startIdx+2].Kind == "keyword" && tokens[startIdx+2].Text == "nil" {
			violations = append(violations, luaPolicyViolation{
				Line:    tok.Line,
				Message: fmt.Sprintf("CRITICAL: Lmaobox built-in '%s' must NOT be checked against nil — these globals always exist at runtime. Remove the nil guard and call the API directly", globalName),
			})
			return violations
		}
	}

	// Pattern 3: "type(http)" or "type(entities)"
	prevIdx = getPrevNonWhitespace(startIdx)
	if prevIdx >= 0 && tokens[prevIdx].Kind == "ident" && strings.ToLower(tokens[prevIdx].Text) == "type" {
		prevPrevIdx := getPrevNonWhitespace(prevIdx)
		if prevPrevIdx >= 0 && tokens[prevPrevIdx].Kind == "symbol" && tokens[prevPrevIdx].Text == "(" {
			violations = append(violations, luaPolicyViolation{
				Line:    tok.Line,
				Message: fmt.Sprintf("CRITICAL: type(%s) is not a valid validation pattern — Lmaobox built-ins don't respond to type() checks. Remove the type() guard and call the API directly", globalName),
			})
			return violations
		}
	}

	// Pattern 4: "globals.http" or "globals.entities" (indirect _G access)
	prevIdx = getPrevNonWhitespace(startIdx)
	if prevIdx >= 1 && tokens[prevIdx].Kind == "symbol" && tokens[prevIdx].Text == "." {
		prevPrevIdx := getPrevNonWhitespace(prevIdx)
		if prevPrevIdx >= 0 && tokens[prevPrevIdx].Kind == "ident" && tokens[prevPrevIdx].Text == "globals" {
			violations = append(violations, luaPolicyViolation{
				Line:    tok.Line,
				Message: fmt.Sprintf("CRITICAL: Accessing Lmaobox built-in '%s' via 'globals.%s' is forbidden — these globals are always in scope. Remove the indirection and call %s.Method(...) directly", globalName, globalName, globalName),
			})
			return violations
		}
	}

	// Pattern 5: standalone builtin global name in conditional context
	// Look back on the same line for keywords to determine context
	for j := startIdx - 1; j >= 0 && startIdx-j < 10; j-- {
		if tokens[j].Line != tok.Line {
			break
		}
		if tokens[j].Kind == "whitespace" {
			continue
		}
		if tokens[j].Kind == "keyword" {
			if tokens[j].Text == "while" || tokens[j].Text == "until" || tokens[j].Text == "and" || tokens[j].Text == "or" {
				violations = append(violations, luaPolicyViolation{
					Line:    tok.Line,
					Message: fmt.Sprintf("CRITICAL: Using '%s' as a conditional guard is invalid — Lmaobox built-ins always exist and don't evaluate to truthy/falsy. Remove the conditional and call %s.Call(...) directly", globalName, globalName),
				})
				break
			}
			if tokens[j].Text == "local" || tokens[j].Text == "function" || tokens[j].Text == "return" {
				break
			}
		}
	}

	return violations
}

func lineNumberForOffset(content string, offset int) int {
	if offset <= 0 {
		return 1
	}
	line := 1
	for i, r := range content {
		if i >= offset {
			break
		}
		if r == '\n' {
			line++
		}
	}
	return line
}

func dedupeLuaPolicyViolations(violations []luaPolicyViolation) []luaPolicyViolation {
	seen := make(map[string]bool, len(violations))
	out := make([]luaPolicyViolation, 0, len(violations))
	for _, violation := range violations {
		key := fmt.Sprintf("%d|%s", violation.Line, violation.Message)
		if !seen[key] {
			seen[key] = true
			out = append(out, violation)
		}
	}
	return out
}

func formatLuaPolicyViolations(filePath string, violations []luaPolicyViolation) string {
	var builder strings.Builder
	builder.WriteString("Zero-Mutation Lbox policy violation(s) detected:\n")
	builder.WriteString(fmt.Sprintf("file: %s\n", filePath))
	for _, violation := range violations {
		builder.WriteString(fmt.Sprintf("- line %d: %s\n", violation.Line, violation.Message))
	}
	return builder.String()
}

func collectLuaAdvisoryWarnings(filePath string) ([]string, error) {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file for advisory scan: %v", err)
	}
	tokens, err := tokenizeLua(string(contentBytes))
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0)
	for i := 0; i < len(tokens); i++ {
		if method, args, _, ok := extractCallbacksCall(tokens, i); ok {
			if !strings.EqualFold(method, "register") {
				continue
			}
			if len(args) < 3 {
				continue
			}
			handlerArg := trimLuaArgTokens(args[2])
			if len(handlerArg) > 0 && handlerArg[0].Kind == "keyword" && handlerArg[0].Text == "function" {
				eventName := stringArgValue(args, 0)
				if eventName == "" {
					eventName = "unknown"
				}
				warnings = append(warnings, fmt.Sprintf("line %d: inline anonymous function passed to callbacks.Register(\"%s\", ...) — prefer a named local handler unless inline callback style was explicitly requested", tokens[i].Line, eventName))
			}
		}
	}
	warnings = append(warnings, collectLateFunctionDefinitionWarnings(tokens)...)

	return dedupeStrings(warnings), nil
}

func collectLateFunctionDefinitionWarnings(tokens []luaToken) []string {
	functions := collectNamedLuaFunctions(tokens)
	definitionLines := make(map[string]int, len(functions))
	for name, functionTokens := range functions {
		if len(functionTokens) == 0 {
			continue
		}
		definitionLines[name] = functionTokens[0].Line
	}

	warnings := make([]string, 0)
	for i := 0; i < len(tokens); i++ {
		calleeName, _, endIndex, ok := extractSimpleLuaFunctionCall(tokens, i, functions)
		if !ok {
			continue
		}
		definitionLine, exists := definitionLines[calleeName]
		if !exists || tokens[i].Line >= definitionLine {
			i = endIndex
			continue
		}
		warnings = append(warnings, fmt.Sprintf("line %d: function '%s' is called before its definition on line %d — prefer defining helpers before the code that uses them for clearer static flow", tokens[i].Line, tokens[i].Text, definitionLine))
		i = endIndex
	}

	return dedupeStrings(warnings)
}

func findIpairsOnFindByClassViolations(content string) []luaPolicyViolation {
	lines := strings.Split(content, "\n")
	findByClassAssignRe := regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*entities\s*\.\s*FindByClass\s*\(`)
	directIpairsRe := regexp.MustCompile(`ipairs\s*\(\s*entities\s*\.\s*FindByClass\s*\(`)
	ipairsVarRe := regexp.MustCompile(`ipairs\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
	tableMutationRe := regexp.MustCompile(`table\s*\.\s*(?:insert|remove|sort)\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\b`)

	sparseVars := make(map[string]bool)
	violations := make([]luaPolicyViolation, 0)

	for lineNumber, rawLine := range lines {
		line := rawLine

		if directIpairsRe.MatchString(line) {
			violations = append(violations, luaPolicyViolation{
				Line:    lineNumber + 1,
				Message: "CRITICAL: entities.FindByClass() returns a sparse entity table — do not iterate it with ipairs(); use pairs() unless you explicitly repack it first",
			})
		}

		if matches := findByClassAssignRe.FindStringSubmatch(line); len(matches) == 2 {
			sparseVars[matches[1]] = true
		}

		for _, matches := range ipairsVarRe.FindAllStringSubmatch(line, -1) {
			if len(matches) == 2 && sparseVars[matches[1]] {
				violations = append(violations, luaPolicyViolation{
					Line:    lineNumber + 1,
					Message: fmt.Sprintf("CRITICAL: %s comes directly from entities.FindByClass() and may be sparse — use pairs(%s) unless you explicitly rebuilt it into a sequential array first", matches[1], matches[1]),
				})
			}
		}

		for varName := range sparseVars {
			reassignRe := regexp.MustCompile(fmt.Sprintf(`^\s*(?:local\s+)?%s\s*=`, regexp.QuoteMeta(varName)))
			indexMutationRe := regexp.MustCompile(fmt.Sprintf(`\b%s\s*\[.*\]\s*=`, regexp.QuoteMeta(varName)))
			if reassignRe.MatchString(line) && !findByClassAssignRe.MatchString(line) {
				delete(sparseVars, varName)
				continue
			}
			if indexMutationRe.MatchString(line) {
				delete(sparseVars, varName)
				continue
			}
		}

		for _, matches := range tableMutationRe.FindAllStringSubmatch(line, -1) {
			if len(matches) == 2 {
				delete(sparseVars, matches[1])
			}
		}
	}

	return dedupeLuaPolicyViolations(violations)
}

func findCachedEntityWithoutIsValidViolations(content string, tokens []luaToken) []luaPolicyViolation {
	lines := strings.Split(content, "\n")
	functionDepthByLine := buildFunctionDepthByLine(tokens)
	topLevelDeclRe := regexp.MustCompile(`^\s*local\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:nil|false)?\s*$`)
	entityAssignRe := regexp.MustCompile(`(?:^|[^\w])([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:entities\s*\.\s*(?:GetLocalPlayer|GetByIndex|GetByUserID|CreateEntityByName|CreateTempEntityByName)\s*\(|[A-Za-z_][A-Za-z0-9_]*\s*:\s*GetPropEntity\s*\()`)
	methodCallTemplate := `\b%s\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`
	isValidGuardTemplate := `\b%s\s*:\s*IsValid\s*\(`

	cachedCandidates := make(map[string]bool)
	entityBackedVars := make(map[string]bool)
	violations := make([]luaPolicyViolation, 0)

	for i, line := range lines {
		lineNumber := i + 1
		if functionDepthByLine[lineNumber] == 0 {
			if matches := topLevelDeclRe.FindStringSubmatch(line); len(matches) == 2 {
				cachedCandidates[matches[1]] = true
			}
		}

		if matches := entityAssignRe.FindAllStringSubmatch(line, -1); len(matches) > 0 {
			for _, match := range matches {
				if len(match) == 2 && cachedCandidates[match[1]] {
					entityBackedVars[match[1]] = true
				}
			}
		}

		for varName := range entityBackedVars {
			methodCallRe := regexp.MustCompile(fmt.Sprintf(methodCallTemplate, regexp.QuoteMeta(varName)))
			callMatch := methodCallRe.FindStringSubmatch(line)
			if len(callMatch) != 2 {
				continue
			}
			methodName := callMatch[1]
			if strings.EqualFold(methodName, "IsValid") {
				continue
			}
			if cachedEntityUseHasIsValidGuard(lines, i, varName, isValidGuardTemplate) {
				continue
			}
			violations = append(violations, luaPolicyViolation{
				Line:    lineNumber,
				Message: fmt.Sprintf("CRITICAL: cached entity '%s' is used across ticks without an IsValid() guard — stale entity handles can become invalid before methods like :%s() are called", varName, methodName),
			})
		}
	}

	return dedupeLuaPolicyViolations(violations)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func buildFunctionDepthByLine(tokens []luaToken) map[int]int {
	depthByLine := make(map[int]int)
	blockStack := make([]luaPolicyBlockKind, 0)
	functionDepth := 0
	pendingLoopDo := 0

	for _, tok := range tokens {
		if _, ok := depthByLine[tok.Line]; !ok {
			depthByLine[tok.Line] = functionDepth
		}
		if tok.Kind != "keyword" {
			continue
		}
		switch tok.Text {
		case "function":
			blockStack = append(blockStack, luaBlockFunction)
			functionDepth++
		case "for", "while":
			blockStack = append(blockStack, luaBlockGeneric)
			pendingLoopDo++
		case "do":
			if pendingLoopDo > 0 {
				pendingLoopDo--
				break
			}
			blockStack = append(blockStack, luaBlockGeneric)
		case "if":
			blockStack = append(blockStack, luaBlockGeneric)
		case "repeat":
			blockStack = append(blockStack, luaBlockRepeat)
		case "end":
			if len(blockStack) > 0 {
				for j := len(blockStack) - 1; j >= 0; j-- {
					if blockStack[j] == luaBlockRepeat {
						continue
					}
					if blockStack[j] == luaBlockFunction && functionDepth > 0 {
						functionDepth--
					}
					blockStack = append(blockStack[:j], blockStack[j+1:]...)
					break
				}
			}
		case "until":
			if len(blockStack) > 0 {
				for j := len(blockStack) - 1; j >= 0; j-- {
					if blockStack[j] == luaBlockRepeat {
						blockStack = append(blockStack[:j], blockStack[j+1:]...)
						break
					}
				}
			}
		}
	}

	return depthByLine
}

func cachedEntityUseHasIsValidGuard(lines []string, lineIndex int, varName string, isValidGuardTemplate string) bool {
	guardRe := regexp.MustCompile(fmt.Sprintf(isValidGuardTemplate, regexp.QuoteMeta(varName)))
	start := lineIndex - 2
	if start < 0 {
		start = 0
	}
	end := lineIndex
	if end >= len(lines) {
		end = len(lines) - 1
	}
	for i := start; i <= end; i++ {
		if guardRe.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// identifyCallbackHandlerFunctions collects all function names that are registered as callback handlers
func identifyCallbackHandlerFunctions(tokens []luaToken) map[string]bool {
	handlers := make(map[string]bool)
	for i := 0; i < len(tokens); i++ {
		method, args, _, ok := extractCallbacksCall(tokens, i)
		if !ok || !strings.EqualFold(method, "register") {
			continue
		}
		// Get the handler argument (typically the 3rd argument)
		if len(args) < 3 {
			continue
		}
		// Check if it's a reference to a named function (not an inline function)
		handlerArg := trimLuaArgTokens(args[2])
		if len(handlerArg) == 1 && handlerArg[0].Kind == "ident" {
			// This is a reference to a named function like: callbacks.Register("Event", "id", onEvent)
			handlers[strings.ToLower(handlerArg[0].Text)] = true
		}
	}
	return handlers
}

// buildCallbackHandlerLineRanges creates a map of line ranges for each callback handler function
func buildCallbackHandlerLineRanges(tokens []luaToken, handlers map[string]bool) map[string][2]int {
	ranges := make(map[string][2]int)

	// Find function definitions and their ranges
	for i := 0; i < len(tokens); i++ {
		if tokens[i].Kind != "keyword" || tokens[i].Text != "function" {
			continue
		}

		// Get function name
		if i+1 >= len(tokens) || tokens[i+1].Kind != "ident" {
			continue
		}

		funcName := strings.ToLower(tokens[i+1].Text)
		if !handlers[funcName] {
			continue
		}

		// Find the matching 'end' for this function
		functionStartLine := tokens[i].Line
		endLine := functionStartLine
		depth := 1

		for j := i + 1; j < len(tokens); j++ {
			if tokens[j].Kind == "keyword" {
				if tokens[j].Text == "function" {
					depth++
				} else if tokens[j].Text == "end" {
					depth--
					if depth == 0 {
						endLine = tokens[j].Line
						break
					}
				}
			}
		}

		ranges[funcName] = [2]int{functionStartLine, endLine}
	}

	return ranges
}

// isLineInCallbackHandler checks if a given line is inside any registered callback handler function
func isLineInCallbackHandler(line int, ranges map[string][2]int) bool {
	for _, lineRange := range ranges {
		if line > lineRange[0] && line <= lineRange[1] {
			return true
		}
	}
	return false
}
