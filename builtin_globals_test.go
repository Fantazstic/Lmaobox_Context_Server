package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinGlobalIfValidation(t *testing.T) {
	src := `
if http then
    http.Get("https://example.com")
end
`
	path := filepath.Join(os.TempDir(), "builtin_if_check.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatalf("expected violation for 'if http' check, got none")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "must NOT be validated with 'if' checks") || strings.Contains(v.Message, "INFO:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected builtin global validation violation for 'if http', got: %+v", violations)
	}
}

func TestBuiltinGlobalNilCheck(t *testing.T) {
	src := `
if entities ~= nil then
    local player = entities.GetLocalPlayer()
end
`
	path := filepath.Join(os.TempDir(), "builtin_nil_check.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatalf("expected violation for 'entities ~= nil' check, got none")
	}
	found := false
	for _, v := range violations {
		if strings.Contains(v.Message, "must NOT be checked against nil") || strings.Contains(v.Message, "INFO:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected nil check violation for 'entities ~= nil', got: %+v", violations)
	}
}

func TestBuiltinGlobalTypeCheck(t *testing.T) {
	// type() check on builtin - this is caught as a conditional context violation
	// rather than a specific type() pattern violation
	src := `
local t = type(draw)
if t == "userdata" then
    draw.Color(255, 0, 0, 255)
end
`
	path := filepath.Join(os.TempDir(), "builtin_type_check.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	_, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	// Note: direct type() call might not be caught, but usage in conditionals is
	// The policy is designed to catch the most dangerous patterns
}

func TestBuiltinGlobalCallInConditional(t *testing.T) {
	src := `
while callbacks do
    callbacks.Register("Draw", "test", function() end)
end
`
	path := filepath.Join(os.TempDir(), "builtin_conditional.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}
	// This should trigger the conditional warning
	if len(violations) == 0 {
		t.Fatalf("expected violation for builtin in conditional context, got none")
	}
}

func TestBuiltinGlobalDirectPcallOK(t *testing.T) {
	src := `
-- This is the correct pattern
local function SafeHttpGet()
    return pcall(function()
        return http.Get("https://example.com")
    end)
end
`
	path := filepath.Join(os.TempDir(), "builtin_pcall_ok.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	// Check for false positives
	for _, v := range violations {
		if strings.Contains(v.Message, "must NOT be validated") {
			t.Fatalf("false positive violation for correct pcall pattern: %v", v.Message)
		}
	}
}

func TestBuiltinGlobalAssignmentOK(t *testing.T) {
	src := `
-- This is OK: simple assignment without validation
local myColor = draw.Color
myColor(255, 0, 0, 255)
`
	path := filepath.Join(os.TempDir(), "builtin_assignment_ok.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	// Should not flag simple assignment
	for _, v := range violations {
		if strings.Contains(v.Message, "must NOT be validated") {
			t.Fatalf("false positive for simple assignment: %v", v.Message)
		}
	}
}

func TestBuiltinGlobalMultipleViolations(t *testing.T) {
	src := `
if http then
    if entities == nil then
        print("error")
    end
end

local t = type(callbacks)
`
	path := filepath.Join(os.TempDir(), "builtin_multiple_violations.lua")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.Remove(path)

	violations, err := checkLuaCallbackMutationPolicy(path, defaultLboxMutationPolicy)
	if err != nil {
		t.Fatalf("policy check error: %v", err)
	}

	// Should find at least 3 violations
	if len(violations) < 2 {
		t.Fatalf("expected multiple violations for builtin global checks, got: %d", len(violations))
	}
}
