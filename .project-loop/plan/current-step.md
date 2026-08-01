# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Активный Шаг
- id: `STEP-002R`
- status: `готово`
- objective: Перенести ownership production ASR в Telegram Harvest, переключить OBS worker на общий локальный контракт, добавить кликабельные уведомления и отполировать репозиторий.
- requirement IDs: `REQ-007..REQ-009`, `CON-004`, `VAL-004..VAL-005`
- owned paths: `telegram-harvest/cmd/telegram-harvest`, OBS pipeline source/tests/docs/install state и disposable E2E artifacts
- validation: focused tests обоих репозиториев, `make check`, `go test -race ./...`, `loopctl validate`, installed-state readback, current-head E2E, UI click test, git diff/status
- done criteria: OBS не содержит Whisper engine/profile, общий ASR-контракт доказан E2E, клик открывает точный result dir, CI/docs/install обновлены и focused commits созданы.

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
