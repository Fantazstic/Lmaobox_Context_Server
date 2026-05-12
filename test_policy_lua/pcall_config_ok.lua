local chunk = load(configString)
if chunk then
    local ok, cfg = pcall(chunk)
    if ok and type(cfg) == "table" then
        return cfg
    end
end
