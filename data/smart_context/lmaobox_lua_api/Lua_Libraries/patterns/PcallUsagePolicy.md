# pcall — Lua Exception Catching (NOT Crash Protection)

## ⚠️ Critical Distinction

`pcall` is **Lua-level exception catching only**. It catches errors thrown by:
- `error("message")` explicit throws
- `assert(condition, "message")` failed assertions
- Bad argument types passed to Lua stdlib functions (e.g. `string.format` with wrong types)

**pcall does NOT:**
- Prevent crashes from Lmaobox C-API calls
- Protect against engine-level faults
- Save you from nil-indexing on userdata objects
- Catch segfaults, access violations, or any error that originates in C code

## INFO: Runtime Policy

Treat `pcall` as **exception catching for Lua code**, not as crash protection.

In this codebase, `pcall` is only appropriate around:
- IO / filesystem reads where user data may be malformed
- Config deserialization (`load(...)` then `pcall(chunk)`)
- Optional external libraries/modules that may throw Lua errors during load/init
- Your own isolated Lua code that intentionally uses `error()` / `assert()`

Avoid `pcall` in runtime hot paths and callbacks. It is especially wrong around Lmaobox APIs, `math.*`, `string.*`, and `table.*` calls.

### Required Context

- Scope: Lua 5.4 standard library
- Pattern: Use only when you specifically want to catch a Lua `error()` / `assert()`, usually at load/config/IO boundaries

### When pcall IS appropriate

```lua
-- ✅ Catching a deliberate Lua error from your own code
local ok, result = pcall(function()
    assert(type(x) == "number", "x must be a number")
    return doSomeComplexLuaLogic(x)
end)
if not ok then
    print("Caught lua error:", result)
end

-- ✅ Loading/executing untrusted Lua strings (e.g. config file deserialization)
local chunk = load(configString)
if chunk then
    local ok, cfg = pcall(chunk)
    if ok and type(cfg) == "table" then
        -- valid config
    end
end
```

### When pcall IS NOT appropriate

```lua
-- ❌ WRONG: pcall does NOT protect Lmaobox API calls from crashing
local ok, player = pcall(function()
    return entities.GetLocalPlayer()  -- if this crashes, pcall won't save you
end)

-- ❌ WRONG: pcall does NOT replace nil-checking entity results
local ok = pcall(function()
    entities.GetLocalPlayer():GetHealth()  -- crashes if player is nil, pcall won't help
end)

-- ❌ WRONG: pcall around pure Lua stdlib — unnecessary overhead
local ok, s = pcall(string.format, "%d", 42)  -- string.format doesn't throw here

-- ❌ WRONG: pcall around math/table operations — pointless
local ok, v = pcall(math.max, 1, 2)  -- math.max never throws

-- ❌ WRONG: pcall in runtime callbacks/hot paths — overhead with no crash safety
callbacks.Register("Draw", "bad_pcall_runtime", function()
    pcall(function()
        draw.Color(255, 255, 255, 255)
    end)
end)
```

### Correct Lmaobox API Pattern (no pcall)

```lua
-- ✅ CORRECT: Call directly, nil-check the result
local player = entities.GetLocalPlayer()
if not player then return end
local hp = player:GetHealth()

-- ✅ CORRECT: Direct draw calls — these are always available
draw.Color(255, 0, 0, 255)
draw.FilledRect(10, 10, 100, 50)

-- ✅ CORRECT: engine calls — no guards needed
local inGame = engine.IsInGame()
```

## Summary

| Use case                                                               | Use pcall?                            |
| ---------------------------------------------------------------------- | ------------------------------------- |
| Catching `error()` / `assert()` from your own Lua code                 | ✅ Yes                                 |
| IO/config boundary (`io.open`, filesystem data, `load()` config chunk) | ✅ Yes                                 |
| Optional external library/module loading                               | ✅ Yes                                 |
| Lmaobox API calls (`entities`, `engine`, `draw`, etc.)                 | ❌ No — call directly                  |
| Pure Lua stdlib (`math`, `string`, `table`)                            | ❌ No — no overhead benefit            |
| Runtime callbacks / hot paths                                          | ❌ No — worst place for pcall overhead |
| "Protecting" against crashes                                           | ❌ No — pcall cannot do this           |
