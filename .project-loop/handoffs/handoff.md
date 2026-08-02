# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Цель

- После остановки OBS дать выбор для конкретной записи, локально расшифровать её общим Harvest ASR, получить компактный проверенный HEVC и безопасно применить выбранную delete/audio policy.

## Текущий Шаг

- active step: `STEP-010R`
- status: `готово`
- requirements: `REQ-027`, `VAL-015`

## Итог Review

- Tooling review удалил stale `ASSUME_SPEECH` из Harvest help и устранил противоречие README между canonical backend и двумя публичными profile: `short-message-v1` и `trusted-long-form-v3`.
- OBS delete lifecycle теперь сохраняет в manifest v3 device/inode, размер и modification time source; identity повторно проверяется перед публикацией и `unlink`.
- Retry требует точного совпадения transcript и output media probe с manifest. Replacement source не удаляется; отсутствие source принимается только для `delete_source=true` после повторной проверки опубликованного результата.
- Crash после успешного `unlink`, но до queue acknowledgement, больше не превращает валидный job в постоянный failure.
- Repo polish добавил OBS CI badge, актуальную ASR performance секцию, точный delete contract и GitHub topics; Harvest CI получил bounded concurrency, timeout, named steps и checkout без persisted credentials.
- Session learnings сохранены в behavior tests и одной короткой repo-specific policy Harvest; отдельный исторический документ не создан.

## Архитектура

- Telegram/default file flow использует `short-message-v1`: русский, whole-file Silero, прежние model/decode/post-filter settings.
- OBS вызывает только `telegram-harvest --profile main transcribe-file --trusted-long-form` и принимает contract v3/profile v3/status `coverage-validated`.
- Long-form flow: bounded first/last Silero → 15 s language probe → selective RU punctuation seed без carry либо EN/auto без prompt → один native timestamped decode → coverage/repetition validation.
- OBS не дублирует model, Metal, beam, VAD, language или prompt policy Harvest.

## Проверка

- `go test ./internal/processor` после каждого repair cycle — зелёный.
- `make check` и `go test -race ./...` обоих репозиториев — зелёные.
- `staticcheck` и `govulncheck` обоих репозиториев — зелёные; вызываемых уязвимостей 0.
- `make install && make doctor` — зелёный; installed readback: contract `3`, profile `trusted-long-form-v3`, status `runtime-ready`, HEVC VideoToolbox и обе app signatures.
- Project Loop validate — зелёный.
- GitHub CI: OBS `f1c6605` и Harvest `b2fbd80` — зелёные; финальный docs/loop closure проходит отдельный post-push CI gate.
- Временные benchmark/E2E artifacts отсутствуют; тесты используют `t.TempDir()`.

## Остаточные Решения

- `coverage-validated` не означает WER/CER; он гарантирует structural timestamps и покрытие последней найденной речи.
- Публичный OBS repository пока без LICENSE. Лицензия не выбрана автоматически, потому что это решение владельца, а не безопасная техническая правка.

## Следующее Действие

- Использовать OBS как обычно; новые manifest получают version 3 и усиленный delete/retry contract.

## Источники Правды

- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/evidence/adaptive-long-form-benchmark.md`
