local function memoryKb()
    return collectgarbage("count")
end

print("Memory KB:", memoryKb())
