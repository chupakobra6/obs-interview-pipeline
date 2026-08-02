# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Активный Шаг
- id: `STEP-010R`
- status: `готово`
- objective: Завершить опубликованные Telegram Harvest и OBS pipeline через tooling-review, repo-polish и выборочный session-learnings pass.
- requirement IDs: `REQ-027`, `VAL-015`
- owned paths: безопасные code/test/docs/DX findings в обоих репозиториях; Project Loop source/checklist/plan/handoff; ближайшие устойчивые repo-specific правила при доказанной необходимости
- validation: help/README/CI review, focused/full/race tests, cross-repo contract check, installed doctor, Project Loop validate, Git/runtime/process inventory и GitHub CI после push
- done criteria: review findings устранены либо явно отклонены с причиной; постоянные learnings не дублируют существующие правила; оба репозитория проверены, закоммичены, запушены и синхронны с upstream.

## Фокус Ревью
- Manifest/delete retry проверяет identity source и точное соответствие transcript/output до `unlink`.
- Public help/README называют только два актуальных ASR profile и один Harvest source of truth.
- GitHub CI выполняет check/audit/race в bounded runner; OBS CI остаётся macOS-native.
- Session learnings живут в behavior tests и одном коротком Harvest rule, без отдельного task-shaped документа.

## Примечания
- `coverage-validated` доказывает timestamps и покрытие хвоста, но не WER/CER.
- Public repository остаётся без LICENSE: выбор лицензии требует отдельного решения владельца и не подменён polish-pass предположением.
- Subagents не использовались; review выполнен отдельным scenario-first self-review проходом.
