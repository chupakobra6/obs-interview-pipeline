# План Поставки

Проект: obs-interview-pipeline
Обновлено: 2026-08-01

## Этапы
| Шаг | Статус | ID требований | Цель | Ревью | Проверка |
| --- | --- | --- | --- | --- | --- |
| STEP-001A | `готово` | REQ-001..REQ-004, REQ-006 | Реализовать worker, очередь, ASR, компрессию и delete gate. | Отдельный self-review по требованиям и failure paths. | Unit + disposable integration tests зелёные. |
| STEP-001B | `готово` | REQ-001, REQ-005 | Установить LaunchAgent/OBS hook и применить 1512x982@30. | Readback установленных файлов и OBS profile. | plist lint, launchctl status, OBS config readback зелёные. |
| STEP-001C | `готово` | VAL-003 | Выполнить реальный короткий OBS E2E и проверить артефакты. | Сопоставить media/readback/log evidence со всеми REQ. | OBS stop → queue → production outputs → source absent подтверждено. |
| STEP-001R | `готово` | все активные | Финальное независимое по фазе self-review и targeted repair. | Проверка diff, failure lifecycle, installed state и residual risks. | `make check`, `go test -race ./...`, loop validate, installed-state readback зелёные. |
| STEP-002A | `готово` | REQ-007..REQ-009, CON-004 | Добавить локальный ASR-контракт Telegram Harvest, удалить дублирование из OBS, сделать notification кликабельным и обновить DX/CI/docs. | Self-review общего контракта, migration config и notification target. | Focused/unit/race checks обоих репозиториев зелёные. |
| STEP-002B | `готово` | VAL-004, VAL-005 | Установить current-head pipeline и провести реальный E2E плюс UI click test. | Сопоставить command output, manifest, ffprobe, source lifecycle и Finder state. | Сквозной прогон через Telegram Harvest и клик notification подтверждены. |
| STEP-002R | `готово` | REQ-007..REQ-009 | Финальный review, cleanup, evidence, focused commits и handoff. | Проверка diff, docs/CI, installed state и residual risks. | Все checks зелёные; два локальных commits; clean owned paths. |

## Примечания По Порядку
- Шаги достаточно маленькие для цикла: реализация, ревью, исправление, проверка, коммит, handoff.
- Активен один шаг; непрерывное выполнение появляется только по явной инструкции Игоря.
- Для существенной работы используются пары `STEP-N` / `STEP-NR`.
- Человекочитаемые проектные артефакты пишутся на русском.
- Имена файлов описательные; ID источников хранятся в карте источников и чеклисте.
