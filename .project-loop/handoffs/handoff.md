# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Цель

- После остановки OBS дать выбор для конкретной записи, локально расшифровать её общим Harvest ASR, получить компактный проверенный HEVC и безопасно применить выбранную delete/audio policy.

## Текущий Шаг

- active step: `STEP-011R`
- status: `в работе`
- requirements: `REQ-028..REQ-030`, `VAL-017`

## Итог Реализации

- Harvest contract v4 оставляет один публичный profile `adaptive-media-v1`; caller больше не выбирает short/long режим.
- Router сохраняет прежний `ru + no_timestamps` для обычных Telegram voice, использует timestamped long-form только при длительности от 180 секунд либо leading silence от 10 секунд и штатно пропускает no-speech.
- Language probe физически извлекает не более 15 секунд WAV. Russian punctuation seed, coverage guard и exact-cycle repetition policy входят в descriptor/cache identity.
- OBS удалил `--trusted-long-form`, принимает как короткий `transcribed`, так и длинный `coverage-validated` результат одного контракта.
- Независимый reviewer после двух targeted repairs дал `PASS`, findings отсутствуют.

## Итог Review

- Stale `ASSUME_SPEECH`, `--trusted-long-form` и прежние public profile IDs удалены из production surface; regression tests требуют их отклонять.
- OBS delete lifecycle теперь сохраняет в manifest v3 device/inode, размер и modification time source; identity повторно проверяется перед публикацией и `unlink`.
- Retry требует точного совпадения transcript и output media probe с manifest. Replacement source не удаляется; отсутствие source принимается только для `delete_source=true` после повторной проверки опубликованного результата.
- Crash после успешного `unlink`, но до queue acknowledgement, больше не превращает валидный job в постоянный failure.
- Repo polish добавил OBS CI badge, актуальную ASR performance секцию, точный delete contract и GitHub topics; Harvest CI получил bounded concurrency, timeout, named steps и checkout без persisted credentials.
- Session learnings сохранены в behavior tests и одной короткой repo-specific policy Harvest; отдельный исторический документ не создан.

## Архитектура

- Один public profile содержит две внутренние стратегии, потому что A/B доказал: timestamp mode для всех коротких media меняет текст и добавляет 0,7–1,8 секунды.
- Short: whole-file Silero bounds → прежний `ru + no_timestamps` decode → terminal cleanup.
- Long: bounded first/last Silero → 1 s lead-in → физический 15 s language probe → selective RU punctuation seed без carry либо EN/auto без prompt → один native timestamped decode → timestamps/tail coverage/exact-loop validation.
- OBS не дублирует model, Metal, beam, VAD, language, routing, prompt или repetition policy Harvest.

## Проверка

- `go test ./internal/processor` после каждого repair cycle — зелёный.
- `make check` и `go test -race ./...` обоих репозиториев — зелёные после final repair.
- `staticcheck` и `govulncheck` обоих репозиториев — зелёные; вызываемых уязвимостей 0.
- Telegram A/B: 42 real media без semantic regression; 6/6 fresh voice exact; short median overhead +0,021 s.
- Real interview: 1529 words, 199 monotonic segments, tail gap 0,334 s, total 42,30 s; exact transcript SHA совпал с ранее принятым long result.
- Clean installed production E2E: 15 s leading silence → offset 14,49 s, punctuated RU transcript, `coverage-validated`, gap 0, Metal true; HEVC 1512×982@30 + AAC readback зелёный.
- Installed `go version -m` показал `vcs.modified=false`; после closure commit установка повторяется из финального чистого HEAD.
- Disposable E2E source/result перемещены в Корзину; `.processing-*` и фоновые Whisper/processor процессы отсутствуют.
- Project Loop validate — зелёный.
- GitHub CI: OBS `f1c6605` и Harvest `b2fbd80` — зелёные; финальный docs/loop closure проходит отдельный post-push CI gate.
- Временные benchmark/E2E artifacts отсутствуют; тесты используют `t.TempDir()`.

## Остаточные Решения

- `coverage-validated` не означает WER/CER; он гарантирует structural timestamps и покрытие последней найденной речи.
- Публичный OBS repository пока без LICENSE. Лицензия не выбрана автоматически, потому что это решение владельца, а не безопасная техническая правка.

## Следующее Действие

- Запушить оба репозитория, дождаться CI и закрыть STEP-011R; mixed-language routing внутри одной записи остаётся явной отложенной границей.

## Источники Правды

- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/evidence/unified-adaptive-asr-benchmark.md`
