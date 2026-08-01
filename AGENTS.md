# AGENTS.md

## Project Loop
- Этот проект использует workflow `project-loop`.
- `.project-loop/requirements/checklist.md` - источник правды по требованиям.
- `.project-loop/plan/current-step.md` - источник правды по активному шагу.
- Входящие материалы сохраняются в `.project-loop/intake/raw/` перед превращением в требования или план.
- Свежие комментарии, правки и решения Игоря сохраняются в `.project-loop/intake/user-deltas.md` перед реализацией; routing описан в `inbox/README.md`.
- Текстовый intake сохраняется в Markdown (`.md`). `.txt` не используется для документов проекта; машинные данные получают явное расширение вроде `.log`, `.csv` или `.json`.
- Свежие прямые инструкции Игоря имеют приоритет над сохраненным состоянием loop; при изменении области обновляются intake, чеклист, план и handoff.
- Навигация идет через `AGENTS.md`, карту источников, чеклист, текущий шаг, план поставки и handoff; исходный архив читается по указанию этих файлов или точке конфликта.
- Выполняется один шаг поставки за раз, пока Игорь явно не разрешит непрерывное выполнение.
- Worker/reviewer subagents получают независимые ограниченные области; orchestrator владеет интеграцией, проверкой, коммитами и handoff.
- `.project-loop/agents/registry.md` создается перед запуском subagents; UUID фиксируются сразу после spawn.
- Промпты worker/reviewer генерируются через `loopctl.py prompt --kind ...`, затем уточняются по области и источникам.
- Предварительная проверка промпта выполняется перед delegation; `.project-loop/reviews/prompt-audit.md` создается при изменении prompts.
- Свежие комментарии Игоря проходят через user-deltas stream: исходный ввод, нормализация, маршрутизация реализации, независимое ревью дельт, handoff.
- Коммиты сфокусированы на текущем шаге и следуют ближайшим version-control rules.

## Язык Проекта
- Человекочитаемые project materials пишутся на русском: требования, планы, checklist, architecture, research notes, handoff, inbox, user-deltas и future knowledge artifacts.
- Служебные keys Project Loop, IDs, paths, commands, API names, tool names и exact external quotes остаются в исходном виде, когда это нужно для validation или traceability.

## Имена Файлов
- Intake, research, converted, derived и review artifacts получают описательные имена файлов: `initial-brief.md`, `api-research.md`, `nutrition-program.pdf`.
- ID источников живут в `source-map.md`, `checklist.md` и review links; формат короткий: `S001`, `S002`.
- Дата, тип источника и provenance хранятся в `source-map.md` и в самом intake-файле.
