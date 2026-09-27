# Day 18 — Unified persistent MCP agent

Один Go-проект и один DeepSeek-агент для двух задач: постоянный мониторинг публичных GitHub-репозиториев и справочных валютных курсов. GitHub и валюты обслуживают два независимых Streamable HTTP MCP-сервера. Агент получает список серверов, объединяет их каталоги инструментов и маршрутизирует каждый вызов обратно в MCP-владельца.

## Что умеет агент

GitHub:

- `schedule_repository_monitor`
- `get_repository_monitor_summary`
- `list_repository_monitors`
- `cancel_repository_monitor`

Валюты:

- `schedule_exchange_rate_monitor`
- `get_exchange_rate_monitor_summary`
- `list_exchange_rate_monitors`
- `cancel_exchange_rate_monitor`
- `convert_currency`

Каждый инструмент имеет строгие input/output JSON Schema, MCP annotations, `structuredContent` и структурированные ошибки. Валютные значения хранятся строками и вычисляются десятичной арифметикой без `float64`. Frankfurter предоставляет ежедневные справочные курсы официальных источников — не биржевые котировки в реальном времени.

## Архитектура

```text
Browser / CLI → agent-app :8084 → DeepSeek tool loop
                              │
                              ├── github-mcp :8083
                              │     4 tools + scheduler + day18.db → GitHub REST API
                              │
                              └── currency-mcp :8085
                                    5 tools + scheduler + currency.db → Frankfurter API v2
```

Базы и процессы разделены, поэтому отказ или перезапуск одного MCP не останавливает другой. Оба файла находятся в общем Docker volume `day18-data`, а результаты видны в одной панели. Web-слой не читает SQLite и не обращается к внешним API напрямую.

## История диалога

Web-приложение выдаёт браузеру HttpOnly session cookie и хранит последние 12 полных ходов диалога. В следующий запрос модели передаются предыдущие сообщения вместе с MCP tool calls и результатами, поэтому фразы вроде «останови созданный монитор» работают без повторного ввода ID. Кнопка «Новый диалог» очищает только историю чата; расписания и снимки MCP при этом не удаляются. История чата хранится в памяти `agent-app` и сбрасывается при его перезапуске.

Ключевые пакеты:

- `internal/mcpmulti` — агрегация каталогов и маршрутизация tool calls по нескольким MCP;
- `internal/mcpgithub`, `internal/store`, `internal/scheduler`, `internal/githubapi` — GitHub-домен;
- `internal/mcpcurrency`, `internal/currencystore`, `internal/currencyscheduler`, `internal/frankfurter`, `internal/decimal` — валютный домен;
- `internal/agent` — общий многошаговый tool-call loop;
- `internal/web` — общая панель GitHub + валюты.

## Надёжность

Оба планировщика используют постоянные SQLite-расписания, атомарный claim и уникальную пару `(schedule_id, scheduled_for)`. Первый запуск назначается сразу; пропущенные интервалы не создают лавину catch-up запросов; незавершённые `running`-запуски восстанавливаются после падения; `max_runs` завершает расписание; отмена сохраняет всю историю. Включены foreign keys, `busy_timeout`, WAL и транзакции. `SIGTERM` останавливает оба scheduler loop и корректно закрывает HTTP/SQLite.

## Настройка и запуск

```dotenv
DEEPSEEK_API_KEY=...
DEEPSEEK_MODEL=deepseek-chat
DEEPSEEK_BASE_URL=https://api.deepseek.com
GITHUB_TOKEN=... # optional
FRANKFURTER_BASE_URL=https://api.frankfurter.dev
MCP_SERVERS=github=http://127.0.0.1:8083/mcp,currency=http://127.0.0.1:8085/mcp
APP_ADDR=:8080
DATABASE_PATH=/data/day18.db
```

Docker:

```bash
cd /Users/romansurzhan/ai_challenge/day-18
docker compose --env-file ../.env up -d --build
docker compose ps
```

Откройте `http://127.0.0.1:8084`. GitHub MCP доступен на `http://127.0.0.1:8083/mcp`, Currency MCP — на `http://127.0.0.1:8085/mcp`. Все порты опубликованы только на loopback.

Локально без Docker:

```bash
DATABASE_PATH=/tmp/day18.db MCP_ADDR=127.0.0.1:8083 \
go run ./cmd/github-mcp-server

DATABASE_PATH=/tmp/day18-currency.db MCP_ADDR=127.0.0.1:8085 \
go run ./cmd/currency-mcp-server

MCP_SERVERS=github=http://127.0.0.1:8083/mcp,currency=http://127.0.0.1:8085/mcp \
go run ./cmd/agent-app -serve -addr 127.0.0.1:8084
```

## Проверки

```bash
go test -race ./...
go vet ./...
go build ./cmd/github-mcp-server ./cmd/currency-mcp-server ./cmd/agent-app
docker compose --env-file ../.env config --quiet
```

Тесты покрывают обе SQLite-модели, точную decimal-арифметику, HTTP-клиенты, конкурентный claim, восстановление, `max_runs`, оба MCP-контракта, объединение каталогов, обнаружение коллизий имён и маршрутизацию tool calls.

Полный ручной сценарий: [DEMO.md](./DEMO.md).

## Ограничения

- Минимальный интервал фонового мониторинга — 60 секунд.
- Без GitHub token действует низкий публичный rate limit; `open_issues_count` включает pull requests.
- Frankfurter — источник справочных дневных курсов, не intraday trading data.
- MCP endpoint не имеет публичной аутентификации и должен оставаться на loopback, внутренней Docker-сети или за SSH-туннелем.
