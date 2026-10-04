# Fixed vs structural chunking

Corpus: `b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451`  
Model: `qwen3-embedding:0.6b`  
Questions: 12, top-k: 5

| Strategy | Hit@1 | Hit@5 | MRR@5 | Chunks | Chunk words min/avg/max | Build, ms | JSON, bytes | Avg top-1 similarity |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| fixed | 0.250 | 1.000 | 0.540 | 136 | 10 / 387.8 / 700 | 67414 | 3605349 | 0.5769 |
| structural | 0.417 | 0.833 | 0.611 | 1025 | 2 / 48.7 / 700 | 70647 | 23407810 | 0.6609 |

## Examples

### q01-multi-mcp-routing — Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?

Fixed:

- 1. `0.6635` `day-18/DEMO.md` — Document
- 2. `0.5968` `day-20/README.md` — Document
- 3. `0.5774` `day-18/README.md` — Document

Structural:

- 1. `0.7183` `day-18/DEMO.md` — Day 18 — демонстрация единого агента
- 2. `0.6198` `day-20/README.md` — Day 20 — Orchestration MCP
- 3. `0.6005` `day-18/README.md` — Day 18 — Unified persistent MCP agent

### q02-monitor-claim — Как планировщик мониторинга атомарно захватывает просроченный запуск и не допускает дубликаты после перезапуска?

Fixed:

- 1. `0.4535` `day-18/DEMO.md` — Document
- 2. `0.4053` `day-18/internal/mcpgithub/server.go` — Document
- 3. `0.3692` `day-18/internal/store/store_test.go` — Document

Structural:

- 1. `0.5725` `day-18/README.md` — Day 18 — Unified persistent MCP agent > Надёжность
- 2. `0.4915` `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 3. GitHub-монитор
- 3. `0.4410` `day-18/DEMO.md` — Day 18 — демонстрация единого агента > 8. Отмена

### q03-decimal-currency — Почему валютные суммы не вычисляются через float64 и как реализована точная десятичная арифметика?

Fixed:

- 1. `0.5457` `day-18/internal/decimal/decimal_test.go` — Document
- 2. `0.5031` `day-18/internal/decimal/decimal.go` — Document
- 3. `0.4913` `day-18/internal/mcpcurrency/server_test.go` — Document

Structural:

- 1. `0.5542` `day-18/internal/decimal/decimal_test.go` — func TestExactDecimalArithmetic
- 2. `0.5380` `day-18/internal/decimal/decimal.go` — package decimal
- 3. `0.5247` `day-18/internal/decimal/decimal_test.go` — package decimal

## Conclusion

Structural chunking ranked relevant chunks earlier on this evaluation set (higher MRR@5).
