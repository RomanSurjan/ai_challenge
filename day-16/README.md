# Day 16 — MCP connection and tools/list

Минимальный MCP-проект на официальном Go SDK:

- `cmd/server` поднимает Streamable HTTP MCP endpoint `/mcp`;
- сервер регистрирует инструмент `server_status`;
- `cmd/client` устанавливает MCP-соединение и получает полный список tools;
- интеграционный тест проверяет handshake, версию протокола и схему tool;
- Docker-контейнер запущен на VPS и доступен только через SSH-туннель.

## Результат задания

Запуск клиента:

```bash
./remote-demo.sh
```

Фактический вывод:

```text
MCP connection established
Server: day16-mcp-server 1.0.0
Protocol: 2026-07-28
Tools (1):
- server_status: Returns the current status and version of the demo MCP server.
```

Видео: [artifacts/mcp-tools-demo.mp4](artifacts/mcp-tools-demo.mp4)

## Локальный запуск

Терминал 1:

```bash
go run ./cmd/server
```

Терминал 2:

```bash
go run ./cmd/client
```

Тесты:

```bash
go test ./...
go vet ./...
```

## Развёртывание MCP-сервера

```bash
./deploy-mcp.sh
./remote-demo.sh
```

Compose публикует контейнер только как `127.0.0.1:8080:8080`. Скрипт
`remote-demo.sh` создаёт временный SSH-туннель на локальный порт `18080`,
запускает MCP-клиент и закрывает туннель после проверки.

## Структура

```text
cmd/server/main.go              MCP HTTP server
cmd/client/main.go              MCP client + tools/list
internal/mcpdemo/server.go      server and tool registration
internal/mcpdemo/client.go      connection and pagination
internal/mcpdemo/integration_test.go
Dockerfile
docker-compose.yml
deploy-mcp.sh
remote-demo.sh
```

## Production VPS baseline

Воспроизводимая базовая настройка VPS для будущего MCP-сервера.

### Сервер

- Provider: H3LLO Cloud capsule
- Host: `91.188.214.231`
- SSH user: `user`
- SSH key: `~/.ssh/ssh-key-5298`
- Application directory: `/srv/day16`

Секреты и приватный SSH-ключ в репозиторий не копируются.

> Капсула создана на Ubuntu 25.10. Этот релиз завершил поддержку 9 июля
> 2026 года. По решению владельца настройка применяется к текущей системе.

### Что делает bootstrap

1. Проверяет Linux, root-доступ и наличие пользователя для деплоя.
2. Обновляет индекс пакетов. Для архивированного Ubuntu при необходимости
   переключает официальные источники на `old-releases.ubuntu.com`.
3. Устанавливает все доступные обновления ОС, затем Docker, Compose, UFW,
   Fail2ban, Git, curl и служебные утилиты.
4. Запрещает root/password SSH login, оставляя вход по ключу.
5. Включает firewall: исходящие соединения разрешены, входящие запрещены,
   SSH ограничен rate limit. Порты 80/443 по умолчанию закрыты.
6. Настраивает Fail2ban и ротацию Docker-логов.
7. Создаёт `/srv/day16/{app,data,logs}` и добавляет пользователя в группу
   `docker`.

### Запуск baseline

Из этой директории:

```bash
./deploy.sh
```

После запуска новая группа `docker` применится при следующем SSH-сеансе.

Проверка без изменений:

```bash
./verify-remote.sh
```

### Сетевой доступ

Контейнер приложения следует публиковать только на loopback, например:

```yaml
ports:
  - "127.0.0.1:8080:8080"
```

После добавления reverse proxy и домена можно явно открыть HTTP/HTTPS:

```bash
ssh -i ~/.ssh/ssh-key-5298 user@91.188.214.231 \
  'sudo ufw allow 80/tcp && sudo ufw allow 443/tcp'
```

Не публикуйте порт MCP-приложения напрямую через `0.0.0.0`: Docker может
обходить правила UFW для опубликованных контейнерных портов.
