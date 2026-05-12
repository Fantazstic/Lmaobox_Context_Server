package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test Summary:
// These tests verify the Zero-Mutation Lbox policy is:
// 1. STRICT ENOUGH for AI to reliably follow without loopholes
// 2. NOT OVERLY RIGID for human developers
//
// Acceptable patterns:
// - State control (local flags) in Unload callback
// - If/for/while/repeat blocks at depth 0 (they don't isolate)
// - Multiple separate callback pairs
// - Unregister without ID
//
// Forbidden patterns:
// - Unregister inside any function (including Unload, nested fns)
// - Register inside any function
// - Register without prior unregister at depth 0 (kill-switch)

func writeTempLua(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(os.TempDir(), "policy_test_"+name+".lua")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp lua file: %v", err)
	}
	return path
}

func TestUnregisterInsideFunction(t *testing.T) {
	src := `
local function stop()
    callbacks.unregister("Draw", "MyLoop")
end

callbacks.register("MyLoop", "MyLoop", function() end)
`
	path := writeTempLua(t, "unregister_in_fn", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatalf("expected violations for unregister inside function, got none")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "Illegal Unregister") || strings.Contains(v.Message, "callbacks.Unregister must be declared at depth 0") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Illegal Unregister violation, got: %+v", violations)
	}
}

func TestKillSwitchViolation(t *testing.T) {
	src := `
callbacks.register("Draw", "MyLoop", function() end)
`
	path := writeTempLua(t, "kill_switch_violation", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatalf("expected kill-switch violation for register without prior unregister, got none")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "Kill-Switch violation") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Kill-Switch violation, got: %+v", violations)
	}
}

func TestValidUnregisterThenRegister(t *testing.T) {
	src := `
callbacks.unregister("Draw", "MyLoop")
callbacks.register("Draw", "MyLoop", function() end)
`
	path := writeTempLua(t, "valid_kill_switch", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for valid unregister->register, got: %+v", violations)
	}
}

func TestValidUnregisterThenRegisterAfterFunctionDef(t *testing.T) {
	// This tests the exact pattern from silent_aim.lua:
	// A function definition, followed immediately by module-level
	// callbacks.Unregister and callbacks.Register. Both calls are at depth 0.
	src := `
local function onFrameStage(stage)
	if stage ~= 7 then
		return
	end
end

callbacks.Unregister("FrameStageNotify", "CD_SilentAim_FSN")
callbacks.Register("FrameStageNotify", "CD_SilentAim_FSN", onFrameStage)
`
	path := writeTempLua(t, "unregister_after_function_def", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for module-level unregister->register after function def, got: %+v", violations)
	}
}

func TestTopLevelRegisterAfterFunctionBlock(t *testing.T) {
	src := `
local function onEvent(event)
    if event == nil then
        return
    end
end

callbacks.unregister("FireGameEvent", "Database_Events")
callbacks.register("FireGameEvent", "Database_Events", onEvent)
`
	path := writeTempLua(t, "top_level_after_function", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for top-level callback pair, got: %+v", violations)
	}
}

func TestTopLevelRegisterAfterFunctionWithWhileDoBlock(t *testing.T) {
	src := `
local function update()
	while true do
		break
	end
end

callbacks.unregister("Draw", "MyLoop")
callbacks.register("Draw", "MyLoop", update)
`
	path := writeTempLua(t, "top_level_after_while_do", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for top-level callback pair after while/do, got: %+v", violations)
	}
}

func TestUnregisterInOnUnload(t *testing.T) {
	src := `
callbacks.register("Unload", function()
	callbacks.unregister("Draw", "MyLoop")
end)
`
	path := writeTempLua(t, "unload_unreg", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatalf("expected violations for unregister inside OnUnload, got none")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "Illegal Unregister") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Illegal Unregister violation, got: %+v", violations)
	}
}

func TestRunLuacheckIntegration(t *testing.T) {
	src := `
local unused = 123
`
	path := writeTempLua(t, "luacheck_test", src)

	issues, err := runLuacheck(context.Background(), path)
	if err != nil {
		if errors.Is(err, errLuacheckNotFound) {
			t.Skip("luacheck not installed; skipping integration test")
		}
		t.Fatalf("luacheck execution error: %v", err)
	}

	if len(issues) == 0 {
		t.Fatalf("expected luacheck to report issues for unused variable, got none")
	}
}

// ── Tests for Balanced Rigidity ──────────────────────────────────────────────

func TestGhostPatternApproved(t *testing.T) {
	// "Ghost Pattern" from user: State control (running flag) inside Unload is OK.
	// This should NOT trigger any violations.
	src := `
local running = true

callbacks.register("Unload", function()
    running = false -- Safe: Just changing a variable
end)

callbacks.register("MyMainLoop", function()
    if not running then return end
    -- ... core logic ...
end)
`
	path := writeTempLua(t, "ghost_pattern_approved", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for Ghost Pattern (state control), got: %+v", violations)
	}
}

func TestIfBlockDoesNotIncrementDepth(t *testing.T) {
	// If/for/while blocks should NOT increment functionDepth.
	// Register inside if block at depth 0 should violate kill-switch, not register depth.
	src := `
if condition then
    callbacks.register("Draw", "MyLoop", function() end)
end
`
	path := writeTempLua(t, "if_block_depth", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	// Should report kill-switch violation, not "register at depth 0" violation
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "Kill-Switch violation") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Kill-Switch violation (if blocks don't increment depth), got: %+v", violations)
	}
}

func TestRepeatUntilBlockAllowed(t *testing.T) {
	// Repeat/until is a loop, but should not prevent register/unregister at depth 0.
	// However, if unregister is INSIDE repeat, it should fail.
	src := `
repeat
    callbacks.register("Draw", "MyLoop", function() end)
until false
`
	path := writeTempLua(t, "repeat_block", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	// Register inside repeat block = depth 0 semantically (not in a function),
	// so it should only report kill-switch, not "register at depth 0".
	for _, v := range violations {
		if strings.Contains(v.Message, "callbacks.Register inside a callback handler") {
			t.Fatalf("repeat block incorrectly flagged as callback handler")
		}
	}
}

func TestMultipleSeparateCallbacks(t *testing.T) {
	// Different event/ID combos can be registered independently.
	// Each should have its own kill-switch check.
	src := `
callbacks.unregister("Draw", "Loop1")
callbacks.register("Draw", "Loop1", function() end)

callbacks.unregister("Tick", "Loop2")
callbacks.register("Tick", "Loop2", function() end)
`
	path := writeTempLua(t, "multi_callbacks", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for separate callback pairs, got: %+v", violations)
	}
}

func TestUnregisterWithoutIDAllowed(t *testing.T) {
	// Some engines allow callbacks.unregister("EventName") without ID.
	// This is at depth 0 so should pass kill-switch check (no ID to check).
	src := `
callbacks.unregister("Draw")
callbacks.register("Draw", "MyLoop", function() end)
`
	path := writeTempLua(t, "unregister_no_id", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for unregister without ID, got: %+v", violations)
	}
}

func TestNestedFunctionCallbacks(t *testing.T) {
	// A callbacks.Register inside a load-time helper function (not a callback handler)
	// is acceptable — only registering from within a running callback is forbidden.
	src := `
local function setup()
    local function innerRegister()
        callbacks.register("Draw", "MyLoop", function() end)
    end
    innerRegister()
end
setup()
`
	path := writeTempLua(t, "nested_function", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	// Depth > 0 but NOT inside a callback handler => no Register violation expected.
	for _, v := range violations {
		if strings.Contains(v.Message, "callbacks.Register inside a callback handler") {
			t.Fatalf("false positive: load-time helper function incorrectly flagged as callback handler: %+v", violations)
		}
	}
}

func TestCommentedOutUnregister(t *testing.T) {
	// Comments should be stripped; this code has unregister commented out.
	// The register should fail kill-switch because unregister is not "real".
	src := `
-- callbacks.unregister("Draw", "MyLoop")
callbacks.register("Draw", "MyLoop", function() end)
`
	path := writeTempLua(t, "commented_unregister", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	// Should report kill-switch violation
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "Kill-Switch violation") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Kill-Switch violation (comment was stripped), got: %+v", violations)
	}
}

func TestCreateFontInsideFunction(t *testing.T) {
	// draw.CreateFont inside a function creates a permanent font handle on every call.
	// This floods VGUI font memory and crashes the game.
	src := `
local font

local function setupFont()
    font = draw.CreateFont("Arial", 14, 400)
end
`
	path := writeTempLua(t, "createfont_in_fn", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.CreateFont") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected draw.CreateFont violation, got: %+v", violations)
	}
}

func TestCreateFontAtDepthZeroAllowed(t *testing.T) {
	// draw.CreateFont at module level (depth 0) is the correct pattern — cache the handle once.
	src := `
local font = draw.CreateFont("Arial", 14, 400)

callbacks.unregister("Draw", "MyDraw")
callbacks.register("Draw", "MyDraw", function()
    draw.SetFont(font)
    draw.Text(10, 10, "hello")
end)
`
	path := writeTempLua(t, "createfont_depth0", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.CreateFont") {
			t.Fatalf("unexpected draw.CreateFont violation at depth 0: %s", v.Message)
		}
	}
}

func TestLegacyBitLibraryRejected(t *testing.T) {
	src := `
local flags = bit.band(cmd.buttons, IN_ATTACK)
`
	path := writeTempLua(t, "legacy_bit", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "bit.band") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected bit.band violation, got: %+v", violations)
	}
}

func TestDrawTextWithoutSetFontRejected(t *testing.T) {
	src := `
callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    draw.Text(10, 10, "hello")
end)
`
	path := writeTempLua(t, "draw_text_no_font", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont() earlier in the same Draw callback") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected draw.Text font setup violation, got: %+v", violations)
	}
}

func TestDrawTextWithoutSetFontInSameHandlerRejected(t *testing.T) {
	src := `
local font = draw.CreateFont("Verdana", 16, 800)

local function setupFontElsewhere()
    draw.SetFont(font)
end

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    draw.Color(255, 255, 255, 255)
    draw.Text(10, 10, "hello")
end)
`
	path := writeTempLua(t, "draw_text_font_elsewhere", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont() earlier in the same Draw callback") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected same-handler draw.Text font setup violation, got: %+v", violations)
	}
}

func TestTexturedPolygonWithoutColorRejected(t *testing.T) {
	src := `
local textureId = 1

callbacks.unregister("Draw", "poly")
callbacks.register("Draw", "poly", function()
    draw.TexturedPolygon(textureId, {
        { 10, 10, 0.0, 0.0 },
        { 20, 10, 1.0, 0.0 },
        { 20, 20, 1.0, 1.0 },
        { 10, 20, 0.0, 1.0 }
    }, true)
end)
`
	path := writeTempLua(t, "textured_polygon_no_color", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.TexturedPolygon() requires draw.Color() earlier in the same Draw callback") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected draw.TexturedPolygon color setup violation, got: %+v", violations)
	}
}

func TestNamedDrawHandlerWithFontAndColorAllowed(t *testing.T) {
	src := `
local font = draw.CreateFont("Verdana", 16, 800)

local function OnDraw()
    draw.SetFont(font)
    draw.Color(255, 255, 255, 255)
    draw.Text(10, 10, "hello")
end

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", OnDraw)
`
	path := writeTempLua(t, "named_draw_handler_ok", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont()") || strings.Contains(v.Message, "draw.Text() requires draw.Color()") {
			t.Fatalf("unexpected draw state violation: %+v", violations)
		}
	}
}

func TestDrawTextWithStaticallyInvalidFontRejected(t *testing.T) {
	src := `
local font = nil

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(font)
    draw.Text(10, 10, "hello")
end)
`
	path := writeTempLua(t, "invalid_font_handle", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	foundInvalidSetFont := false
	foundMissingFont := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.SetFont() uses a font handle that is statically known invalid") {
			foundInvalidSetFont = true
		}
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont() earlier in the same Draw callback") {
			foundMissingFont = true
		}
	}
	if !foundInvalidSetFont || !foundMissingFont {
		t.Fatalf("expected invalid draw.SetFont and follow-up draw.Text font violation, got: %+v", violations)
	}
}

func TestDrawTextWithUnknownFontSourceAllowed(t *testing.T) {
	src := `
callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(shared_font)
    draw.Text(10, 10, "hello")
end)
`
	path := writeTempLua(t, "unknown_font_source", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.SetFont() uses a font handle that is statically known invalid") || strings.Contains(v.Message, "draw.Text() requires draw.SetFont()") {
			t.Fatalf("unknown font source should be accepted for static analysis, got: %+v", violations)
		}
	}
}

func TestDrawTextWithBuiltinFontAllowed(t *testing.T) {
	src := `
callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(Fonts.Verdana)
    draw.Text(10, 10, "hello")
end)
`
	path := writeTempLua(t, "builtin_font_ok", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.SetFont() uses a font handle that is statically known invalid") || strings.Contains(v.Message, "draw.Text() requires draw.SetFont()") {
			t.Fatalf("builtin font should be accepted, got: %+v", violations)
		}
	}
}

func TestHelperDrawCallWithoutStateRejected(t *testing.T) {
	src := `
local function DrawStuff()
    draw.TexturedPolygon(1, {
        { 10, 10, 0.0, 0.0 },
        { 20, 10, 1.0, 0.0 },
        { 20, 20, 1.0, 1.0 },
        { 10, 20, 0.0, 1.0 }
    }, true)
    draw.Text(10, 10, "hello")
end

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    DrawStuff()
end)
`
	path := writeTempLua(t, "helper_draw_no_state", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	foundColor := false
	foundFont := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.TexturedPolygon() requires draw.Color() earlier in the same Draw callback chain") {
			foundColor = true
		}
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont() earlier in the same Draw callback chain") {
			foundFont = true
		}
	}
	if !foundColor || !foundFont {
		t.Fatalf("expected helper-chain draw state violations, got: %+v", violations)
	}
}

func TestHelperDrawCallWithCallerStateAllowed(t *testing.T) {
	src := `
local font = draw.CreateFont("Verdana", 16, 800)

local function DrawStuff()
    draw.TexturedPolygon(1, {
        { 10, 10, 0.0, 0.0 },
        { 20, 10, 1.0, 0.0 },
        { 20, 20, 1.0, 1.0 },
        { 10, 20, 0.0, 1.0 }
    }, true)
    draw.Text(10, 10, "hello")
end

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(font)
    DrawStuff()
end)
`
	path := writeTempLua(t, "helper_draw_with_caller_state", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.TexturedPolygon() requires draw.Color()") || strings.Contains(v.Message, "draw.Text() requires draw.SetFont()") {
			t.Fatalf("caller state should flow into a unique helper chain, got: %+v", violations)
		}
	}
}

func TestSharedHelperDrawRequiresOwnState(t *testing.T) {
	src := `
local font = draw.CreateFont("Verdana", 16, 800)

local function SharedDraw()
    draw.Text(10, 10, "hello")
end

callbacks.unregister("Draw", "HUD1")
callbacks.register("Draw", "HUD1", function()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(font)
    SharedDraw()
end)

callbacks.unregister("Draw", "HUD2")
callbacks.register("Draw", "HUD2", function()
    SharedDraw()
end)
`
	path := writeTempLua(t, "shared_helper_requires_state", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont() earlier in the same Draw callback chain") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected shared helper to require its own font state, got: %+v", violations)
	}
}

func TestLinearHelperChainWithSetupAllowed(t *testing.T) {
	src := `
local font = draw.CreateFont("Verdana", 16, 800)

local function EmitDraw()
    draw.TexturedPolygon(1, {
        { 10, 10, 0.0, 0.0 },
        { 20, 10, 1.0, 0.0 },
        { 20, 20, 1.0, 1.0 },
        { 10, 20, 0.0, 1.0 }
    }, true)
    draw.Text(10, 10, "hello")
end

local function PrepareDraw()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(font)
    EmitDraw()
end

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    PrepareDraw()
end)
`
	path := writeTempLua(t, "linear_helper_chain_ok", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.TexturedPolygon() requires draw.Color()") || strings.Contains(v.Message, "draw.Text() requires draw.SetFont()") {
			t.Fatalf("linear helper chain should preserve prior setup, got: %+v", violations)
		}
	}
}

func TestLinearHelperChainWithLateSetupRejected(t *testing.T) {
	src := `
local font = draw.CreateFont("Verdana", 16, 800)

local function EmitDraw()
    draw.TexturedPolygon(1, {
        { 10, 10, 0.0, 0.0 },
        { 20, 10, 1.0, 0.0 },
        { 20, 20, 1.0, 1.0 },
        { 10, 20, 0.0, 1.0 }
    }, true)
    draw.Text(10, 10, "hello")
end

local function PrepareDraw()
    EmitDraw()
    draw.Color(255, 255, 255, 255)
    draw.SetFont(font)
end

callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    PrepareDraw()
end)
`
	path := writeTempLua(t, "linear_helper_chain_late_setup", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	foundColor := false
	foundFont := false
	for _, v := range violations {
		if strings.Contains(v.Message, "draw.TexturedPolygon() requires draw.Color() earlier in the same Draw callback chain") {
			foundColor = true
		}
		if strings.Contains(v.Message, "draw.Text() requires draw.SetFont() earlier in the same Draw callback chain") {
			foundFont = true
		}
	}
	if !foundColor || !foundFont {
		t.Fatalf("expected late helper-chain setup to fail prefix validation, got: %+v", violations)
	}
}

func TestPostPropUpdateRejected(t *testing.T) {
	src := `
callbacks.unregister("PostPropUpdate", "Legacy")
callbacks.register("PostPropUpdate", "Legacy", function() end)
`
	path := writeTempLua(t, "post_prop_update", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "PostPropUpdate is deprecated") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PostPropUpdate violation, got: %+v", violations)
	}
}

func TestWarpTriggerOutsideCreateMoveRejected(t *testing.T) {
	src := `
callbacks.unregister("Draw", "warp_bad")
callbacks.register("Draw", "warp_bad", function()
    warp.TriggerWarp()
end)
`
	path := writeTempLua(t, "warp_outside_createmove", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "warp trigger calls must be made from a CreateMove callback") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warp trigger location violation, got: %+v", violations)
	}
}

func TestWarpTriggerInsideCreateMoveAllowed(t *testing.T) {
	src := `
callbacks.unregister("CreateMove", "warp_ok")
callbacks.register("CreateMove", "warp_ok", function(cmd)
    warp.TriggerWarp()
end)
`
	path := writeTempLua(t, "warp_inside_createmove", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "warp trigger calls must be made from a CreateMove callback") {
			t.Fatalf("unexpected warp trigger violation: %+v", violations)
		}
	}
}

func TestSuspiciousSetupBonesRejected(t *testing.T) {
	src := `
local bones = entity:SetupBones()
local badMask = entity:SetupBones(0, globals.CurTime())
`
	path := writeTempLua(t, "setupbones_bad", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "SetupBones") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected SetupBones violation, got: %+v", violations)
	}
}

func TestCanonicalSetupBonesAllowed(t *testing.T) {
	src := `
local bones = entity:SetupBones(0x7ff00, globals.CurTime())
`
	path := writeTempLua(t, "setupbones_ok", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "SetupBones") {
			t.Fatalf("unexpected SetupBones violation: %+v", violations)
		}
	}
}

func TestIpairsOnFindByClassVariableRejected(t *testing.T) {
	src := `
local players = entities.FindByClass("CTFPlayer")
for _, player in ipairs(players) do
    print(player)
end
`
	path := writeTempLua(t, "ipairs_findbyclass_var", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "FindByClass() and may be sparse") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ipairs FindByClass violation, got: %+v", violations)
	}
}

func TestDirectIpairsOnFindByClassRejected(t *testing.T) {
	src := `
for _, player in ipairs(entities.FindByClass("CTFPlayer")) do
    print(player)
end
`
	path := writeTempLua(t, "ipairs_findbyclass_direct", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "entities.FindByClass() returns a sparse entity table") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected direct ipairs FindByClass violation, got: %+v", violations)
	}
}

func TestPairsOnFindByClassAllowed(t *testing.T) {
	src := `
local players = entities.FindByClass("CTFPlayer")
for _, player in pairs(players) do
    print(player)
end
`
	path := writeTempLua(t, "pairs_findbyclass_ok", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "FindByClass") && strings.Contains(v.Message, "sparse") {
			t.Fatalf("unexpected FindByClass iteration violation: %+v", violations)
		}
	}
}

func TestIpairsAllowedAfterFindByClassTableModified(t *testing.T) {
	src := `
local players = entities.FindByClass("CTFPlayer")
table.sort(players, function(a, b) return a:GetIndex() < b:GetIndex() end)
for _, player in ipairs(players) do
    print(player)
end
`
	path := writeTempLua(t, "ipairs_findbyclass_modified", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "FindByClass") && strings.Contains(v.Message, "sparse") {
			t.Fatalf("modified table should not trigger FindByClass ipairs rule: %+v", violations)
		}
	}
}

func TestCachedEntityWithoutIsValidRejected(t *testing.T) {
	src := `
local cached_weapon = nil

callbacks.unregister("CreateMove", "cache_weapon")
callbacks.register("CreateMove", "cache_weapon", function(cmd)
    local me = entities.GetLocalPlayer()
    cached_weapon = me:GetPropEntity("m_hActiveWeapon")
    if cached_weapon then
        print(cached_weapon:IsAlive())
    end
end)
`
	path := writeTempLua(t, "cached_entity_no_isvalid", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "cached entity 'cached_weapon'") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cached entity IsValid violation, got: %+v", violations)
	}
}

func TestCachedEntityWithIsValidAllowed(t *testing.T) {
	src := `
local cached_weapon = nil

callbacks.unregister("CreateMove", "cache_weapon_ok")
callbacks.register("CreateMove", "cache_weapon_ok", function(cmd)
    local me = entities.GetLocalPlayer()
    cached_weapon = me:GetPropEntity("m_hActiveWeapon")
    if not cached_weapon or not cached_weapon:IsValid() then return end
    print(cached_weapon:IsAlive())
end)
`
	path := writeTempLua(t, "cached_entity_with_isvalid", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "cached entity 'cached_weapon'") {
			t.Fatalf("unexpected cached entity IsValid violation: %+v", violations)
		}
	}
}

func TestNonCachedLocalEntityNotFlagged(t *testing.T) {
	src := `
callbacks.unregister("CreateMove", "temp_entity")
callbacks.register("CreateMove", "temp_entity", function(cmd)
    local me = entities.GetLocalPlayer()
    if me and me:IsAlive() then
        print(me:GetName())
    end
end)
`
	path := writeTempLua(t, "local_entity_temp", src)
	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	for _, v := range violations {
		if strings.Contains(v.Message, "cached entity") {
			t.Fatalf("unexpected cached entity violation for local temporary entity: %+v", violations)
		}
	}
}

func TestInlineCallbackFunctionProducesAdvisoryWarning(t *testing.T) {
	src := `
callbacks.unregister("Draw", "warn_inline")
callbacks.register("Draw", "warn_inline", function()
    print("hello")
end)
`
	path := writeTempLua(t, "inline_callback_warning", src)
	warnings, err := collectLuaAdvisoryWarnings(path)
	if err != nil {
		t.Fatalf("advisory scan error: %v", err)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "inline anonymous function passed to callbacks.Register") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected inline callback advisory warning, got: %+v", warnings)
	}
}

func TestNamedCallbackFunctionNoAdvisoryWarning(t *testing.T) {
	src := `
local function OnDraw()
    print("hello")
end

callbacks.unregister("Draw", "named_draw")
callbacks.register("Draw", "named_draw", OnDraw)
`
	path := writeTempLua(t, "named_callback_ok", src)
	warnings, err := collectLuaAdvisoryWarnings(path)
	if err != nil {
		t.Fatalf("advisory scan error: %v", err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "inline anonymous function passed to callbacks.Register") {
			t.Fatalf("unexpected inline callback advisory warning: %+v", warnings)
		}
	}
}

func TestLateFunctionDefinitionProducesAdvisoryWarning(t *testing.T) {
	src := `
local direction = NormalizeVector(Vector3(1, 0, 0))

local function NormalizeVector(vec)
    return vec
end

print(direction)
`
	path := writeTempLua(t, "late_function_definition_warning", src)
	warnings, err := collectLuaAdvisoryWarnings(path)
	if err != nil {
		t.Fatalf("advisory scan error: %v", err)
	}
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "function 'NormalizeVector' is called before its definition") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected late function definition advisory warning, got: %+v", warnings)
	}
}

func TestFunctionDefinedBeforeUseProducesNoLateDefinitionWarning(t *testing.T) {
	src := `
local function NormalizeVector(vec)
    return vec
end

local direction = NormalizeVector(Vector3(1, 0, 0))
print(direction)
`
	path := writeTempLua(t, "ordered_function_definition_ok", src)
	warnings, err := collectLuaAdvisoryWarnings(path)
	if err != nil {
		t.Fatalf("advisory scan error: %v", err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "called before its definition") {
			t.Fatalf("unexpected late function definition advisory warning: %+v", warnings)
		}
	}
}
