# Реестр Агентов

Проект: obs-interview-pipeline
Обновлено: 2026-08-02

Записывай UUID или устойчивые agent IDs, которые вернул tool. Display nickname хранится как дополнительное поле.

## Агенты Текущего Шага
| ID | Никнейм | Роль | Область | Статус | Результат / Заметка О Закрытии |
| --- | --- | --- | --- | --- | --- |
| `/root/adaptive_asr_design_review` | adaptive-asr-design-review | reviewer | Read-only review единого adaptive ASR profile, routing/cache/contract и A/B matrix | завершен | `принято с правками`: нужен post-WAV router, штатный long no-speech, Result-owned status/route, полные timings/logs и extreme-loop guard; product files не менялись. |
| `/root/adaptive_asr_design_review` | adaptive-asr-final-review | reviewer | Final read-only diff/evidence review STEP-011B | завершен | `PASS`: после repair repetition policy является runtime/cache source of truth, OBS contract и pipeline diagnostics согласованы; findings отсутствуют. |

## Закрытие Предыдущего Шага
| ID | Предыдущая Роль | Статус Закрытия | Заметка |
| --- | --- | --- | --- |

## Примечания
- Значения статусов: `запущен`, `завершен`, `закрыт`, `потерян после compaction`, `заблокирован`.
- После compaction начинай свежую секцию реестра для текущего шага и записывай доступные ID.
