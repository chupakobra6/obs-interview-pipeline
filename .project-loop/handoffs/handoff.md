# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Цель
- Автоматически обрабатывать завершённые OBS-записи: локальный ASR, H.265, медиапроверка, атомарная публикация и удаление source только после success.

## Текущий Шаг
- active step: `STEP-001R`
- status: `готово`

## Завершено
- Создан Go worker с устойчивой очередью, `flock`, production Whisper/Silero/Metal ASR, VideoToolbox HEVC и строгим delete gate.
- Установлены бинарник/config, `LaunchAgent` и Lua-hook; hook сохранён в текущей OBS scene collection.
- OBS настроен: physical canvas 3024x1964, output 1512x982, 30 fps.
- Реальный OBS E2E прошёл: 23,526,751 → 5,877,841 байт, 3 AAC сохранены, фраза распознана полностью, source удалён после validation.
- Disposable final и diagnostic frame перемещены в Корзину после фиксации evidence.

## Измененные Файлы
- Весь новый репозиторий `/Users/igor/projects/obs-interview-pipeline`.
- Installed state: `~/Library/Application Support/obs-interview-pipeline`, OBS scripts directory и `~/Library/LaunchAgents/com.igor.obs-interview-processor.plist`.
- OBS profile/scene config обновлены через UI.

## Проверка
- `make check` — зелёный.
- `go test -race ./...` — зелёный.
- `plutil -lint` LaunchAgent — OK.
- `make doctor` — ffmpeg/ffprobe/Whisper/models/VideoToolbox/LaunchAgent OK.
- `loopctl.py validate` — зелёный.
- OBS log подтверждает `hook loaded`, exact recording path и `queued` на `RECORDING_STOPPED`.
- `ffprobe` E2E подтверждает HEVC 1512x982@30, длительность 29.50 s и 3 AAC streams.

## Агенты
- Subagents не использовались; review выполнен отдельным self-review проходом после реализации и E2E.

## Аудит Промптов
- Не применимо: delegation prompts отсутствовали.

## Пользовательские Дельты
- Исходная инструкция и уточнение сохранены в `.project-loop/intake/raw/obs-interview-automation-brief.md`; дельт после начала реализации не было.

## Риски И Блокеры
- Финальный HEVC использует `hvc1`: штатно воспроизводится на macOS, но может требовать HEVC support на других платформах.
- ASR runtime и модели читаются из текущего пути Telegram Harvest; `make doctor` обнаружит их перемещение/удаление до следующей диагностики.
- Source удаляется permanent unlink по прямому требованию Игоря; при любом failure до delete gate он остаётся.

## Следующее Действие
- Использовать OBS как обычно: после «Остановить запись» дождаться macOS notification; результат появится в `~/Movies/Interviews/<timestamp>/`.

## Обновленные Источники Правды
- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/handoffs/handoff.md`
