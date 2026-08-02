# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Цель

- После остановки OBS дать выбор для конкретной записи, локально расшифровать её общим Harvest ASR, получить компактный проверенный HEVC и безопасно применить выбранную delete/audio policy.

## Текущий Шаг

- active step: `STEP-009R`
- status: `готово`

## Новая Дельта S013

- Удачный punctuated-initial-context A/B принят как сильный кандидат, но не как заранее выбранное решение.
- Нужно сравнить native baseline, статический/языково-адаптивный style prompt и один dynamic bootstrap на одинаковой large-v3-turbo-q5_0/Metal базе; multi-chunk остаётся последним кандидатом.
- Corpus должен включать русский и английский ground truth, edge cases и реальное интервью с честно обозначенным silver reference.
- Итоговая production surface: основной Telegram ASR и один адаптивный OBS long-form profile; третий профиль остаётся только при подтверждённом caller/use case.
- Рабочие деревья Telegram Harvest и OBS перед STEP-009A чисты; упомянутые в отчёте Яна чужие изменения отсутствуют.
- Benchmark завершён: universal/bilingual/language-matched prompt и content bootstrap дали повторы на длинном English; punctuation-only потерял текст; carry вышел за audio и выдумал титры.
- Выбрана одна policy: 15 s language-only probe; RU — minimal punctuation seed без carry, EN — без prompt, остальные — auto без prompt; затем один native timestamped decode.
- Реальный current-head: 1529 слов, 253 знака, pseudo-WER 18,71%, recall 94,75%, F1 90,08%, wall 53,32 с; VAD/ASR gap 0,33 с; greeting/farewell сохранены.
- Contract/profile подняты до `3 / trusted-long-form-v3`; `--assume-speech` и `trusted-speech-v1` удалены как неиспользуемые, остаются два публичных ASR profile.
- `make check` и `go test -race ./...` зелёные в обоих репозиториях; installed `make doctor` принял v3.
- Installed disposable E2E распознал English без prompt, сохранил 3 AAC и получил HEVC 1512x982@30: 12 653 787 → 4 431 383 bytes; `delete_source=false` сохранил source.
- Первый почти пустой HEVC fixture был штатно отклонён delete/media gate как не уменьшающий размер; более репрезентативный H.264 fixture прошёл весь flow.
- Временный `/tmp/obs-asr-policy.23ljVp` с corpus/results/disposable media перемещён в Корзину; процессы Whisper/benchmark и временные installed-state хвосты отсутствуют.

## Новая Дельта S012

- Предложение явно отправлять `max_len=-1` проверено и отклонено как no-op для текущего режима.
- Installed server действительно заменяет `max_len=0` на 60, но installed core вызывает character wrapping только при `token_timestamps=true`; Harvest long-form отправляет `token_timestamps=false`.
- Два fresh-process прогона на вариант дали exact-identical text/tokens/344 segments/timestamps; maximum segment уже 190 chars, поэтому 60-char wrap не активен.
- Median runtime 48.48 s (`0`) и 50.86 s (`-1`); ни структурного, ни performance improvement нет. Production-код и contract/profile не менялись.
- Первый same-server probe показал межзапросную вариативность и был исключён как несоответствующий one-shot OBS production; итоговый A/B использовал свежий процесс на каждый запрос.
- Все A/B JSON/log/time/WAV и тестовые whisper-server процессы удалены после фиксации агрегированных evidence.

## Новая Дельта S011

- Native timestamped long-form остаётся основным и единственным OBS decode path.
- Межрепозиторный контракт поднят до v2 с явными profile/status вместо дублирования Harvest internals в OBS.
- Bounded tail VAD проверяет достижение последней речи; статус `coverage-validated` описывает именно эту гарантию и не выдаётся за WER/CER.
- Реальный предварительный tail probe занял 0,64 с и нашёл конец речи на 965,41 с source / 786,92 с trimmed WAV; предыдущий ASR last segment 787,40 с покрывает границу.
- Full-file VAD gap scan и отдельный punctuation formatter не входят в текущий шаг.

## Новая Дельта S009

- Fixed chunks `120 s + 1 s overlap`, chunk extraction и word-overlap merge удалены.
- После bounded leading trim Harvest отправляет один request с `no_timestamps=false`, `token_timestamps=false`; нативный Whisper ведёт окна и контекст внутри одного decode.
- Ответ fail-fast проверяется по полной audio duration и монотонным segment timestamps; diagnostics проходят через общий CLI-контракт в OBS manifest.
- Узкий long-form post-filter схлопывает только одинаковые подряд финальные строки, оставляя одно реальное закрывающее слово; обычный Telegram post-filter не менялся.
- Pause-aware chunks и prompt chaining не добавлялись: real-file experiment доказал, что они не нужны.

## Завершено

- После `OBS_FRONTEND_EVENT_RECORDING_STOPPED` Lua-hook неблокирующе открывает подписанный `OBS Interview Prompt.app` с точным именем записи.
- Dialog предлагает «Сжать и расшифровать» либо «Оставить без обработки», checkbox удаления после success и checkbox сведения дорожек; default — сохранить все дорожки раздельно.
- Queue job хранит immutable per-recording options: `delete_source` и `audio_mode=preserve|merge`; manifest и result повторяют policy.
- Default `preserve` кодирует каждую source track в AAC с target 96 Кбит/с; `merge` сводит все tracks через `amix` в одну AAC track.
- Validator проверяет ожидаемое число AAC streams, bitrate каждой дорожки, codec, dimensions, fps, duration и уменьшение размера до публикации/delete gate.
- OBS остаётся на HEVC Apple VideoToolbox, CRF quality 55, physical canvas 3024x1964, output 1512x982@30 и tracks bitmask 7 (дорожки 1–3).
- OBS вызывает только `telegram-harvest --profile main transcribe-file --trusted-long-form`; model q5_0, Metal, ru, beam 5, Silero settings и post-filter принадлежат Harvest.
- Trusted long-form короткими bounded Silero windows находит первый голос, сохраняет секундный lead-in и одним timestamped request декодирует весь остаток без внешней нарезки.
- Реальный файл `2026-07-31 13-28-17`: offset 178.49 s; 793.41 s trimmed audio, 344 монотонных segments до 787.40 s; 1455 слов, приветствие и прощание, runtime 38.38 s.
- Fixed-chunk v1 дал 1381 слово и артефакты у прежних границ; one-shot без timestamps дал только 290 слов и зациклился. Новый flow убрал дубль `документация` около 240 s и восстановил выпавшую фразу около 600 s.
- Исторические `transcript.md`, `transcript.corrected.md` и manifest реального результата не перезаписывались; A/B artifacts создавались только во временном каталоге.
- OBS UI cancel test оставил source на месте и не создал queue job; process E2E точно передал unchecked delete и default preserve.
- Current-head OBS E2E `18-24-50`: 31.13 s, HEVC 1512x982@30 + 3 AAC, 4,917,452 B → video copy + 3 AAC, 3,417,831 B; ASR 5.28 s, весь job 16 s, source сохранён по checkbox.
- Disposable integration подтверждает оба audio mode: preserve оставляет 3 streams, merge создаёт 1 stream; failure/delete-retry/keep-source tests зелёные.
- Tooling-review удалил дублированную установку двух Swift apps в пользу одного installer helper; repo-polish синхронизировал README, help, doctor и current-head evidence.
- Test sources, result, job и диагностические WAV/TXT перемещены в именованный каталог Корзины; test notifier process завершён.
- Contract v2 добавляет `short-message-v1`, `trusted-speech-v1`, `trusted-long-form-v2` и точные validation statuses; OBS принимает только long-form coverage status, а doctor — runtime-ready.
- Harvest bounded-окнами находит обе границы речи; parser проверяет объявленное число и timestamps VAD, timestamped validator отклоняет хвост дальше двухсекундного допуска.
- Current-head real rerun сохранил 1455 слов и 344 segments; VAD end 786.91 s, ASR end 787.40 s, coverage gap 0, preparation 1.47 s, wall 47.44 s.
- Tooling-review схлопнул повторную OBS-проверку публичного контракта в один helper; временный benchmark transcript/result удаляется после фиксации evidence.

## Измененные Области

- Telegram Harvest: trusted long-form profile/runtime, descriptor/CLI/timings/tests/docs.
- OBS pipeline: native prompt, per-job policy, queue/processor/media validation, hook/install/doctor/tests/docs.
- Installed state: worker/config, два подписанных Swift apps, Lua-hook и LaunchAgent.

## Проверка

- Focused Go tests обоих репозиториев — зелёные; regression на 240 s доказывает один request и непрерывную фразу/термин через прежнюю границу 120 s.
- Native prompt type-check, signature и визуальный UI readback — зелёные; первоначальная тесная раскладка исправлена до финального E2E.
- `make doctor` подтвердил ffmpeg/ffprobe, Harvest trusted descriptor, q5_0/Metal/ru/beam 5, VideoToolbox, обе app signatures и LaunchAgent.
- Реальная повторная ASR-диагностика и три A/B-варианта описаны выше; terminal diagnostics зафиксировали удаление трёх повторов `спасибо`.
- `make check` и `go test -race ./...` зелёные в обоих репозиториях.
- Финальный `make install && make doctor`, `plutil -lint`, обе `codesign --verify` и Project Loop validate зелёные.
- Contract v2 negative tests, trailing tolerance pass/fail и malformed VAD count tests зелёные; current-head real response вернул `coverage-validated`.

## Агенты

- Subagents не использовались; multi-agent делегирование отключено. Review выполнен отдельным self-review проходом с `tooling-review` и `repo-polish`.

## Аудит Промптов

- Не применимо: delegation prompts отсутствовали.

## Риски И Поведение

- `96k` — target AAC encoder, а не постоянный средний bitrate; на тишине фактические значения ниже и принимаются validator.
- Merge может сделать общий микс тише из-за normalization; режим включается только явно и сохраняет полную длительность.
- VideoToolbox quality 55 — шкала качества: большее число обычно означает меньше потерь, больший bitrate и размер. Готовый OBS HEVC не перекодируется, поэтому fallback `-q:v 55` применяется только к старому/несовпадающему source.
- Обычные Telegram audio/video продолжают использовать whole-file Silero gate и `no_timestamps=true`. Native timestamped long-form доступен только явному trusted caller и не меняет Telegram workflow.
- Segment timestamps нужны декодеру для полноты; у turbo-модели текст менее пунктуирован, чем первые no-timestamp окна, но контрольный no-timestamp one-shot потерял 80% разговора и ушёл в repetition loop.
- Старый реальный manifest и оба исторических transcript остаются доказательством предыдущих прогонов; benchmark-копия не публикуется в каталог собеседования.
- `coverage-validated` не доказывает WER/CER и не исключает внутреннюю лексическую ошибку; он доказывает structural timestamps и покрытие последней VAD-речи. Full-file gap scan сознательно не добавлен из-за стоимости и слабой дополнительной гарантии без эталона.

## Следующее Действие

- Использовать OBS как обычно: после Stop выбрать нужные параметры в dialog; результат появится в `~/Movies/Interviews/<timestamp>/`.

## Источники Правды

- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/handoffs/handoff.md`
