# Day 6: простой агент с локальным web-чатом

`day-6` реализует агента поверх DeepSeek Chat Completions API. Его можно использовать из CLI или запустить как локальный web-чат в браузере.

Оценка достаточности текущей реализации и сравнение возможных идей для усиления проекта зафиксированы в [answer_consilium.md](answer_consilium.md).

Главная идея дня: вызов модели больше не лежит прямо в `main`. Код разделен по ролям:

- `main.go` - чтение конфигурации, выбор CLI/web-режима и запуск приложения;
- `agent.go` - `AgentConfig`, `Agent`, `NewAgent(...)`, `Ask(...)` и вся логика обращения к DeepSeek;
- `server.go` - локальный HTTP-сервер, страница `/` и endpoint `POST /api/chat`;
- `frontend.go` - встраивает frontend-файл в бинарник через `go:embed`;
- `chat_page.html` - HTML/CSS/vanilla JavaScript интерфейс чата.

`main` остается тонким: он читает конфигурацию, создает агента и выбирает режим. В CLI-режиме он получает prompt из `-prompt` или stdin, вызывает `Ask` и печатает ответ. В web-режиме он запускает локальный HTTP-сервер, который отдает отдельный frontend-файл и проксирует сообщения в агента через `/api/chat`.

## Конфигурация

Приложение читает `.env` из корня проекта.

```bash
DEEPSEEK_API_KEY=<ваш DeepSeek API key>
DEEPSEEK_MODEL=deepseek-v4-flash
DEEPSEEK_BASE_URL=https://api.deepseek.com
```

Значения по умолчанию:

- endpoint: `https://api.deepseek.com/chat/completions`
- model: `deepseek-v4-flash`
- timeout: `60s`
- temperature: `0.7`
- max_tokens: `1024`
- thinking: disabled через `{ "type": "disabled" }`

## Запуск

### CLI

Через флаг:

```bash
go run ./day-6 -prompt "Ответь одним предложением: что такое агент?"
```

Через stdin:

```bash
echo "Объясни простыми словами, зачем нужен AI-агент" | go run ./day-6
```

С переопределением модели или адреса API:

```bash
go run ./day-6 \
  -model deepseek-v4-flash \
  -base-url https://api.deepseek.com \
  -prompt "Назови одно отличие агента от обычной функции"
```

### Локальный web-чат

Запуск на адресе по умолчанию `:8080`:

```bash
go run ./day-6 -serve
```

После запуска откройте:

```text
http://localhost:8080
```

Можно выбрать другой адрес:

```bash
go run ./day-6 -serve -addr :8090
```

Web-режим использует тот же `Agent` и тот же метод `Ask(ctx, userPrompt)`. Страница отправляет сообщения на локальный endpoint `POST /api/chat`, а сервер уже обращается к DeepSeek API.

## Почему это агент

Это не один API-вызов из `main`, потому что логика обращения к LLM инкапсулирована в `Agent`.

CLI и web-интерфейс не знают деталей HTTP-запроса к DeepSeek:

- как устроен JSON для Chat Completions;
- какие headers нужны;
- как отключается `thinking`;
- как обработать HTTP-ошибку;
- как распарсить `choices`;
- как вернуть usage и finish reason.

Эта логика находится внутри метода `Agent.Ask`. Поэтому интерфейс может остаться прежним, а внутреннее поведение агента можно развивать отдельно: добавить память, инструменты, retries или несколько моделей.

## Проверка

```bash
go test ./...
```

Тесты `day-6/main_test.go` не делают реальные API-запросы: они используют подменный HTTP-клиент и проверяют сборку запроса, парсинг ответа и ошибки.
