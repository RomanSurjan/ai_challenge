# Day 17 — GitHub MCP Agent

Самостоятельный Go-проект с настоящим агентским циклом. Пользователь пишет
естественный запрос, DeepSeek самостоятельно выбирает MCP-инструмент, отдельный
MCP-сервер получает актуальные данные публичного репозитория через GitHub REST
API, а structured result возвращается модели для финального ответа.

Видео Full HD: [artifacts/day17-mcp-agent-demo.mp4](artifacts/day17-mcp-agent-demo.mp4)

## Результат

Полный цикл выглядит так:

1. агент подключается к MCP по Streamable HTTP;
2. `tools/list` возвращает `get_github_repository` и JSON Schema;
3. схема без изменений передаётся DeepSeek как function tool;
4. запрос к модели использует `tool_choice: auto`;
5. модель формирует `owner` и `repository`;
6. агент выполняет MCP `tools/call`;
7. MCP-сервер вызывает `GET /repos/{owner}/{repository}`;
8. GitHub metadata возвращается как `structuredContent`;
9. агент добавляет результат в историю как `role=tool` с исходным
   `tool_call_id`;
10. следующий sampling-вызов формирует финальный ответ.

В коде нет поиска ключевых слов, регулярных выражений для определения намерения,
принудительного tool call или прямого обращения агента к GitHub в обход MCP.

Фактическая трассировка: [artifacts/local-real-model-smoke.json](artifacts/local-real-model-smoke.json).
Удалённая трассировка: [artifacts/remote-real-model-smoke.json](artifacts/remote-real-model-smoke.json).
Проверка контейнеров: [artifacts/local-container-verification.txt](artifacts/local-container-verification.txt).

## Архитектура

```text
Browser / CLI
    |
    v
agent-app (:8082)
    |  MCP tools/list
    v
DeepSeek <---- role=tool ---- Agent loop
    | tool_calls                   |
    +------------------------------+
                                   | MCP tools/call
                                   v
                        mcp-server (:8081/mcp)
                                   |
                        HTTPS GET  | /repos/{owner}/{repo}
                                   v
                           GitHub REST API
```

Слои:

- `internal/githubapi` — типизированный и ограниченный HTTP-клиент GitHub;
- `internal/mcpgithub` — MCP tool, Streamable HTTP server и MCP client;
- `internal/agent` — DeepSeek API и ограниченный multi-tool loop;
- `internal/web` — web/API и наблюдаемая трассировка;
- `cmd/mcp-server` и `cmd/agent-app` — независимые процессы.

## MCP-инструмент

Имя: `get_github_repository`.

| Параметр | Тип | Обязательный | Ограничения |
|---|---|---:|---|
| `owner` | `string` | да | 1–39, латинские буквы, цифры и внутренний `-` |
| `repository` | `string` | да | 1–100, латинские буквы, цифры, `.`, `_`, `-` |

Input schema имеет `type: object`, описания параметров, `required` и
`additionalProperties: false`. Tool помечен как read-only и idempotent.

Structured result содержит:

- `full_name`, `description`, `html_url`;
- `language`, `default_branch`;
- `stars`, `forks`, `open_issues`, `archived`;
- `license`, `topics`;
- `created_at`, `updated_at`, `pushed_at`;
- `rate_limit_remaining`, `source`.

`open_issues` — исходный `open_issues_count` GitHub; GitHub учитывает pull
requests как разновидность issues.

## GitHub REST API

Endpoint:

```text
GET https://api.github.com/repos/{owner}/{repository}
```

Клиент отправляет:

```text
Accept: application/vnd.github+json
X-GitHub-Api-Version: 2026-03-10
User-Agent: ai-challenge-day17/1.0
Authorization: Bearer ...  # только если задан GITHUB_TOKEN
```

Публичные репозитории доступны без токена. Опциональный `GITHUB_TOKEN` нужен
только для увеличения rate limit и должен иметь минимальные права. Клиент
намеренно отклоняет приватные репозитории даже при наличии токена.

Защита клиента:

- собственный таймаут и cancellation через `context.Context`;
- лимит ответа 2 MiB с явным обнаружением превышения;
- раздельные ошибки 404 и rate limit;
- обработка `403`/`429`, `X-RateLimit-Reset` и `Retry-After`;
- проверка HTTP-кода, JSON, обязательных полей и rate-limit header;
- нормализация nullable description, language, license, topics и pushed time.

## Переменные окружения

```dotenv
DEEPSEEK_API_KEY=...
DEEPSEEK_MODEL=deepseek-chat
DEEPSEEK_BASE_URL=https://api.deepseek.com
GITHUB_TOKEN=... # optional
MCP_ENDPOINT=http://127.0.0.1:8081/mcp
APP_ADDR=:8080
```

Обязателен только `DEEPSEEK_API_KEY`. `.env`, токены, SSH-ключи и runtime env
не включаются в Docker context и не хранятся в `day-17`.

## Локальный запуск без Docker

MCP-сервер:

```bash
cd day-17
go run ./cmd/mcp-server
```

CLI-запрос:

```bash
go run ./cmd/agent-app -trace-json \
  -prompt 'Расскажи по-русски об актуальном состоянии репозитория modelcontextprotocol/go-sdk.'
```

Web-интерфейс:

```bash
go run ./cmd/agent-app -serve -addr 127.0.0.1:8082
```

Открыть `http://127.0.0.1:8082`. Интерфейс показывает `tools/list` вместе со
схемой, аргументы модели, MCP result и финальный ответ.

## Тестирование

```bash
go test -race ./...
go vet ./...
```

Покрыты:

- GitHub API client через `httptest`;
- обязательные headers и опциональный Bearer token;
- успех, nullable поля, 404, rate limit и приватный репозиторий;
- invalid JSON, неполный и слишком большой ответ;
- timeout и cancellation;
- валидация `owner` и `repository`;
- MCP handshake, `tools/list`, полная JSON Schema и output schema;
- MCP `tools/call`, structured result и tool error;
- агентский loop с fake LLM;
- `tool_choice: auto` и результат как `role=tool` с правильным `tool_call_id`;
- реальный DeepSeek smoke и удалённый smoke на VPS.

## Docker

```bash
cd day-17
docker compose --env-file ../.env up -d --build
docker compose --env-file ../.env ps
```

Compose создаёт отдельные сеть `day17-internal` и volume `day17-data`.
Контейнеры работают от непривилегированного пользователя, с read-only root
filesystem, `cap_drop: ALL`, `no-new-privileges` и healthchecks. Порты
публикуются только через loopback:

- `127.0.0.1:8081` — MCP;
- `127.0.0.1:8082` — web app.

## VPS-деплой

```bash
./deploy.sh
./verify-remote.sh
./remote-demo.sh
```

`deploy.sh` синхронизирует только `day-17`, передаёт secrets через stdin в
`/srv/day17/data/runtime.env` с mode `600`, сохраняет предыдущий Docker image
как `day17-agent:rollback`, затем отдельно выполняет build и recreate Compose-
проекта `day17`.

`verify-remote.sh` проверяет health, loopback-привязки, контейнеры, реальный
`tools/list` и то, что `day16-mcp-server` продолжает работать на
`127.0.0.1:8080`.

## Пример

Естественный запрос:

```text
Расскажи по-русски об актуальном состоянии репозитория
modelcontextprotocol/go-sdk: назначение, основной язык, ветка по умолчанию,
звёзды, форки, открытые issues, лицензия и даты последнего обновления.
```

Tool call формирует модель, например:

```json
{
  "name": "get_github_repository",
  "arguments": {
    "owner": "modelcontextprotocol",
    "repository": "go-sdk"
  }
}
```

Фрагмент structured result:

```json
{
  "full_name": "modelcontextprotocol/go-sdk",
  "language": "Go",
  "default_branch": "main",
  "license": "Apache-2.0",
  "source": "GitHub REST API"
}
```

## Первичные источники

- [GitHub REST: Get a repository](https://docs.github.com/en/rest/repos/repos);
- [GitHub REST API versions](https://docs.github.com/en/rest/about-the-rest-api/api-versions);
- [GitHub REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api);
- [официальный MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk);
- [MCP Go SDK: tools](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/server.md#tools);
- [MCP Go SDK: Streamable HTTP](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/protocol.md#streamable-transport);
- [DeepSeek Tool Calls](https://api-docs.deepseek.com/guides/tool_calls/);
- [DeepSeek Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion/).

## Ограничения

- Без токена основной лимит GitHub — 60 запросов в час на IP; обычный
  пользовательский token обычно даёт 5000 запросов в час.
- Возможны secondary rate limits; автоматические агрессивные retry не делаются.
- Stars, forks, issues и timestamps изменяются со временем.
- История чата не сохраняется между процессами: проект сфокусирован на полном
  MCP tool-call цикле.
- MCP endpoint не имеет публичной аутентификации и поэтому доступен только с
  loopback/внутренней Docker-сети или через SSH-туннель.
