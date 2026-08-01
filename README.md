# OBS Interview Pipeline

После остановки записи OBS проект автоматически создаёт компактное HEVC-видео и Markdown-расшифровку. Исходник удаляется только после того, как `ffprobe` проверил финальное видео и оба результата были атомарно опубликованы.

## Что происходит после остановки записи

1. Lua-hook получает от OBS точный путь последней завершённой записи и кладёт его в локальную очередь.
2. macOS `LaunchAgent` запускает один worker. Worker параллельно вызывает канонический `telegram-harvest transcribe-file --assume-speech` и обрабатывает медиаконтейнер.
3. Готовый source HEVC `1512x982@30` копируется без повторного video encode; для старого или иного входа включается VideoToolbox transcode и только необходимые `scale`/`fps` filters.
4. Первая master audio track кодируется в AAC 96 Кбит/с. Результат проходит проверку кодека, разрешения, fps, длительности, одной аудиодорожки, её codec/bitrate и размера.
5. Готовая пара публикуется одним переименованием каталога. Только после этого исходник удаляется.

Результат записи `2026-08-01 15-49-42.mp4` выглядит так:

```text
~/Movies/Interviews/2026-08-01 15-49-42/
├── recording.mp4
├── transcript.md
└── manifest.json
```

`manifest.json` хранит техническое доказательство обработки: параметры source/output, ASR timings, флаг подтверждённого Metal runtime и выбранный video mode (`copy` либо `transcode`).

## Локальный профиль

- OBS canvas: `3024x1964`, физическое Retina-разрешение.
- OBS output: `1512x982`, логическое разрешение macOS.
- Частота: `30 fps`.
- Source: H.265/HEVC через Apple VideoToolbox + три выбранные в OBS AAC-дорожки. Они существуют до успешного завершения job.
- Final: тот же video stream без повторной потери качества, если source уже HEVC `1512x982@30`; одна первая master-дорожка AAC 96 Кбит/с. После delete gate раздельные source tracks не сохраняются.
- Video quality: OBS использует CRF quality `55`; fallback-transcode старого входа использует тот же `-q:v 55`. В шкале VideoToolbox большее число означает выше качество и больший ожидаемый bitrate/размер.
- ASR: production-вход Telegram Harvest с явным `--assume-speech`. Он пропускает только whole-file Silero gate для гарантированно речевой OBS-записи; `large-v3-turbo-q5_0`, Metal, русский decode profile и post-filter остаются каноническими в Harvest. Обычный Telegram workflow сохраняет Silero.
- Уведомление: клик по success notification открывает Finder сразу в каталоге готового собеседования.

## Установка и проверка

Требуются macOS, OBS Studio, Go, соседний `/Users/igor/projects/telegram-harvest` с готовым ASR runtime и `ffmpeg`:

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
- `com.igor.obs-interview-processor.plist` в `~/Library/LaunchAgents`.
- собственный `OBS Interview Notifier.app` в каталоге pipeline; внешняя notification-утилита не нужна.

Повторный `make install` мигрирует старую конфигурацию: удаляет дублированные Whisper/model keys, добавляет master audio bitrate 96 Кбит/с и сохраняет пользовательские output/quality/delete settings.

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
bin/obs-interview-processor enqueue "/Users/igor/Movies/recording.mp4"
```

## Инварианты безопасности

- Worker не сканирует старые файлы в `~/Movies`; он обрабатывает только пути, переданные OBS hook.
- Вход обязан быть обычным непустым `.mp4`, `.mov` или `.mkv` внутри `allowed_input_dir`.
- Jobs выполняются последовательно: два ASR/VideoToolbox pipeline одновременно не запускаются.
- При ошибке ASR, компрессии, проверки, публикации или удаления исходник остаётся на месте.
- Если output не меньше source, проверка не проходит и исходник сохраняется. На прямом HEVC fast path уменьшение дают удаление двух дополнительных audio tracks и master AAC 96 Кбит/с.
- Повтор после сбоя удаления заново проверяет уже опубликованные результаты и только затем повторяет удаление source.

## Исторический сквозной сценарий до fast path

Реальный тест 2026-08-01 прошёл через OBS event, Lua-hook, `LaunchAgent`, общий ASR-контракт Telegram Harvest и VideoToolbox:

- source: H.264, `1512x982@30`, 3 AAC tracks, 21.03 секунды, 16,765,693 байта;
- final: HEVC, `1512x982@30`, те же 3 AAC tracks, 21.07 секунды, 8,745,466 байт;
- размер уменьшился примерно на 48%;
- синтезированная русская фраза распознана полностью;
- manifest зафиксировал contract v1, `whispercpp`, `large-v3-turbo-q5_0`, русский язык и `metal_confirmed=true`;
- source отсутствовал после успешной публикации и проверки.
- UI-проверка временного notification открыла Finder точно в каталоге результата.

## Проверенный fast path

Current-head E2E `2026-08-01 17-38-24` прошёл через настоящий OBS stop event, Lua-hook, LaunchAgent и установленный Harvest:

- source: HEVC `1512x982@30`, 3 AAC × 160 Кбит/с, 29.23 секунды, 3,439,630 байт;
- final: тот же video stream (`compression.video_mode=copy`), 1 master AAC с target 96 Кбит/с, 1,863,425 байт;
- ASR: `large-v3-turbo-q5_0`, Metal, `ru`, beam 5, `terminal-exact-v1`, `speech_gate=0`;
- ASR занял 3.43 секунды, весь фоновый job — около 4 секунд;
- source удалён только после успешной публикации и media validation.

Средний bitrate AAC в коротком тесте с паузами получился 48.3 Кбит/с: `96k` — target кодировщика, а не обещание постоянного среднего bitrate для тишины. Проверка ограничивает верхний bitrate и принимает экономичное кодирование тишины, сохраняя строгие codec/track/duration проверки.
