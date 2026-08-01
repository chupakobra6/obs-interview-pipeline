# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Цель

- После остановки OBS дать выбор для конкретной записи, локально расшифровать её общим Harvest ASR, получить компактный проверенный HEVC и безопасно применить выбранную delete/audio policy.

## Текущий Шаг

- active step: `STEP-005R`
- status: `готово`

## Завершено

- После `OBS_FRONTEND_EVENT_RECORDING_STOPPED` Lua-hook неблокирующе открывает подписанный `OBS Interview Prompt.app` с точным именем записи.
- Dialog предлагает «Сжать и расшифровать» либо «Оставить без обработки», checkbox удаления после success и checkbox сведения дорожек; default — сохранить все дорожки раздельно.
- Queue job хранит immutable per-recording options: `delete_source` и `audio_mode=preserve|merge`; manifest и result повторяют policy.
- Default `preserve` кодирует каждую source track в AAC с target 96 Кбит/с; `merge` сводит все tracks через `amix` в одну AAC track.
- Validator проверяет ожидаемое число AAC streams, bitrate каждой дорожки, codec, dimensions, fps, duration и уменьшение размера до публикации/delete gate.
- OBS остаётся на HEVC Apple VideoToolbox, CRF quality 55, physical canvas 3024x1964, output 1512x982@30 и tracks bitmask 7 (дорожки 1–3).
- OBS вызывает только `telegram-harvest --profile main transcribe-file --trusted-long-form`; model q5_0, Metal, ru, beam 5, Silero settings и post-filter принадлежат Harvest.
- Trusted long-form короткими bounded Silero windows находит первый голос, сохраняет секундный lead-in и отправляет независимые 120-секундные chunks в один загруженный Whisper server.
- Реальный файл `2026-07-31 13-28-17`: первая речь обнаружена на 179.49 s; новая расшифровка началась с приветствия без `Продолжение следует`/`ibrahim`, дошла до прощания и заняла 37.82 s вместо прежних 32:42.
- Исправленный текст сохранён рядом с реальным результатом как `transcript.corrected.md`; исторические `transcript.md`/manifest не перезаписывались.
- OBS UI cancel test оставил source на месте и не создал queue job; process E2E точно передал unchecked delete и default preserve.
- Current-head OBS E2E `18-24-50`: 31.13 s, HEVC 1512x982@30 + 3 AAC, 4,917,452 B → video copy + 3 AAC, 3,417,831 B; ASR 5.28 s, весь job 16 s, source сохранён по checkbox.
- Disposable integration подтверждает оба audio mode: preserve оставляет 3 streams, merge создаёт 1 stream; failure/delete-retry/keep-source tests зелёные.
- Tooling-review удалил дублированную установку двух Swift apps в пользу одного installer helper; repo-polish синхронизировал README, help, doctor и current-head evidence.
- Test sources, result, job и диагностические WAV/TXT перемещены в именованный каталог Корзины; test notifier process завершён.

## Измененные Области

- Telegram Harvest: trusted long-form profile/runtime, descriptor/CLI/timings/tests/docs.
- OBS pipeline: native prompt, per-job policy, queue/processor/media validation, hook/install/doctor/tests/docs.
- Installed state: worker/config, два подписанных Swift apps, Lua-hook и LaunchAgent.

## Проверка

- Focused Go tests обоих репозиториев — зелёные.
- Native prompt type-check, signature и визуальный UI readback — зелёные; первоначальная тесная раскладка исправлена до финального E2E.
- `make doctor` подтвердил ffmpeg/ffprobe, Harvest trusted descriptor, q5_0/Metal/ru/beam 5, VideoToolbox, обе app signatures и LaunchAgent.
- Реальная повторная ASR-диагностика и OBS preserve E2E описаны выше.
- `make check` и `go test -race ./...` зелёные в обоих репозиториях.
- Финальный `make install && make doctor`, `plutil -lint`, обе `codesign --verify` и Project Loop validate зелёные.

## Агенты

- Subagents не использовались; multi-agent делегирование отключено. Review выполнен отдельным self-review проходом с `tooling-review` и `repo-polish`.

## Аудит Промптов

- Не применимо: delegation prompts отсутствовали.

## Риски И Поведение

- `96k` — target AAC encoder, а не постоянный средний bitrate; на тишине фактические значения ниже и принимаются validator.
- Merge может сделать общий микс тише из-за normalization; режим включается только явно и сохраняет полную длительность.
- VideoToolbox quality 55 — шкала качества: большее число обычно означает меньше потерь, больший bitrate и размер. Готовый OBS HEVC не перекодируется, поэтому fallback `-q:v 55` применяется только к старому/несовпадающему source.
- Обычные Telegram audio/video продолжают использовать whole-file Silero gate. Bounded long-form режим доступен только явному trusted caller и не меняет Telegram workflow.
- Старый реальный manifest остаётся историческим доказательством исходного прогона; новый исправленный transcript хранится отдельным файлом.

## Следующее Действие

- Использовать OBS как обычно: после Stop выбрать нужные параметры в dialog; результат появится в `~/Movies/Interviews/<timestamp>/`.

## Источники Правды

- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/handoffs/handoff.md`
