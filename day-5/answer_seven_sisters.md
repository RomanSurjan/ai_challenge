# Day 5: сравнение моделей через API

## Задача

Ответь на логическую задачу точно и кратко

Условие:

В доме 7 сестёр. Каждая из них занята каким-то делом. Первая сестра читает книгу, вторая — готовит еду, третья — играет в шахматы, четвёртая — разгадывает кроссворд, пятая — занимается стиркой, шестая — ухаживает за растениями.

Вопрос:

Чем занимается седьмая сестра?

## Источники и модели

- Hugging Face Open LLM Leaderboard использовался как ориентир для подбора моделей.
- DeepSeek API: `https://api-docs.deepseek.com/` и `https://api-docs.deepseek.com/quick_start/pricing`.
- Groq models/pricing: `https://console.groq.com/docs/models`.

- `GPT OSS 20B` (Groq, `openai/gpt-oss-20b`).
- `DeepSeek V4 Flash` (DeepSeek, `deepseek-v4-flash`).
- `DeepSeek V4 Pro` (DeepSeek, `deepseek-v4-pro`).

## Настройки

- temperature: `0.20`
- max_tokens: `16384`
- task_name: `seven_sisters`
- DeepSeek thinking: `disabled`, чтобы сравнение было ближе к обычному chat-completion режиму.

## Метрики

| Провайдер | Модель | Время | Input | Output | Total | Tokens/sec | Цена input/output за 1M | Стоимость |
|---|---|---:|---:|---:|---:|---:|---|---:|
| Groq | `openai/gpt-oss-20b` | 13.40 s | 202 | 11698 | 11900 | 873.0 | $0.075 miss / $0.300 out (standard) | $0.00352455 |
| DeepSeek | `deepseek-v4-flash` | 656 ms | 149 | 18 | 167 | 27.4 | $0.007 hit, $0.220 miss / $0.660 out (off-peak) | $0.00001740 |
| DeepSeek | `deepseek-v4-pro` | 349 ms | 149 | 18 | 167 | 51.5 | $0.022 hit, $0.660 miss / $1.980 out (off-peak) | $0.00005232 |

## Ответ: GPT OSS 20B

_Provider: Groq; model: `openai/gpt-oss-20b`; time=13.40 s; tokens: prompt=202, completion=11698, total=11900; estimated_cost=$0.00352455; finish_reason=stop_

Седьмая сестра — **сестра**.  

В задаче перечислены занятия, но в конце вопроса спрашивается, чем занимается «седьмая сестра», а не «седьмой человек». Поэтому ответом является слово «сестра» (то есть она сама является сестрой).

## Ответ: DeepSeek V4 Flash

_Provider: DeepSeek; model: `deepseek-v4-flash`; time=656 ms; tokens: prompt=149, completion=18, total=167, cache_hit=128, cache_miss=21; estimated_cost=$0.00001740; finish_reason=stop_

Седьмая сестра играет в шахматы с третьей сестрой.

## Ответ: DeepSeek V4 Pro

_Provider: DeepSeek; model: `deepseek-v4-pro`; time=349 ms; tokens: prompt=149, completion=18, total=167, cache_hit=128, cache_miss=21; estimated_cost=$0.00005232; finish_reason=stop_

Седьмая сестра играет в шахматы с третьей сестрой.

## Краткий вывод

- Самая быстрая по wall-clock времени: `deepseek-v4-pro` (349 ms).
- Самая дешевая в этом запуске: `deepseek-v4-flash` ($0.00001740).
- Меньше всего токенов потратила: `deepseek-v4-flash` (167 total tokens).
- Корректность лучше оценивать по самим ответам: метрики скорости и цены не гарантируют правильность решения.
