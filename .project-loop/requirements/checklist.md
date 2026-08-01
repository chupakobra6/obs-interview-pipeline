# Чеклист Требований

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Значения Статусов
Используй `кандидат`, `принято`, `в работе`, `готово`, `отложено`, `заблокировано` или `отклонено`.

## Требования
| ID | Статус | Источник | Требование | Критерий приемки | Доказательства |
| --- | --- | --- | --- | --- | --- |
| REQ-001 | `готово` | S001, S002 | После `OBS_FRONTEND_EVENT_RECORDING_STOPPED` точный путь последней записи автоматически попадает в устойчивую локальную очередь. | OBS не блокируется; задание переживает завершение OBS и перезапуск worker. | OBS log `15:50:12.143 … queued`; LaunchAgent run 1, exit 0; job в `done/`. |
| REQ-002 | `готово` | S001, S002 | Запись транскрибируется локально production-профилем Telegram Harvest: Whisper large-v3-turbo q5_0, русский, Metal, beam 5 и whole-file Silero gate. | Интеграционный прогон создает UTF-8 Markdown-транскрипт, runtime подтверждает Metal. | Исторический E2E подтвердил полный production-профиль; OBS-исключение заменяется REQ-011. |
| REQ-003 | `готово` | S001, S002, S005 | Видео сжимается в H.265 через Apple VideoToolbox с сохранением всех аудиодорожек. | `ffprobe` подтверждает HEVC, 1512x982, 30 fps, исходное число audio streams и близкую длительность; финальный файл меньше исходного. | Исторический real E2E подтверждён; новый master-only контракт заменяется REQ-013. |
| REQ-004 | `готово` | S001, S005 | Исходник удаляется только после успешных ASR, сжатия, атомарной публикации и медиапроверки. | Инъекция ошибки оставляет исходник; явно выбранное реальное видео удаляется только после success. | Unit failure/delete-retry tests + disposable integration + job S005 завершён без error, source отсутствует после success. |
| REQ-005 | `готово` | S001, S002 | OBS записывает итог 1512x982 при 30 fps, сохраняя физический canvas 3024x1964 для Retina capture. | Readback профиля и пробная запись подтверждают разрешение и fps. | `basic.ini`: Base 3024x1964, Output 1512x982, FPSCommon=30; source ffprobe совпал. |
| REQ-006 | `готово` | S001, S004 | Пользователь получает понятные логи и macOS-уведомление об успехе либо ошибке. | Интеграционный прогон создаёт job log/status и отправляет best-effort notification. | LaunchAgent stdout/error logs, done/failed job JSON и подписанный `OBS Interview Notifier.app` встроены; E2E status сохранён. |
| REQ-007 | `готово` | S003 | Единственная production-реализация и профиль Whisper находятся в Telegram Harvest; OBS pipeline вызывает его локальный ASR-вход. | В OBS-репозитории отсутствуют Whisper server/gate/decode реализации и пути моделей; сквозной тест проходит через текущий код Telegram Harvest. | OBS вызывает `telegram-harvest --profile main transcribe-file`; manifest E2E: contract v1, whispercpp/Metal/q5_0/ru/beam 5/Silero. |
| REQ-008 | `готово` | S003, S004, S006 | Клик по временному success-уведомлению macOS открывает Finder в каталоге готового собеседования; helper ждёт, пока карточка доступна. | UI-тест клика активирует точный output directory; alert style readback равен «Временно»; очистка карточки не оставляет helper-процесс. | Клик UI проверен; delivered-notification monitor завершает helper после click/clear; Swift type-check включён в validation. |
| REQ-009 | `готово` | S003 | OBS-проект оформлен согласованно с соседними Go-проектами и ведётся в Git. | README/Makefile/CI дают понятный setup и validation; Git repository и focused commit присутствуют. | Git уже инициализирован; README/Makefile/CI и локальный focused commit проверены. |
| REQ-010 | `готово` | S006 | Репозиторий и installed state не содержат disposable E2E-артефактов, сиротских helper-процессов или непроверенного native-кода. | Clean inventory оставляет только реальный result/job/notifier; `make clean` удаляет build class; macOS CI проверяет Go и Swift. | Тестовый output/job/error history перемещён в Корзину; `.processing-*`, temp dirs и test notifier processes отсутствуют. |
| REQ-011 | `готово` | S007 | OBS-команда Harvest пропускает whole-file Silero gate, не дублируя production model/decode/Metal/ru/post-filter настройки. | ASR descriptor не содержит speech gate, но подтверждает тот же model q5_0, Metal, ru и beam 5; transcript проходит Harvest post-filter. | E2E manifest: `speech_gate=0`, q5_0/Metal/ru/beam 5/`terminal-exact-v1`; ASR 3.43 s. |
| REQ-012 | `готово` | S007 | OBS пишет HEVC Apple VideoToolbox; готовый HEVC 1512x982@30 проходит без video re-encode, а scale/fps применяются только при несовпадении. | OBS source probe показывает HEVC 1512x982@30; compressor выбирает stream copy; unit tests покрывают условные filters и fallback transcode. | UI readback: Apple VT HEVC/CRF 55; E2E source и final имеют один video bitrate 452502, manifest `video_mode=copy`; unit tests покрывают filters. |
| REQ-013 | `готово` | S007 | Финальный MP4 оставляет только первую master audio track и кодирует её в AAC 96 Кбит/с. | Source OBS содержит 3 audio streams; final — ровно 1 AAC stream с encoder target 96 Кбит/с; duration и video validation зелёные. | E2E: 3×AAC ~160 Kbit/s → 1×AAC, target 96; короткая запись с тишиной дала average 48255 bit/s и прошла codec/track/duration validation. |

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
| VAL-006 | `готово` | S005 | Реальное собеседование проходит установленный pipeline без ручных обходов. | Job 16:32:48–17:05:31 завершён; HEVC/media/ASR manifest проверены; source удалён после validation. |
| VAL-007 | `готово` | S006 | Tooling-review/repo-polish заканчиваются clean inventory и current-head validation. | `make check`, race, doctor, loop validate, Git/runtime/process inventory зелёные после cleanup. |
| VAL-008 | `готово` | S007 | Короткая запись реальным OBS проходит новый fast path через установленные HEAD обоих репозиториев. | OBS source HEVC/1512x982@30/3 audio; ASR без gate; final video stream copied, 1×AAC target 96 Кбит/с; source удалён только после validation; disposable result/job/notification очищены. | E2E 17:38:55–17:38:59: 3,439,630 → 1,863,425 B, video copy, speech gate 0, 1 AAC; source absent; test result/job/audio перемещены в Корзину. |

## Границы Объема
| ID | Статус | Источник | Граница | Примечания |
| --- | --- | --- | --- | --- |
| SCOPE-001 | `принято` | S001 | Обрабатываются только новые записи, переданные установленным OBS hook. | Существующие файлы в Movies не сканируются и не удаляются. |
| SCOPE-002 | `принято` | S001 | Удаление означает permanent unlink после всех проверок. | Для E2E удаляется только специально созданная тестовая запись. |
