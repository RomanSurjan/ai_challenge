# Day 24 — источники, точные цитаты и безопасный отказ

Run: `2026-10-04T17:42:18Z`  
Models: answer `qwen2.5:3b`, judge `qwen2.5:3b`, embedding `qwen3-embedding:0.6b`  
Index: `../day-21/artifacts/index-structural.json` (corpus `b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451`)  
Dataset: `eval/questions.json`, SHA-256 `013e72b5fe3ce770b526ee47e63ec7d17b07aca3e2bcdd89e9ea25f42c2ddbe7`  
Pipeline: candidate-K 20, final-K 5, chunk threshold 0.45, answer threshold 0.55, weights 0.70 / 0.20 / 0.10  
Quotes: 20–160 Unicode characters

## Answer quality and retrieval

| Mode | Concepts | All concepts | Answered | Abstained | False refusal | Candidate recall | Final recall | Precision | Hit@1 | MRR | Top-1 cosine | Context |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| day23 | 0.333 | 0.100 | 10 | 0 | 0.000 | 0.758 | 0.458 | 0.370 | 0.700 | 0.820 | 0.673 | 4.70 |
| grounded | 0.000 | 0.000 | 5 | 5 | 0.500 | 0.758 | 0.458 | 0.370 | 0.700 | 0.820 | 0.673 | 4.70 |
| strict | 0.033 | 0.000 | 5 | 5 | 0.500 | 0.758 | 0.458 | 0.370 | 0.700 | 0.820 | 0.673 | 4.70 |

## Source, quote, and claim validation

| Mode | Answer+source | Metadata complete | Valid source ID | Cited recall | Invalid IDs | Answer+quote | Claims+quote | Exact quotes | Invalid / wrong | Claims+source | Claims+evidence | Fully supported |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| day23 | 0.600 | 1.000 | 1.000 | 0.192 | 0 | 0.000 | 0.000 | 0.000 | 0 / 0 | 0.000 | 0.000 | 0.000 |
| grounded | 1.000 | 1.000 | 1.000 | 0.167 | 0 | 1.000 | 1.000 | 1.000 | 0 / 0 | 1.000 | 1.000 | 0.200 |
| strict | 1.000 | 1.000 | 1.000 | 0.167 | 0 | 1.000 | 1.000 | 1.000 | 0 / 0 | 1.000 | 1.000 | 0.200 |

## Entailment proxy

| Mode | Supported | Partial | Unsupported | Unverifiable |
|---|---:|---:|---:|---:|
| day23 | 0 | 0 | 0 | 0 |
| grounded | 1 | 0 | 0 | 4 |
| strict | 1 | 0 | 0 | 4 |

The local LLM judge is an automatic proxy: it is a small model evaluating answers from the same or a closely related model, so its verdict is not final proof and should be complemented with human review.

## Latency and tokens

| Mode | Retrieval | Rerank | Generation | Validation | Judge | Total ms | Generator prompt/completion | Judge prompt/completion |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| day23 | 87.1 | 0.4 | 16438.6 | 0.0 | 0.0 | 16526.1 | 3581.0 / 547.3 | 0.0 / 0.0 |
| grounded | 87.1 | 0.4 | 14990.9 | 2.5 | 338.4 | 15425.1 | 3606.0 / 368.1 | 113.5 / 7.1 |
| strict | 87.1 | 0.4 | 11035.7 | 0.0 | 188.6 | 11316.5 | 3606.8 / 371.4 | 114.4 / 7.1 |

## Results for all 10 questions

| ID | day23 concepts/status | grounded concepts/status | strict concepts/status | strict reason | strict supported/claims |
|---|---|---|---|---|---:|
| q01-multi-mcp-routing | 0.000 / answered | 0.000 / insufficient_context | 0.000 / insufficient_context | output_validation_failed | 0/0 |
| q02-monitor-claim | 0.667 / answered | 0.000 / answered | 0.000 / answered |  | 1/1 |
| q03-decimal-currency | 0.000 / answered | 0.000 / answered | 0.000 / answered |  | 0/1 |
| q04-github-http-safety | 0.250 / answered | 0.000 / insufficient_context | 0.000 / insufficient_context | output_validation_failed | 0/0 |
| q05-agent-tool-loop | 0.667 / answered | 0.000 / insufficient_context | 0.000 / insufficient_context | output_validation_failed | 0/0 |
| q06-pipeline-integrity | 0.000 / answered | 0.000 / insufficient_context | 0.000 / insufficient_context | output_validation_failed | 0/0 |
| q07-artifact-sandbox | 0.250 / answered | 0.000 / answered | 0.000 / answered |  | 0/1 |
| q08-session-history | 0.250 / answered | 0.000 / answered | 0.000 / answered |  | 0/1 |
| q09-mcp-handshake | 1.000 / answered | 0.000 / answered | 0.333 / answered |  | 0/1 |
| q10-wikipedia-search | 0.250 / answered | 0.000 / insufficient_context | 0.000 / insufficient_context | output_validation_failed | 0/0 |

## Detailed examples

### Correctly supported answer

Original question: Как планировщик мониторинга атомарно захватывает просроченный запуск и не допускает дубликаты после перезапуска?

Status: `answered`; relevance `0.572491` vs threshold `0.550000`; reason ``.

Final context:

- S1: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Надёжность — `structural-48f25c926c4b8f19033a5f25`; cosine `0.572491`

  > ## Надёжность Оба планировщика используют постоянные SQLite-расписания, атомарный claim и уникальную пару `(schedule_id, scheduled_for)`. Первый запуск назначается сразу; пропущенные интервалы не создают лавину catch-up запросов; незавершённые `running`-запуски восстанавливаются после падения; `max_runs` завершает расписание; отмена сохраняет всю историю. Включены foreign keys, `busy_timeout`, WAL и транзакции. `SIGT…
- S2: `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 3. GitHub-монитор — `structural-19c812c780af19908167c6c8`; cosine `0.491501`

  > ## 3. GitHub-монитор > Создай мониторинг репозитория modelcontextprotocol/go-sdk раз в минуту и останови после двух запусков. Проверьте в trace вызов `schedule_repository_monitor`, настоящий `schedule_id` и карточку в колонке GitHub. Первый снимок назначается сразу.

Answer:

Первый запуск назначается сразу;

Claims:

- C1: Первый запуск назначается сразу; — sources S1

Verified source metadata:

- S1: `day-18/README.md` — Day 18 — Unified persistent MCP agent > Надёжность — `structural-48f25c926c4b8f19033a5f25`; cosine `0.572491`, rerank `0.590372`

Exact quotes:

- S1 → C1, exact `true`: “Первый запуск назначается сразу;”

Validation: 1 attempt(s), 0 final error(s).

Judge verdicts:

- C1: `supported` — supported

### Invalid or semantically weak evidence

Original question: Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?

Status: `insufficient_context`; relevance `0.718259` vs threshold `0.550000`; reason `output_validation_failed`.

Final context:

- S1: `day-18/README.md` — Day 18 — Unified persistent MCP agent — `structural-4e3a63032d3beaf99ea33874`; cosine `0.600517`

  > # Day 18 — Unified persistent MCP agent Один Go-проект и один DeepSeek-агент для двух задач: постоянный мониторинг публичных GitHub-репозиториев и справочных валютных курсов. GitHub и валюты обслуживают два независимых Streamable HTTP MCP-сервера. Агент получает список серверов, объединяет их каталоги инструментов и маршрутизирует каждый вызов обратно в MCP-владельца.
- S2: `day-20/README.md` — Day 20 — Orchestration MCP — `structural-9adc7c97de777669396a5699`; cosine `0.619797`

  > # Day 20 — Orchestration MCP Самостоятельный Go-модуль с тремя независимыми Streamable HTTP MCP-серверами и агентом DeepSeek. Агент получает объединённый `tools/list`, сохраняет владельца каждого инструмента и маршрутизирует каждый `tools/call` в соответствующую MCP-сессию.
- S3: `day-18/DEMO.md` — Day 18 — демонстрация единого агента — `structural-233b0f6da63ac99f66752b43`; cosine `0.718259`

  > # Day 18 — демонстрация единого агента Сценарий доказывает, что один агент подключается к двум независимым MCP, видит оба набора инструментов и направляет вызовы в правильный сервер.
- S4: `day-20/internal/web/page.go` — const chatPageHTML — `structural-2d1aad6b17ecb14c434e8ff6`; cosine `0.491191`

  > const chatPageHTML = `<!doctype html> <html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"> <title>Day 20 · MCP Orchestration</title> <style> :root{color-scheme:dark;--bg:#0b0d12;--panel:#151922;--panel2:#0f131b;--line:#2c3442;--ink:#f6f2e8;--muted:#929cad;--cyan:#5ce1e6;--amber:#ffca62;--green:#9dec83;--red:#ff7b82}*{box-sizing:border-box}body{margin:0;backgr…
- S5: `day-17/README.md` — Day 17 — GitHub MCP Agent > Результат — `structural-07ca3e8180ef5ea440236ae8`; cosine `0.559485`

  > ## Результат Полный цикл выглядит так: 1. агент подключается к MCP по Streamable HTTP; 2. `tools/list` возвращает `get_github_repository` и JSON Schema; 3. схема без изменений передаётся DeepSeek как function tool; 4. запрос к модели использует `tool_choice: auto`; 5. модель формирует `owner` и `repository`; 6. агент выполняет MCP `tools/call`; 7. MCP-сервер вызывает `GET /repos/{owner}/{repository}`; 8. GitHub metad…

Answer:

Не знаю: в базе недостаточно релевантных данных для надёжного ответа.

Claims:

- None.

Verified source metadata:

- None.

Exact quotes:

- None.

Validation: 2 attempt(s), 1 final error(s).

Judge verdicts:

- None.

Clarification: Уточните, к какому проекту, файлу или компоненту относится вопрос.

### Safe refusal

Original question: Какая дозировка амоксициллина назначается ребёнку семи лет при остром отите?

Status: `insufficient_context`; relevance `0.000000` vs threshold `0.550000`; reason `empty_context`.

Final context:


Answer:

Не знаю: в базе недостаточно релевантных данных для надёжного ответа.

Claims:

- None.

Verified source metadata:

- None.

Exact quotes:

- None.

Validation: 0 attempt(s), 0 final error(s).

Judge verdicts:

- None.

Clarification: Уточните, к какому проекту, файлу или компоненту относится вопрос.

## Honest conclusion

The run does not assume a winner. Concept coverage was 0.333 / 0.000 / 0.033 for day23 / grounded / strict. Exact-quote enforcement made accepted structured answers auditable, but invalid output is rejected rather than silently repaired. Strict abstained on 5 of 10 in-domain questions at the calibrated threshold; grounded abstained 5 times (empty context or validation failure). Fully supported answer rates were 0.200 and 0.200 for grounded and strict. Retrieval remains the limiting factor when the right implementation chunk is absent from final top-5.
