# Day 20 — Orchestration MCP

Самостоятельный Go-модуль с тремя независимыми Streamable HTTP MCP-серверами и агентом DeepSeek. Агент получает объединённый `tools/list`, сохраняет владельца каждого инструмента и маршрутизирует каждый `tools/call` в соответствующую MCP-сессию.

## Сервисы

- Knowledge MCP (`127.0.0.1:8088`): `knowledge_search`, `knowledge_summarize`.
- GitHub MCP (`127.0.0.1:8089`): `github_get_repository`, `github_get_latest_release`.
- Artifact MCP (`127.0.0.1:8090`): `artifact_build_report`, `artifact_save_to_file`, `artifact_list_files`, `artifact_read_file`.
- Agent UI (`http://127.0.0.1:8091`).

Flow полностью выбирается моделью через `tool_choice: auto`. Knowledge и GitHub можно вызывать в любом порядке и повторять при необходимости. Агент проверяет только реальные зависимости данных: summarize использует точный search result, build — точные knowledge/GitHub results, save — точный Markdown из build. Также проверяются исходные `tool_call_id` и совпадение SHA-256.

Идентичные read-only вызовы в пределах одного запроса обслуживаются из локального cache с сохранением нового `tool_call_id`. Бюджет ограничивает реальные MCP-вызовы (24) и раунды модели (18); при исчерпании агент отключает tools и просит модель сформировать финальный ответ по уже собранным данным вместо аварийного завершения.

## Проверка и запуск

```sh
go test ./...
go vet ./...
docker compose -p day20 --env-file ../.env config
docker compose -p day20 --env-file ../.env up --build -d
```

Секреты читаются только из окружения. `GITHUB_TOKEN` необязателен. Volume `day20-data` доступен на запись только Artifact MCP; web-приложение получает его read-only.
