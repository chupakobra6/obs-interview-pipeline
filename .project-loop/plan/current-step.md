# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Активный Шаг
- id: `STEP-004R`
- status: `готово`
- objective: Устранить лишний whole-file gate и video re-encode в OBS workflow, оставить master AAC 96 и настроить прямую запись HEVC VideoToolbox.
- requirement IDs: `REQ-011..REQ-013`, `VAL-008`
- owned paths: Telegram Harvest local ASR contract; OBS compressor/validation/tests/docs; OBS profile and installed state
- validation: focused tests, `make check`, race, OBS UI/config readback, real short OBS E2E, ffprobe/manifest/log inventory
- done criteria: ASR сохраняет канонический Harvest профиль без gate; готовое HEVC video копируется; final содержит один AAC 96; current-head E2E зелёный и clean.

## Фокус Ревью
- Delete gate исключает потерю исходника при любой частичной ошибке.
- Queue/LaunchAgent не блокируют OBS и не запускают параллельные ASR jobs.
- Команды не зависят от shell quoting или пользовательского PATH.
- Медиапроверка покрывает codec, dimensions, fps, duration и audio streams.
- Установка идемпотентна и не захватывает старые записи.

## Примечания
- Игорь явно попросил выполнить и проверить весь pipeline, поэтому разрешено непрерывное выполнение всех подэтапов STEP-001.
- S003 явно продолжает выполнение без паузы для переноса ASR ownership и улучшения уведомлений.
- S004 возвращает alert style «Временно»; helper остаётся активным до отложенного клика.
- S005 разрешает обработку явно выбранного реального собеседования обычным delete gate.
- S006 требует tooling-review/repo-polish и удаления только созданных проверками хвостов.
- S007 заменяет OBS-специфичные gate/audio/video требования: основной Telegram Harvest workflow остаётся с Silero, OBS получает явный fast path.
