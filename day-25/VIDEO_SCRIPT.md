# День 25 — сценарий видео на 6–7 минут

## 0:00–0:30 — от RAG к долгому диалогу

Показать `day-25/README.md` и схему.

Сказать: Day 21 построил structural index на 1025 chunks, Day 22 добавил первый
RAG-запрос, Day 23 — top-20, threshold 0.45 и reranking, Day 24 — relevance gate
0.55, structured answer, exact quotes и entailment judge. Day 25 не строит
корпус и embeddings заново: он читает тот же индекс с corpus ID
`b7d4f836…0cc451` и добавляет persistent multi-turn слой.

## 0:30–1:05 — почему history недостаточно

Показать режимы CLI:

```bash
go run ./cmd/rag-chat chat --session artifact-audit --mode task-memory
```

Объяснить: stateless не видит старые turn; history видит только bounded окно;
task-memory отдельно хранит цель, ограничения, термины, решения, уточнения и
open questions. Полная история остаётся на диске, но prompt получает максимум 8
последних turns и 6000 Unicode-символов. Старое уточнение после выхода из окна
остаётся доступным через task state.

## 1:05–1:45 — durable session store и turn transaction

Открыть `internal/chatstore/store.go` и `internal/chat/model.go`.

Показать safe session ID, version и два atomic commits. JSON пишется во
временный файл с `0600`, затем `fsync`, rename и `fsync` каталога. Absolute
path, `..` и separators отклоняются. Stale `expectedVersion` даёт conflict.
Mutex не удерживается во время Ollama calls.

Сказать: первый commit сохраняет user message; второй — assistant result, state
и trace целиком. Поэтому model error не теряет ввод, а version conflict не
перезаписывает более новую session.

## 1:45–2:35 — task memory и provenance

Открыть `internal/memory/memory.go`.

Показать operation contract: `set_goal`, `add`, `supersede`,
`resolve_open_question`, `noop`. Модель не возвращает весь новый state.
Динамическая JSON Schema ограничивает current turn ID, kind, operation, quote и
active supersede IDs, но результат всё равно повторно проверяет
`MemoryValidator`.

Каждый item имеет стабильный `M…` ID, `source_turn_id` и `user_quote`. Quote
должна быть exact substring user message. Assistant answer, RAG chunk, stale ID,
duplicate или cross-kind supersede запрещены. После второй ошибки старый state
остаётся неизменным, а обе ошибки сохраняются.

Показать `artifacts/memory-timeline.md`: Artifact turn 9 архивировал
`Mcb1f0dea8baf`, а новый `M4843fc58324a` содержит `supersedes` и exact quote
«Больше не учитывай ограничение „только Go-реализацию“».

## 2:35–3:10 — contextual resolver

Открыть `internal/queryresolver/resolver.go`.

Показать реальный follow-up «А что происходит после перезапуска?». Stateless
оставил исходный вопрос и safely refused. Task-memory превратила его в
`restart persistence scheduler Go`; resolver fallback — false. Validator
принимает только существующие user-turn и active memory IDs. Assistant turns не
могут быть источником session facts. Malformed/error даёт original-message
fallback, а original text всегда отдельно доступен generator.

## 3:10–3:55 — S и U sources, gate и exact quote

Открыть `internal/evidence/evidence.go`.

Corpus source имеет ID S1, source/section/chunk ID, cosine и rerank. User-memory
source имеет U1, `conversation:<session>`, user turn, `user-turn-U…` и exact
user quote. Corpus claim обязан иметь S, memory claim — U, mixed claim — оба.
Предыдущий assistant text никогда не source.

Memory не ослабляет gate: empty final context или top relevance ниже 0.55
означает «Не знаю», clarification и ноль answer model calls. После gate answer
получает одну repair-попытку. Quote должна быть непрерывной подстрокой именно
связанного final chunk/user turn длиной 20–160 Unicode-символов.

## 3:55–4:45 — демонстрация чата, reload и supersede

Выполнить три turns:

```bash
go run ./cmd/rag-chat ask --session demo --mode task-memory \
  --message "Наша цель — проверить безопасность Artifact MCP."

go run ./cmd/rag-chat ask --session demo --mode task-memory \
  --message "Ограничение: учитывай только Go-реализацию."

go run ./cmd/rag-chat ask --session demo --mode task-memory \
  --message "А как он защищается от path traversal?"
```

Показать U-source и exact quote на первых turns. Затем имитировать restart новой
командой:

```bash
go run ./cmd/rag-chat show-memory --session demo
```

Показать сохранённые goal/constraint. Затем:

```bash
go run ./cmd/rag-chat ask --session demo --mode task-memory \
  --message "Больше не учитывай ограничение «только Go-реализацию»."
```

Показать active replacement и старую версию в `superseded_history`.

## 4:45–5:20 — corpus success и безопасный отказ

Показать реальный успешный strict turn:

```bash
go run ./cmd/rag-chat ask --session corpus-demo --mode stateless \
  --message "Как планировщик мониторинга атомарно захватывает просроченный запуск и не допускает дубликаты после перезапуска?"
```

Фактический ответ: «Первый запуск назначается сразу и незавершённые
`running`-запуски восстанавливаются после падения». Source — S1,
`day-18/README.md`, section «Надёжность»; две exact quotes: «Первый запуск
назначается сразу;» и «незавершённые `running`-запуски восстанавливаются после
падения;».

Затем OOD:

```bash
go run ./cmd/rag-chat ask --session ood-demo --mode stateless \
  --message "Как приготовить тесто для неаполитанской пиццы с холодной ферментацией 72 часа?"
```

Результат: `insufficient_context`, «Не знаю», clarification; answer model не
вызывается.

## 5:20–6:15 — два длинных сценария × три режима

Показать:

```bash
go run ./cmd/rag-chat eval-scenarios
```

Dataset: 11 + 12 user messages, SHA
`675eeebb79dbc1e323bf538410090ef71bf4cd28d97647be173d0ed77949e8c8`.

| Mode | Goal | Constraints | Follow-up | Answered / refused / memory | Final recall | MRR | Avg total ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| stateless | 0.000 | 0.000 | 0.077 | 5 / 18 / 0 | 0.000 | 0.000 | 1965.8 |
| history | 0.000 | 0.000 | 0.154 | 13 / 10 / 0 | **0.625** | **0.313** | 12531.3 |
| task-memory | **1.000** | **1.000** | **0.231** | 5 / 6 / 12 | 0.500 | 0.229 | 8085.9 |

Task-memory: term retention 1.000, supersede 1.000, resolver fallback 0,
provenance user-turn/exact user quote 1.000. Все режимы: answered-with-source,
metadata completeness, valid ID, answered-with-quote, claims-with-evidence и
exact substring quote — 1.000; assistant cited as fact — 0.

Entailment: stateless 4 supported / 1 unverifiable; history 1 / 12;
task-memory 4 / 1. Fully-supported rate: 0.800 / 0.077 / 0.941, но последнее
число включает 12 deterministic memory-only recaps, поэтому это не доказательство
лучшего corpus answer quality.

## 6:15–6:45 — cost, failure и честный вывод

Показать latency/token table в README. Task-memory потратила 8031/1626 memory
prompt/completion tokens и 7469/646 resolver tokens. History оказалась самой
медленной: в среднем 12.53 s из-за более длинных принятых generations;
task-memory — 8.09 s, stateless — 1.97 s.

Показать failure Artifact turn 4: resolver правильно восстановил query, gate
прошёл в smoke, но маленькая модель дважды нарушила claim contract; система
вернула `output_validation_failed`. Это безопасно, но снижает полноту.

Финальный вывод: task-memory уверенно выиграла continuity/provenance, history —
retrieval recall и число содержательных ответов, stateless — latency и
corpus-only fully-supported rate. Общего победителя нет. Exact quote доказывает
происхождение, но не полноту; следующий шаг — более сильная structured-output
модель, hybrid retrieval, context compression и независимый NLI judge.
