# Единый адаптивный ASR: A/B

Дата: 2026-08-02

## Вывод

Один публичный профиль безопасен только как адаптивный router, а не как одинаковый decode для любого файла. Финальная policy сохраняет прежний short request для обычных Telegram-медиа и автоматически включает проверенный timestamped long-form при длительности от 180 секунд либо leading silence от 10 секунд.

## Механизм

```text
convert to WAV
→ duration >= 180 s: bounded Silero first/tail scan
→ иначе: прежний whole-file Silero с чтением speech bounds
→ no speech: no-speech без запуска Whisper
→ first speech >= 10 s: long route
→ иначе: прежний ru + no_timestamps short route
→ long: 1 s lead-in trim → физический 15 s language probe
        → один native timestamped decode
        → timestamps + tail coverage + extreme exact-loop validation
```

Порог 10 секунд выбран с запасом: на реальном Telegram voice с добавленным pre-roll 20 секунд short decode уже потерял часть текста, а при 30 секундах добавил `Продолжение следует`. Long route на 15/30/180 секундах начинался с реальной первой фразы без boilerplate.

## Telegram corpus

Corpus: 42 реальных Telegram media, 2178,413 секунды; 30 speech и 12 non-speech.

| Метрика | Прежний production | `adaptive-media-v1` |
| --- | ---: | ---: |
| WER против large-v3 silver reference | 10,576% | 10,576% |
| CER | 6,394% | 6,394% |
| Word recall | 95,432% | 95,432% |
| Word F1 | 94,614% | 94,614% |
| Negation recall | 97,945% | 97,945% |
| Number recall | 94,030% | 94,030% |
| Missed speech | 0 | 0 |
| Non-speech hallucinations | 0 | 0 |
| Failed transcripts | 0 | 0 |

Все 30 speech media выбрали `russian-no-timestamps-v1 / short-media`; все 12 silent media — `no-speech`. Из 42 raw outputs 40 побайтно совпали со старым сохранённым benchmark; оставшиеся два отличались только удалением уже известной финальной строки `DimaTorzok`/`Продолжение следует`, поэтому все нормализованные quality-метрики идентичны.

Отдельный fresh-process A/B на шести реальных voice messages дал 6/6 exact text. Median wall delta нового router: +0,021 секунды; диапазон −0,104…+0,062 секунды, то есть измеримого overhead нет.

Один shared-run benchmark показал 20,54× ASR / 19,83× pipeline против исторических 16,80× / 16,32×. Это подтверждает отсутствие slowdown, но ускорение не приписывается router: прогоны не interleaved и short inference не менялся.

## Leading silence и no-speech

- 15 секунд pre-roll: `leading-silence-threshold`, greeting сохранён, coverage validated.
- 30 секунд: прежний short добавлял boilerplate; adaptive long exact совпал с ранее выбранным long output.
- 180 секунд: прежний short вернул только 18 слов boilerplate; adaptive long вернул 196 слов реальной речи.
- 21,38 секунды реального non-speech Telegram video: `no-speech` за 0,152 секунды.
- 240 секунд полной тишины: bounded long scan вернул `no-speech` за 0,655 секунды без Whisper inference.

## Реальное интервью

Источник: сохранённое 971,904-секундное OBS interview с первой речью около 179,49 секунды.

- route: `duration-threshold`;
- trim offset: 178,49 секунды;
- decoded audio: 793,413 секунды;
- transcript: 1529 JSON words, 199 timestamped segments;
- последний segment: 786,58 секунды;
- последняя Silero-речь: 786,914 секунды;
- coverage gap: 0,334 секунды при tolerance 2 секунды;
- repetition validation: passed; реальное четырёхкратное `привет` не признано extreme;
- total: 42,30 секунды, включая 1,62 секунды preparation и 0,66 секунды физического language probe;
- transcript SHA-256: `21d8ddbcc8c3649740b90e5e4ba284370ccd493113180ceba50558983e3efe17`, exact совпадение с ранее принятым adaptive long-form current-head результатом.

## Ограничение

Один probe выбирает доминирующий язык всей записи. Mixed-language routing внутри одного файла не реализован по явному решению пользователя.

## Установленный OBS→Harvest E2E

После focused commits оба бинарника собраны из чистых HEAD: `vcs.modified=false`. `make install && make doctor` подтвердил contract `4`, profile `adaptive-media-v1`, локальный Harvest runtime, Metal backend contract, HEVC VideoToolbox и оба signed macOS helper.

Disposable production input содержал 15 секунд тишины перед реальным 26,77-секундным русским Telegram voice:

- route `leading-silence-threshold`, offset 14,49 секунды;
- физический probe 0,84 секунды, один timestamped inference;
- полный русский текст с пунктуацией;
- `coverage-validated`, tail gap 0, `repetition_validated=true`;
- Metal подтверждён;
- HEVC 1512×982@30 скопирован без повторного encode, AAC сохранён;
- source был сохранён test policy, затем весь disposable source/result перенесён в Корзину;
- временных `.processing-*` и фоновых Whisper/process jobs не осталось.
