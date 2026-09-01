# DeepSeek Go CLI

Небольшое CLI-приложение на Go, которое отправляет промпт в DeepSeek Chat Completions API и печатает ответ в консоль.

## Требования

- Go 1.26+
- API-ключ DeepSeek

## Конфигурация

Приложение читает локальный файл `.env` в корне проекта:

```bash
DEEPSEEK_API_KEY=<ваш DeepSeek API key>
DEEPSEEK_MODEL=deepseek-v4-flash
```

По умолчанию используется:

- API: `https://api.deepseek.com/chat/completions`
- model: `deepseek-v4-flash`

Модель можно поменять через флаг `-model`, переменную окружения `DEEPSEEK_MODEL` или значение в `.env`.

## Запуск

```bash
go run ./day-1 -prompt "Объясни, что такое интерфейсы в Go, простыми словами"
```

Или через stdin:

```bash
echo "Напиши короткий план изучения Go" | go run ./day-1
```

Дополнительные параметры:

```bash
go run ./day-1 \
  -model deepseek-v4-pro \
  -temperature 0.4 \
  -max-tokens 800 \
  -prompt "Сделай ревью этой идеи"
```

## Проверка

```bash
go test ./...
```

Тесты не отправляют реальные запросы в DeepSeek.
