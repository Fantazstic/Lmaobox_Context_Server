local function safeEngineCall()
    local ok, inGame = pcall(function()
        return engine.IsInGame()
    end)
    if ok and inGame then
        print("In game")
    end
end
