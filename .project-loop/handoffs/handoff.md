# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-09-04

## Цель

- Исправить short-form Telegram Harvest, который принудительно передавал Whisper язык `ru`, не ухудшив русскую речь и не изменив long-form.

## Завершённый Шаг

- step: `STEP-012A`
- status: `готово`
- requirements: `REQ-031`, `VAL-018`

## Реализация

- Production language и short-form HTTP request переведены на `auto`; short path остался одним быстрым `no_timestamps` request без language probe и prompt.
- Long-form router, 15-секундный probe, selective Russian punctuation prompt, model и decode settings не менялись.
- Strategy и language policy переименованы в `auto-language-no-timestamps-v2` и `auto-short-detect-russian-punctuation-long-v2`; descriptor и cache identity теперь отличаются от forced-RU.
- Public profile заменён на `adaptive-media-v2`; OBS consumer принимает только новый ID, compatibility path не добавлялся.

## Проверка

- Installed whisper.cpp v1.9.1 source подтвердил request-level `language` и core auto-detection для значения `auto`.
- Current-head FLEURS, 10 RU: auto и explicit `ru` дали одинаковые WER 0,61%, CER 0,09% и 9/10 exact.
- Current-head FLEURS, 10 EN: auto и explicit `en` дали одинаковые WER 4,22%, CER 1,76% и 4/10 exact; прежний forced-RU production имел WER 70,46%.
- Смешанный 25-секундный RU/SQL учебный фрагмент побайтно совпал с forced-RU baseline.
- Все 20 FLEURS responses вернули contract v4, `adaptive-media-v2`, backend `language=auto`, strategy `auto-language-no-timestamps-v2` и `short-media`.
- Harvest `make verify`, OBS `make check` и `go test -race ./...` прошли; current-head `make doctor` принял v4/`adaptive-media-v2` и показал готовые ASR/Metal/HEVC dependencies.

## Review

- Self-review проверил minimal diff, old-profile rejection, cache invalidation и отсутствие изменений long-form; findings отсутствуют.
- Subagent reviewer не запускался: текущий orchestration constraint запрещает delegation без прямого запроса пользователя.

## Остаточные Границы

- Auto-detection выбирает доминирующий язык одного short request; intra-file language switching остаётся вне этого шага.
- FLEURS — короткий чистый speech corpus; production noise, акценты и код-свитчинг могут давать иное распределение ошибок, но не отменяют устранение forced-RU дефекта.

## Следующее Действие

- Использовать Telegram Harvest и OBS pipeline как обычно; дополнительной миграции настроек нет.

## Источники Правды

- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/intake/user-deltas.md`
