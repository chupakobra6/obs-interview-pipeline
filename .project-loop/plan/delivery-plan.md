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

## Примечания По Порядку
- Шаги достаточно маленькие для цикла: реализация, ревью, исправление, проверка, коммит, handoff.
- Активен один шаг; непрерывное выполнение появляется только по явной инструкции Игоря.
- Для существенной работы используются пары `STEP-N` / `STEP-NR`.
- Человекочитаемые проектные артефакты пишутся на русском.
- Имена файлов описательные; ID источников хранятся в карте источников и чеклисте.
