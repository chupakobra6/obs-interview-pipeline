# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Активный Шаг
- id: `STEP-007R`
- status: `готово`
- objective: Contract v2 и trailing coverage установлены, проверены на реальном интервью и прошли финальный tooling-review.
- requirement IDs: `REQ-020..REQ-022`, `VAL-011`
- owned paths: Telegram Harvest long-form preparation/validator/CLI/tests/docs; OBS ASR consumer/doctor/tests/docs; Project Loop artifacts
- validation: focused/full/race checks, installed doctor, real-file current-head rerun, Git/process/artifact inventory
- done criteria: оба репозитория согласованы по v2; Harvest отклоняет непокрытый хвост; OBS не дублирует внутренние настройки; real/current installed state и cleanup подтверждены.

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
- S009 требует качества без потери межчанкового контекста; fixed-chunk v1 заменяется после A/B, а не расширяется без доказательства необходимости.
- S011 требует contract v2 и bounded tail validation. Статус `coverage-validated` не трактуется как WER/CER; full-file VAD-gap scan и formatter пунктуации остаются отдельными задачами.
