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
- max_tokens: `4096`
- task_name: `seven_sisters`
- DeepSeek thinking: `disabled`, чтобы сравнение было ближе к обычному chat-completion режиму.

## Метрики

| Провайдер | Модель | Время | Input | Output | Total | Tokens/sec | Цена input/output за 1M | Стоимость |
|---|---|---:|---:|---:|---:|---:|---|---:|
| Groq | `openai/gpt-oss-20b` | 4.88 s | 202 | 4096 | 4298 | 839.6 | $0.075 miss / $0.300 out (standard) | $0.00124395 |
| DeepSeek | `deepseek-v4-flash` | 795 ms | 149 | 18 | 167 | 22.6 | $0.007 hit, $0.220 miss / $0.660 out (off-peak) | $0.00004466 |
| DeepSeek | `deepseek-v4-pro` | 312 ms | 149 | 18 | 167 | 57.6 | $0.022 hit, $0.660 miss / $1.980 out (off-peak) | $0.00013398 |

## Ответ: GPT OSS 20B

_Provider: Groq; model: `openai/gpt-oss-20b`; time=4.88 s; tokens: prompt=202, completion=4096, total=4298; estimated_cost=$0.00124395; finish_reason=length_

_Модель вернула пустой `message.content`._

## Ответ: DeepSeek V4 Flash

_Provider: DeepSeek; model: `deepseek-v4-flash`; time=795 ms; tokens: prompt=149, completion=18, total=167, cache_hit=0, cache_miss=149; estimated_cost=$0.00004466; finish_reason=stop_

Седьмая сестра играет в шахматы с третьей сестрой.

## Ответ: DeepSeek V4 Pro

_Provider: DeepSeek; model: `deepseek-v4-pro`; time=312 ms; tokens: prompt=149, completion=18, total=167, cache_hit=0, cache_miss=149; estimated_cost=$0.00013398; finish_reason=stop_

Седьмая сестра играет в шахматы с третьей сестрой.

## Краткий вывод

- Самая быстрая по wall-clock времени: `deepseek-v4-pro` (312 ms).
- Самая дешевая в этом запуске: `deepseek-v4-flash` ($0.00004466).
- Меньше всего токенов потратила: `deepseek-v4-flash` (167 total tokens).
- Качество лучше смотреть по разделу API-оценки и по самим ответам: метрики скорости и цены не гарантируют правильность решения.
