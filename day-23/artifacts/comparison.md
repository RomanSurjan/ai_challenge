# Day 23 — filter, reranker и query rewrite

Run: `2026-10-04T17:33:22Z`  
Models: chat `qwen2.5:3b`, rewrite `qwen2.5:3b`, embedding `qwen3-embedding:0.6b`  
Index: `../day-21/artifacts/index-structural.json` (corpus `b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451`)  
Dataset: `eval/questions.json`, SHA-256 `013e72b5fe3ce770b526ee47e63ec7d17b07aca3e2bcdd89e9ea25f42c2ddbe7`  
Pipeline: candidate-K 20, final-K 5, threshold 0.45, weights 0.70 / 0.20 / 0.10

## Overall

| Mode | Concept coverage | All concepts | Cited recall | Invalid | Errors | Gen ms | Total ms | Prompt / completion tokens | Context runes |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| baseline | 0.400 | 0.100 | 0.358 | 0 | 0 | 5777.9 | 5858.2 | 1959.8 / 218.8 | 3939.4 |
| filtered | 0.350 | 0.000 | 0.242 | 0 | 0 | 20661.8 | 20715.3 | 3124.9 / 545.1 | 8670.5 |
| rewritten | 0.283 | 0.100 | 0.392 | 0 | 0 | 7088.4 | 8128.5 | 1943.6 / 175.4 | 5079.7 |
| enhanced | 0.183 | 0.000 | 0.200 | 12 | 0 | 17746.7 | 18758.8 | 3075.0 / 510.1 | 8425.2 |

## Retrieval before and after

| Mode | Candidate recall | Final recall | Final precision | Hit@1 | MRR | Before / after | Threshold rejected | Empty | Rank Δ | Rewrite ms | Retrieval ms | Rerank ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| baseline | 0.442 | 0.442 | 0.340 | 0.500 | 0.700 | 5.0 / 5.0 | 0.000 | 0.000 | +0.00 | 0.0 | 79.3 | 0.0 |
| filtered | 0.758 | 0.458 | 0.370 | 0.700 | 0.820 | 20.0 / 4.7 | 0.165 | 0.000 | +0.60 | 0.0 | 51.3 | 0.8 |
| rewritten | 0.442 | 0.442 | 0.320 | 0.500 | 0.667 | 5.0 / 5.0 | 0.000 | 0.000 | +0.00 | 952.7 | 86.3 | 0.1 |
| enhanced | 0.750 | 0.425 | 0.360 | 0.400 | 0.567 | 20.0 / 5.0 | 0.075 | 0.000 | +0.00 | 952.7 | 57.8 | 0.5 |

## Per question

| ID | Baseline concepts | Filtered | Rewritten | Enhanced | Baseline / enhanced final recall | Baseline / enhanced MRR |
|---|---:|---:|---:|---:|---:|---:|
| q01-multi-mcp-routing | 0.333 | 0.000 | 0.333 | 0.000 | 0.500 / 0.500 | 0.500 / 1.000 |
| q02-monitor-claim | 0.667 | 0.667 | 1.000 | 0.667 | 0.333 / 0.333 | 1.000 / 1.000 |
| q03-decimal-currency | 0.333 | 0.333 | 0.000 | 0.000 | 0.500 / 0.500 | 0.500 / 0.500 |
| q04-github-http-safety | 0.250 | 0.750 | 0.000 | 0.500 | 0.333 / 0.333 | 1.000 / 1.000 |
| q05-agent-tool-loop | 1.000 | 0.333 | 0.667 | 0.667 | 0.250 / 0.000 | 0.500 / 0.000 |
| q06-pipeline-integrity | 0.500 | 0.000 | 0.500 | 0.000 | 0.500 / 0.250 | 0.500 / 0.500 |
| q07-artifact-sandbox | 0.000 | 0.000 | 0.000 | 0.000 | 0.333 / 0.667 | 1.000 / 0.333 |
| q08-session-history | 0.250 | 0.250 | 0.000 | 0.000 | 0.667 / 0.667 | 1.000 / 0.333 |
| q09-mcp-handshake | 0.667 | 0.667 | 0.333 | 0.000 | 1.000 / 1.000 | 1.000 / 1.000 |
| q10-wikipedia-search | 0.000 | 0.500 | 0.000 | 0.000 | 0.000 / 0.000 | 0.000 / 0.000 |

### q01-multi-mcp-routing — Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?

Original: Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?

Rewritten: `Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента` (fallback: false)

Candidates before filtering:

- #1 cosine `0.7169`, lexical `0.167`, metadata `0.000`, rerank `0.6342`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента
- #2 cosine `0.6352`, lexical `0.333`, metadata `0.083`, rerank `0.6473`: `day-20/README.md` — Day 20 — Orchestration MCP
- #3 cosine `0.6264`, lexical `0.500`, metadata `0.083`, rerank `0.6776`: `day-18/README.md` — Day 18 — Unified persistent MCP agent
- #4 cosine `0.5799`, lexical `0.167`, metadata `0.083`, rerank `0.5946`: `day-16/README.md` — Day 16 — MCP connection and tools/list
- #5 cosine `0.5734`, lexical `0.250`, metadata `0.083`, rerank `0.6090`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Архитектура
- #6 cosine `0.5708`, lexical `0.250`, metadata `0.083`, rerank `0.6081`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Результат
- #7 cosine `0.5670`, lexical `0.167`, metadata `0.000`, rerank `0.5818`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 2. Проверка общего набора инструментов
- #8 cosine `0.5576`, lexical `0.167`, metadata `0.083`, rerank `0.5868`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Архитектура
- #9 cosine `0.5559`, lexical `0.333`, metadata `0.000`, rerank `0.6112`: `day-20/internal/agent/agent.go` — const SystemPrompt
- #10 cosine `0.5483`, lexical `0.250`, metadata `0.083`, rerank `0.6002`: `day-20/README.md` — Day 20 — Orchestration MCP > Сервисы
- #11 cosine `0.5443`, lexical `0.083`, metadata `0.083`, rerank `0.5655`: `day-17/README.md` — Day 17 — GitHub MCP Agent
- #12 cosine `0.5362`, lexical `0.167`, metadata `0.167`, rerank `0.5877`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Что умеет агент
- #13 cosine `0.5290`, lexical `0.333`, metadata `0.000`, rerank `0.6018`: `day-19/internal/agent/agent.go` — const SystemPrompt
- #14 cosine `0.5289`, lexical `0.000`, metadata `0.000`, rerank `0.5351`: `day-20/internal/mcpcontract/schema.go` — package mcpcontract
- #15 cosine `0.5234`, lexical `0.500`, metadata `0.000`, rerank `0.6332`: `day-20/internal/web/page.go` — const chatPageHTML
- #16 cosine `0.5119`, lexical `0.083`, metadata `0.083`, rerank `0.5542`: `day-16/README.md` — Day 16 — MCP connection and tools/list > Развёртывание MCP-сервера
- #17 cosine `0.5080`, lexical `0.000`, metadata `0.000`, rerank `0.5278`: `day-18/internal/mcpmulti/connector.go` — package mcpmulti
- #18 cosine `0.5050`, lexical `0.167`, metadata `0.083`, rerank `0.5684`: `day-19/README.md` — Day 19 — композиция MCP-инструментов
- #19 cosine `0.4913`, lexical `0.000`, metadata `0.000`, rerank `0.5219`: `day-19/internal/agent/agent.go` — package agent
- #20 cosine `0.4870`, lexical `0.250`, metadata `0.000`, rerank `0.5704`: `day-17/internal/agent/agent_test.go` — func TestAgentToolCallLoopReturnsModelAnswerBasedOnMCPResult

Rejected / omitted:

- #5 `day-17/README.md`: outside final-k=5 after reranking
- #6 `day-17/README.md`: outside final-k=5 after reranking
- #13 `day-19/internal/agent/agent.go`: outside final-k=5 after reranking
- #10 `day-20/README.md`: outside final-k=5 after reranking
- #4 `day-16/README.md`: outside final-k=5 after reranking
- #12 `day-18/README.md`: outside final-k=5 after reranking
- #8 `day-18/README.md`: outside final-k=5 after reranking
- #7 `day-18/DEMO.md`: outside final-k=5 after reranking
- #20 `day-17/internal/agent/agent_test.go`: outside final-k=5 after reranking
- #18 `day-19/README.md`: outside final-k=5 after reranking
- #11 `day-17/README.md`: outside final-k=5 after reranking
- #16 `day-16/README.md`: outside final-k=5 after reranking
- #14 `day-20/internal/mcpcontract/schema.go`: outside final-k=5 after reranking
- #17 `day-18/internal/mcpmulti/connector.go`: outside final-k=5 after reranking
- #19 `day-19/internal/agent/agent.go`: outside final-k=5 after reranking

Final order:

- S1 cosine `0.6264`: `day-18/README.md` — Day 18 — Unified persistent MCP agent
- S2 cosine `0.6352`: `day-20/README.md` — Day 20 — Orchestration MCP
- S3 cosine `0.7169`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента
- S4 cosine `0.5234`: `day-20/internal/web/page.go` — const chatPageHTML
- S5 cosine `0.5559`: `day-20/internal/agent/agent.go` — const SystemPrompt

Baseline answer (concepts 0.333, citations [S1]=day-18/DEMO.md, [S2]=day-20/README.md, [S3]=day-18/README.md):

Единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента с использованием информации из [S1] и [S2]. Агент подключается к двум независимым MCP-серверам, видит оба набора инструментов и направляет вызовы в правильный сервер. Он также объединяет каталоги инструментов из нескольких MCP-серверов и маршрутизирует каждый вызов обратно в MCP-владельца, как указано в [S3].

Enhanced answer (concepts 0.000, citations [S1]=day-18/README.md):

Констант SystemPrompt был успешно переписан в соответствии с предоставленным шаблоном. Ниже представлен исправленный текст с учетом всех указаний:

```go
const SystemPrompt = `Ты — агент-оркестратор трёх независимых MCP-серверов. Всегда самостоятельно решай, какие инструменты нужны и в каком порядке их вызывать, через tool_choice=auto. Для простого запроса используй только нужные инструменты: Wikipedia — knowledge_search и при необходимости knowledge_summarize; GitHub — github_get_repository или github_get_latest_release; файлы — artifact_list_files или artifact_read_file. Для полного исследования собери knowledge summary, repository и latest release; независимые Knowledge и GitHub операции можно выполнять в выбранном тобой порядке. Соблюдай только зависимости данных: knowledge_summarize получает точный результат knowledge_search, artifact_build_report получает точные результаты knowledge и GitHub, artifact_save_to_file получает точный результат artifact_build_report. После сохранения дай финальный ответ с путём и SHA-256. Можно повторять независимые поисковые вызовы, если это полезно. Не добавляй полей вне JSON Schema и не изменяй structured results при передаче.`

[S1]
```

Этот текст соответствует шаблону и содержит все необходимые инструкции для агента-оркестратора. Ниже приведена ссылка на [S1] для дополнительной информации.

Metrics: final recall 0.500 → 0.500; precision 0.400 → 0.400; MRR 0.500 → 1.000.

### q04-github-http-safety — Какие ограничения и проверки применяются при запросе метаданных публичного репозитория GitHub?

Original: Какие ограничения и проверки применяются при запросе метаданных публичного репозитория GitHub?

Rewritten: `ограничения и проверки при запросе метаданных публичного репозитория GitHub` (fallback: false)

Candidates before filtering:

- #1 cosine `0.7563`, lexical `0.143`, metadata `0.143`, rerank `0.6576`: `day-17/README.md` — Day 17 — GitHub MCP Agent > GitHub REST API
- #2 cosine `0.7238`, lexical `0.286`, metadata `0.143`, rerank `0.6747`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Пример
- #3 cosine `0.7039`, lexical `0.286`, metadata `0.143`, rerank `0.6678`: `day-17/README.md` — Day 17 — GitHub MCP Agent > MCP-инструмент
- #4 cosine `0.6946`, lexical `0.143`, metadata `0.000`, rerank `0.6217`: `day-20/internal/githubapi/server.go` — const RepositoryTool
- #5 cosine `0.6800`, lexical `0.143`, metadata `0.000`, rerank `0.6166`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 6. Сводки по обоим доменам
- #6 cosine `0.6624`, lexical `0.000`, metadata `0.000`, rerank `0.5818`: `day-17/internal/mcpgithub/server.go` — type RepositoryOutput
- #7 cosine `0.6343`, lexical `0.143`, metadata `0.000`, rerank `0.6006`: `day-20/README.md` — Day 20 — Orchestration MCP > Сервисы
- #8 cosine `0.6340`, lexical `0.143`, metadata `0.000`, rerank `0.6005`: `day-20/internal/agent/agent.go` — const SystemPrompt
- #9 cosine `0.6258`, lexical `0.143`, metadata `0.143`, rerank `0.6119`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Первичные источники
- #10 cosine `0.6137`, lexical `0.000`, metadata `0.000`, rerank `0.5648`: `day-18/internal/githubapi/client.go` — type repositoryResponse
- #11 cosine `0.6137`, lexical `0.000`, metadata `0.000`, rerank `0.5648`: `day-17/internal/githubapi/client.go` — type repositoryResponse
- #12 cosine `0.5852`, lexical `0.000`, metadata `0.000`, rerank `0.5548`: `day-17/internal/githubapi/client.go` — type Repository
- #13 cosine `0.5852`, lexical `0.000`, metadata `0.000`, rerank `0.5548`: `day-18/internal/githubapi/client.go` — type Repository
- #14 cosine `0.5836`, lexical `0.286`, metadata `0.143`, rerank `0.6257`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 3. GitHub-монитор
- #15 cosine `0.5821`, lexical `0.286`, metadata `0.286`, rerank `0.6395`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Ограничения
- #16 cosine `0.5805`, lexical `0.286`, metadata `0.143`, rerank `0.6246`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Ограничения
- #17 cosine `0.5749`, lexical `0.143`, metadata `0.000`, rerank `0.5798`: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Что умеет агент
- #18 cosine `0.5655`, lexical `0.143`, metadata `0.000`, rerank `0.5765`: `day-17/internal/githubapi/client_test.go` — const repositoryJSON
- #19 cosine `0.5655`, lexical `0.143`, metadata `0.000`, rerank `0.5765`: `day-18/internal/githubapi/client_test.go` — const repositoryJSON
- #20 cosine `0.5648`, lexical `0.000`, metadata `0.000`, rerank `0.5477`: `day-20/internal/domain/types.go` — type RepositoryOutput

Rejected / omitted:

- #16 `day-18/README.md`: outside final-k=5 after reranking
- #4 `day-20/internal/githubapi/server.go`: outside final-k=5 after reranking
- #5 `day-18/DEMO.md`: outside final-k=5 after reranking
- #9 `day-17/README.md`: outside final-k=5 after reranking
- #7 `day-20/README.md`: outside final-k=5 after reranking
- #8 `day-20/internal/agent/agent.go`: outside final-k=5 after reranking
- #6 `day-17/internal/mcpgithub/server.go`: outside final-k=5 after reranking
- #17 `day-18/README.md`: outside final-k=5 after reranking
- #18 `day-17/internal/githubapi/client_test.go`: outside final-k=5 after reranking
- #19 `day-18/internal/githubapi/client_test.go`: outside final-k=5 after reranking
- #10 `day-18/internal/githubapi/client.go`: outside final-k=5 after reranking
- #11 `day-17/internal/githubapi/client.go`: outside final-k=5 after reranking
- #12 `day-17/internal/githubapi/client.go`: outside final-k=5 after reranking
- #13 `day-18/internal/githubapi/client.go`: outside final-k=5 after reranking
- #20 `day-20/internal/domain/types.go`: outside final-k=5 after reranking

Final order:

- S1 cosine `0.7238`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Пример
- S2 cosine `0.7039`: `day-17/README.md` — Day 17 — GitHub MCP Agent > MCP-инструмент
- S3 cosine `0.7563`: `day-17/README.md` — Day 17 — GitHub MCP Agent > GitHub REST API
- S4 cosine `0.5821`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Ограничения
- S5 cosine `0.5836`: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 3. GitHub-монитор

Baseline answer (concepts 0.250, citations [S4]=day-17/README.md, [S1]=day-17/README.md):

Ограничения и проверки, применяемые при запросе метаданных публичного репозитория GitHub, включают:

- Без токена основной лимит GitHub — 60 запросов в час на IP; обычный пользовательский токен обычно даёт 5000 запросов в час. [S4]
- Возможны secondary rate limits. [S4]
- Stars, forks, issues и timestamps изменяются со временем. [S4]
- История чата не сохраняется между процессами. [S4]
- MCP endpoint не имеет публичной аутентификации и поэтому доступен только с loopback/внутренней Docker-сети или через SSH-туннель. [S4]

Проверки включают:

- Проверка HTTP-кода. [S1]
- Проверка JSON. [S1]
- Проверка обязательных полей. [S1]
- Проверка rate-limit header. [S1]
- Нормализация nullable description, language, license, topics и pushed time. [S1]

Enhanced answer (concepts 0.500, citations [S5]=day-18/DEMO.md, [S4]=day-17/README.md, [S2]=day-17/README.md):

Ограничения и проверки, применяемые при запросе метаданных публичного репозитория GitHub, включают:

- Без токена основной лимит GitHub — 60 запросов в час на IP [S5].
- Возможны secondary rate limits [S4].
- Проверка HTTP-кода, JSON, обязательных полей и rate-limit header [S2].
- Обработка ошибок 404 и rate limit [S2].
- Обработка `403`/`429`, `X-RateLimit-Reset` и `Retry-After` [S2].
- Проверка превышения лимита ответа 2 MiB [S2].
- Раздельные ошибки 404 и rate limit [S2].
- Проверка pull requests как разновидности issues [S2].
- Проверка приватных репозиториев даже при наличии токена [S2].
- Проверка лимита запросов на IP [S2].
- Проверка приватных репозиториев даже при наличии токена [S2].
- Проверка приватных репозиториев даже при наличии токена [S2].
- Проверка приватных репозиториев даже при наличии токена [S2].

Metrics: final recall 0.333 → 0.333; precision 0.800 → 0.800; MRR 1.000 → 1.000.

### q09-mcp-handshake — Как минимальный Go MCP-клиент устанавливает соединение и получает список tools с пагинацией?

Original: Как минимальный Go MCP-клиент устанавливает соединение и получает список tools с пагинацией?

Rewritten: `Go MCP client minimal setup connection tools pagination` (fallback: false)

Candidates before filtering:

- #1 cosine `0.6998`, lexical `0.571`, metadata `0.429`, rerank `0.7521`: `day-16/README.md` — Day 16 — MCP connection and tools/list
- #2 cosine `0.6920`, lexical `0.143`, metadata `0.143`, rerank `0.6351`: `day-16/internal/mcpdemo/client.go` — package mcpdemo
- #3 cosine `0.6912`, lexical `0.000`, metadata `0.000`, rerank `0.5919`: `day-20/internal/mcpcontract/schema.go` — package mcpcontract
- #4 cosine `0.6840`, lexical `0.143`, metadata `0.000`, rerank `0.6180`: `day-16/internal/mcpdemo/server.go` — package mcpdemo
- #5 cosine `0.6704`, lexical `0.143`, metadata `0.143`, rerank `0.6275`: `day-20/internal/mcpclient/client.go` — package mcpclient
- #6 cosine `0.6690`, lexical `0.143`, metadata `0.000`, rerank `0.6127`: `day-20/internal/agent/agent.go` — const SystemPrompt
- #7 cosine `0.6681`, lexical `0.143`, metadata `0.143`, rerank `0.6267`: `day-19/internal/mcpclient/client.go` — package mcpclient
- #8 cosine `0.6547`, lexical `0.286`, metadata `0.000`, rerank `0.6363`: `day-20/internal/web/page.go` — const chatPageHTML
- #9 cosine `0.6437`, lexical `0.429`, metadata `0.429`, rerank `0.7039`: `day-16/README.md` — Day 16 — MCP connection and tools/list > Результат задания
- #10 cosine `0.6432`, lexical `0.286`, metadata `0.000`, rerank `0.6323`: `day-19/internal/agent/agent.go` — const SystemPrompt
- #11 cosine `0.6335`, lexical `0.143`, metadata `0.143`, rerank `0.6146`: `day-18/internal/mcpcurrency/client.go` — package mcpcurrency
- #12 cosine `0.6189`, lexical `0.143`, metadata `0.000`, rerank `0.5952`: `day-18/internal/mcpcurrency/server.go` — package mcpcurrency
- #13 cosine `0.6081`, lexical `0.143`, metadata `0.143`, rerank `0.6057`: `day-17/internal/mcpgithub/client.go` — package mcpgithub
- #14 cosine `0.6081`, lexical `0.143`, metadata `0.143`, rerank `0.6057`: `day-18/internal/mcpgithub/client.go` — package mcpgithub
- #15 cosine `0.6079`, lexical `0.143`, metadata `0.000`, rerank `0.5913`: `day-19/internal/pipeline/contract_test.go` — package pipeline
- #16 cosine `0.6058`, lexical `0.429`, metadata `0.143`, rerank `0.6620`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Архитектура
- #17 cosine `0.6037`, lexical `0.000`, metadata `0.000`, rerank `0.5613`: `day-18/internal/mcpmulti/connector.go` — package mcpmulti
- #18 cosine `0.6013`, lexical `0.143`, metadata `0.000`, rerank `0.5890`: `day-18/internal/mcpcurrency/server_test.go` — package mcpcurrency
- #19 cosine `0.5970`, lexical `0.143`, metadata `0.000`, rerank `0.5875`: `day-19/internal/pipeline/server.go` — package pipeline
- #20 cosine `0.5939`, lexical `0.429`, metadata `0.143`, rerank `0.6579`: `day-16/internal/mcpdemo/client.go` — func ConnectAndListTools

Rejected / omitted:

- #2 `day-16/internal/mcpdemo/client.go`: outside final-k=5 after reranking
- #10 `day-19/internal/agent/agent.go`: outside final-k=5 after reranking
- #5 `day-20/internal/mcpclient/client.go`: outside final-k=5 after reranking
- #7 `day-19/internal/mcpclient/client.go`: outside final-k=5 after reranking
- #4 `day-16/internal/mcpdemo/server.go`: outside final-k=5 after reranking
- #11 `day-18/internal/mcpcurrency/client.go`: outside final-k=5 after reranking
- #6 `day-20/internal/agent/agent.go`: outside final-k=5 after reranking
- #13 `day-17/internal/mcpgithub/client.go`: outside final-k=5 after reranking
- #14 `day-18/internal/mcpgithub/client.go`: outside final-k=5 after reranking
- #12 `day-18/internal/mcpcurrency/server.go`: outside final-k=5 after reranking
- #3 `day-20/internal/mcpcontract/schema.go`: outside final-k=5 after reranking
- #15 `day-19/internal/pipeline/contract_test.go`: outside final-k=5 after reranking
- #18 `day-18/internal/mcpcurrency/server_test.go`: outside final-k=5 after reranking
- #19 `day-19/internal/pipeline/server.go`: outside final-k=5 after reranking
- #17 `day-18/internal/mcpmulti/connector.go`: outside final-k=5 after reranking

Final order:

- S1 cosine `0.6998`: `day-16/README.md` — Day 16 — MCP connection and tools/list
- S2 cosine `0.6437`: `day-16/README.md` — Day 16 — MCP connection and tools/list > Результат задания
- S3 cosine `0.6058`: `day-17/README.md` — Day 17 — GitHub MCP Agent > Архитектура
- S4 cosine `0.5939`: `day-16/internal/mcpdemo/client.go` — func ConnectAndListTools
- S5 cosine `0.6547`: `day-20/internal/web/page.go` — const chatPageHTML

Baseline answer (concepts 0.667, citations [S1]=day-16/README.md, [S2]=day-16/internal/mcpdemo/client.go):

Минимальный Go MCP-клиент устанавливает соединение с сервером MCP по адресу [S1] и устанавливает транспортный объект [S2]. Затем он вызывает метод [S2].Connect, передавая контекст [S2] и транспортный объект [S2]. В случае успешного соединения, клиент вызывает метод [S2].InitializeResult для получения информации о версии протокола и имени сервера. Затем клиент использует метод [S2].ListTools для получения списка инструментов. Для получения следующей страницы инструментов, клиент передает параметры [S2] с новым значением параметра [S2].NextCursor. Этот процесс повторяется до тех пор, пока следующий курсор не станет пустым.

Enhanced answer (concepts 0.000, citations none):

Конечно, давайте перепишем ответ с учетом точных ссылок и уточнений. Вот обновленный текст:

---

<Html>
<Head>
<Title>DeepSeek выбирает инструменты</Title>
</Head>
<body>
<main>
<section class="chat">
<div class="card">
<div class="head"><h2>Технический запрос</h2><small>DeepSeek выбирает инструменты</small></div>
<div class="examples">
<button class="example" data-prompt="Исследуй Model Context Protocol по Wikipedia, подготовь краткую сводку, проверь репозиторий modelcontextprotocol/go-sdk и его последний релиз, собери единый технический отчёт и сохрани его в файл mcp-go-sdk-report.md.">Полный flow</button>
<button class="example" data-prompt="Покажи сведения о репозитории modelcontextprotocol/go-sdk.">GitHub</button>
<button class="example" data-prompt="Найди по Wikipedia информацию о Model Context Protocol и кратко изложи её.">Wikipedia</button>
<button class="example" data-prompt="Покажи список сохранённых Markdown-файлов.">Файлы</button>
</div>
<textarea id="message">Исследуй Model Context Protocol по Wikipedia, подготовь краткую сводку, проверь репозитории modelcontextprotocol/go-sdk и его последний релиз, собери единый технический отчёт и сохрани его в файл mcp-go-sdk-report.md.</textarea>
<button id="send" class="send">Запустить агента</button>
<div class="head" style="margin-top:19px">
<h2>История диалога</h2>
<button id="reset" class="reset">Новый диалог</button>
</div>
<div id="dialog" class="dialog">
<div class="empty">История появится здесь.</div>
</div>
</div>
<div class="card">
<div class="head">
<h2>MCP trace</h2>
<small id="sequence">Ожидается запуск</small>
</div>
<div id="trace" class="trace">
<div class="empty">

Metrics: final recall 1.000 → 1.000; precision 0.400 → 0.600; MRR 1.000 → 1.000.

## Honest conclusion

Enhanced reduced concept coverage by **0.217** on this run; filtering/rewrite are not an automatic win. Final expected-source precision changed 0.340 → 0.360, recall 0.442 → 0.425, and average total latency 5858.2 → 18758.8 ms. These deterministic metrics are useful for comparison but do not prove semantic correctness.
