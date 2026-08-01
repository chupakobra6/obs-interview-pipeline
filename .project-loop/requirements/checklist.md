# Чеклист Требований

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Значения Статусов
Используй `кандидат`, `принято`, `в работе`, `готово`, `отложено`, `заблокировано` или `отклонено`.

## Требования
| ID | Статус | Источник | Требование | Критерий приемки | Доказательства |
| --- | --- | --- | --- | --- | --- |
| REQ-001 | `готово` | S001, S002 | После `OBS_FRONTEND_EVENT_RECORDING_STOPPED` точный путь последней записи автоматически попадает в устойчивую локальную очередь. | OBS не блокируется; задание переживает завершение OBS и перезапуск worker. | OBS log `15:50:12.143 … queued`; LaunchAgent run 1, exit 0; job в `done/`. |
| REQ-002 | `готово` | S001, S002 | Запись транскрибируется локально production-профилем Telegram Harvest: Whisper large-v3-turbo q5_0, русский, Metal, beam 5 и whole-file Silero gate. | Интеграционный прогон создает UTF-8 Markdown-транскрипт, runtime подтверждает Metal. | Реальная русская фраза распознана полностью; manifest: `speech_detected=true`, `metal_confirmed=true`. |
| REQ-003 | `готово` | S001, S002 | Видео сжимается в H.265 через Apple VideoToolbox с сохранением всех аудиодорожек. | `ffprobe` подтверждает HEVC, 1512x982, 30 fps, исходное число audio streams и близкую длительность; финальный файл меньше исходного. | E2E: H.264 23,526,751 B → HEVC 5,877,841 B; 1512x982@30; 3 AAC; 29.47→29.50 s. |
| REQ-004 | `готово` | S001 | Исходник удаляется только после успешных ASR, сжатия, атомарной публикации и медиапроверки. | Инъекция ошибки оставляет исходник; успешный disposable E2E удаляет только тестовый исходник. | Unit failure/delete-retry tests + disposable ffmpeg integration + реальный OBS source отсутствует после success. |
| REQ-005 | `готово` | S001, S002 | OBS записывает итог 1512x982 при 30 fps, сохраняя физический canvas 3024x1964 для Retina capture. | Readback профиля и пробная запись подтверждают разрешение и fps. | `basic.ini`: Base 3024x1964, Output 1512x982, FPSCommon=30; source ffprobe совпал. |
| REQ-006 | `готово` | S001, S004 | Пользователь получает понятные логи и macOS-уведомление об успехе либо ошибке. | Интеграционный прогон создаёт job log/status и отправляет best-effort notification. | LaunchAgent stdout/error logs, done/failed job JSON и подписанный `OBS Interview Notifier.app` встроены; E2E status сохранён. |
| REQ-007 | `готово` | S003 | Единственная production-реализация и профиль Whisper находятся в Telegram Harvest; OBS pipeline вызывает его локальный ASR-вход. | В OBS-репозитории отсутствуют Whisper server/gate/decode реализации и пути моделей; сквозной тест проходит через текущий код Telegram Harvest. | OBS вызывает `telegram-harvest --profile main transcribe-file`; manifest E2E: contract v1, whispercpp/Metal/q5_0/ru/beam 5/Silero. |
| REQ-008 | `готово` | S003, S004 | Клик по временному success-уведомлению macOS открывает Finder в каталоге готового собеседования; helper ждёт отложенного клика. | UI-тест клика активирует Finder и показывает точный output directory; alert style readback равен «Временно». | Notification Center показал карточку helper; клик открыл `2026-08-01 16-09-58` с manifest/video/transcript; helper завершился по клику. |
| REQ-009 | `готово` | S003 | OBS-проект оформлен согласованно с соседними Go-проектами и ведётся в Git. | README/Makefile/CI дают понятный setup и validation; Git repository и focused commit присутствуют. | Git уже инициализирован; README/Makefile/CI и локальный focused commit проверены. |

## Ограничения
| ID | Статус | Источник | Ограничение | Доказательства |
| --- | --- | --- | --- | --- |
| CON-001 | `готово` | S001 | Никакие существующие записи в `/Users/igor/Movies` не используются как destructive test targets. | Удалён только созданный в ходе E2E source `2026-08-01 15-49-42.mp4`. |
| CON-002 | `отклонено` | S002, S003 | Не изменять `telegram-harvest`; использовать его runtime read-only. | Отменено прямой инструкцией S003; рабочее дерево перед новой реализацией проверено и чисто. |
| CON-003 | `готово` | S001 | Обработка сериализована, чтобы несколько записей не запускали конкурирующие Whisper/VideoToolbox jobs. | `processor.lock` использует non-blocking `flock`; `go test -race ./...` зелёный. |
| CON-004 | `принято` | S003 | Новый ASR-вход Telegram Harvest не выполняет Telegram RPC и не меняет Telegram state. | Команда использует только локальные runtime/model/input/output paths. |

## Обязательная Валидация
| ID | Статус | Источник | Валидация | Доказательства |
| --- | --- | --- | --- | --- |
| VAL-001 | `готово` | S001 | Unit tests для очереди, проверок медиа, атомарной публикации и delete gate. | `make check`, `go test -race ./...`. |
| VAL-002 | `готово` | S001 | Disposable CLI integration с синтетическим видео и stub ASR проверяет success/failure lifecycle. | `TestDisposableFFmpegIntegration` зелёный с реальным VideoToolbox/ffprobe. |
| VAL-003 | `готово` | S001, S002 | Реальная короткая OBS-запись проходит hook, launchd, production ASR, H.265 и readback. | E2E 15:49:42–15:50:28: hook → queue → ASR/compress → verify → unlink. |
| VAL-004 | `готово` | S003 | Текущие HEAD обоих репозиториев проходят интегрированный локальный прогон через общий ASR-контракт. | OBS stop E2E: H.264 16,765,693 B → HEVC 8,745,466 B, 1512x982@30, 3 AAC, точный русский transcript, source удалён после validation. |
| VAL-005 | `готово` | S003, S004 | Кликабельное macOS notification проверено через реальный UI. | Alert style «Временно»; после клика Finder открыл точный result directory с тремя артефактами. |

## Границы Объема
| ID | Статус | Источник | Граница | Примечания |
| --- | --- | --- | --- | --- |
| SCOPE-001 | `принято` | S001 | Обрабатываются только новые записи, переданные установленным OBS hook. | Существующие файлы в Movies не сканируются и не удаляются. |
| SCOPE-002 | `принято` | S001 | Удаление означает permanent unlink после всех проверок. | Для E2E удаляется только специально созданная тестовая запись. |
