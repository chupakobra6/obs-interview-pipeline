# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-09-04

## Активный Шаг
- id: `STEP-012A`
- status: `готово`
- objective: Исправить forced-RU в short-form без регрессии русской речи и синхронно обновить OBS→Harvest contract.
- requirement IDs: `REQ-031`, `VAL-018`
- owned paths: Harvest production language/strategy/tests/docs; OBS ASR consumer/tests/docs; Project Loop state
- validation: request-level regression, cache identity, 10 RU + 10 EN FLEURS, mixed 25 s lecture, full/race/audit обоих репозиториев, current-head OBS→Harvest doctor
- done criteria: short-form отправляет `language=auto`; RU metrics равны forced-RU baseline, EN metrics равны explicit-EN baseline; `adaptive-media-v2` атомарно принят обоими репозиториями; checks зелёные.

## Фокус Ревью
- Short-form сохраняет один быстрый `no_timestamps` request; `auto` не должен превращаться в отдельный probe.
- Long-form probe и selective Russian prompt не меняются.
- Language probe физически ограничивает вход, не только request metadata.
- Repetition guard должен ловить только явные циклы и не повреждать допустимые повторения речи.
- Telegram и OBS используют один descriptor/cache contract без caller-owned ASR internals.

## Примечания
- `coverage-validated` доказывает timestamps и покрытие хвоста, но не WER/CER.
- Mixed-language внутри одной long-form записи остаётся отложен; short auto определяет доминирующий язык одного запроса.
