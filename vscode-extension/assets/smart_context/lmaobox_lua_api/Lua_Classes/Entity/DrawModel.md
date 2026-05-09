## Function/Symbol: Entity.DrawModel

> Draws the entity model with Source-style `STUDIO_*` draw flags.

### Signature

```lua
entity:DrawModel(flags)
```

### Parameters

- `flags` (integer): Bitfield of `STUDIO_*` flags.

### Common Draw Flags

| Flag | Value | Notes |
|---|---:|---|
| `STUDIO_NONE` | `0x00000000` | No flags |
| `STUDIO_RENDER` | `0x00000001` | Most common: normal render |
| `STUDIO_VIEWXFORMATTACHMENTS` | `0x00000002` | Attachment transforms |
| `STUDIO_DRAWTRANSLUCENTSUBMODELS` | `0x00000004` | Translucent submodels |
| `STUDIO_TWOPASS` | `0x00000008` | Two-pass rendering |
| `STUDIO_STATIC_LIGHTING` | `0x00000010` | Static lighting |
| `STUDIO_WIREFRAME` | `0x00000020` | Wireframe |
| `STUDIO_ITEM_BLINK` | `0x00000040` | Item blink |
| `STUDIO_NOSHADOWS` | `0x00000080` | No shadows |
| `STUDIO_WIREFRAME_VCOLLIDE` | `0x00000100` | VCollide wireframe |
| `STUDIO_NO_OVERRIDE_FOR_ATTACH` | `0x00000200` | Attachment override |

### Curated Usage Examples

#### Draw entity normally

```lua
local ent = entities.GetLocalPlayer()
assert(ent, "DrawModel: LocalPlayer missing")
assert(ent:IsValid(), "DrawModel: LocalPlayer invalid")

ent:DrawModel(STUDIO_RENDER)
```

#### Wireframe draw

```lua
local ent = entities.GetLocalPlayer()
assert(ent, "DrawModel: LocalPlayer missing")
assert(ent:IsValid(), "DrawModel: LocalPlayer invalid")

local flags = STUDIO_RENDER | STUDIO_WIREFRAME
ent:DrawModel(flags)
```

### Notes

- `flags` is a bitfield: combine with `|`.
- For player *state* flags, use `m_fFlags` + `E_PlayerFlag` (different concept).
- Official docs: https://lmaobox.net/lua/Lua_Classes/Entity/#drawmodel-drawflagsinteger

