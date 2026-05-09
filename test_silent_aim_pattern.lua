local function onFrameStage(stage)
	if stage ~= 7 then
		return
	end
end

callbacks.Unregister("FrameStageNotify", "CD_SilentAim_FSN")
callbacks.Register("FrameStageNotify", "CD_SilentAim_FSN", onFrameStage)
