# Lmaobox Built-In Globals Protocol

## Overview

Lmaobox exposes certain globals (`http`, `entities`, `callbacks`, `engine`, `draw`, etc.) as special objects that **do NOT respond to standard Lua validation patterns**. This protocol enforces safe patterns by forbidding dangerous guard checks. These globals are always present at runtime — just call them directly.

> **IMPORTANT — pcall misconception:** `pcall()` in Lua is a Lua-level error catcher only. It catches `error()` calls and failed assertions. It does **NOT** prevent crashes from Lmaobox C-API calls. Do not wrap Lmaobox API calls in `pcall()` thinking it protects against crashes — it does not, and it adds overhead with zero benefit for API calls.

## The Problem

Attempting to validate Lmaobox built-in globals with standard Lua patterns causes silent failures or dead code:

```lua
-- ❌ FORBIDDEN: These all produce wrong results (global exists but guards evaluate incorrectly)
if http then end                           -- evaluates incorrectly, misleading guard
if entities ~= nil then end               -- evaluates incorrectly, misleading guard
if type(callbacks) == "userdata" then end -- always false, misleading

-- ❌ FORBIDDEN: Indirection doesn't help
if globals.http then end                  -- still wrong
local client = http or fallback           -- fallback may be called incorrectly
```

## The Solution

Call the API directly. These globals always exist at runtime in the Lmaobox environment:

```lua
-- ✅ CORRECT: Direct call, no guard needed
local player = entities.GetLocalPlayer()
if player then
    print(player:GetHealth())
end

-- ✅ CORRECT: Just call it
http.Get("https://example.com", function(body) print(body) end)
```

## Validation Rules

The MCP tool and bundle validation enforce:

### 1. Forbidden: `if`/`while`/`until` existence checks
```lua
-- ❌ FORBIDDEN
if http then
    http.Get(url)
end

-- ✅ CORRECT
http.Get(url)
```

### 2. Forbidden: nil comparisons
```lua
-- ❌ FORBIDDEN
if entities == nil then return end
if callbacks ~= nil then ... end

-- ✅ CORRECT
local player = entities.GetLocalPlayer()
```

### 3. Forbidden: type() checks
```lua
-- ❌ FORBIDDEN
if type(draw) == "userdata" then ... end

-- ✅ CORRECT
draw.Color(255, 0, 0, 255)
```

### 4. Forbidden: globals.X access (indirect)
```lua
-- ❌ FORBIDDEN
if globals.http then ... end

-- ✅ CORRECT
http.Get(url)
```

## Known Lmaobox Built-In Globals

The following globals are always in scope — call them directly without guards:

- `http` – HTTP client
- `entities` – Entity/player access
- `callbacks` – Event system (note: `callbacks.Register` is a special case with other rules)
- `engine` – Game engine API
- `draw` – Drawing/rendering
- `input` – Input handling
- `profiler` – Profiling tools
- `warp` – Warp/strafe triggers

## MCP Tool Integration

When using the MCP tool (`luacheck`, `bundle`, etc.), the validation automatically:

1. **Detects** any use of forbidden guard patterns
2. **Reports** the violation with line number and explanation
3. **Blocks** bundling if violations are found

Example violation message:
```
CRITICAL: Lmaobox built-in 'http' must NOT be validated with 'if' checks — these globals always exist at runtime. Remove the guard and call http.SomeCall(...) directly
```

## Configuration

### .luacheckrc Setup

Create or copy `.luacheckrc.example` to `.luacheckrc` and add:

```lua
globals = {
    "http", "entities", "callbacks", "engine", "draw", "input",
    "profiler", "warp", "globals"
}
```

This suppresses false "undefined global" warnings while MCP enforces the protocol.

### Bundling

When bundling Lua code:

```bash
mcp_lmaobox_conte_bundle --projectDir=/path/to/project
```

The bundle tool will:
1. Run syntax check
2. Run **Lmaobox Built-In Globals Protocol** validation
3. Reject the bundle if violations are found
4. Deploy only if all checks pass

## Common Patterns

### HTTP Request
```lua
-- ✅ CORRECT: Call directly
http.Get("https://example.com", function(body)
    print("Response:", body)
end)
```

### Entity Access
```lua
-- ✅ CORRECT: Direct call, nil-check the result (entity may not exist)
local player = entities.GetLocalPlayer()
if not player then return end
print(player:GetHealth())
```

### Draw Call
```lua
-- ✅ CORRECT: Set state and draw in same callback
local function OnDraw()
    draw.Color(255, 0, 0, 255)
    draw.FilledRect(10, 10, 50, 50)
end
```

## Migration Guide

If you have existing code with forbidden guard patterns:

**Before:**
```lua
if entities then
    local player = entities.GetLocalPlayer()
    if player then
        print(player:GetHealth())
    end
end
```

**After:**
```lua
local player = entities.GetLocalPlayer()
if player then
    print(player:GetHealth())
end
```

## Testing

The validation includes comprehensive test coverage:

```bash
go test -run TestBuiltin ./...
```

This runs all tests related to the Lmaobox Built-In Globals Protocol validation.

## FAQ

### Q: Can I test if a global exists?
**A:** No — and you don't need to. These globals always exist in the Lmaobox runtime. Just call them.

### Q: Why not just use `type()`?
**A:** Lmaobox built-ins don't respond to `type()` correctly. The check is meaningless.

### Q: Should I wrap API calls in pcall()?
**A:** No. `pcall()` only catches Lua-level errors (`error()`, failed `assert()`). It does not protect against Lmaobox C-API crashes. Wrapping Lmaobox calls in `pcall()` adds overhead without any protection benefit.

### Q: What if the API call truly fails?
**A:** The game will report the error via the Lua error system. Fix the root cause; don't mask it with pcall.

### Q: Does this apply to Lua standard library (math, table, string)?
**A:** No. Standard library globals are always valid in Lua 5.4 and need no guards.

## References

- **MCP Documentation:** See `mcp_lmaobox_conte_luacheck` and `mcp_lmaobox_conte_bundle`
- **Bundle Tool:** See [BUNDLE_TOOL_FIX.md](./BUNDLE_TOOL_FIX.md)
- **Zero-Mutation Policy:** See [lua_policy.go](./lua_policy.go)
