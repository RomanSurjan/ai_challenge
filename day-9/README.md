# Day 9: управление контекстом через сжатие истории

`day-9` показывает учебный прием для длинных диалогов: агент хранит полную историю всегда, но в запрос к модели отправляет не всю историю, а `summary + последние сообщения`.

Есть два режима:

- без сжатия: `system + full history + current user message`;
- со сжатием: `system + summary + recent history + current user message`.

Полная история не удаляется и остается в `history.json`. Summary хранится отдельно в `summary.json`.

## Главное правило

Сжатие запускается не от размера token budget и не от `-context-limit`. Оно запускается только от количества сообщений в истории.

Формула:

```go
targetCovered = len(history) - KeepLastMessages
newBlock = history[summary.CoveredMessages : targetCovered]

if len(newBlock) >= ChunkSize {
	update summary
} else {
	do not update summary yet
}
```

Что это значит:

- `KeepLastMessages` - сколько последних сообщений истории всегда остаются в prompt дословно;
- `ChunkSize` - минимальный размер старого непокрытого блока, после которого можно обновить summary;
- `SummaryCoversMessages` - сколько первых сообщений истории уже заменены summary;
- `PendingMessages` / `old messages waiting for summary` - старые сообщения между уже покрытой summary частью и окном последних `KeepLastMessages`; они временно идут в prompt дословно и ждут следующего summary chunk;
- `MessagesUntilSummaryUpdate` - сколько новых сообщений должно накопиться, чтобы блок достиг `ChunkSize`.

Пример: `KeepLastMessages=6`, `ChunkSize=10`, в истории 18 сообщений, summary покрывает 10.

```text
targetCovered = 18 - 6 = 12
newBlock = history[10:12]
len(newBlock) = 2
```

Summary не обновляется, потому что 2 сообщения меньше chunk size 10. До следующего обновления нужно еще 8 сообщений.

## Что попадает в prompt

Когда summary уже есть, prompt собирается так:

```text
system message
system: "Краткое содержание предыдущей части диалога:\n..."
old messages not covered by summary yet
last KeepLastMessages messages
current user message
```

Важно: `old messages waiting for summary` тоже временно отправляются как есть. Они станут частью summary только когда накопится полный chunk.

По умолчанию summary собирает `APISummarizer`: это отдельный OpenAI-compatible запрос `POST /chat/completions` к той же DeepSeek-инфраструктуре, с тем же base URL, API key, моделью и HTTP client, что и основной chat. Prompt суммаризации просит связно сохранить факты, решения, ограничения, цели, текущий статус, предпочтения и незавершенные задачи, учитывая existing summary и новый блок сообщений. Он также запрещает выдумывать детали, пересказывать каждую реплику и возвращать markdown-заголовки или служебные пояснения.

Demo-режимы и unit-тесты используют fake transport или локальный `LocalSummarySummarizer`, чтобы не ходить в реальный API.

## Отчет компрессии

CLI, web UI и `POST /api/chat` возвращают подробный `compression_report`.

Пример, когда summary пока не обновилась:

```text
Compression report:
  mode:                       enabled
  total history messages:     18
  summary covers:             10 messages
  recent messages kept:       6
  old messages waiting for summary: 2
  messages until summary:     8
  summary updated now:        no
  reason:                     pending block is smaller than summary chunk size
  full prompt input:          1240 tokens
  actual prompt input:        430 tokens
  estimated saved input:      810 tokens (65.3%)
  saving status:              compressed prompt is shorter than full prompt
  context-limit check:        actual prompt input fits local context-limit with 7762 tokens remaining
```

Пример, когда summary обновилась:

```text
  summary updated now:        yes
  newly compressed:           10 messages
  reason:                     pending block reached summary chunk size
```

## Почему экономия может быть 0

`estimated saved input` считается только как разница между полным prompt и фактически отправленным prompt:

```text
estimated saved input = full prompt input - actual prompt input
```

Если summary еще нет, экономии нет:

```text
estimated saved input:      0 tokens (0.0%)
saving status:              no summary yet
```

Если summary уже есть, но она пока не делает prompt короче, отчет покажет:

```text
saving status:              summary exists, but compressed prompt is not shorter yet
```

На маленькой истории summary может не появляться до достижения `summary-chunk-size`. Это ожидаемое поведение.

## Что такое `-context-limit`

`-context-limit` - это не условие сжатия.

Это локальная учебная проверка: примерный максимум input-токенов, после которого агент не отправляет запрос в API и показывает ошибку переполнения контекста.

Порядок такой:

1. агент читает полную историю;
2. если compression включена, агент обновляет summary только по правилу `ChunkSize`;
3. если summary нужно обновить, агент делает отдельный API-запрос суммаризации до сборки actual prompt;
4. агент выбирает actual prompt: полный prompt или `summary + recent`;
5. агент считает `actual prompt input`;
6. только после этого агент проверяет `actual prompt input` против `context-limit`;
7. если actual prompt помещается, основной chat request отправляется в API.

Так можно увидеть пользу compression: полный prompt мог бы быть больше лимита, но actual prompt после summary помещается.

## Конфигурация

Приложение читает `.env` из корня проекта.

```bash
DEEPSEEK_API_KEY=<ваш DeepSeek API key>
DEEPSEEK_MODEL=deepseek-v4-flash
DEEPSEEK_BASE_URL=https://api.deepseek.com
DAY9_HISTORY_PATH=day-9/history.json
DAY9_SUMMARY_PATH=day-9/summary.json
DAY9_CONTEXT_LIMIT=8192
DAY9_COMPRESSION_ENABLED=true
DAY9_KEEP_LAST_MESSAGES=6
DAY9_SUMMARY_CHUNK_SIZE=10
DAY9_PROMPT_TOKEN_MULTIPLIER=1
DAY9_COMPLETION_TOKEN_MULTIPLIER=1
DAY9_INPUT_PRICE_PER_1M=0
DAY9_OUTPUT_PRICE_PER_1M=0
```

Основные флаги:

```text
-compress-history       включить или выключить отправку summary + recent history
-keep-last-messages     сколько последних сообщений оставить дословно
-summary-chunk-size     сколько старых сообщений нужно накопить до обновления summary
-summary                путь к summary.json
-context-limit          локальный учебный лимит input-токенов после выбора actual prompt
-demo-compression       показать этапы сжатия без API
-demo-tokens            показать рост токенов без API
-history                путь к history.json
-prompt                 отправить один CLI-запрос
-serve                  запустить web-чат
-calibrate-tokens       сравнить локальную оценку токенов с API usage
```

По умолчанию compression включена, потому что Day 9 посвящен управлению контекстом. Для сравнения можно отключить ее:

```bash
go run ./day-9 -compress-history=false -prompt "Что такое управление контекстом?"
```

## Запуск CLI

```bash
go run ./day-9 \
  -keep-last-messages 6 \
  -summary-chunk-size 10 \
  -prompt "Продолжи с учетом предыдущих решений"
```

После ответа CLI печатает `Token report` и подробный `Compression report`.

## Demo без API

Сравнение роста токенов:

```bash
go run ./day-9 -demo-tokens
```

Демонстрация compression:

```bash
go run ./day-9 -demo-compression
```

Demo показывает этапы:

- история маленькая, summary еще нет;
- накопился chunk, summary создан;
- история растет, экономия появляется;
- следующий chunk еще ожидается.

В конце demo сравнивает полный prompt и prompt со summary.

## Локальный web-чат

Запуск:

```bash
go run ./day-9 -serve
```

Или helper-скрипт:

```bash
./day-9/run_web.sh
```

После запуска откройте:

```text
http://localhost:8080
```

`POST /api/chat` возвращает:

- `content`;
- `usage`, если DeepSeek вернул фактические токены;
- `token_report`;
- `compression_report`.

`POST /api/context/prepare` можно вызвать перед chat: если старый блок достиг `summary-chunk-size`, он обновит summary и вернет свежий `compression_report`. Если frontend не вызовет prepare, `/api/chat` выполнит ту же подготовку сам до основного запроса к модели. `GET /api/compression-report` только читает состояние и не обновляет summary.

В `compression_report` есть поля:

- `total_history_messages`;
- `summary_covers_messages`;
- `recent_messages_kept`;
- `pending_old_messages` - старые сообщения, ожидающие следующего summary chunk;
- `messages_until_summary_update`;
- `summary_update_status`;
- `summary_update_label`;
- `summary_updated_now`;
- `newly_compressed_messages`;
- `reason`;
- `full_prompt_input_tokens`;
- `actual_prompt_input_tokens`;
- `estimated_saved_input_tokens`;
- `estimated_saved_percent`;
- `saving_status`;
- `context_limit_status`.

## Проверка

```bash
GOCACHE=/private/tmp/ai_challenge_go_cache go test ./day-9
GOCACHE=/private/tmp/ai_challenge_go_cache go run ./day-9 -demo-compression
DEEPSEEK_API_KEY=test GOCACHE=/private/tmp/ai_challenge_go_cache go run ./day-9 -serve -addr :18080
```

Обычный `go test ./day-9` тоже подходит, если системный Go cache доступен для записи.
