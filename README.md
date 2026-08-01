# OBS Interview Pipeline

После остановки записи OBS проект автоматически создаёт сжатое HEVC-видео и Markdown-расшифровку. Исходный H.264-файл удаляется только после того, как `ffprobe` проверил финальное видео и оба результата были атомарно опубликованы.

## Что происходит после остановки записи

1. Lua-hook получает от OBS точный путь последней завершённой записи и кладёт его в локальную очередь.
2. macOS `LaunchAgent` запускает один worker. Worker параллельно транскрибирует первую аудиодорожку и кодирует видео через Apple VideoToolbox.
3. Результат проходит проверку кодека, разрешения, fps, длительности, числа аудиодорожек и размера.
4. Готовая пара публикуется одним переименованием каталога. Только после этого исходник удаляется.

Результат записи `2026-08-01 15-49-42.mp4` выглядит так:

```text
~/Movies/Interviews/2026-08-01 15-49-42/
├── recording.mp4
├── transcript.md
└── manifest.json
```

`manifest.json` хранит техническое доказательство обработки: параметры source/output, ASR timings и флаг подтверждённого Metal runtime.

## Локальный профиль

- OBS canvas: `3024x1964`, физическое Retina-разрешение.
- OBS output: `1512x982`, логическое разрешение macOS.
- Частота: `30 fps`.
- Source: H.264 + все выбранные в OBS AAC-дорожки.
- Final: H.265/HEVC `1512x982@30` через `hevc_videotoolbox`; AAC-дорожки копируются без перекодирования.
- ASR: тот же локальный runtime, что у Telegram Harvest — `large-v3-turbo-q5_0`, русский язык, 4 threads, beam size 5, whole-file Silero gate и обязательное подтверждение Metal.

## Установка и проверка

```bash
make check
make install
make doctor
```

`make install` устанавливает:

- бинарник и конфигурацию в `~/Library/Application Support/obs-interview-pipeline`;
- Lua-hook в `~/Library/Application Support/obs-studio/scripts`;
- `com.igor.obs-interview-processor.plist` в `~/Library/LaunchAgents`.

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
- Jobs выполняются последовательно: два Whisper/VideoToolbox pipeline одновременно не запускаются.
- При ошибке ASR, компрессии, проверки, публикации или удаления исходник остаётся на месте.
- Если output не меньше source, проверка не проходит и исходник сохраняется.
- Повтор после сбоя удаления заново проверяет уже опубликованные результаты и только затем повторяет удаление source.

## Проверенный сквозной сценарий

Реальный тест 2026-08-01 прошёл через OBS event, Lua-hook, `LaunchAgent`, production Whisper и VideoToolbox:

- source: H.264, `1512x982@30`, 3 AAC tracks, 29.47 секунды, 23,526,751 байт;
- final: HEVC, `1512x982@30`, те же 3 AAC tracks, 29.50 секунды, 5,877,841 байт;
- размер уменьшился примерно на 75%;
- синтезированная русская фраза распознана полностью;
- `metal_confirmed=true`;
- source отсутствовал после успешной публикации и проверки.
