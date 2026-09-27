# Day 19 — композиция MCP-инструментов

Самостоятельный Go-проект с тремя отдельными MCP-инструментами:

```text
search → summarize → save_to_file
```

Порядок выбирает и выполняет DeepSeek в настоящем `tool_choice: auto` цикле. HTTP-handler web-приложения только передаёт естественный запрос агенту; он не вызывает этапы вручную. Между вызовами действует проверка целостности: `summarize` принимает точные документы `search`, а `save_to_file` — точные `summary` и `sources` предыдущего шага.

## Запуск

```bash
cd /Users/romansurzhan/ai_challenge/day-19
cp .env.example .env
# добавьте DEEPSEEK_API_KEY в .env
docker compose -p day19 up --build -d
```

- MCP Streamable HTTP: `http://127.0.0.1:8086/mcp`
- Web UI: `http://127.0.0.1:8087`
- health checks: `/healthz` на обоих портах

Compose использует отдельные `day19-mcp-server`, `day19-agent-app`, сеть `day19-internal` и volume `day19-data`. Предыдущие дни не используются.

## Проверка

```bash
go test ./...
docker compose -p day19 config
```

Тесты не обращаются к реальным Wikipedia или DeepSeek: используются локальные HTTP-серверы, fake LLM и официальный in-memory MCP transport.
