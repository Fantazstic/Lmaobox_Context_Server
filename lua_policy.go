package main

import (
	"fmt"
	"os"
	"strings"
)

// Policy focuses on callback safety and real resource/API mistakes - not style or
// defensive guards around globals. pcall is only appropriate for optional require(),
// http, and file I/O - never to "protect" Lmaobox API calls.

type LboxMutationPolicy struct {
	RequireDepthZeroRegister   bool
	RequireDepthZeroUnregister bool
	RequireKillSwitchOrder     bool
	ForbidRuntimeUnregister    bool
	ForbidCollectGarbage       bool
	ForbidRequireInFunction    bool
	ForbidGlobalTable          bool
	ForbidCreateFontInFunction bool
	ForbidLegacyBitLibrary     bool
	ForbidDeprecatedCallbacks  bool
	ForbidAllowListener        bool
	ForbidForceFullUpdate      bool
	ForbidMisusedPcall         bool
}

type luaPolicyViolation struct {
	Line    int
	Message string
}

type luaPolicyBlockKind int

const (
	luaBlockGeneric luaPolicyBlockKind = iota
	luaBlockFunction
	luaBlockRepeat
)

var defaultLboxMutationPolicy = LboxMutationPolicy{
	RequireDepthZeroRegister:   true,
	RequireDepthZeroUnregister: true,
	RequireKillSwitchOrder:     true,
	ForbidRuntimeUnregister:    true,
	ForbidCollectGarbage:       true,
	ForbidRequireInFunction:    true,
	ForbidGlobalTable:          true,
	ForbidCreateFontInFunction: true,
	ForbidLegacyBitLibrary:     true,
	ForbidDeprecatedCallbacks:  true,
	ForbidAllowListener:        true,
	ForbidForceFullUpdate:      true,
	ForbidMisusedPcall:         true,
}

// bundleMutationPolicy is for tests/simulated bundle output only (not used by the bundle tool).
var bundleMutationPolicy = func() LboxMutationPolicy {
	p := defaultLboxMutationPolicy
	p.ForbidRequireInFunction = false
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
	tokenDepths := buildFunctionDepthAtToken(tokens)
	unregisteredAtDepthZero := make(map[string]bool)

	// Identify callback handler functions (those passed to callbacks.Register)
	callbackHandlers := identifyCallbackHandlerFunctions(tokens)
	callbackHandlerRanges := buildCallbackHandlerLineRanges(tokens, callbackHandlers)

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		if method, args, endIndex, ok := extractCallbacksCall(tokens, i); ok {
			line := tok.Line
			callDepth := tokenDepths[i]
			eventName := stringArgValue(args, 0)
			uniqueID := stringArgValue(args, 1)
			isInCallbackHandler := isLineInCallbackHandler(line, callbackHandlerRanges)

			if strings.EqualFold(method, "register") {
				if policy.ForbidDeprecatedCallbacks && strings.EqualFold(eventName, "PostPropUpdate") {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage - use FrameStageNotify instead"})
				}
				if policy.RequireDepthZeroRegister && callDepth > 0 && isInCallbackHandler {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: callbacks.Register inside a callback handler function is forbidden - register all callbacks at module load time, never from within a running callback"})
				}
				if policy.RequireKillSwitchOrder && callDepth == 0 && eventName != "" {
					killSwitchKeyExact := strings.ToLower(eventName + "|" + uniqueID)
					killSwitchKeyEvent := strings.ToLower(eventName + "|")
					hasExactMatch := unregisteredAtDepthZero[killSwitchKeyExact]
					hasEventMatch := unregisteredAtDepthZero[killSwitchKeyEvent]
					if uniqueID != "" && !hasExactMatch && !hasEventMatch {
						violations = append(violations, luaPolicyViolation{Line: line, Message: fmt.Sprintf("CRITICAL: Kill-Switch violation for id '%s' on event '%s': callbacks.Unregister must appear before callbacks.Register at module scope", uniqueID, eventName)})
					}
				}
			}

			if strings.EqualFold(method, "unregister") {
				if policy.ForbidDeprecatedCallbacks && strings.EqualFold(eventName, "PostPropUpdate") {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage - use FrameStageNotify instead"})
				}
				reportedRuntimeUnregister := false
				if policy.ForbidRuntimeUnregister && callDepth > 0 {
					if isInCallbackHandler {
						violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: Illegal Unregister inside a callback handler function - runtime callback table mutation is forbidden"})
					} else {
						violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: Illegal Unregister inside function scope (including Unload). Runtime callback table mutation is forbidden"})
					}
					reportedRuntimeUnregister = true
				}
				if policy.RequireDepthZeroUnregister && callDepth > 0 && !reportedRuntimeUnregister {
					violations = append(violations, luaPolicyViolation{Line: line, Message: "CRITICAL: callbacks.Unregister must be at module scope (outside all functions)"})
				}
				if callDepth == 0 && eventName != "" {
					if uniqueID != "" {
						unregisteredAtDepthZero[strings.ToLower(eventName+"|"+uniqueID)] = true
					} else {
						unregisteredAtDepthZero[strings.ToLower(eventName+"|")] = true
					}
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
				nestedDepth := tokenDepths[j]
				nestedEvent := stringArgValue(nestedArgs, 0)

				if strings.EqualFold(nestedMethod, "register") {
					if policy.ForbidDeprecatedCallbacks && strings.EqualFold(nestedEvent, "PostPropUpdate") {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage - use FrameStageNotify instead"})
					}
					if policy.RequireDepthZeroRegister && nestedDepth > 0 {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: callbacks.Register inside a callback handler is forbidden - register all callbacks at module load time, never from within a running callback"})
					}
				}

				if strings.EqualFold(nestedMethod, "unregister") {
					if policy.ForbidDeprecatedCallbacks && strings.EqualFold(nestedEvent, "PostPropUpdate") {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: PostPropUpdate is deprecated legacy-only callback usage - use FrameStageNotify instead"})
					}
					reportedRuntimeUnregister := false
					if policy.ForbidRuntimeUnregister && nestedDepth > 0 {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: Illegal Unregister inside function scope (including Unload). Runtime callback table mutation is forbidden"})
						reportedRuntimeUnregister = true
					}
					if policy.RequireDepthZeroUnregister && nestedDepth > 0 && !reportedRuntimeUnregister {
						violations = append(violations, luaPolicyViolation{Line: nestedLine, Message: "CRITICAL: callbacks.Unregister must be at module scope (outside all functions)"})
					}
				}
			}

			i = endIndex
			continue
		}
	}

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		callDepth := tokenDepths[i]
		if tok.Kind != "ident" {
			continue
		}
		if policy.ForbidCollectGarbage && isForbiddenCollectGarbageCall(tokens, i) {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "WARNING: collectgarbage collection/control calls are forbidden - forcing GC in Lmaobox causes runtime lag and does not fix leaks; collectgarbage(\"count\") is allowed for read-only profiling"})
		}
		if policy.ForbidRequireInFunction && tok.Text == "require" && callDepth > 0 && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: require() inside a function causes memory leaks - move all require() calls to the top of the file"})
		}
		if policy.ForbidGlobalTable && tok.Text == "_G" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: _G usage is forbidden - use the G module for shared state instead"})
		}
		if policy.ForbidCreateFontInFunction && strings.EqualFold(tok.Text, "draw") && callDepth > 0 && i+3 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "." && strings.EqualFold(tokens[i+2].Text, "CreateFont") && tokens[i+3].Kind == "symbol" && tokens[i+3].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: draw.CreateFont inside a function creates a permanent irremovable font on every call - move it to module scope and cache the handle"})
		}
		if policy.ForbidLegacyBitLibrary && strings.EqualFold(tok.Text, "bit") && i+3 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "." && tokens[i+2].Kind == "ident" && isLegacyBitMethod(tokens[i+2].Text) && tokens[i+3].Kind == "symbol" && tokens[i+3].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: fmt.Sprintf("CRITICAL: bit.%s() does not exist in Lua 5.4 - use native bitwise operators instead", tokens[i+2].Text)})
		}
		if policy.ForbidAllowListener && tok.Text == "AllowListener" && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: AllowListener() is deprecated and does nothing - remove the call"})
		}
		if policy.ForbidForceFullUpdate && tok.Text == "ForceFullUpdate" && i+1 < len(tokens) && tokens[i+1].Kind == "symbol" && tokens[i+1].Text == "(" {
			violations = append(violations, luaPolicyViolation{Line: tok.Line, Message: "CRITICAL: ForceFullUpdate() is dangerous and should be avoided - it can lag or crash the game if misused"})
		}
	}

	if policy.ForbidMisusedPcall {
		violations = append(violations, scanMisusedPcallViolations(tokens)...)
	}

	return dedupeLuaPolicyViolations(violations), nil
}

// scanMisusedPcallViolations flags pcall/xpcall used to wrap game API or anonymous
// guards. Allowed: pcall(require, ...), pcall(http..., ...), load/loadfile/io.
func scanMisusedPcallViolations(tokens []luaToken) []luaPolicyViolation {
	violations := make([]luaPolicyViolation, 0)
	for i := 0; i < len(tokens); i++ {
		name := strings.ToLower(tokens[i].Text)
		if name != "pcall" && name != "xpcall" {
			continue
		}
		if i+1 >= len(tokens) || tokens[i+1].Kind != "symbol" || tokens[i+1].Text != "(" {
			continue
		}
		args, _ := collectLuaCallArgs(tokens, i+1)
		if len(args) == 0 {
			continue
		}
		first := trimLuaArgTokens(args[0])
		if len(first) == 0 {
			continue
		}
		line := tokens[i].Line

		if first[0].Kind == "keyword" && first[0].Text == "function" {
			violations = append(violations, luaPolicyViolation{
				Line:    line,
				Message: "CRITICAL: pcall(function() ...) does not protect Lmaobox API - use pcall only for optional require(), http, or file I/O; call draw/engine/entities/callbacks directly",
			})
			continue
		}

		if len(first) >= 1 && first[0].Kind == "ident" {
			root := strings.ToLower(first[0].Text)
			if root == "require" || root == "load" || root == "loadfile" || root == "http" || root == "io" {
				continue
			}
			if root == "draw" || root == "engine" || root == "entities" || root == "callbacks" || root == "warp" || root == "client" || root == "gui" || root == "input" {
				violations = append(violations, luaPolicyViolation{
					Line:    line,
					Message: fmt.Sprintf("CRITICAL: pcall(%s, ...) does not protect native API - call %s directly; pcall is for optional require/http/file I/O only", first[0].Text, first[0].Text),
				})
			}
		}
	}
	return violations
}

func isForbiddenCollectGarbageCall(tokens []luaToken, startIdx int) bool {
	if startIdx+1 >= len(tokens) || tokens[startIdx].Text != "collectgarbage" || tokens[startIdx+1].Kind != "symbol" || tokens[startIdx+1].Text != "(" {
		return false
	}
	args, _ := collectLuaCallArgs(tokens, startIdx+1)
	if len(args) == 0 {
		return true
	}
	mode := strings.ToLower(stringArgValue(args, 0))
	if mode == "count" {
		return false
	}
	return true
}

func isLegacyBitMethod(name string) bool {
	switch strings.ToLower(name) {
	case "band", "bor", "bnot", "bxor", "lshift", "rshift", "arshift":
		return true
	default:
		return false
	}
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

// buildFunctionDepthAtToken returns nesting depth inside Lua functions at each token.
// if/for/while/repeat/do blocks do not increase depth; only function ... end does.
// Depth is recorded before the keyword that changes it, so callbacks.register on the
// same line as function() still sees module scope for the register call itself.
func buildFunctionDepthAtToken(tokens []luaToken) []int {
	depths := make([]int, len(tokens))
	blockStack := make([]luaPolicyBlockKind, 0)
	functionDepth := 0
	pendingLoopDo := 0

	for i, tok := range tokens {
		depths[i] = functionDepth
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

	return depths
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
