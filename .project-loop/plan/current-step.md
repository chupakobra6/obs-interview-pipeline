# Текущий Шаг

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

## Активный Шаг
- id: `STEP-011R`
- status: `готово`
- objective: Закрыть поставку единого adaptive ASR: чистая установка, integrated OBS→Harvest проверка, cleanup, push и CI.
- requirement IDs: `REQ-028`, `REQ-029`, `REQ-030`, `CON-006`, `VAL-017`
- owned paths: Project Loop closure state; installed OBS/Harvest runtime; disposable E2E artifacts; GitHub CI
- validation: clean current-head install, doctor contract v4, real adaptive OBS→Harvest E2E, process/temp inventory, Project Loop validate и post-push CI
- done criteria: установленный binary имеет чистый final provenance; contract v4/profile adaptive-media-v1 активен; временных артефактов/процессов нет; оба origin и post-push CI зелёные.

## Фокус Ревью
- Один public profile не означает один и тот же decode для любой длительности: adaptive router обязан сохранять short quality/performance и включать long protection только по проверяемым признакам.
- Language probe физически ограничивает вход, не только request metadata.
- Repetition guard должен ловить только явные циклы и не повреждать допустимые повторения речи.
- Telegram и OBS используют один descriptor/cache contract без caller-owned ASR internals.

## Примечания
- `coverage-validated` доказывает timestamps и покрытие хвоста, но не WER/CER.
- Mixed-language внутри одной записи явно отложен по решению пользователя.
