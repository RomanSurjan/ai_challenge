# Day 2: DeepSeek CLI с контролем ответа

CLI-скрипт отправляет запрос в DeepSeek Chat Completions API и позволяет управлять техническими ограничениями ответа через флаги.

## Конфигурация

Скрипт читает `.env` из корня проекта:

```bash
DEEPSEEK_API_KEY=<ваш DeepSeek API key>
```

Внутренние настройки задаются в коде и пока не меняются через CLI:

- API: `https://api.deepseek.com/chat/completions`
- model: `deepseek-v4-flash`
- temperature: `0.7`
- timeout: `60s`
- thinking mode: disabled

## Флаги

- `-prompt` - текст пользовательского запроса. Если флаг не задан, prompt читается из stdin.
- `-response-format` - включает controlled-режим: ответ должен быть валидным JSON по фиксированной movie-review схеме.
- `-max-tokens` - технический лимит генерации DeepSeek API. Если не задан или равен `0`, поле `max_tokens` не отправляется.
- `-stop` - stop sequence для API. Если не задан, поле `stop` не отправляется.

## Обычный режим

Без `-response-format` скрипт отправляет обычный запрос и печатает текстовый ответ модели:

```bash
go run ./day-2 \
  -prompt "Напиши короткую рецензию на фильм Интерстеллар"
```

Prompt можно передать через stdin:

```bash
echo "Напиши короткую рецензию на фильм Интерстеллар" | go run ./day-2
```

## Controlled JSON режим

Флаг `-response-format` включает JSON mode DeepSeek API:

```json
{"type":"json_object"}
```

Дополнительно скрипт добавляет инструкцию модели вернуть только JSON без markdown и без пояснений вне JSON.

Ожидаемая схема ответа:

```json
{
  "is_movie_review": true,
  "title": "...",
  "release_date": "...",
  "rating": "...",
  "short_description": "...",
  "actors": ["..."],
  "error": null
}
```

После ответа скрипт локально парсит JSON, проверяет обязательные поля, лишние поля и базовые типы. В stdout выводится только валидированный JSON.

Пример с ограничениями:

```bash
go run ./day-2 \
  -response-format \
  -max-tokens 700 \
  -stop "END_JSON" \
  -prompt "Напиши рецензию на фильм Интерстеллар"
```

## Краевой случай: запрос не про кино

Если `-response-format` включен, но prompt не является запросом на рецензию фильма, ответ все равно должен быть валидным JSON той же формы:

```json
{
  "is_movie_review": false,
  "title": null,
  "release_date": null,
  "rating": null,
  "short_description": "Запрос пользователя не является запросом на рецензию фильма.",
  "actors": [],
  "error": null
}
```

Пример:

```bash
go run ./day-2 \
  -response-format \
  -max-tokens 300 \
  -stop "END_JSON" \
  -prompt "Объясни, чем интерфейсы в Go отличаются от структур"
```

## Stop sequence

Флаг `-stop` передается в API как `stop`.

Если одновременно включен `-response-format`, модель получает инструкцию вывести stop sequence только после полностью закрытого JSON-объекта. Сам stop-маркер не должен попадать внутрь JSON.

## Проверка

Из корня проекта:

```bash
go test ./...
```
