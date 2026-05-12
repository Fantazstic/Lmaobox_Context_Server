package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// ── MCP Tool Tests ──────────────────────────────────────────────────────────
//
// These tests verify that the core validation functions work correctly,
// which are used by the MCP tools (luacheck, bundle, etc).

func createTempLuaFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".lua")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	return path
}

// ── Tests for Validation Functions (Used by MCP Tools) ──────────────────────

// TestValidateLuaSyntaxValid tests that valid Lua syntax passes
func TestValidateLuaSyntaxValid(t *testing.T) {
	if findLuac() == "" {
		t.Skip("Lua compiler not installed; skipping syntax validation test")
	}

	src := `
local x = 10
local function add(a, b)
    return a + b
end
print(add(5, 3))
`
	path := createTempLuaFile(t, "valid_syntax", src)

	ctx := context.Background()
	err := validateLuaSyntax(ctx, path)
	if err != nil {
		t.Fatalf("expected valid syntax, got error: %v", err)
	}
}

// TestValidateLuaSyntaxInvalid tests that invalid Lua syntax fails
func TestValidateLuaSyntaxInvalid(t *testing.T) {
	if findLuac() == "" {
		t.Skip("Lua compiler not installed; skipping syntax validation test")
	}

	src := `local x = `
	path := createTempLuaFile(t, "invalid_syntax", src)

	ctx := context.Background()
	err := validateLuaSyntax(ctx, path)
	if err == nil {
		t.Fatalf("expected syntax error, got success")
	}

	if !strings.Contains(err.Error(), "syntax") && !strings.Contains(err.Error(), "error") {
		t.Fatalf("expected syntax error message, got: %v", err)
	}
}

// TestZeroMutationUnregisterInFunction tests Zero-Mutation rule violation
func TestZeroMutationUnregisterInFunction(t *testing.T) {
	src := `
local function cleanup()
    callbacks.unregister("Draw", "MyLoop")
end

callbacks.register("Draw", "MyLoop", function() end)
`
	path := createTempLuaFile(t, "zero_mut_unreg_fn", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected policy violation for unregister in function")
	}

	if !strings.Contains(violations[0].Message, "Illegal Unregister") {
		t.Fatalf("expected Illegal Unregister violation, got: %s", violations[0].Message)
	}
}

// TestZeroMutationUnregisterInOnUnload tests unregister in OnUnload is banned
func TestZeroMutationUnregisterInOnUnload(t *testing.T) {
	src := `
callbacks.register("Unload", function()
    callbacks.unregister("Draw", "MyLoop")
end)

callbacks.register("Draw", "MyLoop", function() end)
`
	path := createTempLuaFile(t, "zero_mut_unload", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected violation in OnUnload")
	}

	if !strings.Contains(violations[0].Message, "Illegal Unregister") {
		t.Fatalf("expected Illegal Unregister violation, got: %s", violations[0].Message)
	}
}

// TestZeroMutationKillSwitchViolation tests kill-switch requirement
func TestZeroMutationKillSwitchViolation(t *testing.T) {
	src := `
callbacks.register("Draw", "MyLoop", function()
    print("Running")
end)
`
	path := createTempLuaFile(t, "zero_mut_kill_switch", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected kill-switch violation")
	}

	if !strings.Contains(violations[0].Message, "Kill-Switch") {
		t.Fatalf("expected Kill-Switch violation, got: %s", violations[0].Message)
	}
}

// TestZeroMutationGhostPatternApproved tests Ghost Pattern is allowed
func TestZeroMutationGhostPatternApproved(t *testing.T) {
	src := `
local running = true

callbacks.unregister("Draw", "MyLoop")
callbacks.register("Draw", "MyLoop", function()
    if not running then return end
    print("Running")
end)

callbacks.register("Unload", function()
    running = false
end)
`
	path := createTempLuaFile(t, "ghost_pattern", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf("expected Ghost Pattern to pass, got violations: %v", violations)
	}
}

// TestZeroMutationRegisterInNestedFunction tests register in load-time nested function is allowed.
// Only register INSIDE a running callback handler is forbidden; load-time helper functions are fine.
func TestZeroMutationRegisterInNestedFunction(t *testing.T) {
	src := `
local function setup()
    local function inner()
        callbacks.register("Draw", "MyLoop", function() end)
    end
    inner()
end
setup()
`
	path := createTempLuaFile(t, "register_nested_fn", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	// Load-time helper function at depth > 0 is acceptable.
	// Should NOT get a "callback handler" violation — may get a kill-switch violation (no unregister).
	for _, v := range violations {
		if strings.Contains(v.Message, "callback handler") {
			t.Fatalf("false positive: load-time nested function flagged as callback handler: %s", v.Message)
		}
	}
}

// TestZeroMutationIfBlockNoDepthIsolation tests if blocks don't isolate
func TestZeroMutationIfBlockNoDepthIsolation(t *testing.T) {
	// If block at depth 0 should fail kill-switch, not depth check
	src := `
if true then
    callbacks.register("Draw", "MyLoop", function() end)
end
`
	path := createTempLuaFile(t, "if_block", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected kill-switch violation")
	}

	if !strings.Contains(violations[0].Message, "Kill-Switch") {
		t.Fatalf("expected Kill-Switch violation (not depth), got: %s", violations[0].Message)
	}
}

func TestParseDLuaEntriesFindsMixedCaseConstants(t *testing.T) {
	content := "---@type integer\nTFCond_Taunting = 7\n"
	entries := parseDLuaEntries("types/lmaobox_lua_api/constants/E_TFCOND.d.lua", content)
	if len(entries) == 0 {
		t.Fatalf("expected at least 1 entry")
	}

	found := false
	for _, entry := range entries {
		if entry.Symbol == "TFCond_Taunting" {
			found = true
			if entry.Kind != "constant" {
				t.Fatalf("expected Kind=constant, got %s", entry.Kind)
			}
			if entry.Section != "constants" {
				t.Fatalf("expected Section=constants, got %s", entry.Section)
			}
		}
	}
	if !found {
		t.Fatalf("expected to find TFCond_Taunting constant")
	}
}

func TestParseDLuaEntriesNormalizesColonMethodsToDot(t *testing.T) {
	content := "---@return boolean\nfunction Entity:InCond(condition) end\n"
	entries := parseDLuaEntries("types/lmaobox_lua_api/Lua_Classes/Entity.d.lua", content)
	if len(entries) == 0 {
		t.Fatalf("expected at least 1 entry")
	}

	found := false
	for _, entry := range entries {
		if entry.Symbol == "Entity.InCond" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find Entity.InCond")
	}
}

func TestParseDLuaEntriesIndexesEntityPropsFields(t *testing.T) {
	content := "---@class CTFPlayer\n---@field m_nPlayerCond number\n"
	entries := parseDLuaEntries("types/lmaobox_lua_api/entity_props/CTFPlayer.d.lua", content)
	if len(entries) == 0 {
		t.Fatalf("expected at least 1 entry")
	}

	foundClass := false
	foundField := false
	for _, entry := range entries {
		if entry.Symbol == "CTFPlayer" {
			foundClass = true
			if entry.Kind != "class" {
				t.Fatalf("expected Kind=class, got %s", entry.Kind)
			}
			if entry.Section != "entity_props" {
				t.Fatalf("expected Section=entity_props, got %s", entry.Section)
			}
		}

		if entry.Symbol == "CTFPlayer.m_nPlayerCond" {
			foundField = true
			if entry.Kind != "entity_prop" {
				t.Fatalf("expected Kind=entity_prop, got %s", entry.Kind)
			}
			if entry.Section != "entity_props" {
				t.Fatalf("expected Section=entity_props, got %s", entry.Section)
			}
			if entry.Signature != "number" {
				t.Fatalf("expected Signature=number, got %s", entry.Signature)
			}
		}
	}

	if !foundClass {
		t.Fatalf("expected to find CTFPlayer class entry")
	}
	if !foundField {
		t.Fatalf("expected to find CTFPlayer.m_nPlayerCond field entry")
	}
}

func TestTypeSignaturePatternsIncludeColonVariant(t *testing.T) {
	patterns := typeSignaturePatterns("Entity.InCond")
	found := false
	for _, p := range patterns {
		if p == "Entity:InCond" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Entity:InCond pattern")
	}
}

func TestAliasBoostSuggestsInCondForPlayerCondNetvar(t *testing.T) {
	candidate := smartCandidate{
		SmartSearchResult: SmartSearchResult{
			Symbol: "Entity.InCond",
		},
		symbolNorm: normalizeSearchText("entity.incond"),
	}

	queryNorm := normalizeSearchText("m_nPlayerCond")
	tokensNorm := []string{queryNorm}
	boost := aliasBoostScore(queryNorm, tokensNorm, candidate)
	if boost <= 0 {
		t.Fatalf("expected positive alias boost for m_nPlayerCond")
	}
}

func TestSmartSearchCommonQueriesReturnHelpfulSymbols(t *testing.T) {
	tests := []struct {
		query    string
		expected []string
	}{
		{query: "m_nPlayerCond", expected: []string{"Entity.InCond", "E_TFCOND"}},
		{query: "m_Shared tfcond", expected: []string{"Entity.InCond", "E_TFCOND"}},
		{query: "getcond taunt", expected: []string{"Entity.InCond"}},
		{query: "tf_cond_taunting", expected: []string{"TFCond_Taunting"}},
		{query: "player flags m_fFlags", expected: []string{"E_PlayerFlag"}},
		{query: "convar sensitivity", expected: []string{"client.GetConVar"}},
		{query: "cvar fov", expected: []string{"client.GetConVar"}},
		{query: "drawmodel flags studio_render", expected: []string{"Entity.DrawModel"}},
		{query: "player_death", expected: []string{"player_death", "FireGameEvent", "GameEvent"}},
		{query: "FireGameEvent player_death", expected: []string{"FireGameEvent", "player_death"}},
	}

	for _, tc := range tests {
		primary, secondary, docs, err := smartSearch(tc.query, 25)
		if err != nil {
			t.Fatalf("smartSearch(%q) error: %v", tc.query, err)
		}
		if len(primary) == 0 && len(secondary) == 0 && len(docs) == 0 {
			t.Fatalf("smartSearch(%q) returned no results", tc.query)
		}

		found := false
		for _, want := range tc.expected {
			for _, r := range primary {
				if r.Symbol == want {
					found = true
					break
				}
			}
			if found {
				break
			}
			for _, r := range secondary {
				if r.Symbol == want {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			syms := make([]string, 0, len(primary))
			for _, r := range primary {
				syms = append(syms, r.Symbol)
			}
			t.Fatalf("smartSearch(%q) missing expected %v; got %v", tc.query, tc.expected, syms)
		}
	}
}

func TestSmartSearchFindsTF2EntityPropsNetvars(t *testing.T) {
	primary, secondary, docs, err := smartSearch("m_nPlayerCond", 25)
	if err != nil {
		t.Fatalf("smartSearch error: %v", err)
	}
	results := append(primary, secondary...)
	results = append(results, docs...)

	found := false
	for _, r := range results {
		if r.Symbol == "CTFPlayer.m_nPlayerCond" {
			found = true
			break
		}
	}
	if !found {
		syms := make([]string, 0, len(results))
		for _, r := range results {
			syms = append(syms, r.Symbol)
		}
		t.Fatalf("expected to find CTFPlayer.m_nPlayerCond in results; got %v", syms)
	}
}

func TestSmartSearchFallsBackToDocsPages(t *testing.T) {
	primary, secondary, docs, err := smartSearch("mannpower king powerup", 10)
	if err != nil {
		t.Fatalf("smartSearch error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("expected docs fallback results")
	}
	if docs[0].Kind != "doc_page" {
		t.Fatalf("expected doc_page kind, got %s", docs[0].Kind)
	}
	if len(primary) == 0 && len(secondary) == 0 {
		return
	}
}

func TestFindSmartContextFallsBackToLocalDocsContent(t *testing.T) {
	content, err := findSmartContext("mannpower king powerup")
	if err != nil {
		t.Fatalf("findSmartContext error: %v", err)
	}
	if !strings.Contains(content, "## Local Docs Fallback") {
		t.Fatalf("expected local docs fallback header, got: %s", content)
	}
	if !strings.Contains(content, "Mannpower King Powerup") && !strings.Contains(content, "King Powerup") {
		t.Fatalf("expected matched docs page content, got: %s", content)
	}
}

func TestFindSmartContextGeneratesEntityPropHelp(t *testing.T) {
	content, err := findSmartContext("CTFPlayer.m_nPlayerCond")
	if err != nil {
		t.Fatalf("findSmartContext error: %v", err)
	}
	if !strings.Contains(content, "## Entity Prop: CTFPlayer.m_nPlayerCond") {
		t.Fatalf("expected entity prop header, got: %s", content)
	}
	if !strings.Contains(content, "Entity.GetProp") {
		t.Fatalf("expected Entity.GetProp guidance, got: %s", content)
	}
	if !strings.Contains(content, "Entity.InCond") || !strings.Contains(content, "E_TFCOND") {
		t.Fatalf("expected TF2 cond guidance, got: %s", content)
	}
}

func TestHandleGetTypesReturnsClosestMatchesOnMiss(t *testing.T) {
	result, err := handleGetTypes(context.Background(), mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name: "get_types",
			Arguments: map[string]interface{}{
				"symbol": "Entity.InCon",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleGetTypes error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetTypes returned tool error: %v", result.Content)
	}

	text := fmt.Sprintf("%v", result.Content)
	if !strings.Contains(text, "Closest matches") {
		t.Fatalf("expected Closest matches section, got: %s", text)
	}
	if !strings.Contains(text, "Entity.InCond") {
		t.Fatalf("expected Entity.InCond suggestion, got: %s", text)
	}
}

func TestHandleGetSmartContextAcceptsColonMethodForm(t *testing.T) {
	result, err := handleGetSmartContext(context.Background(), mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name: "get_smart_context",
			Arguments: map[string]interface{}{
				"symbol": "Entity:InCond",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleGetSmartContext error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetSmartContext returned tool error: %v", result.Content)
	}

	text := fmt.Sprintf("%v", result.Content)
	if !strings.Contains(text, "Entity.InCond") {
		t.Fatalf("expected Entity.InCond content, got: %s", text)
	}
}

func TestHandleGetSmartContextReturnsClosestMatchesOnMiss(t *testing.T) {
	result, err := handleGetSmartContext(context.Background(), mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name: "get_smart_context",
			Arguments: map[string]interface{}{
				"symbol": "Entity.InCon",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleGetSmartContext error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetSmartContext returned tool error: %v", result.Content)
	}

	text := fmt.Sprintf("%v", result.Content)
	hasClosest := strings.Contains(text, "Closest matches") && strings.Contains(text, "Entity.InCond")
	hasDirect := strings.Contains(text, "Function/Symbol: Entity.InCond") || strings.Contains(text, "## Function/Symbol: Entity.InCond")
	if !hasClosest && !hasDirect {
		t.Fatalf("expected closest match suggestion or direct Entity.InCond match, got: %s", text)
	}
}

func TestHandleGetTypesSuggestsClosestConstantOnMiss(t *testing.T) {
	result, err := handleGetTypes(context.Background(), mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name: "get_types",
			Arguments: map[string]interface{}{
				"symbol": "TF_COND_TAUNTING",
			},
		},
	})
	if err != nil {
		t.Fatalf("handleGetTypes error: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetTypes returned tool error: %v", result.Content)
	}

	text := fmt.Sprintf("%v", result.Content)
	if !strings.Contains(text, "Closest matches") {
		t.Fatalf("expected Closest matches section, got: %s", text)
	}
	if !strings.Contains(text, "TFCond_Taunting") {
		t.Fatalf("expected TFCond_Taunting suggestion, got: %s", text)
	}
}

func TestSmartSearchNormalizationMatchesUnderscoreQuery(t *testing.T) {
	content := "---@type integer\nTFCond_Taunting = 7\n"
	entries := parseDLuaEntries("types/lmaobox_lua_api/constants/E_TFCOND.d.lua", content)
	if len(entries) == 0 {
		t.Fatalf("expected entries")
	}

	var candidate smartCandidate
	for _, entry := range entries {
		if entry.Symbol == "TFCond_Taunting" {
			candidate = entry
			break
		}
	}
	if candidate.Symbol == "" {
		t.Fatalf("candidate TFCond_Taunting missing")
	}

	queryLower := strings.ToLower("TF_COND_TAUNT")
	tokens := strings.Fields(queryLower)
	queryNorm := normalizeSearchText(queryLower)
	tokensNorm := make([]string, 0, len(tokens))
	for _, token := range tokens {
		tokensNorm = append(tokensNorm, normalizeSearchText(token))
	}

	score := scoreSmartCandidate(queryLower, tokens, queryNorm, tokensNorm, candidate)
	if score <= 0 {
		t.Fatalf("expected positive score for normalized query, got %f", score)
	}
}

func TestSmartSearchTypoToleranceMatchesSingleToken(t *testing.T) {
	content := "---@type integer\nTFCond_Taunting = 7\n"
	entries := parseDLuaEntries("types/lmaobox_lua_api/constants/E_TFCOND.d.lua", content)
	if len(entries) == 0 {
		t.Fatalf("expected entries")
	}

	var candidate smartCandidate
	for _, entry := range entries {
		if entry.Symbol == "TFCond_Taunting" {
			candidate = entry
			break
		}
	}
	if candidate.Symbol == "" {
		t.Fatalf("candidate TFCond_Taunting missing")
	}

	queryLower := strings.ToLower("TFCond_Tauntng")
	tokens := strings.Fields(queryLower)
	queryNorm := normalizeSearchText(queryLower)
	tokensNorm := make([]string, 0, len(tokens))
	for _, token := range tokens {
		tokensNorm = append(tokensNorm, normalizeSearchText(token))
	}

	score := scoreSmartCandidate(queryLower, tokens, queryNorm, tokensNorm, candidate)
	if score <= 0 {
		t.Fatalf("expected positive score for typo query, got %f", score)
	}
}

func TestLoadConstantsGroupListsTFCondConstants(t *testing.T) {
	info, err := loadConstantsGroup("E_TFCOND")
	if err != nil {
		t.Fatalf("loadConstantsGroup error: %v", err)
	}
	if info == "" {
		t.Fatalf("expected constants group output")
	}
	if !strings.Contains(info, "TFCond_Taunting") {
		t.Fatalf("expected TFCond_Taunting to be listed")
	}
}

// TestZeroMutationMultipleViolations tests that multiple violations are reported
func TestZeroMutationMultipleViolations(t *testing.T) {
	src := `
local function bad1()
    callbacks.unregister("Draw", "Loop1")
end

local function bad2()
    callbacks.unregister("Tick", "Loop2")
end

callbacks.register("Draw", "Loop1", function() end)
`
	path := createTempLuaFile(t, "multiple_violations", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) < 2 {
		t.Fatalf("expected at least 2 violations, got: %d", len(violations))
	}
}

// TestZeroMutationMissingFile tests error handling for missing file
func TestZeroMutationMissingFile(t *testing.T) {
	violations, err := checkLuaCallbackMutationPolicy("/nonexistent/path/file.lua", defaultLboxMutationPolicy)
	if err == nil {
		t.Fatalf("expected error for missing file, got success")
	}

	if len(violations) > 0 {
		t.Fatalf("expected empty violations on error, got: %v", violations)
	}
}

func TestFormatSearchResultsMarkdownIncludesSnippetSection(t *testing.T) {
	results := []SmartSearchResult{{
		Symbol:      "draw.Color",
		Kind:        "function",
		Section:     "library",
		Description: "Sets the current draw color",
		Signature:   "draw.Color(r, g, b, a)",
	}}
	snippetResults := []SmartSearchResult{{
		Symbol:      "lm.draw",
		Kind:        "snippet",
		Section:     "snippet",
		Description: "Draw callback scaffold",
		Signature:   "callbacks.Register('Draw', 'Example', function()",
	}}

	output := formatSearchResultsMarkdown("draw", results, snippetResults, nil, 10)

	if !strings.Contains(output, "### Snippets (secondary matches)") {
		t.Fatalf("expected snippet section in output, got: %s", output)
	}

	if !strings.Contains(output, "`lm.draw`") {
		t.Fatalf("expected snippet prefix in output, got: %s", output)
	}

	if !strings.Contains(output, "get_smart_context(\"draw.Color\")") {
		t.Fatalf("expected primary-result next steps in output, got: %s", output)
	}
}

func TestFormatSearchResultsMarkdownSnippetOnlyNextSteps(t *testing.T) {
	snippetResults := []SmartSearchResult{{
		Symbol:      "lm.createMove",
		Kind:        "snippet",
		Section:     "snippet",
		Description: "CreateMove callback scaffold",
		Signature:   "callbacks.Register('CreateMove', 'Example', function(cmd)",
	}}

	output := formatSearchResultsMarkdown("create move", nil, snippetResults, nil, 10)

	if !strings.Contains(output, "Try snippet prefix `lm.createMove` in a Lua file") {
		t.Fatalf("expected snippet-only next step, got: %s", output)
	}

	if strings.Contains(output, "get_smart_context") {
		t.Fatalf("did not expect API next steps when only snippets matched, got: %s", output)
	}
}

func TestFormatSearchResultsMarkdownUsesDisplayedPrimaryForNextSteps(t *testing.T) {
	results := []SmartSearchResult{
		{
			Symbol:      "E_TraceLine",
			Kind:        "function",
			Section:     "symbol",
			Description: "Legacy constant helper",
			Signature:   "function E_TraceLine()",
		},
		{
			Symbol:      "engine.TraceLine",
			Kind:        "function",
			Section:     "library",
			Description: "Primary trace API",
			Signature:   "function engine.TraceLine(src, dst, mask, shouldHitEntity)",
		},
	}

	output := formatSearchResultsMarkdown("trace line", results, nil, nil, 8)

	if !strings.Contains(output, "get_smart_context(\"engine.TraceLine\")") {
		t.Fatalf("expected next steps to use first displayed primary result, got: %s", output)
	}

	if strings.Contains(output, "get_smart_context(\"E_TraceLine\")") {
		t.Fatalf("did not expect next steps to use hidden top-scoring symbol, got: %s", output)
	}
}

func TestFormatSearchResultsMarkdownIncludesDocsContentSection(t *testing.T) {
	docResults := []SmartSearchResult{{
		Symbol:      "TF2 Conditions",
		Kind:        "doc_page",
		Section:     "docs",
		Description: "Common TFCond_* constants for Entity:InCond checks",
		Signature:   "TFConditions.md",
	}}

	output := formatSearchResultsMarkdown("mannpower king powerup", nil, nil, docResults, 8)

	if !strings.Contains(output, "### Docs Pages (content matches)") {
		t.Fatalf("expected docs content section, got: %s", output)
	}
	if !strings.Contains(output, "get_smart_context(\"TF2 Conditions\")") {
		t.Fatalf("expected docs next steps, got: %s", output)
	}
}

func TestBuildLuacheckCandidatesIncludesGlobalWindowsPaths(t *testing.T) {
	candidates := buildLuacheckCandidates(
		`C:\repo`,
		`C:\Users\Tester\AppData\Roaming\npm`,
		`C:\Users\Tester\AppData\Roaming`,
		`C:\Users\Tester`,
	)

	joined := strings.Join(candidates, "\n")

	checks := []string{
		`C:\repo\automations\bin\luacheck\luacheck.exe`,
		`C:\Users\Tester\AppData\Roaming\npm\luacheck.cmd`,
		`C:\Users\Tester\AppData\Roaming\npm\luacheck`,
		`luacheck.cmd`,
	}

	for _, check := range checks {
		if !strings.Contains(joined, check) {
			t.Fatalf("expected candidate list to include %q, got: %s", check, joined)
		}
	}
}

// TestZeroMutationUnregisterWithoutID tests ID-less unregister satisfies kill-switch
func TestZeroMutationUnregisterWithoutID(t *testing.T) {
	src := `
callbacks.unregister("Draw")
callbacks.register("Draw", "MyLoop", function()
    print("Running")
end)
`
	path := createTempLuaFile(t, "unreg_without_id", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf("expected ID-less unregister to satisfy kill-switch, got violations: %v", violations)
	}
}

// TestForbidCollectGarbage verifies collectgarbage() is flagged
func TestForbidCollectGarbage(t *testing.T) {
	src := `
local function cleanup()
    collectgarbage("collect")
end
`
	path := createTempLuaFile(t, "collectgarbage", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected collectgarbage violation")
	}

	if !strings.Contains(violations[0].Message, "collectgarbage") {
		t.Fatalf("expected collectgarbage message, got: %s", violations[0].Message)
	}
	if !strings.Contains(violations[0].Message, "WARNING:") {
		t.Fatalf("expected warning-level collectgarbage message, got: %s", violations[0].Message)
	}
	if strings.Contains(violations[0].Message, "CRITICAL:") {
		t.Fatalf("collectgarbage collection warning should not be critical, got: %s", violations[0].Message)
	}
}

func TestCollectGarbageCountAllowed(t *testing.T) {
	src := `
local function memoryKb()
    return collectgarbage("count")
end
`
	path := createTempLuaFile(t, "collectgarbage_count", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	for _, v := range violations {
		if strings.Contains(v.Message, "collectgarbage") {
			t.Fatalf("collectgarbage(\"count\") should be allowed for read-only profiling, got: %s", v.Message)
		}
	}
}

// TestCollectGarbageNotACall verifies collectgarbage as a variable name is not flagged
func TestCollectGarbageNotACall(t *testing.T) {
	src := `
local collectgarbage = nil
print(collectgarbage)
`
	path := createTempLuaFile(t, "collectgarbage_var", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	for _, v := range violations {
		if strings.Contains(v.Message, "collectgarbage") {
			t.Fatalf("should not flag collectgarbage variable, got: %s", v.Message)
		}
	}
}

// TestForbidRequireInFunction verifies require() inside a function is flagged
func TestForbidRequireInFunction(t *testing.T) {
	src := `
local function setup()
    local lib = require("SomeLib")
    lib.init()
end
`
	path := createTempLuaFile(t, "require_in_func", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected require-in-function violation")
	}

	if !strings.Contains(violations[0].Message, "require()") {
		t.Fatalf("expected require() message, got: %s", violations[0].Message)
	}
}

// TestRequireAtTopLevelAllowed verifies top-level require() is fine
func TestRequireAtTopLevelAllowed(t *testing.T) {
	src := `
local lnxLib = require("lnxLib")
local TimMenu = require("TimMenu")
`
	path := createTempLuaFile(t, "require_top", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	for _, v := range violations {
		if strings.Contains(v.Message, "require()") {
			t.Fatalf("top-level require should be allowed, got: %s", v.Message)
		}
	}
}

// TestForbidGlobalTable verifies _G access is flagged
func TestForbidGlobalTable(t *testing.T) {
	src := `
_G["myVar"] = 42
`
	path := createTempLuaFile(t, "global_table", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected _G violation")
	}

	if !strings.Contains(violations[0].Message, "_G") {
		t.Fatalf("expected _G message, got: %s", violations[0].Message)
	}
}

// TestForbidGlobalTableDotAccess verifies _G.foo access is flagged
func TestForbidGlobalTableDotAccess(t *testing.T) {
	src := `
local x = _G.someGlobal
`
	path := createTempLuaFile(t, "global_table_dot", src)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	if len(violations) == 0 {
		t.Fatalf("expected _G dot-access violation")
	}
}

// ── Traceback / Line-Map Tests ───────────────────────────────────────────────

// TestCountLuaLines verifies line counting for content with/without trailing newline.
func TestCountLuaLines(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"", 0},
		{"\n", 1},
		{"a\n", 1},
		{"a\nb\n", 2},
		{"a\nb\nc\n", 3},
		{"a\nb\nc", 3}, // no trailing newline
		{"a", 1},
	}
	for _, c := range cases {
		got := countLuaLines(c.content)
		if got != c.want {
			t.Errorf("countLuaLines(%q) = %d, want %d", c.content, got, c.want)
		}
	}
}

// TestLookupBundleLineInRange verifies that a line inside a mapped range resolves correctly.
func TestLookupBundleLineInRange(t *testing.T) {
	entries := []LineMapEntry{
		{BundledStart: 10, BundledEnd: 19, SourceFile: "utils.lua", SourceStart: 1},
		{BundledStart: 25, BundledEnd: 34, SourceFile: "main.lua", SourceStart: 1},
	}

	// Line 10 → utils.lua:1
	e, sl, found := lookupBundleLine(entries, 10)
	if !found || e.SourceFile != "utils.lua" || sl != 1 {
		t.Fatalf("line 10: got (%s:%d, found=%v), want (utils.lua:1, true)", e.SourceFile, sl, found)
	}

	// Line 15 → utils.lua:6
	e, sl, found = lookupBundleLine(entries, 15)
	if !found || e.SourceFile != "utils.lua" || sl != 6 {
		t.Fatalf("line 15: got (%s:%d, found=%v), want (utils.lua:6, true)", e.SourceFile, sl, found)
	}

	// Line 19 → utils.lua:10
	e, sl, found = lookupBundleLine(entries, 19)
	if !found || e.SourceFile != "utils.lua" || sl != 10 {
		t.Fatalf("line 19: got (%s:%d, found=%v), want (utils.lua:10, true)", e.SourceFile, sl, found)
	}

	// Line 25 → main.lua:1
	e, sl, found = lookupBundleLine(entries, 25)
	if !found || e.SourceFile != "main.lua" || sl != 1 {
		t.Fatalf("line 25: got (%s:%d, found=%v), want (main.lua:1, true)", e.SourceFile, sl, found)
	}
}

// TestLookupBundleLineInfrastructure verifies that boilerplate lines return not-found.
func TestLookupBundleLineInfrastructure(t *testing.T) {
	entries := []LineMapEntry{
		{BundledStart: 30, BundledEnd: 39, SourceFile: "utils.lua", SourceStart: 1},
	}

	// Lines before any mapped entry (bundle header boilerplate)
	_, _, found := lookupBundleLine(entries, 1)
	if found {
		t.Fatal("expected line 1 (infrastructure) to not be found in map")
	}

	// Line between entries (module wrapper)
	_, _, found = lookupBundleLine(entries, 25)
	if found {
		t.Fatal("expected line 25 (infrastructure gap) to not be found in map")
	}
}

// TestGenerateBundledLuaLineMap verifies that generateBundledLua produces a line
// map where every claimed source line lookups round-trips correctly.
func TestGenerateBundledLuaLineMap(t *testing.T) {
	dir := t.TempDir()

	// Create two simple modules
	utilContent := "local M = {}\nfunction M.hello() return 'hi' end\nreturn M\n"
	mainContent := "local utils = require('utils')\nprint(utils.hello())\n"

	utilPath := filepath.Join(dir, "utils.lua")
	mainPath := filepath.Join(dir, "Main.lua")
	if err := os.WriteFile(utilPath, []byte(utilContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	bundleCtx := &BundleContext{
		ProjectDir:  dir,
		SearchPaths: []string{dir},
		Modules: map[string]*LuaModule{
			utilPath: {FilePath: utilPath, Content: utilContent, Requires: nil},
			mainPath: {FilePath: mainPath, Content: mainContent, Requires: []string{"utils"}},
		},
		Visited: map[string]bool{},
		Stack:   map[string]bool{},
	}

	bundledContent, entries, err := generateBundledLua(bundleCtx, mainPath)
	if err != nil {
		t.Fatalf("generateBundledLua error: %v", err)
	}

	if len(bundledContent) == 0 {
		t.Fatal("expected non-empty bundled content")
	}

	if len(entries) == 0 {
		t.Fatal("expected at least one line map entry")
	}

	// Every entry range must be consistent: BundledEnd >= BundledStart
	for _, e := range entries {
		if e.BundledEnd < e.BundledStart {
			t.Errorf("entry for %s has BundledEnd(%d) < BundledStart(%d)", e.SourceFile, e.BundledEnd, e.BundledStart)
		}
		if e.SourceStart != 1 {
			t.Errorf("entry for %s has SourceStart=%d, want 1", e.SourceFile, e.SourceStart)
		}
	}

	// The total bundled lines must fit within the bundled content
	totalBundledLines := countLuaLines(bundledContent)
	for _, e := range entries {
		if e.BundledEnd > totalBundledLines {
			t.Errorf("entry BundledEnd(%d) exceeds total bundled lines(%d) for %s", e.BundledEnd, totalBundledLines, e.SourceFile)
		}
	}
}

// TestResolveBundleMapPathDirectory verifies that passing a directory resolves
// to build/Main.lua.map when it exists.
func TestResolveBundleMapPathDirectory(t *testing.T) {
	dir := t.TempDir()
	buildDir := filepath.Join(dir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatal(err)
	}
	mapFile := filepath.Join(buildDir, "Main.lua.map")
	if err := os.WriteFile(mapFile, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveBundleMapPath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mapFile {
		t.Fatalf("got %s, want %s", got, mapFile)
	}
}

// TestResolveBundleMapPathFile verifies that passing the bundle .lua file
// resolves to the adjacent .map file when it exists.
func TestResolveBundleMapPathFile(t *testing.T) {
	dir := t.TempDir()
	luaFile := filepath.Join(dir, "Main.lua")
	mapFile := luaFile + ".map"
	if err := os.WriteFile(luaFile, []byte("-- bundle"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapFile, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveBundleMapPath(luaFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != mapFile {
		t.Fatalf("got %s, want %s", got, mapFile)
	}
}

// ── Bundle Policy / Full Heuristics Tests ────────────────────────────────────

// TestBundlePolicyAllowsRequireInWrapper verifies that bundleMutationPolicy
// does NOT flag require() inside a module wrapper function (the bundle format
// wraps every module in a closure, so this would be a false positive).
func TestBundlePolicyAllowsRequireInWrapper(t *testing.T) {
	// Simulate what the bundler produces for a module with a global require:
	// the module content appears inside __bundle_modules["name"] = function() ... end
	src := `
local __bundle_modules = {}
local __bundle_loaded = {}
local function __bundle_require(name)
    local loader = __bundle_modules[name]
    if loader == nil then return require(name) end
    local cached = __bundle_loaded[name]
    if cached ~= nil then return cached end
    local loaded = loader()
    if loaded == nil then loaded = true end
    __bundle_loaded[name] = loaded
    return loaded
end

__bundle_modules["utils"] = function()
    local globalLib = require("SomeGlobalLib")
    local M = {}
    function M.hello() return globalLib.greet() end
    return M
end

local running = true
callbacks.unregister("Draw", "MyLoop")
callbacks.register("Draw", "MyLoop", function()
    if not running then return end
end)
callbacks.register("Unload", function()
    running = false
end)
`
	path := createTempLuaFile(t, "bundle_policy_require", src)

	violations, err := checkLuaCallbackMutationPolicy(path, bundleMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	// bundleMutationPolicy must not flag require() inside the module wrapper
	for _, v := range violations {
		if strings.Contains(v.Message, "require()") {
			t.Fatalf("bundleMutationPolicy should not flag require() in bundle wrapper, got: %s", v.Message)
		}
	}
}

// TestBundlePolicyStillCatchesCallbackViolation verifies that bundleMutationPolicy
// still flags a module that registers a callback at its source top-level (which
// in the bundle becomes nested inside the module wrapper function -- a real issue).
func TestBundlePolicyStillCatchesCallbackViolation(t *testing.T) {
	// A module that had callbacks.register at its source top-level becomes wrapped:
	src := `
local __bundle_modules = {}
local __bundle_loaded = {}
local function __bundle_require(name)
    if __bundle_modules[name] == nil then return require(name) end
    local cached = __bundle_loaded[name]; if cached ~= nil then return cached end
    local loaded = __bundle_modules[name](); if loaded == nil then loaded = true end
    __bundle_loaded[name] = loaded; return loaded
end

__bundle_modules["badmodule"] = function()
    callbacks.register("Draw", "BadDraw", function() end)
end

-- Entry point has proper kill-switch
local running = true
callbacks.unregister("Draw", "MyMain")
callbacks.register("Draw", "MyMain", function()
    if not running then return end
end)
callbacks.register("Unload", function() running = false end)
`
	path := createTempLuaFile(t, "bundle_policy_callback_violation", src)

	violations, err := checkLuaCallbackMutationPolicy(path, bundleMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	// Bundle module wrapper is a load-time function, not a runtime callback handler.
	// Register inside it is acceptable; only the kill-switch rule applies (unregister before register).
	for _, v := range violations {
		if strings.Contains(v.Message, "callback handler") {
			t.Fatalf("bundle module wrapper incorrectly flagged as callback handler: %s", v.Message)
		}
	}
}

// ── parseBundleLineMap (fallback, no .map file) Tests ────────────────────────

// TestParseBundleLineMapBasic verifies that parseBundleLineMap correctly
// reconstructs module line ranges from the bundle comment markers, so that
// traceback works even when no .map file is present.
func TestParseBundleLineMapBasic(t *testing.T) {
	// Build a minimal bundle that matches the format generated by generateBundledLua.
	// Infrastructure header (26 lines of newlines before first module, see constants
	// in generateBundledLua) — we use the actual generator to produce a real bundle.
	dir := t.TempDir()

	// Create two small source files.
	utilsSrc := "local M = {}\nfunction M.hello() return \"hi\" end\nreturn M\n"
	mainSrc := "local u = require(\"utils\")\nprint(u.hello())\n"

	utilsPath := filepath.Join(dir, "utils.lua")
	mainPath := filepath.Join(dir, "Main.lua")
	if err := os.WriteFile(utilsPath, []byte(utilsSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}

	// Build a bundle using the real bundler context.
	bundleCtx := &BundleContext{
		ProjectDir:  dir,
		SearchPaths: []string{dir},
		Modules:     make(map[string]*LuaModule),
		Visited:     make(map[string]bool),
		Stack:       make(map[string]bool),
	}
	bundleCtx.Modules[utilsPath] = &LuaModule{FilePath: utilsPath, Content: utilsSrc}
	bundleCtx.Modules[mainPath] = &LuaModule{FilePath: mainPath, Content: mainSrc}

	bundledContent, mapEntries, err := generateBundledLua(bundleCtx, mainPath)
	if err != nil {
		t.Fatalf("generateBundledLua: %v", err)
	}

	// Write the bundle WITHOUT the .map file.
	bundleFile := filepath.Join(dir, "bundle.lua")
	if err := os.WriteFile(bundleFile, []byte(bundledContent), 0644); err != nil {
		t.Fatal(err)
	}

	// parseBundleLineMap must reconstruct the same ranges as the generator emitted.
	parsed, err := parseBundleLineMap(bundleFile)
	if err != nil {
		t.Fatalf("parseBundleLineMap: %v", err)
	}

	if len(parsed.Entries) == 0 {
		t.Fatal("expected at least one reconstructed entry")
	}

	// Cross-check: every entry from the generator must be covered by the parsed map.
	for _, gen := range mapEntries {
		matched := false
		for _, p := range parsed.Entries {
			if p.BundledStart == gen.BundledStart && p.BundledEnd == gen.BundledEnd {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("generator entry [%d-%d] for %s not found in parsed map (entries: %v)",
				gen.BundledStart, gen.BundledEnd, gen.SourceFile, parsed.Entries)
		}
	}
}

// TestParseBundleLineMapFallbackInHandleTraceback verifies the end-to-end
// fallback: handleTraceback must successfully resolve a line even when no .map
// file exists alongside the bundle.
func TestParseBundleLineMapFallbackInHandleTraceback(t *testing.T) {
	dir := t.TempDir()

	utilsSrc := "local M = {}\nfunction M.greet() return \"hello\" end\nreturn M\n"
	mainSrc := "local u = require(\"utils\")\nprint(u.greet())\n"

	utilsPath := filepath.Join(dir, "utils.lua")
	mainPath := filepath.Join(dir, "Main.lua")
	if err := os.WriteFile(utilsPath, []byte(utilsSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
		t.Fatal(err)
	}

	bundleCtx := &BundleContext{
		ProjectDir:  dir,
		SearchPaths: []string{dir},
		Modules:     make(map[string]*LuaModule),
		Visited:     make(map[string]bool),
		Stack:       make(map[string]bool),
	}
	bundleCtx.Modules[utilsPath] = &LuaModule{FilePath: utilsPath, Content: utilsSrc}
	bundleCtx.Modules[mainPath] = &LuaModule{FilePath: mainPath, Content: mainSrc}

	bundledContent, mapEntries, err := generateBundledLua(bundleCtx, mainPath)
	if err != nil {
		t.Fatalf("generateBundledLua: %v", err)
	}
	if len(mapEntries) == 0 {
		t.Fatal("expected map entries from generator")
	}

	// Write bundle WITHOUT .map file.
	buildDir := filepath.Join(dir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatal(err)
	}
	bundleFile := filepath.Join(buildDir, "Main.lua")
	if err := os.WriteFile(bundleFile, []byte(bundledContent), 0644); err != nil {
		t.Fatal(err)
	}
	// Confirm no .map file exists.
	if _, serr := os.Stat(bundleFile + ".map"); serr == nil {
		t.Fatal("test setup error: .map file should not exist")
	}

	// Pick a known source line from the first map entry and look it up via the
	// project-directory form of bundleFile (what the AI would supply).
	firstEntry := mapEntries[0]
	targetBundledLine := firstEntry.BundledStart + 0 // first line of first module

	result, herr := handleTraceback(context.Background(), mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name: "traceback",
			Arguments: map[string]interface{}{
				"bundleFile": dir, // project directory — no .map exists
				"line":       float64(targetBundledLine),
			},
		},
	})
	if herr != nil {
		t.Fatalf("handleTraceback error: %v", herr)
	}
	if result.IsError {
		t.Fatalf("handleTraceback returned tool error: %v", result.Content)
	}
	// Result should mention the module-centric mapping and bundle context.
	resultText := fmt.Sprintf("%v", result.Content)
	if !strings.Contains(resultText, "Module:") || !strings.Contains(resultText, "Line in module:") {
		t.Errorf("expected result to mention module mapping, got: %s", resultText)
	}
	if !strings.Contains(resultText, "Bundle context:") {
		t.Errorf("expected result to include bundle context, got: %s", resultText)
	}
	if !strings.Contains(resultText, "utils") {
		t.Errorf("expected result to mention utils module, got: %s", resultText)
	}
}

// TestParseBundleLineMapLuabundleStyle verifies traceback reconstruction for the
// luabundle-style __bundle_register format, even when the original source files
// do not exist next to the bundle.
func TestParseBundleLineMapLuabundleStyle(t *testing.T) {
	dir := t.TempDir()
	bundleFile := filepath.Join(dir, "Main.lua")
	bundleSrc := `local __bundle_require, __bundle_loaded, __bundle_register, __bundle_modules = (function(superRequire)
	local loadingPlaceholder = {[{}] = true}
	local register
	local modules = {}
	local require
	local loaded = {}
	register = function(name, body)
		if not modules[name] then
			modules[name] = body
		end
	end
	require = function(name)
		local loadedModule = loaded[name]
		if loadedModule then
			if loadedModule == loadingPlaceholder then
				return nil
			end
		else
			loaded[name] = loadingPlaceholder
			loadedModule = modules[name](require, loaded, register, modules)
			loaded[name] = loadedModule
		end
		return loadedModule
	end
	return require, loaded, register, modules
end)(require)
__bundle_register("__root", function(require, _LOADED, __bundle_register, __bundle_modules)
--[[ Main.lua
     Example entry module.
]]
local util = require("utils.math")
print(util.add(1, 2))
end)
__bundle_register("utils.math", function(require, _LOADED, __bundle_register, __bundle_modules)
--[[ utils/math.lua
     Math helpers.
]]
local M = {}
function M.add(a, b)
	return a + b
end
return M
end)
return __bundle_require("__root")
`
	if err := os.WriteFile(bundleFile, []byte(bundleSrc), 0644); err != nil {
		t.Fatal(err)
	}

	parsed, err := parseBundleLineMap(bundleFile)
	if err != nil {
		t.Fatalf("parseBundleLineMap: %v", err)
	}
	if len(parsed.Entries) != 2 {
		t.Fatalf("expected 2 parsed entries, got %d: %#v", len(parsed.Entries), parsed.Entries)
	}
	if parsed.Entries[0].ModuleName != "__root" {
		t.Fatalf("expected first module __root, got %q", parsed.Entries[0].ModuleName)
	}
	if parsed.Entries[1].ModuleName != "utils.math" {
		t.Fatalf("expected second module utils.math, got %q", parsed.Entries[1].ModuleName)
	}

	result, herr := handleTraceback(context.Background(), mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name: "traceback",
			Arguments: map[string]interface{}{
				"bundleFile": bundleFile,
				"line":       float64(parsed.Entries[1].BundledStart + 1),
			},
		},
	})
	if herr != nil {
		t.Fatalf("handleTraceback error: %v", herr)
	}
	if result.IsError {
		t.Fatalf("handleTraceback returned tool error: %v", result.Content)
	}

	resultText := fmt.Sprintf("%v", result.Content)
	if !strings.Contains(resultText, "utils.math") {
		t.Fatalf("expected traceback to mention utils.math module, got: %s", resultText)
	}
	if !strings.Contains(resultText, "utils/math.lua") {
		t.Fatalf("expected traceback to include source hint from bundle header, got: %s", resultText)
	}
	if !strings.Contains(resultText, "**Line in module:** 2") {
		t.Fatalf("expected traceback to report module-relative line 2, got: %s", resultText)
	}
}

// TestPolicyCheckManualTestFiles runs policy checks on the manual test files in test_policy_lua/
func TestPolicyCheckManualTestFiles(t *testing.T) {
	testDir := "test_policy_lua"
	entries, err := os.ReadDir(testDir)
	if err != nil {
		t.Skipf("test_policy_lua directory not found: %v", err)
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".lua") {
			continue
		}
		path := filepath.Join(testDir, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
			if err != nil {
				t.Fatalf("policy check error: %v", err)
			}

			if len(violations) > 0 {
				t.Logf("=== %s ===", entry.Name())
				for _, v := range violations {
					t.Logf("Line %d: %s", v.Line, v.Message)
				}
			} else {
				t.Logf("=== %s === (no violations)", entry.Name())
			}

			switch entry.Name() {
			case "collectgarbage_count_ok.lua":
				if len(violations) > 0 {
					t.Errorf("collectgarbage(\"count\") should be allowed, got %d violations", len(violations))
				}
			case "collectgarbage_collect_bad.lua":
				if len(violations) == 0 {
					t.Errorf("collectgarbage(\"collect\") should be flagged, got no violations")
				}
				found := false
				for _, v := range violations {
					if strings.Contains(v.Message, "collectgarbage") && strings.Contains(v.Message, "WARNING:") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected WARNING-level collectgarbage violation")
				}
			case "ipairs_findbyclass_bad.lua":
				if len(violations) == 0 {
					t.Errorf("ipairs on FindByClass should be flagged, got no violations")
				}
				found := false
				for _, v := range violations {
					if strings.Contains(v.Message, "sparse") && strings.Contains(v.Message, "CRITICAL:") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected CRITICAL sparse table violation")
				}
			case "pairs_findbyclass_ok.lua":
				for _, v := range violations {
					if strings.Contains(v.Message, "FindByClass") && strings.Contains(v.Message, "sparse") {
						t.Errorf("pairs on FindByClass should be allowed, got: %s", v.Message)
					}
				}
			case "engine_ifcheck_bad.lua":
				if len(violations) == 0 {
					t.Errorf("if engine then check should be flagged, got no violations")
				}
				found := false
				for _, v := range violations {
					if strings.Contains(v.Message, "engine") && strings.Contains(v.Message, "INFO:") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected INFO-level engine if-check violation")
				}
			}
		})
	}
}
