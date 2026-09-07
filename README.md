# OBS Interview Pipeline

[![CI](https://github.com/chupakobra6/obs-interview-pipeline/actions/workflows/ci.yml/badge.svg)](https://github.com/chupakobra6/obs-interview-pipeline/actions/workflows/ci.yml)

После остановки записи OBS проект показывает короткий macOS-диалог: обработать запись или оставить как есть, удалять ли исходник после успеха и сводить ли аудиодорожки. Выбранная запись превращается в компактное HEVC-видео и Markdown-расшифровку; удаление возможно только после полной проверки результата.

## Что происходит после остановки записи

1. Lua-hook получает от OBS точный путь последней завершённой записи и неблокирующе открывает `OBS Interview Prompt.app`.
2. Кнопка «Оставить без обработки» ничего не меняет. Кнопка «Сжать и расшифровать» кладёт в очередь путь и выбранные для этой записи параметры; macOS `LaunchAgent` запускает один worker.
3. Worker сначала обрабатывает медиаконтейнер, затем вызывает канонический адаптивный `telegram-harvest transcribe-file`. Тяжёлые VideoToolbox и Whisper-этапы не конкурируют за ресурсы Mac.
4. Готовый source HEVC `1512x982@30` копируется без повторного video encode; для старого или иного входа включается приоритетный по скорости VideoToolbox transcode и только необходимые `scale`/`fps` filters.
5. По умолчанию все source audio tracks сохраняются раздельно и кодируются в AAC с target 96 Кбит/с. Галочка merge сводит их в одну AAC-дорожку. Проверка подтверждает ожидаемое число streams, codec/bitrate, разрешение, fps, длительность и уменьшение размера.
6. Готовая тройка файлов публикуется одним переименованием каталога. Исходник удаляется только при выбранной галочке и только после этой публикации и повторной проверки.

Результат записи `2026-08-01 15-49-42.mp4` выглядит так:

```text
~/Movies/Interviews/2026-08-01 15-49-42/
├── recording.mp4
├── transcript.md
└── manifest.json
```

`manifest.json` хранит техническое доказательство обработки: identity и параметры source, точный output probe, ASR timings, флаг подтверждённого Metal runtime и выбранный video mode (`copy` либо `transcode`). Перед повторной попыткой удаления worker сверяет source identity, transcript и output с этим manifest.

## Локальный профиль

- OBS canvas: `3024x1964`, физическое Retina-разрешение.
- OBS output: `1512x982`, логическое разрешение macOS.
- Частота: `30 fps`.
- Source: H.265/HEVC через Apple VideoToolbox + три выбранные в OBS AAC-дорожки.
- Final: тот же video stream без повторной потери качества, если source уже HEVC `1512x982@30`; по умолчанию те же три дорожки в AAC с target 96 Кбит/с либо одна сведённая дорожка при выборе merge.
- Video quality: OBS использует CRF quality `55`; fallback-transcode старого входа использует тот же `-q:v 55`. В шкале VideoToolbox большее число означает выше качество и больший ожидаемый bitrate/размер.
- ASR: единый production-вход Telegram Harvest сам выбирает short либо protected long-form по длительности и leading silence. Short-form сохраняет быстрый `no_timestamps` decode, но определяет язык автоматически. Для интервью bounded Silero находит первую и последнюю речь, сохраняет секундный lead-in, физический 15-секундный probe выбирает language policy, после чего один timestamped request ведёт контекст до конца. Harvest проверяет duration/timestamps, хвост и extreme repetition и возвращает contract v4: `profile_id=adaptive-media-v2`, `validation_status=coverage-validated`. OBS не передаёт ASR-настройки и не содержит отдельного профиля.
- Уведомление: клик по success notification открывает Finder сразу в каталоге готового собеседования.

## Установка и проверка

Требуются macOS, OBS Studio, Go 1.26.5, соседний [Telegram Harvest](https://github.com/chupakobra6/telegram-harvest) в `/Users/igor/projects/telegram-harvest` с готовым ASR runtime и `ffmpeg`:

```bash
brew install ffmpeg
```

```bash
make setup
make check
make install
make doctor
```

`make install` устанавливает:

- бинарник и конфигурацию в `~/Library/Application Support/obs-interview-pipeline`;
- Lua-hook в `~/Library/Application Support/obs-studio/scripts`;
- `com.igor.obs-interview-processor.plist` в `~/Library/LaunchAgents`;
- собственный `OBS Interview Notifier.app` в каталоге pipeline — внешняя notification-утилита не нужна;
- собственный `OBS Interview Prompt.app` с выбором параметров конкретной записи.

Повторный `make install` мигрирует старую конфигурацию до текущей схемы и сохраняет пользовательские output/quality/delete defaults.

Lua-hook один раз добавляется через `OBS → Сервис → Скрипты`. OBS сохраняет путь в текущей коллекции сцен и загружает скрипт при следующих запусках.

## Где смотреть состояние

```text
~/Library/Application Support/obs-interview-pipeline/
├── config.json
├── queue/
├── done/
├── failed/
└── logs/
```

- `queue/` содержит ожидающие записи; наличие файла автоматически будит worker.
- `done/` и `failed/` сохраняют компактный статус каждого задания.
- `logs/processor.log` и `logs/processor-error.log` показывают фоновые запуски.
- success notification открывает `~/Movies/Interviews/<recording>/`; error notification — каталог исходной записи. Для `OBS Interview Notifier` используется стиль «Временно»: баннер исчезает с экрана, но остаётся в Центре уведомлений. Невидимый helper живёт, пока карточка доступна: клик открывает нужную папку, а ручная очистка карточки завершает helper без фонового хвоста.

Проверка окружения:

```bash
bin/obs-interview-processor doctor
```

Ручная постановка конкретной новой записи в очередь:

```bash
bin/obs-interview-processor enqueue --delete-source=false --audio-mode=preserve "/Users/igor/Movies/recording.mp4"
```

## Инварианты безопасности

- Worker не сканирует старые файлы в `~/Movies`; он обрабатывает только пути, переданные OBS hook.
- Вход обязан быть обычным непустым `.mp4`, `.mov` или `.mkv` внутри `allowed_input_dir`.
- Jobs выполняются последовательно: два ASR/VideoToolbox pipeline одновременно не запускаются.
- Проверенный сжатый файл сохраняется в скрытом checkpoint-каталоге при последующей ошибке ASR. Retry сверяет source identity, параметры обработки и полный media probe, затем повторяет только ASR; повреждённый или устаревший checkpoint автоматически пересобирается.
- Сжатие и ASR имеют отдельные safety timeouts, зависящие от длительности записи. Зависший этап завершается, исходник и валидный checkpoint остаются для диагностики и безопасного retry.
- При ошибке ASR, компрессии, проверки или публикации исходник остаётся на месте; `unlink` начинается только после этих gates. Если worker завершился уже после успешного `unlink`, retry распознаёт это состояние по валидному опубликованному результату.
- Device/inode, размер и modification time source фиксируются до работы и повторно проверяются перед публикацией и `unlink`; файл, заменённый по тому же пути во время обработки или retry, не удаляется.
- Retry после публикации требует точного совпадения transcript и media probe с manifest. Если предыдущий worker уже удалил source, повтор считается успешным только для `delete_source=true` и только после повторной проверки опубликованного результата.
- Если output не меньше source, проверка не проходит и исходник сохраняется. На прямом HEVC fast path уменьшение даёт перекодирование каждой AAC-дорожки со 160 до target 96 Кбит/с; для тишины фактический средний bitrate может быть заметно ниже target.
- Повтор после сбоя удаления заново проверяет уже опубликованные результаты и только затем повторяет удаление source.

## Границы ASR

`coverage-validated` подтверждает структуру timestamps и покрытие хвоста, но не точность каждого слова или WER/CER. Определяется доминирующий язык; переключение языков внутри одной long-form записи остаётся отложенным. Short-form использует `language=auto` без отдельного probe, prompt или timestamps.

Модель, Metal и быстрые decode settings принадлежат Harvest. При сравнении long-form алгоритмов медианное время не должно ухудшаться более чем на 10% без доказанного выигрыша качества и отдельного обоснования. Проверять RU/EN, слабую речь, leading silence, no-speech, начало/середину/конец, повторы и prompt leakage; реальная запись с silver reference не заменяет ground truth.

[Проверка унификации ASR](docs/benchmarks/unified-adaptive-asr-benchmark.md) сохраняет доказательства выбора одного публичного профиля. Локальный ASR-вход не выполняет Telegram RPC и не меняет Telegram state.

## Производительность ASR

На реальном интервью исходный файл длительностью `971,97 с` содержал `178,49 с` pre-roll; после trimming Whisper декодировал `793,41 с` аудио. Свежий production-прогон занял `47,60 с`: это `20,42× realtime` относительно всего файла или консервативные `16,67×` относительно реально декодированной части. Языковой probe занял `0,89 с`, long-form preparation — `1,67 с`, хвост ASR отстал от последней VAD-речи только на `0,33 с`.

На английском ground-truth sample длительностью `204,78 с` тот же профиль закончил за `9,91 с` (`20,66× realtime`, WER `0,96%`). Практический диапазон на текущем Mac — примерно `15–21× realtime` в зависимости от Metal-нагрузки. Полная матрица вариантов и оговорки по silver reference находятся в [adaptive-long-form-benchmark.md](docs/benchmarks/adaptive-long-form-benchmark.md).

Это скорость ASR. Полный job также включает AAC/HEVC обработку и публикацию. Для готового OBS HEVC video stream копируется, поэтому длительное повторное video encode отсутствует. Для нестандартного H.264 worker сначала заканчивает быстрый VideoToolbox transcode, сохраняет checkpoint и только затем запускает Whisper: последовательность предотвращает резкое замедление обоих тяжёлых этапов и исключает повторный encode при ошибке ASR.

## Проверенный HEVC fast path

Current-head E2E `2026-08-01 18-24-50` прошёл через настоящий OBS Stop, новый native dialog, Lua-hook, LaunchAgent и установленный Harvest:

- source: HEVC `1512x982@30`, 3 AAC × ~160 Кбит/с, 31.13 секунды, 4,917,452 байта;
- в dialog отключено удаление и оставлен default `preserve`;
- final: тот же video stream (`compression.video_mode=copy`), 3 AAC с target 96 Кбит/с, 3,417,831 байт;
- ASR точно сохранил первое предложение после 6.33 секунды pre-roll; `large-v3-turbo-q5_0`, Metal, `ru`, beam 5, `terminal-exact-v1`;
- ASR занял 5.28 секунды, весь фоновый job — 16 секунд вместе с запуском worker, медиапроверкой и публикацией;
- исходник остался на месте согласно выбранной галочке.

Средние bitrates трёх AAC-дорожек с паузами получились 42.6, 38.0 и 9.4 Кбит/с: `96k` — target кодировщика, а не постоянный bitrate. Disposable integration отдельно подтверждает режим merge с одной выходной AAC-дорожкой.
