# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Активный Шаг
- id: `STEP-008R`
- status: `готово`
- objective: Гипотеза `max_len=-1` проверена и отклонена как no-op для текущего `token_timestamps=false` path.
- requirement IDs: `REQ-023`, `VAL-012`
- owned paths: Telegram Harvest request contract/tests/docs при пройденном gate; Project Loop evidence
- validation: installed source readback, fresh-process real-file A/B, exact/normalized/segment/runtime comparison
- done criteria: output exact-identical, отсутствие структурного/performance benefit зафиксировано; production не усложнён; A/B artifacts/processes очищены.

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
- S012 предлагает `max_len=-1`; source inspection показывает, что character wrap зависит от `token_timestamps=true`, тогда как production отправляет false. Решение принимается только после same-file A/B.
