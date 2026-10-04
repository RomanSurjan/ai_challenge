# Day 22 — фактическое сравнение plain и RAG

Запуск: `2026-10-04T17:31:14Z`  
Chat model: `qwen2.5:3b`  
Embedding model: `qwen3-embedding:0.6b`  
Index: `../day-21/artifacts/index-structural.json`  
Вопросов: 10, top-k: 5, temperature: 0.00, max tokens: 512

## Общие результаты

| Метрика | Plain | RAG |
|---|---:|---:|
| Coverage обязательных concepts | 0.250 | 0.417 |
| Ответы со всеми concepts | 0.000 | 0.100 |
| Средняя generation latency, ms | 3040.0 | 8525.0 |
| Средняя длина ответа, Unicode-символы | 418.5 | 654.3 |
| Средние prompt / completion tokens | 139.8 / 116.8 | 2100.9 / 227.3 |

Expected-source recall в retrieved top-k: **0.442**. В citations: **0.333**. Invalid citations: **0**. Средняя retrieval latency: **148.5 ms**. Ошибок запуска: **0**.

Concept coverage — детерминированный Unicode-aware substring match после lowercase и нормализации пробелов. Он удобен для воспроизводимого сравнения, но не доказывает полную семантическую корректность ответа.

## Результаты по вопросам

| ID | Plain concepts | RAG concepts | Top-k source recall | Cited source recall | Invalid |
|---|---:|---:|---:|---:|---:|
| q01-multi-mcp-routing | 0.333 | 0.333 | 0.500 | 0.250 | 0 |
| q02-monitor-claim | 0.333 | 0.667 | 0.333 | 0.333 | 0 |
| q03-decimal-currency | 0.333 | 0.333 | 0.500 | 0.500 | 0 |
| q04-github-http-safety | 0.000 | 0.500 | 0.333 | 0.333 | 0 |
| q05-agent-tool-loop | 0.667 | 1.000 | 0.250 | 0.250 | 0 |
| q06-pipeline-integrity | 0.000 | 0.500 | 0.500 | 0.000 | 0 |
| q07-artifact-sandbox | 0.000 | 0.000 | 0.333 | 0.333 | 0 |
| q08-session-history | 0.250 | 0.250 | 0.667 | 0.333 | 0 |
| q09-mcp-handshake | 0.333 | 0.333 | 1.000 | 1.000 | 0 |
| q10-wikipedia-search | 0.250 | 0.250 | 0.000 | 0.000 | 0 |

### q01-multi-mcp-routing — Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?

Ожидание: Агент получает tools/list каждой MCP-сессии, объединяет определения для модели и хранит отображение имени инструмента на сессию-владельца; выбранный моделью tools/call отправляется только этому владельцу.

**Plain answer** (concepts 1/3, 5094 ms):

Единый агент объединяет каталоги нескольких MCP-серверов, создавая единую точку доступа для управления всеми серверами. Он маршрутизирует tool calls, анализируя запросы и направляя их на соответствующий сервер. Администратор получает обновленную информацию о состоянии всех серверов и может выполнять операции, как будто они происходят на одном и том же месте.

**RAG answer** (concepts 1/3, retrieval 57 ms, generation 5298 ms):

Единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента с использованием информации из [S1] и [S2]. Агент подключается к двум независимым MCP-серверам, видит оба набора инструментов и направляет вызовы в правильный сервер. Он также объединяет каталоги инструментов из нескольких MCP-серверов и маршрутизирует каждый вызов обратно в MCP-владельца, как указано в [S2].

Retrieved sources:

- S1, score `0.718259`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента — `structural-233b0f6da63ac99f66752b43`
- S2, score `0.619797`: `day-20/README.md` — Day 20 — Orchestration MCP — `structural-9adc7c97de777669396a5699`
- S3, score `0.600517`: `day-18/README.md` — Day 18 — Unified persistent MCP agent — `structural-4e3a63032d3beaf99ea33874`
- S4, score `0.570855`: `day-16/README.md` — Day 16 — MCP connection and tools/list — `structural-1e2b0064262053e9b004765a`
- S5, score `0.559485`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Результат — `structural-07ca3e8180ef5ea440236ae8`

Cited sources:

- [S1]: `day-18/DEMO.md` — Day 18 — демонстрация единого агента — `structural-233b0f6da63ac99f66752b43`
- [S2]: `day-20/README.md` — Day 20 — Orchestration MCP — `structural-9adc7c97de777669396a5699`

### q02-monitor-claim — Как планировщик мониторинга атомарно захватывает просроченный запуск и не допускает дубликаты после перезапуска?

Ожидание: ClaimDueRun работает в транзакции: проверяет активное расписание и срок, создаёт running-запуск и переносит next_run. Уникальность пары schedule_id/scheduled_for блокирует дубль, а SQLite сохраняет состояние между перезапусками.

**Plain answer** (concepts 1/3, 2823 ms):

Планировщик мониторинга, используя механизмы контроля за атомарным захватом, может захватывать просроченный запуск, чтобы избежать дубликатов после перезапуска. Это достигается путем установки специального состояния или метки, которая указывает на то, что запуск уже был выполнен. При перезапуске планировщик проверяет это состояние и не выполняет повторный запуск, если он уже был выполнен.

**RAG answer** (concepts 2/3, retrieval 50 ms, generation 11114 ms):

Планировщик мониторинга атомарно захватывает просроченный запуск и не допускает дубликаты после перезапуска, используя уникальную пару `(schedule_id, scheduled_for)`. Это достигается благодаря использованию постоянных SQLite-расписаний, атомарного claim и транзакций, как указано в [S1]. В случае просроченного запуска, планировщик восстанавливает незавершённые `running`-запуски и завершает расписание при достижении максимального количества запусков (`max_runs`). Отмена сохраняет всю историю, включая отменённые запуски.

Retrieved sources:

- S1, score `0.572491`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Надёжность — `structural-48f25c926c4b8f19033a5f25`
- S2, score `0.491501`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 3. GitHub-монитор — `structural-19c812c780af19908167c6c8`
- S3, score `0.440987`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 8. Отмена — `structural-f7e892bda05c23223164b190`
- S4, score `0.440906`: `day-18/internal/scheduler/scheduler_test.go` — func TestRunOnceRecoversOverdueScheduleWithoutRapidCatchupAndHonorsMaxRuns — `structural-96548f4c0bfe5d4877621c36`
- S5, score `0.417296`: `day-18/internal/scheduler/scheduler_test.go` — func TestRunOnceExecutesDueButNotEarlyAndCalculatesNextRun — `structural-3449ca4500e3ce80096578cc`

Cited sources:

- [S1]: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Надёжность — `structural-48f25c926c4b8f19033a5f25`

### q03-decimal-currency — Почему валютные суммы не вычисляются через float64 и как реализована точная десятичная арифметика?

Ожидание: float64 даёт двоичные ошибки округления. Decimal разбирает строку в целочисленный коэффициент и десятичный scale, выполняет операции целыми числами и форматирует результат без потери десятичных знаков.

**Plain answer** (concepts 1/3, 2951 ms):

Валютные суммы обычно вычисляются с использованием специализированных типов данных, таких как `decimal.Decimal` в Python, который обеспечивает точную десятичную арифметику, необходимую для работы с валютами. Это избегает проблем с плавающей точкой, такими как дробные части, которые могут приводить к округлению и ошибкам. Валютные суммы часто имеют ограничение количества знаков после запятой, что делает использование `float64` неэффективным.

**RAG answer** (concepts 1/3, retrieval 84 ms, generation 5126 ms):

Валютные суммы не вычисляются через float64, так как float64 использует двоичную систему счисления, что может приводить к погрешностям при работе с десятичными значениями. Вместо этого в коде используется точная десятичная арифметика, реализованная в библиотеке [S2].

Retrieved sources:

- S1, score `0.554220`: `day-18/internal/decimal/decimal_test.go` — func TestExactDecimalArithmetic — `structural-3e989fc26e10f4c2dbf1de46`
- S2, score `0.538036`: `day-18/internal/decimal/decimal.go` — package decimal — `structural-6089ea73371c5178d1ca663e`
- S3, score `0.524710`: `day-18/internal/decimal/decimal_test.go` — package decimal — `structural-0cd0c4d97ddd44fd54b9c54a`
- S4, score `0.489228`: `day-18/internal/mcpcurrency/server_test.go` — func TestUnsupportedCurrencyIsStructured — `structural-088ddcce9fe0e3e95f187bb5`
- S5, score `0.478055`: `day-18/internal/mcpcurrency/server.go` — func RegisterTools — `structural-8b93e2f535b73a8eabc9e899`

Cited sources:

- [S2]: `day-18/internal/decimal/decimal.go` — package decimal — `structural-6089ea73371c5178d1ca663e`

### q04-github-http-safety — Какие ограничения и проверки применяются при запросе метаданных публичного репозитория GitHub?

Ожидание: Входные owner/repository валидируются, запрос имеет timeout и обязательные GitHub headers, тело ограничено по размеру. Клиент проверяет HTTP-коды и rate-limit headers, обязательные поля JSON и отклоняет private-репозитории.

**Plain answer** (concepts 0/4, 1965 ms):

При запросе метаданных публичного репозитория GitHub применяются ограничения и проверки, включая проверку авторизации через токен OAuth или API ключа для доступа к репозиториям, которые требуют авторизации. Также могут быть ограничения по количеству запросов в минуту для защиты от несанкционированного использования.

**RAG answer** (concepts 2/4, retrieval 64 ms, generation 10743 ms):

Ограничения и проверки, применяемые при запросе метаданных публичного репозитория GitHub, включают:

- Без токена основной лимит GitHub — 60 запросов в час на IP; обычный пользовательский токен обычно даёт 5000 запросов в час. [S4]
- Возможны secondary rate limits. [S4]
- Stars, forks, issues и timestamps изменяются со временем. [S4]
- История чата не сохраняется между процессами. [S4]
- MCP endpoint не имеет публичной аутентификации и поэтому доступен только с loopback/внутренней Docker-сети или через SSH-туннель. [S4]

Для запроса метаданных используется следующий запрос:

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

Публичные репозитории доступны без токена. Опциональный `GITHUB_TOKEN` нужен только для увеличения rate limit и должен иметь минимальные права. Клиент намеренно отклоняет приватные репозитории даже при наличии токена. [S1]

Retrieved sources:

- S1, score `0.626431`: `day-17/README.md` — Day 17 — GitHub MCP Agent > GitHub REST API — `structural-2797cfa895d89b6334b86cca`
- S2, score `0.578910`: `day-17/README.md` — Day 17 — GitHub MCP Agent > MCP-инструмент — `structural-4d8c8f1362acd426edf96f6a`
- S3, score `0.556228`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Пример — `structural-a1ce2305dc7ceeadb605b80a`
- S4, score `0.545216`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Ограничения — `structural-ddb51d6741369eb36dada8e7`
- S5, score `0.532446`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Ограничения — `structural-6a60fcf8927ad0e3d2b96cfe`

Cited sources:

- [S4]: `day-17/README.md` — Day 17 — GitHub MCP Agent > Ограничения — `structural-ddb51d6741369eb36dada8e7`
- [S1]: `day-17/README.md` — Day 17 — GitHub MCP Agent > GitHub REST API — `structural-2797cfa895d89b6334b86cca`

### q05-agent-tool-loop — Как агент сохраняет tool_call_id и передаёт результат MCP-инструмента обратно модели в следующем раунде?

Ожидание: После assistant-сообщения с tool_calls агент вызывает MCP, сериализует результат и добавляет role=tool с исходным tool_call_id в историю; затем отправляет всю историю в следующий sampling-вызов модели.

**Plain answer** (concepts 2/3, 2512 ms):

Агент сохраняет tool_call_id для идентификации конкретного вызова инструмента. Результат MCP-инструмента передаётся обратно в модель в следующем раунде, обычно через механизм обратной связи или ответа на предыдущий запрос. Это может быть сделано через API, если используется автоматическая передача данных, или через интерфейс пользователя вручную, если это необходимо для визуализации или анализа результатов.

**RAG answer** (concepts 3/3, retrieval 80 ms, generation 18047 ms):

Агент сохраняет tool_call_id и передаёт результат MCP-инструмента обратно модели в следующем раунде через следующие шаги:

1. В тесте [S1] показано, как агент использует `tool_choice: auto` для выбора соответствующего MCP-инструмента (`get_github_repository` в данном случае). Tool_call_id сохраняется в истории сообщений (`toolMessage.ToolCallID != "call-1"`).

2. В тесте [S2] описано, что агент добавляет результат выполнения MCP-инструмента в историю как `role=tool` с исходным `tool_call_id`. Это подтверждается строкой `second := llm.requests[1]` и `toolMessage.ToolCallID != "call-1"`.

3. В тесте [S3] показано, как агент использует `tool_choice: auto` для выбора соответствующего MCP-инструмента (`ScheduleToolName` и `SummaryToolName` в данном случае). Tool_call_id сохраняется в истории сообщений (`firstToolResult.ToolCallID != "call-create"` и `secondToolResult.ToolCallID != "call-summary"`).

4. В тесте [S4] описано, что MCP-инструменты возвращают структурированные результаты и idempotent-ный механизм для отмены. Tool_call_id сохраняется в истории сообщений (`firstToolResult.Role != "tool"` и `secondToolResult.Role != "tool"`).

5. В тесте [S5] описано, что история диалога хранится в памяти `agent-app` и содержит предыдущие сообщения, MCP tool calls и результаты. Tool_call_id сохраняется в истории сообщений (`toolMessage.ToolCallID != "call-1"`).

Таким образом, агент сохраняет tool_call_id и передаёт результат MCP-инструмента обратно модели в следующем раунде, используя `tool_choice: auto` для выбора соответствующего MCP-инструмента и сохраняя Tool_call_id в истории сообщений.

Retrieved sources:

- S1, score `0.675013`: `day-17/internal/agent/agent_test.go` — func TestAgentToolCallLoopReturnsModelAnswerBasedOnMCPResult — `structural-26505143760f1ebc08dc4c85`
- S2, score `0.673821`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Результат — `structural-07ca3e8180ef5ea440236ae8`
- S3, score `0.602320`: `day-18/internal/agent/agent_test.go` — func TestAgentSupportsSequentialModelSelectedMCPCalls — `structural-6101e521b70ada5f1ff3ac9b`
- S4, score `0.587629`: `day-18/internal/mcpgithub/server_test.go` — func TestMCPToolCallsReturnStructuredResultsAndIdempotentCancel — `structural-9b2395c62cb4d238c3b2fb6b`
- S5, score `0.587297`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > История диалога — `structural-30be5c06a819a4ce27c31900`

Cited sources:

- [S1]: `day-17/internal/agent/agent_test.go` — func TestAgentToolCallLoopReturnsModelAnswerBasedOnMCPResult — `structural-26505143760f1ebc08dc4c85`
- [S2]: `day-17/README.md` — Day 17 — GitHub MCP Agent > Результат — `structural-07ca3e8180ef5ea440236ae8`
- [S3]: `day-18/internal/agent/agent_test.go` — func TestAgentSupportsSequentialModelSelectedMCPCalls — `structural-6101e521b70ada5f1ff3ac9b`
- [S4]: `day-18/internal/mcpgithub/server_test.go` — func TestMCPToolCallsReturnStructuredResultsAndIdempotentCancel — `structural-9b2395c62cb4d238c3b2fb6b`
- [S5]: `day-18/README.md` — Day 18 — Unified persistent MCP agent > История диалога — `structural-30be5c06a819a4ce27c31900`

### q06-pipeline-integrity — Как в композиции search summarize save_to_file проверяется целостность данных между этапами?

Ожидание: Pipeline state разрешает фиксированный порядок. summarize обязан получить точные query/documents результата search, а save_to_file — точные query/summary/sources результата summarize; сохранённый Markdown сопровождается SHA-256.

**Plain answer** (concepts 0/4, 2657 ms):

В композиции `search summarize save_to_file` целостность данных между этапами обычно проверяется путем валидации результатов предыдущего этапа перед началом следующего. Например, после выполнения этапа `search` данные проверяются на наличие ошибок или несоответствий, а затем передаются в этап `summarize` для обработки. Этап `save_to_file` обычно выполняется только после успешного завершения предыдущих этапов.

**RAG answer** (concepts 2/4, retrieval 83 ms, generation 6444 ms):

В композиции search summarize save_to_file проверяется целостность данных между этапами через проверку целостности, описанную в тесте [S3]. Между вызовами инструментов выполняется проверка, что `summarize` принимает точные документы `search`, а `save_to_file` — точные `summary` и `sources` предыдущего шага.

Retrieved sources:

- S1, score `0.699242`: `day-19/internal/agent/agent.go` — const SystemPrompt — `structural-5f7fe11f5be896804b84c790`
- S2, score `0.649193`: `day-19/README.md` — Day 19 — композиция MCP-инструментов — `structural-c564e4de307961349698ccad`
- S3, score `0.626187`: `day-19/internal/agent/agent_test.go` — func TestAgentModelSelectedPipelineAndExactTransfer — `structural-03750b2b2e5d6072cb8352ba`
- S4, score `0.624690`: `day-20/internal/knowledge/server.go` — const SummarizeTool — `structural-a1c41d127c3b4acf1b4b57c9`
- S5, score `0.614907`: `day-19/internal/pipeline/summarize.go` — func Summarize — `structural-989f15f2eee2d4b7459f88a9`

Cited sources:

- [S3]: `day-19/internal/agent/agent_test.go` — func TestAgentModelSelectedPipelineAndExactTransfer — `structural-03750b2b2e5d6072cb8352ba`

### q07-artifact-sandbox — Как Artifact MCP безопасно сохраняет отчёты и предотвращает выход пути за разрешённый каталог?

Ожидание: Store принимает только безопасное относительное имя .md, запрещает absolute path, .. и разделители, затем сравнивает вычисленный каталог с OutputDir. Запись идёт через временный файл, fsync и atomic rename с лимитом размера.

**Plain answer** (concepts 0/4, 2133 ms):

Artifact MCP безопасно сохраняет отчёты и предотвращает выход пути за разрешённый каталог, используя механизмы безопасности и контроля доступа. Он настраивает права доступа к каталогам и файлам, чтобы предотвратить несанкционированный доступ. Также Artifact MCP может применять политики безопасности, такие как блокировка несанкционированных операций, для защиты данных.

**RAG answer** (concepts 0/4, retrieval 96 ms, generation 4538 ms):

Artifact MCP безопасно сохраняет отчёты и предотвращает выход пути за разрешённый каталог, используя константу SaveTool, которая указывает, куда сохранять файлы. В контексте [S1], мы видим, что SaveTool определен как "artifact_save_to_file". Это означает, что отчёты сохраняются в определённый каталог, который не должен выходить за пределы разрешённого.

Retrieved sources:

- S1, score `0.612994`: `day-20/internal/artifact/server.go` — const SaveTool — `structural-70bc5348feb26b9eda1d62d0`
- S2, score `0.536295`: `day-19/internal/agent/agent.go` — const SystemPrompt — `structural-5f7fe11f5be896804b84c790`
- S3, score `0.535641`: `day-20/internal/artifact/server.go` — package artifact — `structural-dd22c45ec2e83fc8b71d44f3`
- S4, score `0.521193`: `day-20/internal/agent/agent.go` — const SystemPrompt — `structural-02a0d96a15d2f293c21b27f2`
- S5, score `0.507986`: `day-20/internal/mcpcontract/schema.go` — package mcpcontract — `structural-9f12345845b43a929207fe29`

Cited sources:

- [S1]: `day-20/internal/artifact/server.go` — const SaveTool — `structural-70bc5348feb26b9eda1d62d0`

### q08-session-history — Как web-приложение хранит историю диалога по session cookie и что делает кнопка нового диалога?

Ожидание: Случайный session ID хранится в HttpOnly SameSite cookie, а серверная map содержит ограниченное число последних turn-ов с mutex. Новый диалог удаляет conversation по ID и истекает cookie, не затрагивая MCP-расписания.

**Plain answer** (concepts 1/4, 2058 ms):

Web-приложение хранит историю диалога обычно через сессионные cookies, чтобы сохранять состояние между запросами. Кнопка нового диалога обычно используется для создания нового сессионного сеанса или диалога, что позволяет пользователю начать новый разговор или переписку без необходимости авторизации заново.

**RAG answer** (concepts 1/4, retrieval 126 ms, generation 11123 ms):

Web-приложение хранит историю диалога по session cookie с именем `day18_agent_session`. Количество последних полных ходов диалога составляет 12. Кнопка «Новый диалог» очищает только историю чата, расписания и снимки MCP не удаляются. История чата хранится в памяти `agent-app` и сбрасывается при его перезапуске.

[С1] В приложении используется HttpOnly session cookie для хранения истории диалога. Кнопка «Новый диалог» очищает только историю чата, расписания и снимки MCP не удаляются.

[С2] Тестовый код в [S2] подтверждает, что при повторном запросе с использованием сохраненной session cookie, приложение может восстановить историю диалога.

[С3] Константа `sessionCookieName` в [S3] указывает на имя session cookie, используемого для хранения истории диалога.

[С4] Тестовый код в [S4] показывает, что приложение передает историю диалога в следующий запрос и восстанавливает её при повторном запросе с использованием сохраненной session cookie.

[С5] Метод `get` в [S5] conversationStore подтверждает, что приложение использует session cookie для хранения и восстановления истории диалога.

Retrieved sources:

- S1, score `0.806151`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > История диалога — `structural-30be5c06a819a4ce27c31900`
- S2, score `0.573037`: `day-18/internal/web/sessions_test.go` — func TestConversationStoreReusesCookieAndCanReset — `structural-ac28a3aae1d4485c5e56267b`
- S3, score `0.562393`: `day-18/internal/web/sessions.go` — const sessionCookieName, maxConversationTurns, maxConversationCount — `structural-dcad4874e0b158fd26953bdb`
- S4, score `0.558223`: `day-18/internal/web/server_test.go` — func TestChatSessionPassesHistoryToNextRequestAndRestoresIt — `structural-cadc590fd22ddfc055e29ec0`
- S5, score `0.555727`: `day-19/internal/web/sessions.go` — method (*conversationStore).get — `structural-9b290b620f2f98932eea4caa`

Cited sources:

- [S2]: `day-18/internal/web/sessions_test.go` — func TestConversationStoreReusesCookieAndCanReset — `structural-ac28a3aae1d4485c5e56267b`
- [S3]: `day-18/internal/web/sessions.go` — const sessionCookieName, maxConversationTurns, maxConversationCount — `structural-dcad4874e0b158fd26953bdb`
- [S4]: `day-18/internal/web/server_test.go` — func TestChatSessionPassesHistoryToNextRequestAndRestoresIt — `structural-cadc590fd22ddfc055e29ec0`
- [S5]: `day-19/internal/web/sessions.go` — method (*conversationStore).get — `structural-9b290b620f2f98932eea4caa`

### q09-mcp-handshake — Как минимальный Go MCP-клиент устанавливает соединение и получает список tools с пагинацией?

Ожидание: Клиент создаёт официальный mcp.Client и StreamableClientTransport, вызывает Connect для initialize handshake и читает InitializeResult. Затем повторяет session.ListTools, передавая NextCursor до пустого cursor.

**Plain answer** (concepts 1/3, 5226 ms):

Минимальный Go MCP-клиент устанавливает соединение с сервером MCP и получает список инструментов с пагинацией следующим образом:

1. Устанавливает соединение с сервером MCP через HTTP или HTTPS.
2. Отправляет запрос на получение списка инструментов, используя пагинацию для обработки большого количества инструментов в нескольких запросах.
3. Обрабатывает ответ сервера, который включает список инструментов и метаданные о следующем списке инструментов для получения.
4. Повторяет шаги 2 и 3, пока не будет получено все инструменты.

Этот подход позволяет клиенту эффективно обрабатывать большие объемы данных, не загружая сервер и не тормозя процесс взаимодействия.

**RAG answer** (concepts 1/3, retrieval 739 ms, generation 6889 ms):

Минимальный Go MCP-клиент устанавливает соединение с сервером MCP по адресу [S1] и устанавливает транспортный объект [S2]. Затем он вызывает метод [S2].Connect, передавая контекст [S2] и транспортный объект [S2]. В случае успешного соединения, клиент вызывает метод [S2].ListTools с пустыми параметрами, чтобы получить первый список инструментов. Если следующий курсор [S2] не пуст, клиент продолжает вызывать [S2].ListTools с указанным курсором, добавляя новые инструменты к уже полученным. Этот процесс повторяется до тех пор, пока следующий курсор не станет пустым.

Retrieved sources:

- S1, score `0.816778`: `day-16/README.md` — Day 16 — MCP connection and tools/list — `structural-1e2b0064262053e9b004765a`
- S2, score `0.704705`: `day-16/internal/mcpdemo/client.go` — func ConnectAndListTools — `structural-8b8adfcb6bcdc36d1cc5cb1d`
- S3, score `0.647965`: `day-20/README.md` — Day 20 — Orchestration MCP — `structural-9adc7c97de777669396a5699`
- S4, score `0.634688`: `day-16/internal/mcpdemo/integration_test.go` — func TestConnectAndListTools — `structural-29bb2f2b88c68671a52ea4c1`
- S5, score `0.631195`: `day-16/cmd/client/main.go` — func main — `structural-7aea3ea6336972e8de5c70ca`

Cited sources:

- [S1]: `day-16/README.md` — Day 16 — MCP connection and tools/list — `structural-1e2b0064262053e9b004765a`
- [S2]: `day-16/internal/mcpdemo/client.go` — func ConnectAndListTools — `structural-8b8adfcb6bcdc36d1cc5cb1d`

### q10-wikipedia-search — Как knowledge search обращается к Wikipedia, ограничивает ответ и обрабатывает пустые результаты?

Ожидание: Клиент вызывает MediaWiki action=query с generator=search и extracts|info, валидирует язык/limit, timeout и HTTP/JSON/API errors. Тело ограничено, тексты обрезаются, некорректные документы отбрасываются, а пустой список возвращается как успешный count=0.

**Plain answer** (concepts 1/4, 2981 ms):

Knowledge Search обращается к Wikipedia для получения информации, но ограничивает ответы и обрабатывает пустые результаты с помощью интеллектуальной аналитики и фильтрации, чтобы обеспечить точность и полноту информации, предлагаемой пользователям. Это включает в себя анализ контекста запроса, проверку источников и использование алгоритмов для уточнения и упрощения информации. Если результаты пусты, это может быть связано с тем, что информация на определенные запросы не найдена или запрос не был понят.

**RAG answer** (concepts 1/4, retrieval 106 ms, generation 5928 ms):

Knowledge search обращается к Wikipedia через инструмент SearchTool, который в свою очередь вызывает Wikipedia. Если ответы из Wikipedia ограничены, они обрабатываются и возвращаются в ответе Knowledge search. Если результаты пустые, Knowledge search не обращается к другим источникам, так как ограничения касаются только Wikipedia. [S1]

Retrieved sources:

- S1, score `0.649416`: `day-20/internal/knowledge/server.go` — const SearchTool — `structural-1b2c0caeee3e893f0c54502f`
- S2, score `0.553253`: `day-20/internal/agent/agent.go` — const SystemPrompt — `structural-02a0d96a15d2f293c21b27f2`
- S3, score `0.538517`: `day-19/internal/agent/agent.go` — const SystemPrompt — `structural-5f7fe11f5be896804b84c790`
- S4, score `0.533556`: `day-20/internal/knowledge/server.go` — const SummarizeTool — `structural-a1c41d127c3b4acf1b4b57c9`
- S5, score `0.528419`: `day-20/internal/knowledge/client.go` — package knowledge — `structural-a5087e3cb9a92e34d41e6326`

Cited sources:

- [S1]: `day-20/internal/knowledge/server.go` — const SearchTool — `structural-1b2c0caeee3e893f0c54502f`

## Вывод

На этом наборе RAG повысил среднее substring concept coverage на 0.167. Это фактический результат данного запуска, а не доказательство общего превосходства RAG.
