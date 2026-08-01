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
| REQ-006 | `готово` | S001 | Пользователь получает понятные логи и macOS-уведомление об успехе либо ошибке. | Интеграционный прогон создает job log/status и отправляет best-effort notification. | LaunchAgent stdout/error logs, done/failed job JSON и best-effort `osascript` notification встроены; E2E status сохранён. |

## Ограничения
| ID | Статус | Источник | Ограничение | Доказательства |
| --- | --- | --- | --- | --- |
| CON-001 | `готово` | S001 | Никакие существующие записи в `/Users/igor/Movies` не используются как destructive test targets. | Удалён только созданный в ходе E2E source `2026-08-01 15-49-42.mp4`. |
| CON-002 | `готово` | S002 | Не изменять dirty worktree `telegram-harvest`; использовать его runtime read-only. | `telegram-harvest` не изменялся; runtime/model только читались. |
| CON-003 | `готово` | S001 | Обработка сериализована, чтобы несколько записей не запускали конкурирующие Whisper/VideoToolbox jobs. | `processor.lock` использует non-blocking `flock`; `go test -race ./...` зелёный. |

## Обязательная Валидация
| ID | Статус | Источник | Валидация | Доказательства |
| --- | --- | --- | --- | --- |
| VAL-001 | `готово` | S001 | Unit tests для очереди, проверок медиа, атомарной публикации и delete gate. | `make check`, `go test -race ./...`. |
| VAL-002 | `готово` | S001 | Disposable CLI integration с синтетическим видео и stub ASR проверяет success/failure lifecycle. | `TestDisposableFFmpegIntegration` зелёный с реальным VideoToolbox/ffprobe. |
| VAL-003 | `готово` | S001, S002 | Реальная короткая OBS-запись проходит hook, launchd, production ASR, H.265 и readback. | E2E 15:49:42–15:50:28: hook → queue → ASR/compress → verify → unlink. |

## Границы Объема
| ID | Статус | Источник | Граница | Примечания |
| --- | --- | --- | --- | --- |
| SCOPE-001 | `принято` | S001 | Обрабатываются только новые записи, переданные установленным OBS hook. | Существующие файлы в Movies не сканируются и не удаляются. |
| SCOPE-002 | `принято` | S001 | Удаление означает permanent unlink после всех проверок. | Для E2E удаляется только специально созданная тестовая запись. |
