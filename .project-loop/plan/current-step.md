# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Активный Шаг
- id: `STEP-005R`
- status: `готово`
- objective: Review, cleanup, документация и current-head validation закрыты; изменения готовы к focused commits.
- requirement IDs: `REQ-014..REQ-016`, `VAL-009`
- owned paths: оба репозитория, project-loop docs и installed/runtime state
- validation: full checks/race, doctor, loop validate, Git/process/artifact inventory
- done criteria: focused commits готовы; installed current head проверен; disposable artifacts отсутствуют.

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
- S008 заменяет master-only default и автоматический enqueue: решение принимается в prompt для каждой записи.
