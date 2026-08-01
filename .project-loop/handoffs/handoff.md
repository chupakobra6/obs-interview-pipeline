# Handoff

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Цель
- Автоматически обрабатывать завершённые OBS-записи: локальный ASR, H.265, медиапроверка, атомарная публикация и удаление source только после success.

## Текущий Шаг
- active step: `STEP-003R`
- status: `готово`

## Завершено
- Production ASR и его профиль теперь принадлежат Telegram Harvest; OBS вызывает версионированный локальный `transcribe-file` контракт без Telegram RPC.
- OBS worker хранит только adapter/оркестрацию, параллельно запускает общий ASR и VideoToolbox HEVC, затем применяет строгий delete gate.
- Установлены бинарник/config, `LaunchAgent` и Lua-hook; hook сохранён в текущей OBS scene collection.
- Установлен подписанный `OBS Interview Notifier.app`: временная карточка остаётся в Центре уведомлений, helper живёт до клика и открывает точный каталог результата.
- OBS настроен: physical canvas 3024x1964, output 1512x982, 30 fps.
- Current-head OBS E2E прошёл: 16,765,693 → 8,745,466 байт, 3 AAC сохранены, фраза распознана полностью общим Harvest ASR, source удалён после validation.
- Alert style readback — «Временно»; UI-клик открыл Finder в E2E-каталоге с video/transcript/manifest.
- Уведомления при видеоповторе/общем доступе к экрану возвращены в режим «уведомления выкл.».
- Реальное собеседование `2026-07-31 13-28-17` прошло pipeline: 363,398,845 → 253,265,579 B, 3 AAC, 971.97 s, Harvest ASR/Metal; source удалён после success.
- Tooling-review/repo-polish добавил Swift type-check в `make check`, перевёл CI на macOS, сделал doctor deterministic и закрыл lifecycle helper после очистки карточки.
- Disposable output, test job/error history, `.DS_Store` и repo build artifacts перемещены в Корзину/очищены; runtime оставляет только реальный result/job/notifier.

## Измененные Файлы
- Весь новый репозиторий `/Users/igor/projects/obs-interview-pipeline`.
- Installed state: `~/Library/Application Support/obs-interview-pipeline`, OBS scripts directory и `~/Library/LaunchAgents/com.igor.obs-interview-processor.plist`.
- OBS profile/scene config обновлены через UI.

## Проверка
- `make check` — зелёный.
- `go test -race ./...` — зелёный.
- `plutil -lint` LaunchAgent — OK.
- `make doctor` — ffmpeg/ffprobe, Telegram Harvest ASR/Metal, VideoToolbox, notifier signature и LaunchAgent OK.
- `loopctl.py validate` — зелёный.
- OBS log подтверждает `hook loaded`, exact recording path и `queued` на `RECORDING_STOPPED`.
- E2E manifest подтверждает HEVC 1512x982@30, длительность 21.07 s, 3 AAC streams и ASR contract v1.
- Computer Use readback подтверждает стиль «Временно» и точный каталог Finder после клика.
- Real-job manifest/ffprobe и done JSON подтверждают текущий installed pipeline без error.
- Repo/runtime/process inventory после cleanup не показывает `.processing-*`, temp E2E dirs или test notifier processes.

## Агенты
- Subagents не использовались; review выполнен отдельным self-review проходом после реализации и E2E.

## Аудит Промптов
- Не применимо: delegation prompts отсутствовали.

## Пользовательские Дельты
- S003–S006 сохранены в `.project-loop/intake/user-deltas.md`: общий Harvest ASR, notification, реальный E2E и финальный cleanup/review.

## Риски И Блокеры
- Финальный HEVC использует `hvc1`: штатно воспроизводится на macOS, но может требовать HEVC support на других платформах.
- OBS зависит от соседнего пути Telegram Harvest; `make doctor` обнаружит его перемещение, проблемы сборки или ASR runtime до следующей записи.
- Невидимый notifier-процесс ждёт, пока карточка доступна; клик или очистка карточки завершают его.
- Source удаляется permanent unlink по прямому требованию Игоря; при любом failure до delete gate он остаётся.

## Следующее Действие
- Использовать OBS как обычно: после «Остановить запись» дождаться macOS notification; результат появится в `~/Movies/Interviews/<timestamp>/`.

## Обновленные Источники Правды
- `.project-loop/requirements/source-map.md`
- `.project-loop/requirements/checklist.md`
- `.project-loop/plan/delivery-plan.md`
- `.project-loop/plan/current-step.md`
- `.project-loop/handoffs/handoff.md`
