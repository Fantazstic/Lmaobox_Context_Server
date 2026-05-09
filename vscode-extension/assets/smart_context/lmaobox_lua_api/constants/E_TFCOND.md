## Constants Reference: E_TFCOND (TF2 Conditions)

> TF2 player conditions (taunting, cloaked, ubercharged, jarated, etc.) used with `Entity:InCond`, `Entity:AddCond`, and `Entity:RemoveCond`.

### How To Read

- Check a condition: `player:InCond(TFCond_Taunting)`
- Conditions live in the `TFCond_*` constants list (see `types/lmaobox_lua_api/constants/E_TFCOND.d.lua`)

### Curated Usage Examples

#### Detect taunting

```lua
local player = entities.GetLocalPlayer()
assert(player, "E_TFCOND: LocalPlayer missing")
assert(player:IsValid(), "E_TFCOND: LocalPlayer invalid")

local isTaunting = player:InCond(TFCond_Taunting)
if isTaunting then
    print("Local player is taunting")
end
```

#### Detect spy cloak / disguise

```lua
local function IsSpyHidden(player)
    assert(player, "IsSpyHidden: player missing")
    assert(player:IsValid(), "IsSpyHidden: player invalid")

    local isCloaked = player:InCond(TFCond_Cloaked) or player:InCond(TFCond_Stealthed)
    local isDisguised = player:InCond(TFCond_Disguised) or player:InCond(TFCond_Disguising)
    return isCloaked or isDisguised
end
```

#### Detect invulnerability / crit boost

```lua
local function IsInvulnerable(player)
    assert(player, "IsInvulnerable: player missing")
    assert(player:IsValid(), "IsInvulnerable: player invalid")

    local uber = player:InCond(TFCond_Ubercharged) or player:InCond(TFCond_UberchargedHidden)
    local bonk = player:InCond(TFCond_Bonked)
    return uber or bonk
end

local function IsCritBoosted(player)
    assert(player, "IsCritBoosted: player missing")
    assert(player:IsValid(), "IsCritBoosted: player invalid")

    return player:InCond(TFCond_Kritzkrieged) or player:InCond(TFCond_CritCanteen)
end
```

### Search Tips

- Try `smart_search("TFCond")` or `smart_search("taunt")` to find specific condition constants.
- For the API method, use `smart_search("InCond")` then `get_smart_context("Entity.InCond")`.

