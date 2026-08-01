obs = obslua

local processor_path = "__PROCESSOR_PATH__"
local config_path = "__CONFIG_PATH__"

local function shell_quote(value)
    return "'" .. string.gsub(value, "'", "'\\''") .. "'"
end

local function enqueue_recording(path)
    if path == nil or path == "" then
        obs.script_log(obs.LOG_ERROR, "OBS Interview: last recording path is empty")
        return
    end
    local command = shell_quote(processor_path)
        .. " enqueue --config " .. shell_quote(config_path)
        .. " " .. shell_quote(path)
        .. " >/dev/null 2>&1 &"
    local ok = os.execute(command)
    if ok then
        obs.script_log(obs.LOG_INFO, "OBS Interview: queued " .. path)
    else
        obs.script_log(obs.LOG_ERROR, "OBS Interview: failed to queue " .. path)
    end
end

local function on_frontend_event(event)
    if event == obs.OBS_FRONTEND_EVENT_RECORDING_STOPPED then
        enqueue_recording(obs.obs_frontend_get_last_recording())
    end
end

function script_description()
    return "После остановки записи передаёт точный файл локальному обработчику: Whisper + H.265 + проверка."
end

function script_properties()
    local props = obs.obs_properties_create()
    obs.obs_properties_add_path(props, "processor_path", "Обработчик", obs.OBS_PATH_FILE, "", processor_path)
    obs.obs_properties_add_path(props, "config_path", "Конфигурация", obs.OBS_PATH_FILE, "JSON (*.json)", config_path)
    return props
end

function script_defaults(settings)
    obs.obs_data_set_default_string(settings, "processor_path", processor_path)
    obs.obs_data_set_default_string(settings, "config_path", config_path)
end

function script_update(settings)
    processor_path = obs.obs_data_get_string(settings, "processor_path")
    config_path = obs.obs_data_get_string(settings, "config_path")
end

function script_load(settings)
    script_update(settings)
    obs.obs_frontend_add_event_callback(on_frontend_event)
    obs.script_log(obs.LOG_INFO, "OBS Interview hook loaded")
end

function script_unload()
    obs.obs_frontend_remove_event_callback(on_frontend_event)
end
