# План Поставки

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

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
| STEP-003A | `готово` | REQ-010, VAL-006..VAL-007 | Провести реальное собеседование через pipeline, затем выполнить tooling-review и repo-polish. | Проверить stage evidence, runtime residue, native helper lifecycle и CI contract. | Реальный job зелёный; безопасные review fixes реализованы. |
| STEP-003R | `готово` | REQ-008..REQ-010 | Удалить disposable state, переустановить current head и закрыть репозиторий clean commit. | Повторный inventory repo/runtime/processes и installed-state readback. | В runtime остаются только реальные артефакты; checks и Git status зелёные. |
| STEP-004A | `готово` | REQ-011..REQ-013 | Добавить Harvest assume-speech контракт, условный video fast path и master AAC 96; применить HEVC VideoToolbox в OBS. | Self-review единственного источника ASR-настроек, ffmpeg mapping/filter decisions и OBS readback. | Focused/unit/race checks обоих репозиториев и OBS config readback зелёные. |
| STEP-004B | `готово` | VAL-008 | Провести короткий реальный OBS E2E через установленный current-head pipeline. | Сопоставить manifest, ffprobe, logs, source lifecycle и notification process. | HEVC source → video copy final; ASR без gate; один AAC target 96; disposable artifacts очищены. |
| STEP-004R | `готово` | REQ-010..REQ-013 | Выполнить tooling-review/repo-polish, current-head checks, focused commits и handoff. | Проверить diffs, docs, installed/runtime state и остаточные риски. | Focused commits; checks/doctor/loop validate зелёные; тестовый мусор отсутствует. |
| STEP-005A | `готово` | REQ-014..REQ-016 | Диагностировать начало ASR; добавить per-job policy, preserve/merge audio и native post-recording prompt. | Self-review audio selection, non-blocking UI launch, cancel/delete safety и single source of truth. | Focused tests и реальный diagnostic sample зелёные. |
| STEP-005B | `готово` | VAL-009 | Установить current head и провести OBS UI/E2E проверки prompt и audio lifecycle. | Сопоставить OBS logs, dialog state, queue job, ffprobe, manifest и source lifecycle. | Cancel/process paths и preserve/merge contracts подтверждены. |
| STEP-005R | `готово` | REQ-010, REQ-014..REQ-016 | Финальный review, cleanup, docs, validation и focused commit. | Проверить diff, installed/runtime state и остаточные риски. | Full/race checks, doctor и loop validate зелёные; disposable artifacts перемещены в Корзину. |
| STEP-006A | `готово` | REQ-017..REQ-019 | Проверить первопричину на whisper.cpp v1.9.1 и реализовать минимальный качественный long-form decode в Harvest без искусственных границ. | Self-review API options, timestamp semantics, обычных Telegram/file flows и удаления прежней chunk/merge ветки. | Primary-source evidence, focused Harvest tests и descriptor contract зелёные. |
| STEP-006B | `готово` | VAL-010 | Установить current head и провести A/B на реальном собеседовании плюс long-form regression cases. | Проверить начало/середину/конец, прежние 120-секундные границы, повторения, timestamps и runtime. | Real A/B полный; boundary regression использует 240 s и речь/термин через прежнюю 120 s границу. |
| STEP-006R | `готово` | REQ-010, REQ-017..REQ-019, VAL-010 | Финальный tooling-review, cleanup, docs, current-head validation и focused commits обоих репозиториев. | Проверить diff, единственный источник ASR, installed/runtime state и остаточные риски. | Full/race checks и installed doctor зелёные; benchmark artifacts очищены; focused commits созданы. |

## Примечания По Порядку
- Шаги достаточно маленькие для цикла: реализация, ревью, исправление, проверка, коммит, handoff.
- Активен один шаг; непрерывное выполнение появляется только по явной инструкции Игоря.
- Для существенной работы используются пары `STEP-N` / `STEP-NR`.
- Человекочитаемые проектные артефакты пишутся на русском.
- Имена файлов описательные; ID источников хранятся в карте источников и чеклисте.
