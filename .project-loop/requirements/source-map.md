# Карта Источников

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Приоритет Источников
1. Текущая прямая инструкция Игоря.
2. Применимые `AGENTS.md` и system/developer instructions.
3. Существующий код, тесты, схемы и канонические проектные документы.
4. Принятое состояние `.project-loop/`.
5. Предыдущий handoff.
6. Исходный intake и внешний research.
7. Вывод модели.

## Источники
| ID | Тип | Дата | Расположение | Статус | Примечания |
| --- | --- | --- | --- | --- | --- |
| S001 | user | 2026-08-01 | `.project-loop/intake/raw/obs-interview-automation-brief.md` | принято | Автоматизация OBS, итог 1512x982@30, H.265, ASR, проверка и безопасное удаление исходника. |
| S002 | local inspection | 2026-08-01 | OBS profile, display metadata, Telegram Harvest runtime | принято | Подтверждены разрешения, OBS 32.2.1, ffmpeg VideoToolbox и готовый локальный Whisper runtime. |
| S003 | user delta | 2026-08-01 | `.project-loop/intake/user-deltas.md` | принято | Канонический ASR должен жить в Telegram Harvest; macOS notification открывает каталог результата; новый проект оформляется как соседние Go-репозитории. |
| S004 | user delta | 2026-08-01 | `.project-loop/intake/user-deltas.md` | принято | Notification остаётся временным, но helper живёт до клика, чтобы карточка из Центра уведомлений позже открыла папку. |
| S005 | user delta | 2026-08-01 | `.project-loop/intake/user-deltas.md` | принято | Последнее реальное видео в Movies явно разрешено провести через pipeline с обычным delete gate. |
| S006 | user delta | 2026-08-01 | `.project-loop/intake/user-deltas.md` | принято | Провести tooling-review, repo-polish и удалить созданные проверками артефакты без затрагивания реального результата. |
| S007 | user delta | 2026-08-01 | `.project-loop/intake/user-deltas.md` | принято | OBS fast path: Harvest ASR без whole-file Silero gate, прямой HEVC VideoToolbox, отсутствие лишних scale/fps/video encode, первая master AAC 96 Кбит/с. |

## Конфликты
| Источники | Решение | Дата |
| --- | --- | --- |
| Физическое 3024x1964 и ожидаемое «в два раза меньше» | Холст сохраняет физический размер, итоговый output устанавливается в логические 1512x982. | 2026-08-01 |
| CON-002 из S002 и новая инструкция S003 | S003 имеет больший приоритет: разрешено изменить Telegram Harvest, сохранив read-only границу Telegram. | 2026-08-01 |
| REQ-002/REQ-003 и новая инструкция S007 | S007 заменяет OBS-специфичные gate/audio/video требования; основной Telegram Harvest workflow не меняется. | 2026-08-01 |
