# Adaptive Long-Form ASR Benchmark

Дата: 2026-08-02
Модель/backend: `large-v3-turbo-q5_0`, Metal, 4 threads, beam 5, temperature fallback 0→0.2

## Метод

- 8 временных samples: реальное 793,41-секундное русское интервью и 7 TTS/edge записей с точным RU/EN текстом, включая 204,78-секундный English long-form, отсутствие приветствия, технические термины, паузу, слабое начало и тишину/шум.
- Real interview сравнивается с историческим fixed-chunk transcript только как с silver reference; его pseudo-WER не объявляется человеческим WER.
- Сопоставимые long-file результаты получены в fresh `whisper-server` process, как в one-shot OBS flow. Каждый финальный RU/EN output повторён и проверен SHA-256.
- Проверены слова/нормализованный WER, multiset recall/F1, пунктуация, timestamps/coverage, adjacent repetition, prompt leakage и wall time.

## Ключевые Результаты

| Policy | Sample | Слова | Знаки `. ! ? …` | WER / pseudo-WER | Recall | F1 | Время | Вывод |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| native, forced RU | real RU | 1462 | 1 | 25,04% | 91,37% | 88,69% | 35,02 с | Быстро, но пунктуация и полнота хуже. |
| full RU style seed | real RU | 1528 | 273 | 20,50% | 94,17% | 89,47% | 39,08 с | Сильное улучшение, но seed длиннее необходимого. |
| minimal RU style seed | real RU | 1529 | 253 | **18,71%** | **94,75%** | **90,08%** | 37,19–51,15 с | Лучший русский текст; output побайтно стабилен, разброс времени зависит от длительной Metal-нагрузки. |
| punctuation-only seed | real RU | 1247 | 356 | 47,34% | 69,78% | 69,31% | 39,92 с | Отклонён: потеря текста и 153 adjacent repeats. |
| full seed + carry | real RU | 1527 | 259 | 20,79% | 94,03% | 89,12% | 32,97 с | Отклонён: hallucinated subtitles и timestamp 822,24 с за пределами 793,41 с audio. |
| auto native, no prompt | long EN | 524 | 49 | **0,96%** | **99,43%** | **99,24%** | 9,80 с | Канонический английский decode. |
| bilingual prompt | long EN | 597 | 62 | 16,09% | 98,66% | 92,05% | 13,95 с | Отклонён: повтор крупного блока. |
| detected EN + English prompt | long EN | 597 | 62 | 15,90% | 98,85% | 92,23% | 13,98 с | Отклонён: тот же повтор. |
| content bootstrap | long EN | 539 | 67 | 3,83% | 99,43% | 97,83% | 12,99 с | Отклонён: повтор предложений и лишний decode. |
| detect → EN without prompt | long EN | 524 | 49 | **0,96%** | **99,43%** | **99,24%** | 9,91 с | Побайтно равен native; probe почти полностью компенсируется фиксированным language decode. |

## Выбранная Политика

```text
bounded leading/trailing Silero
→ 15 s Whisper language-only probe
→ Russian: language=ru + "Да. Нет? Хорошо! Пожалуйста, продолжайте."
→ English: language=en, no prompt
→ Other: language=auto, no prompt
→ one native timestamped full decode
→ timestamp/coverage/repetition/post-filter validation
```

`carry_initial_prompt=false` обязателен. Probe не строит и не склеивает отдельный transcript. Внешних chunks нет. Policy, probe duration, prompt и carry входят в Harvest descriptor/cache identity; пользовательских knobs нет.

## Current-Head Real Run

- contract/profile/status: `3 / trusted-long-form-v3 / coverage-validated`;
- wall 53,32 с; language detection 1,03 с; preparation 1,62 с;
- leading offset 178,49 с; detected `russian`; prompt applied;
- 1529 raw words, 253 punctuation marks, 199 segments;
- last VAD speech 786,91 с; last ASR segment 786,58 с; gap 0,33 с при tolerance 2 с;
- greeting and farewell present; adjacent repeats 0; prompt-only leakage на RU edge cases 0;
- output SHA-256 стабилен между CLI current-head и тремя independent direct-policy runs.
- Повторный post-publish production run на том же полном source: wall `47,60 с`, language detection `0,89 с`, preparation `1,67 с`, те же `199` timestamped segments и coverage gap `0,33 с`; это `20,42× realtime` по исходным `971,97 с` или `16,67×` по декодированным `793,41 с`.

## Вывод

Лучший баланс даёт одна адаптивная OBS policy с дешёвым language probe и условным prompt только для русского. Статический универсальный/bilingual prompt и content bootstrap объективно проиграли на длинном английском; fixed/speech-aware chunking не нужен, потому что выбранный flow сохраняет один native decode и проходит coverage.
