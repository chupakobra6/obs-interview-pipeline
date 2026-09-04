# Карта Источников

Проект: obs-interview-pipeline
Обновлено: 2026-09-04

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
| S008 | user delta | 2026-08-01 | `.project-loop/intake/user-deltas.md` | принято | Исправить потерю начала transcript; после OBS Stop спрашивать режим обработки, delete policy и merge tracks; по умолчанию сохранять все tracks с AAC 96. |
| S009 | user delta | 2026-08-02 | `.project-loop/intake/user-deltas.md` | принято | Заменить фиксированные 120-секундные чанки качественным long-form алгоритмом без искусственных границ; проверить нативный timestamped decode и реальный transcript. |
| S010 | primary research + experiment | 2026-08-02 | `ggml-org/whisper.cpp@v1.9.1/examples/server/server.cpp`, `openai/whisper/whisper/transcribe.py`, real-file A/B | принято | Server поддерживает request-level timestamps; нативный decoder переносит контекст и продвигается по timestamp-токенам. На trimmed interview timestamps дали полный текст, `no_timestamps` зациклился после 290 слов. |
| S011 | user review + local verification | 2026-08-02 | `.project-loop/intake/raw/long-form-contract-validation-review.md`, current code, real-file tail VAD benchmark | принято с уточнением | Справедливы contract v2 и bounded-проверка последней речи. Статус называется `coverage-validated`: он доказывает структуру и покрытие хвоста, но не WER/CER и не каждое внутреннее слово. Tail VAD занял 0,64 с. |
| S012 | user delta + primary local source | 2026-08-02 | `.project-loop/intake/user-deltas.md`, installed `whisper.cpp@v1.9.1` server/core, real-file A/B | принято с выводом no-op | Server преобразует default 0 в 60, но core wrapping выполняется только при `token_timestamps=true`; production отправляет false. Fresh-process A/B дал exact-identical text/tokens/segments/timestamps, поэтому production-настройка отклонена как не дающая улучшения. |
| S013 | user + experiment report | 2026-08-02 | `.project-loop/intake/raw/adaptive-multilingual-asr-selection.md`, installed `whisper.cpp@v1.9.1`, multilingual benchmark corpus | принято, завершено | Сравнение 12 вариантов выбрало detect → selective RU prompt; contract v3 оставляет два публичных профиля и один источник ASR policy в Harvest. |
| S014 | user delta | 2026-08-02 | `.project-loop/intake/user-deltas.md` | принято | Провести повторный tooling-review, repo-polish и session-learnings после публикации обоих репозиториев; исправить безопасные gaps, сохранить только устойчивые правила, проверить, закоммитить и запушить результат. |
| S015 | user delta + local A/B | 2026-08-02 | `.project-loop/intake/user-deltas.md`, Telegram ASR corpus, real OBS interview | принято, реализация завершена | Исправить найденные ASR-хвосты и при доказанной безопасности заменить short/long public profiles одним адаптивным профилем для Telegram и OBS; mixed-language внутри записи отложен. |
| S016 | user delta + ground-truth A/B | 2026-09-04 | `.project-loop/intake/user-deltas.md`, Telegram Harvest FLEURS RU/EN corpus | принято, реализация завершена | Убрать принудительный `ru` из short-form, сохранить быстрый decode и доказать отсутствие регрессии на русской речи; синхронно обновить OBS contract. |

## Конфликты
| Источники | Решение | Дата |
| --- | --- | --- |
| Физическое 3024x1964 и ожидаемое «в два раза меньше» | Холст сохраняет физический размер, итоговый output устанавливается в логические 1512x982. | 2026-08-01 |
| CON-002 из S002 и новая инструкция S003 | S003 имеет больший приоритет: разрешено изменить Telegram Harvest, сохранив read-only границу Telegram. | 2026-08-01 |
| REQ-002/REQ-003 и новая инструкция S007 | S007 заменяет OBS-специфичные gate/audio/video требования; основной Telegram Harvest workflow не меняется. | 2026-08-01 |
| REQ-013 и новая инструкция S008 | S008 заменяет master-only default: новый default сохраняет все дорожки; сведение в одну становится per-job opt-in. | 2026-08-01 |
| Реализованный fixed-chunk v1 из S008 и требование качества S009 | S009 заменяет фиксированные чанки и word-overlap merge; bounded leading trim сохраняется, а дальнейший decode выбирается по доказательству полноты и качества. | 2026-08-02 |
| Формулировка `validation_status: complete` из S011 и фактическая сила проверки | Использовать точный статус `coverage-validated`: structural timestamp validation плюс достижение последней найденной VAD-речи в пределах допуска; лексическая точность остаётся вне контракта. | 2026-08-02 |
| S010 утверждает перенос контекста, а installed server задаёт `no_context=true` | Installed core очищает межзапросную историю в начале `whisper_full`, но затем обновляет и использует `prompt_past1` между внутренними окнами одного вызова. One-request контекст существует; объяснение проверяется по core, а не по имени флага. | 2026-08-02 |
| REQ-019 сохранял отдельный trusted OBS mode, а S015 требует унификации | S015 выше по приоритету: публичный контракт становится единым и сам выбирает short либо long strategy; внутренние стратегии остаются различными, пока это необходимо для качества и скорости. | 2026-08-02 |
