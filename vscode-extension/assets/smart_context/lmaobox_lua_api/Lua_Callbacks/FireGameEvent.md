## Callback: FireGameEvent

> Called for selected game events from server (GameEvent object)

### Pattern

```lua
callbacks.Register("FireGameEvent", "events", function(ev)
    local name = ev:GetName()
    if name == "player_death" then
        local victim = entities.GetByUserID(ev:GetInt("userid"))
        local attacker = entities.GetByUserID(ev:GetInt("attacker"))
        if victim then print("Victim: " .. victim:GetName()) end
        if attacker then print("Killer: " .. attacker:GetName()) end
    end
end)
```

### Notes

- See `GameEvent` smart context for field access patterns
- Event names are lowercase strings
- TF2 event list: https://wiki.alliedmods.net/Team_Fortress_2_Events
