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

// These tests call handleLuacheck / handleBundle exactly as the MCP server does.
// They exist because unit tests on checkLuaCallbackMutationPolicy alone can pass
// while the MCP path (luac + policy + luacheck, argument parsing, timeouts) fails.

func mcpToolRequest(toolName string, args map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments,omitempty"`
			Meta      *struct {
				ProgressToken mcp.ProgressToken `json:"progressToken,omitempty"`
			} `json:"_meta,omitempty"`
		}{
			Name:      toolName,
			Arguments: args,
		},
	}
}

func toolResultString(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	return fmt.Sprintf("%v", result.Content)
}

func callLuacheckMCP(t *testing.T, filePath string, checkBundle bool) (*mcp.CallToolResult, error) {
	t.Helper()
	args := map[string]interface{}{"filePath": filePath}
	if checkBundle {
		args["checkBundle"] = true
	}
	return handleLuacheck(context.Background(), mcpToolRequest("luacheck", args))
}

func callBundleMCP(t *testing.T, projectDir string, bundleOutputDir, deployDir string) (*mcp.CallToolResult, error) {
	t.Helper()
	args := map[string]interface{}{"projectDir": projectDir}
	if bundleOutputDir != "" {
		args["bundleOutputDir"] = bundleOutputDir
	}
	if deployDir != "" {
		args["deployDir"] = deployDir
	}
	return handleBundle(context.Background(), mcpToolRequest("bundle", args))
}

func requireLuac(t *testing.T) {
	t.Helper()
	if findLuac() == "" {
		t.Skip("luac not installed; skipping full MCP luacheck pipeline test")
	}
}

func TestHandleLuacheckMCP_MissingFilePath(t *testing.T) {
	result, err := callLuacheckMCP(t, "", false)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error for missing filePath, got success: %s", toolResultString(result))
	}
	if !strings.Contains(toolResultString(result), "filePath is required") {
		t.Fatalf("expected filePath error, got: %s", toolResultString(result))
	}
}

func TestHandleLuacheckMCP_FileNotFound(t *testing.T) {
	requireLuac(t)
	result, err := callLuacheckMCP(t, filepath.Join(t.TempDir(), "does_not_exist.lua"), false)
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error for missing file, got: %s", toolResultString(result))
	}
	text := toolResultString(result)
	if !strings.Contains(text, "syntax") && !strings.Contains(text, "error") {
		t.Fatalf("expected syntax/read error in message, got: %s", text)
	}
}

func TestHandleLuacheckMCP_EndToEndScenarios(t *testing.T) {
	requireLuac(t)

	tests := []struct {
		name           string
		src            string
		wantError      bool
		wantSubstrings []string
		notSubstrings  []string
	}{
		{
			name: "valid ghost pattern at module scope",
			src: `
local running = true
callbacks.unregister("Draw", "Ghost")
callbacks.register("Draw", "Ghost", function()
    if not running then return end
end)
callbacks.register("Unload", function()
    running = false
end)
`,
			wantError:      false,
			wantSubstrings: []string{"passed luac syntax", "policy"},
		},
		{
			name: "kill switch without unregister",
			src: `
callbacks.register("Draw", "NoKill", function() end)
`,
			wantError:      true,
			wantSubstrings: []string{"Kill-Switch"},
		},
		{
			name: "unregister inside unload handler",
			src: `
callbacks.register("Unload", function()
    callbacks.unregister("Draw", "X")
end)
`,
			wantError:      true,
			wantSubstrings: []string{"Illegal Unregister"},
		},
		{
			name: "require inside draw callback body",
			src: `
callbacks.unregister("Draw", "HUD")
callbacks.register("Draw", "HUD", function()
    local x = require("Menu")
end)
`,
			wantError:      true,
			wantSubstrings: []string{"require() inside a function"},
		},
		{
			name: "pcall require optional lib allowed",
			src: `
local ok, lib = pcall(require, "Menu")
if not ok then return end
`,
			wantError: false,
		},
		{
			name: "pcall anonymous wrapping engine",
			src: `
local ok, v = pcall(function()
    return engine.IsInGame()
end)
`,
			wantError:      true,
			wantSubstrings: []string{"pcall(function()"},
		},
		{
			name: "module scope callbacks after local function",
			src: `
local function onFrame(stage)
    if stage ~= 7 then return end
end
callbacks.unregister("FrameStageNotify", "id")
callbacks.register("FrameStageNotify", "id", onFrame)
`,
			wantError: false,
		},
		{
			name:           "syntax error rejected before policy",
			src:            `local x = `,
			wantError:      true,
			wantSubstrings: []string{"syntax"},
		},
		{
			// Kill-switch only applies at module scope (depth 0). Nested load-time
			// register is allowed — same as TestZeroMutationRegisterInNestedFunction.
			name: "load time nested register allowed no kill switch",
			src: `
local function setup()
    local function inner()
        callbacks.register("Draw", "Nested", function() end)
    end
    inner()
end
setup()
`,
			wantError:     false,
			notSubstrings: []string{"callback handler"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := createTempLuaFile(t, "mcp_"+strings.ReplaceAll(tc.name, " ", "_"), tc.src)
			result, herr := callLuacheckMCP(t, path, false)
			if herr != nil {
				t.Fatalf("handler returned Go error: %v", herr)
			}
			text := toolResultString(result)
			if tc.wantError {
				if !result.IsError {
					t.Fatalf("expected MCP tool error, got success: %s", text)
				}
				for _, sub := range tc.wantSubstrings {
					if !strings.Contains(text, sub) {
						t.Fatalf("expected error to contain %q, got:\n%s", sub, text)
					}
				}
			} else if result.IsError {
				t.Fatalf("expected MCP success, got error:\n%s", text)
			}
			for _, sub := range tc.wantSubstrings {
				if !strings.Contains(text, sub) {
					t.Fatalf("expected message to contain %q, got:\n%s", sub, text)
				}
			}
			for _, sub := range tc.notSubstrings {
				if strings.Contains(text, sub) {
					t.Fatalf("expected message NOT to contain %q, got:\n%s", sub, text)
				}
			}
		})
	}
}

func TestHandleLuacheckMCP_PolicyFixtureFiles(t *testing.T) {
	requireLuac(t)

	fixtures := []struct {
		file           string
		wantError      bool
		wantSubstrings []string
	}{
		{
			file:      "pcall_engine_bad.lua",
			wantError: true,
			wantSubstrings: []string{
				"pcall(function()",
			},
		},
		{
			file:      "pcall_config_ok.lua",
			wantError: false,
		},
		{
			file:      "collectgarbage_collect_bad.lua",
			wantError: true,
			wantSubstrings: []string{
				"collectgarbage",
			},
		},
		{
			file:      "collectgarbage_count_ok.lua",
			wantError: false,
		},
	}

	for _, fx := range fixtures {
		t.Run(fx.file, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("test_policy_lua", fx.file))
			if err != nil {
				t.Fatalf("abs path: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Skipf("fixture missing: %v", err)
			}
			result, herr := callLuacheckMCP(t, path, false)
			if herr != nil {
				t.Fatalf("handler error: %v", herr)
			}
			text := toolResultString(result)
			if fx.wantError {
				if !result.IsError {
					t.Fatalf("expected failure for %s, got success: %s", fx.file, text)
				}
				for _, sub := range fx.wantSubstrings {
					if !strings.Contains(text, sub) {
						t.Fatalf("%s: expected %q in error, got:\n%s", fx.file, sub, text)
					}
				}
				return
			}
			if result.IsError {
				t.Fatalf("%s: expected success through MCP, got:\n%s", fx.file, text)
			}
		})
	}
}

func TestHandleLuacheckMCP_CheckBundleDryRun(t *testing.T) {
	projectDir, err := filepath.Abs("test_bundle_project")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	mainPath := filepath.Join(projectDir, "Main.lua")
	if _, err := os.Stat(mainPath); err != nil {
		t.Skipf("test_bundle_project missing: %v", err)
	}

	result, herr := callLuacheckMCP(t, mainPath, true)
	if herr != nil {
		t.Fatalf("handler error: %v", herr)
	}
	if result.IsError {
		t.Fatalf("checkBundle should pass for test_bundle_project, got:\n%s", toolResultString(result))
	}
	if !strings.Contains(toolResultString(result), "bundle") {
		t.Fatalf("expected bundle success message, got: %s", toolResultString(result))
	}
}

func TestHandleLuacheckMCP_CheckBundleDoesNotRunFullValidation(t *testing.T) {
	// checkBundle=true must not run policy on broken callback code — only resolve requires.
	src := `
local utils = require("utils")
callbacks.register("Draw", "bad", function() end)
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "utils.lua"), []byte("return {}\n"), 0644); err != nil {
		t.Fatalf("write utils: %v", err)
	}
	mainPath := filepath.Join(dir, "Main.lua")
	if err := os.WriteFile(mainPath, []byte(src), 0644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	result, herr := callLuacheckMCP(t, mainPath, true)
	if herr != nil {
		t.Fatalf("handler error: %v", herr)
	}
	if result.IsError {
		t.Fatalf("checkBundle should only resolve requires (kill-switch not checked), got:\n%s", toolResultString(result))
	}

	// Full validation must catch kill-switch.
	result2, herr2 := callLuacheckMCP(t, mainPath, false)
	if herr2 != nil {
		t.Fatalf("handler error: %v", herr2)
	}
	if findLuac() == "" {
		t.Skip("luac not installed")
	}
	if !result2.IsError || !strings.Contains(toolResultString(result2), "Kill-Switch") {
		t.Fatalf("full luacheck should fail kill-switch; isError=%v body=%s", result2.IsError, toolResultString(result2))
	}
}

func TestHandleBundleMCP_SucceedsWithoutValidation(t *testing.T) {
	projectDir, err := filepath.Abs("test_bundle_project")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	outDir := filepath.Join(t.TempDir(), "build")
	deployDir := t.TempDir()

	result, herr := callBundleMCP(t, projectDir, outDir, deployDir)
	if herr != nil {
		t.Fatalf("handler error: %v", herr)
	}
	if result.IsError {
		t.Fatalf("bundle should succeed, got:\n%s", toolResultString(result))
	}
	bundlePath := filepath.Join(outDir, "Main.lua")
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("expected bundle output at %s: %v", bundlePath, err)
	}
}

func TestHandleBundleMCP_MissingRequireExplainsFailure(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "Main.lua")
	if err := os.WriteFile(mainPath, []byte(`local x = require("missing_module")\n`), 0644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	result, herr := callBundleMCP(t, dir, filepath.Join(t.TempDir(), "build"), t.TempDir())
	if herr != nil {
		t.Fatalf("handler error: %v", herr)
	}
	if !result.IsError {
		t.Fatalf("expected bundle failure for missing require")
	}
	text := toolResultString(result)
	for _, sub := range []string{"Bundle failed", "require", "luacheck"} {
		if !strings.Contains(text, sub) {
			t.Fatalf("expected bundle error to mention %q, got:\n%s", sub, text)
		}
	}
}

func TestHandleBundleMCP_BadSyntaxStillBundles(t *testing.T) {
	// Bundle does not run luac; luacheck on the same file must still catch syntax errors.
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "Main.lua")
	if err := os.WriteFile(mainPath, []byte(`local x = `), 0644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	result, herr := callBundleMCP(t, dir, filepath.Join(t.TempDir(), "build"), t.TempDir())
	if herr != nil {
		t.Fatalf("handler error: %v", herr)
	}
	if result.IsError {
		t.Fatalf("bundle intentionally skips validation; should succeed, got:\n%s", toolResultString(result))
	}

	requireLuac(t)
	check, herr := callLuacheckMCP(t, mainPath, false)
	if herr != nil {
		t.Fatalf("handler error: %v", herr)
	}
	if !check.IsError || !strings.Contains(toolResultString(check), "syntax") {
		t.Fatalf("luacheck should catch bad syntax; isError=%v body=%s", check.IsError, toolResultString(check))
	}
}

// TestTokenDepthMatchesExpectations documents what our heuristic depth actually sees.
func TestTokenDepthMatchesExpectations(t *testing.T) {
	src := `callbacks.register("Draw", "id", function()
    callbacks.unregister("Draw", "id")
end)`
	tokens, err := tokenizeLua(src)
	if err != nil {
		t.Fatalf("tokenize: %v", err)
	}
	depths := buildFunctionDepthAtToken(tokens)

	findCallbacksRegisterDepth := -1
	findUnregisterDepth := -1
	for i, tok := range tokens {
		if tok.Kind == "ident" && strings.EqualFold(tok.Text, "callbacks") {
			if method, _, _, ok := extractCallbacksCall(tokens, i); ok {
				if method == "register" && findCallbacksRegisterDepth < 0 {
					findCallbacksRegisterDepth = depths[i]
				}
				if method == "unregister" {
					findUnregisterDepth = depths[i]
				}
			}
		}
	}

	if findCallbacksRegisterDepth != 0 {
		t.Fatalf("register call should be depth 0 at callbacks token, got %d", findCallbacksRegisterDepth)
	}
	if findUnregisterDepth <= 0 {
		t.Fatalf("unregister inside handler should be depth > 0, got %d", findUnregisterDepth)
	}
}
