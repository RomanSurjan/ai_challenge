# Day 5: сравнение моделей через API

## Задача

Ответь на вопрос точно и кратко. Перед финальным ответом проверь подсчет букв.

Вопрос:

Какая буква стоит на седьмом месте с конца в слове "делопроизводство"?

## Источники и модели

- Hugging Face Open LLM Leaderboard использовался как ориентир для подбора моделей.
- DeepSeek API: `https://api-docs.deepseek.com/` и `https://api-docs.deepseek.com/quick_start/pricing`.
- Groq models/pricing: `https://console.groq.com/docs/models`.

- `GPT OSS 20B` (Groq, `openai/gpt-oss-20b`).
- `DeepSeek V4 Flash` (DeepSeek, `deepseek-v4-flash`).
- `DeepSeek V4 Pro` (DeepSeek, `deepseek-v4-pro`).

## Настройки

- temperature: `0.20`
- max_tokens: `4096`
- task_name: `seventh_letter`
- DeepSeek thinking: `disabled`, чтобы сравнение было ближе к обычному chat-completion режиму.

## Метрики

| Провайдер | Модель | Время | Input | Output | Total | Tokens/sec | Цена input/output за 1M | Стоимость |
|---|---|---:|---:|---:|---:|---:|---|---:|
| Groq | `openai/gpt-oss-20b` | 800 ms | 141 | 354 | 495 | 442.3 | $0.075 miss / $0.300 out (standard) | $0.00011677 |
| DeepSeek | `deepseek-v4-flash` | 490 ms | 84 | 140 | 224 | 285.6 | $0.007 hit, $0.220 miss / $0.660 out (off-peak) | $0.00011088 |
| DeepSeek | `deepseek-v4-pro` | 324 ms | 84 | 133 | 217 | 409.9 | $0.022 hit, $0.660 miss / $1.980 out (off-peak) | $0.00031878 |

## Ответ: GPT OSS 20B

_Provider: Groq; model: `openai/gpt-oss-20b`; time=800 ms; tokens: prompt=141, completion=354, total=495; estimated_cost=$0.00011677; finish_reason=stop_

в

## Ответ: DeepSeek V4 Flash

_Provider: DeepSeek; model: `deepseek-v4-flash`; time=490 ms; tokens: prompt=84, completion=140, total=224, cache_hit=0, cache_miss=84; estimated_cost=$0.00011088; finish_reason=stop_

Слово "делопроизводство" состоит из 15 букв.  
Седьмая буква с конца — это 15 - 7 + 1 = 9-я буква с начала.  
Считаем: д(1) е(2) л(3) о(4) п(5) р(6) о(7) и(8) з(9).  
Это буква **з**.

Проверка: с конца — ...з(7-я), и(6), о(5), р(4), п(3), о(2), л(1). Верно.

Ответ: **з**.

## Ответ: DeepSeek V4 Pro

_Provider: DeepSeek; model: `deepseek-v4-pro`; time=324 ms; tokens: prompt=84, completion=133, total=217, cache_hit=0, cache_miss=84; estimated_cost=$0.00031878; finish_reason=stop_

Давай посчитаем.

Слово: **делопроизводство**  
Количество букв: д(1) е(2) л(3) о(4) п(5) р(6) о(7) и(8) з(9) в(10) о(11) д(12) с(13) т(14) в(15) о(16) — всего 16 букв.

Седьмая с конца:  
16 - 7 + 1 = 10-я буква с начала.

10-я буква — **в**.

Ответ: **в**.

## Краткий вывод

- Самая быстрая по wall-clock времени: `deepseek-v4-pro` (324 ms).
- Самая дешевая в этом запуске: `deepseek-v4-flash` ($0.00011088).
- Меньше всего токенов потратила: `deepseek-v4-pro` (217 total tokens).
- Качество лучше смотреть по разделу API-оценки и по самим ответам: метрики скорости и цены не гарантируют правильность решения.
