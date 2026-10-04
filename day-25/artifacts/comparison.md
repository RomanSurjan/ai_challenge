# Day 25 — multi-turn RAG + task memory

Run: `2026-10-04T16:33:51Z`  
Scenario SHA-256: `675eeebb79dbc1e323bf538410090ef71bf4cd28d97647be173d0ed77949e8c8`  
Models: embedding `qwen3-embedding:0.6b`, memory/resolver/answer/judge `qwen2.5:3b`  
Index: `../day-21/artifacts/index-structural.json` (corpus `b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451`)  
Pipeline: candidate-K 20, final-K 5, chunk threshold 0.45, answer threshold 0.55, weights 0.70 / 0.20 / 0.10  
Quotes: 20–160 Unicode characters

## Modes

- `stateless`: each turn is an independent Day 24 strict ask; history is stored only for reporting.
- `history`: bounded recent history plus contextual resolver, without persistent task memory.
- `task-memory`: bounded history, validated task state with user provenance, resolver, and atomic session commits.

## Overall metrics

| Mode | Goal | Constraints | Terms | Supersede | Follow-up | Fallbacks | Answered / abstained / memory | Final recall | Precision | MRR | Exact quotes | Fully supported | Avg ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| stateless | 0.000 | 0.000 | 0.000 | n/a | 0.077 | 0 | 5 / 18 / 0 | 0.000 | 0.000 | 0.000 | 1.000 | 0.800 | 1965.8 |
| history | 0.000 | 0.000 | 0.000 | n/a | 0.154 | 0 | 13 / 10 / 0 | 0.625 | 0.125 | 0.312 | 1.000 | 0.077 | 12531.3 |
| task-memory | 1.000 | 1.000 | 1.000 | 1.000 | 0.231 | 0 | 5 / 6 / 12 | 0.500 | 0.100 | 0.229 | 1.000 | 0.941 | 8085.9 |

## Memory provenance and grounding

| Mode | Valid user turn | Exact user quote | Failed updates | Repairs | S claims | U claims | Mixed claims | Assistant cited | Metadata | Valid IDs |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| stateless | 0.000 | 0.000 | 0 | 0 | 1.000 | 0.000 | 0.000 | 0 | 1.000 | 1.000 |
| history | 0.000 | 0.000 | 0 | 0 | 1.000 | 0.000 | 0.000 | 0 | 1.000 | 1.000 |
| task-memory | 1.000 | 1.000 | 0 | 0 | 1.000 | 1.000 | 0.000 | 0 | 1.000 | 1.000 |

## Every scenario turn

### Artifact security audit — stateless

| Turn | Goal | Active constraints | Search query | Status | Sources |
|---:|---|---|---|---|---|
| 1 | — |  | Наша цель — проверить безопасность Artifact MCP. | insufficient_context |  |
| 2 | — |  | Ограничение: учитывай только Go-реализацию. | answered | S1:day-20/internal/knowledge/server.go |
| 3 | — |  | Уточнение: особенно важны path traversal и atomic write. | insufficient_context |  |
| 4 | — |  | Как он защищается от первого? | insufficient_context |  |
| 5 | — |  | А как реализовано второе? | insufficient_context |  |
| 6 | — |  | Решение: теперь приоритет — лимит размера Markdown. | answered | S1:day-20/internal/artifact/store.go |
| 7 | — |  | Какой именно лимит? | insufficient_context |  |
| 8 | — |  | Сравни его с ранее обсуждённой атомарной записью. | insufficient_context |  |
| 9 | — |  | Больше не учитывай ограничение «только Go-реализацию». | insufficient_context |  |
| 10 | — |  | Вернись к исходной цели: какие риски мы проверяем? | insufficient_context |  |
| 11 | — |  | Дай финальную сводку с текущими ограничениями и проверяемыми источниками. | insufficient_context |  |

### Artifact security audit — history

| Turn | Goal | Active constraints | Search query | Status | Sources |
|---:|---|---|---|---|---|
| 1 | — |  | Artifact MCP защита от path traversal в Go | insufficient_context |  |
| 2 | — |  | Artifact MCP защита от path traversal в Go | answered | S1:day-20/internal/artifact/server.go |
| 3 | — |  | Artifact MCP защита от path traversal в Go,path traversal и atomic write в Go | answered | S1:day-20/internal/artifact/server.go; S3:day-18/internal/mcpgithub/server_test.go |
| 4 | — |  | Artifact MCP защита от path traversal в Go | insufficient_context |  |
| 5 | — |  | Artifact MCP защита от path traversal в Go | answered | S1:day-20/internal/artifact/server.go; S2:day-20/internal/artifact/store.go |
| 6 | — |  | Artifact MCP защита от path traversal в Go | insufficient_context |  |
| 7 | — |  | Artifact MCP защита от path traversal в Go | answered | S1:day-20/internal/artifact/server.go; S5:day-20/internal/artifact/store_test.go |
| 8 | — |  | Artifact MCP защита от path traversal в Go | insufficient_context |  |
| 9 | — |  | Artifact MCP защита от path traversal и atomic write в Go | answered | S1:day-20/internal/artifact/store.go; S3:day-20/internal/artifact/store_test.go |
| 10 | — |  | Artifact MCP защита от path traversal и atomic write в Go | answered | S2:day-20/internal/artifact/server.go; S4:day-19/internal/pipeline/save_test.go |
| 11 | — |  | атомарная запись защита от path traversal в Go | answered | S3:day-20/README.md |

### Artifact security audit — task-memory

| Turn | Goal | Active constraints | Search query | Status | Sources |
|---:|---|---|---|---|---|
| 1 | Проверка безопасности Artifact MCP |  | Наша цель — проверить безопасность Artifact MCP. | memory_only | U1:conversation:eval-artifact-security-audit-task_memory |
| 2 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Ограничение: учитывай только Go-реализацию. | memory_only | U2:conversation:eval-artifact-security-audit-task_memory |
| 3 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Уточнение: особенно важны path traversal и atomic write. | memory_only | U3:conversation:eval-artifact-security-audit-task_memory |
| 4 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Artifact MCP защита от path traversal в Go | insufficient_context |  |
| 5 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Artifact MCP защита от path traversal в Go | answered | U3:conversation:eval-artifact-security-audit-task_memory |
| 6 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Решение: теперь приоритет — лимит размера Markdown. | memory_only | U3:conversation:eval-artifact-security-audit-task_memory |
| 7 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Artifact MCP лимит размера Markdown Go-реализация | insufficient_context |  |
| 8 | Проверка безопасности Artifact MCP | Ограничение: учитывай только Go-реализацию. | Artifact MCP ограничение размера Markdown в Go | answered | S2:day-20/internal/artifact/store.go; S3:day-20/internal/agent/agent.go |
| 9 | Проверка безопасности Artifact MCP | Учитывать только Go-реализацию не требуется. | Больше не учитывай ограничение «только Go-реализацию». | memory_only | U2:conversation:eval-artifact-security-audit-task_memory |
| 10 | Проверка безопасности Artifact MCP | Учитывать только Go-реализацию не требуется. | Artifact MCP защита от path traversal в Go | answered | S1:day-20/internal/artifact/server.go; U4:conversation:eval-artifact-security-audit-task_memory |
| 11 | Проверка безопасности Artifact MCP | Учитывать только Go-реализацию не требуется. | Artifact MCP защита от path traversal в Go | insufficient_context |  |

### MCP agent reliability — stateless

| Turn | Goal | Active constraints | Search query | Status | Sources |
|---:|---|---|---|---|---|
| 1 | — |  | Наша цель — понять полный tool-call и scheduler flow MCP-агента. | insufficient_context |  |
| 2 | — |  | Термин: tool_call_id — идентификатор вызова, который надо сохранить. | answered | S2:day-19/internal/agent/types.go |
| 3 | — |  | Термин: role=tool — сообщение с результатом MCP-инструмента. | insufficient_context |  |
| 4 | — |  | Ограничение: обязательно учитывай restart persistence scheduler-а. | insufficient_context |  |
| 5 | — |  | А что происходит после перезапуска? | insufficient_context |  |
| 6 | — |  | Термин: pagination означает повторять ListTools с NextCursor до пустого cursor. | insufficient_context |  |
| 7 | — |  | А где она используется? | insufficient_context |  |
| 8 | — |  | Ограничение: не обсуждай UI. | answered | S2:day-18/README.md |
| 9 | — |  | Как модель получает результат инструмента в следующем раунде? | insufficient_context |  |
| 10 | — |  | А что было нашей целью? | insufficient_context |  |
| 11 | — |  | Когда заканчивается эта пагинация? | insufficient_context |  |
| 12 | — |  | Дай итог по tool-call и scheduler flow с учётом всех активных ограничений. | answered | S1:day-20/README.md |

### MCP agent reliability — history

| Turn | Goal | Active constraints | Search query | Status | Sources |
|---:|---|---|---|---|---|
| 1 | — |  | tool-call и scheduler flow MCP-агента | insufficient_context |  |
| 2 | — |  | tool_call_id | answered | S1:day-18/internal/agent/types.go |
| 3 | — |  | tool_call_id role=tool | answered | S2:day-20/internal/agent/agent.go |
| 4 | — |  | tool_call_id role=tool | insufficient_context |  |
| 5 | — |  | restart persistence scheduler-а | answered | S2:day-18/README.md |
| 6 | — |  | pagination означает повторять ListTools с NextCursor до пустого cursor | insufficient_context |  |
| 7 | — |  | tool_call_id role=tool pagination | insufficient_context |  |
| 8 | — |  | tool_call_id role=tool | insufficient_context |  |
| 9 | — |  | tool_call_id role=tool | insufficient_context |  |
| 10 | — |  | tool_call_id после перезапуска persistence scheduler | answered | S5:day-18/README.md |
| 11 | — |  | pagination U7 role=user U8 role=user U9 role=user U10 role=user | answered | S3:day-19/internal/agent/agent.go |
| 12 | — |  | Artifact MCP защита от path traversal в Go | answered | S5:day-20/internal/artifact/store_test.go |

### MCP agent reliability — task-memory

| Turn | Goal | Active constraints | Search query | Status | Sources |
|---:|---|---|---|---|---|
| 1 | понять полный tool-call и scheduler flow MCP-агента |  | Наша цель — понять полный tool-call и scheduler flow MCP-агента. | memory_only | U1:conversation:eval-mcp-agent-reliability-task_memory |
| 2 | понять полный tool-call и scheduler flow MCP-агента |  | Термин: tool_call_id — идентификатор вызова, который надо сохранить. | memory_only | U2:conversation:eval-mcp-agent-reliability-task_memory |
| 3 | понять полный tool-call и scheduler flow MCP-агента |  | Термин: role=tool — сообщение с результатом MCP-инструмента. | memory_only | U2:conversation:eval-mcp-agent-reliability-task_memory; U3:conversation:eval-mcp-agent-reliability-task_memory |
| 4 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: обязательно учитывай restart persistence scheduler-а. | Ограничение: обязательно учитывай restart persistence scheduler-а. | memory_only | U2:conversation:eval-mcp-agent-reliability-task_memory |
| 5 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: обязательно учитывай restart persistence scheduler-а. | restart persistence scheduler Go | answered | U2:conversation:eval-mcp-agent-reliability-task_memory |
| 6 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: обязательно учитывай restart persistence scheduler-а. | Термин: pagination означает повторять ListTools с NextCursor до пустого cursor. | memory_only | U3:conversation:eval-mcp-agent-reliability-task_memory; U4:conversation:eval-mcp-agent-reliability-task_memory; U5:conversation:eval-mcp-agent-reliability-task_memory |
| 7 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: обязательно учитывай restart persistence scheduler-а. | restart persistence scheduler Go tool_call_id tool_call_result_message | insufficient_context |  |
| 8 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: не обсуждай UI.; Ограничение: обязательно учитывай restart persistence scheduler-а. | Ограничение: не обсуждай UI. | memory_only | U2:conversation:eval-mcp-agent-reliability-task_memory; U3:conversation:eval-mcp-agent-reliability-task_memory |
| 9 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: не обсуждай UI.; Ограничение: обязательно учитывай restart persistence scheduler-а. | restart persistence scheduler pagination tool_call_id | insufficient_context |  |
| 10 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: не обсуждай UI.; Ограничение: обязательно учитывай restart persistence scheduler-а. | А что было нашей целью? | memory_only | U1:conversation:eval-mcp-agent-reliability-task_memory |
| 11 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: не обсуждай UI.; Ограничение: обязательно учитывай restart persistence scheduler-а. | pagination tool_call_result_message restart persistence scheduler | insufficient_context |  |
| 12 | понять полный tool-call и scheduler flow MCP-агента | Ограничение: не обсуждай UI.; Ограничение: обязательно учитывай restart persistence scheduler-а. | restart persistence scheduler pagination tool_call_result_message tool_call_id | answered | S4:day-20/README.md |

## Detailed examples

1. **Follow-up recovery.** MCP reliability turn 5 was the pronoun follow-up `А что происходит после перезапуска?`. Stateless embedded it literally and refused. Task-memory resolved `restart persistence scheduler Go`, returned `answered`, and cited the validated turn-4 constraint as U2 with exact quote. This success is narrow: the model chose a memory claim instead of explaining the corpus mechanism.
2. **Old constraint outside the recent window.** At MCP turn 10 the initial goal was no longer in the 8-message prompt window. Task-memory returned `Цель: понять полный tool-call и scheduler flow MCP-агента [U1]` from memory `Maa032798f8ba`; the exact quote points to user turn 1. Stateless refused.
3. **Supersede.** Artifact turn 9 replaced `Mcb1f0dea8baf` with `M4843fc58324a`. The active item says that Go-only scope is no longer required and carries `supersedes=Mcb1f0dea8baf`; the old item remains in `superseded_history` with its original turn/quote.
4. **Safe failure.** Artifact turn 4 resolved the useful query `Artifact MCP защита от path traversal в Go`, but structured output still failed deterministic validation twice in the smoke path. The final result was `output_validation_failed` with `Не знаю`, not an unverified corpus claim. OOD pizza smoke similarly stopped at `empty_context` without an answer-model call.

## Full validated source metadata and exact quotes

### artifact-security-audit / stateless / turn 2

- `S1` kind=`corpus`, source=`day-20/internal/knowledge/server.go`, section=`func searchIn`, chunk=`structural-6393f8ffdfe6be75e010c6f5`, cosine=`0.581217`, rerank=`0.553426`
  - quote `S1` → C1: "func searchIn() map[string]any {" (exact=true)
  - quote `S1` → C1: "func searchIn() map[string]any {" (exact=true)

### artifact-security-audit / stateless / turn 6

- `S1` kind=`corpus`, source=`day-20/internal/artifact/store.go`, section=`const MaxMarkdown`, chunk=`structural-a2d191ded1648e57a1abb51c`, cosine=`0.723225`, rerank=`0.603129`
  - quote `S1` → C1: "const MaxMarkdown = 512 << 10" (exact=true)

### artifact-security-audit / history / turn 2

- `S1` kind=`corpus`, source=`day-20/internal/artifact/server.go`, section=`package artifact`, chunk=`structural-dd22c45ec2e83fc8b71d44f3`, cosine=`0.607205`, rerank=`0.662522`
  - quote `S1` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `S1` → C1: "c \"ai-challenge/day-20/internal/mcpcontract\"" (exact=true)

### artifact-security-audit / history / turn 3

- `S1` kind=`corpus`, source=`day-20/internal/artifact/server.go`, section=`package artifact`, chunk=`structural-dd22c45ec2e83fc8b71d44f3`, cosine=`0.558082`, rerank=`0.616757`
- `S3` kind=`corpus`, source=`day-18/internal/mcpgithub/server_test.go`, section=`package mcpgithub`, chunk=`structural-a520f35bdc9afde20e18ad8a`, cosine=`0.540963`, rerank=`0.596480`
  - quote `S1` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `S3` → C1: "package mcpgithub\n\nimport (\n\t\"context\"\n\t\"encoding/json\"\n\t\"path/filepath\"\n\t\"strings\"\n\t\"testing\"\n\t\"time\"\n\n\t\"ai-challenge/day-18/internal/store\"\n\t\"github." (exact=true)

### artifact-security-audit / history / turn 5

- `S1` kind=`corpus`, source=`day-20/internal/artifact/server.go`, section=`package artifact`, chunk=`structural-dd22c45ec2e83fc8b71d44f3`, cosine=`0.607205`, rerank=`0.662522`
- `S2` kind=`corpus`, source=`day-20/internal/artifact/store.go`, section=`package artifact`, chunk=`structural-13737ffc30383e3a898e9ab0`, cosine=`0.574298`, rerank=`0.651004`
  - quote `S1` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `S2` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)

### artifact-security-audit / history / turn 7

- `S1` kind=`corpus`, source=`day-20/internal/artifact/server.go`, section=`package artifact`, chunk=`structural-dd22c45ec2e83fc8b71d44f3`, cosine=`0.607205`, rerank=`0.662522`
- `S5` kind=`corpus`, source=`day-20/internal/artifact/store_test.go`, section=`package artifact`, chunk=`structural-73aa414f6094a46dfb27686d`, cosine=`0.573242`, rerank=`0.610635`
  - quote `S1` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `S5` → C1: "package artifact\n\nimport (\n\t\"ai-challenge/day-20/internal/domain\"\n\t\"crypto/sha256\"\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)" (exact=true)

### artifact-security-audit / history / turn 9

- `S1` kind=`corpus`, source=`day-20/internal/artifact/store.go`, section=`package artifact`, chunk=`structural-13737ffc30383e3a898e9ab0`, cosine=`0.576162`, rerank=`0.623085`
- `S3` kind=`corpus`, source=`day-20/internal/artifact/store_test.go`, section=`func TestBuildSaveReadListAndSafety`, chunk=`structural-9e96c6337ba04ed24def968b`, cosine=`0.568853`, rerank=`0.620527`
  - quote `S1` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `S3` → C1: "func TestBuildSaveReadListAndSafety(t *testing.T) {" (exact=true)

### artifact-security-audit / history / turn 10

- `S2` kind=`corpus`, source=`day-20/internal/artifact/server.go`, section=`package artifact`, chunk=`structural-dd22c45ec2e83fc8b71d44f3`, cosine=`0.571321`, rerank=`0.621391`
- `S4` kind=`corpus`, source=`day-19/internal/pipeline/save_test.go`, section=`func TestSaveAtomicSHAAndMarkdown`, chunk=`structural-326178ce521d339b0e44cda6`, cosine=`0.587758`, rerank=`0.612858`
  - quote `S2` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `S4` → C1: "func TestSaveAtomicSHAAndMarkdown(t *testing.T) {" (exact=true)

### artifact-security-audit / history / turn 11

- `S3` kind=`corpus`, source=`day-20/README.md`, section=`Day 20 — Orchestration MCP > Проверка и запуск`, chunk=`structural-631042608fb4c917c9933390`, cosine=`0.516121`, rerank=`0.570642`
  - quote `S3` → C1: "## Проверка и запуск" (exact=true)
  - quote `S3` → C1: "docker compose -p day20 --env-file ../.env config" (exact=true)

### artifact-security-audit / task-memory / turn 1

- `U1` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 1`, chunk=`user-turn-U1`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U1` → C1: "Наша цель — проверить безопасность Artifact MCP." (exact=true)

### artifact-security-audit / task-memory / turn 2

- `U2` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 2`, chunk=`user-turn-U2`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Ограничение: учитывай только Go-реализацию." (exact=true)

### artifact-security-audit / task-memory / turn 3

- `U3` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 3`, chunk=`user-turn-U3`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U3` → C1: "Уточнение: особенно важны path traversal и atomic write." (exact=true)

### artifact-security-audit / task-memory / turn 5

- `U3` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 3`, chunk=`user-turn-U3`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U3` → C1: "Уточнение: особенно важны path traversal и atomic write." (exact=true)

### artifact-security-audit / task-memory / turn 6

- `U3` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 6`, chunk=`user-turn-U6`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U3` → C1: "Решение: теперь приоритет — лимит размера Markdown." (exact=true)

### artifact-security-audit / task-memory / turn 8

- `S2` kind=`corpus`, source=`day-20/internal/artifact/store.go`, section=`const MaxMarkdown`, chunk=`structural-a2d191ded1648e57a1abb51c`, cosine=`0.782695`, rerank=`0.643943`
- `S3` kind=`corpus`, source=`day-20/internal/agent/agent.go`, section=`const SystemPrompt`, chunk=`structural-02a0d96a15d2f293c21b27f2`, cosine=`0.597293`, rerank=`0.639053`
  - quote `S2` → C1: "const MaxMarkdown = 512 << 10" (exact=true)
  - quote `S3` → C1: "const SystemPrompt = `Ты — агент-оркестратор трёх независимых MCP-серверов." (exact=true)

### artifact-security-audit / task-memory / turn 9

- `U2` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 9`, chunk=`user-turn-U9`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Больше не учитывай ограничение «только Go-реализацию»." (exact=true)

### artifact-security-audit / task-memory / turn 10

- `S1` kind=`corpus`, source=`day-20/internal/artifact/server.go`, section=`package artifact`, chunk=`structural-dd22c45ec2e83fc8b71d44f3`, cosine=`0.607199`, rerank=`0.662520`
- `U4` kind=`task_memory`, source=`conversation:eval-artifact-security-audit-task_memory`, section=`user turn 3`, chunk=`user-turn-U3`, cosine=`0.000000`, rerank=`0.000000`
  - quote `S1` → C1: "\"ai-challenge/day-20/internal/domain\"" (exact=true)
  - quote `U4` → C1: "Уточнение: особенно важны path traversal и atomic write." (exact=true)

### mcp-agent-reliability / stateless / turn 2

- `S2` kind=`corpus`, source=`day-19/internal/agent/types.go`, section=`type ToolCall`, chunk=`structural-006b63772c3dea6437a674db`, cosine=`0.691552`, rerank=`0.592043`
  - quote `S2` → C1: "type ToolCall struct {" (exact=true)
  - quote `S2` → C1: "ID       string           `json:\"id\"`" (exact=true)

### mcp-agent-reliability / stateless / turn 8

- `S2` kind=`corpus`, source=`day-18/README.md`, section=`Day 18 — Unified persistent MCP agent > Ограничения`, chunk=`structural-6a60fcf8927ad0e3d2b96cfe`, cosine=`0.533057`, rerank=`0.536570`
  - quote `S2` → C1: "- Минимальный интервал фонового мониторинга — 60 секунд." (exact=true)

### mcp-agent-reliability / stateless / turn 12

- `S1` kind=`corpus`, source=`day-20/README.md`, section=`Day 20 — Orchestration MCP > Сервисы`, chunk=`structural-7372f1b5d2578535aef621bb`, cosine=`0.551063`, rerank=`0.602872`
  - quote `S1` → C1: "Также проверяются исходные `tool_call_id` и совпадение SHA-256." (exact=true)

### mcp-agent-reliability / history / turn 2

- `S1` kind=`corpus`, source=`day-18/internal/agent/types.go`, section=`type Message`, chunk=`structural-099b0e7b61c141205cd8bade`, cosine=`0.498343`, rerank=`0.724420`
  - quote `S1` → C1: "ToolCallID       string     `json:\"tool_call_id,omitempty\"`" (exact=true)
  - quote `S1` → C1: "ToolCalls        []ToolCall `json:\"tool_calls,omitempty\"`" (exact=true)

### mcp-agent-reliability / history / turn 3

- `S2` kind=`corpus`, source=`day-20/internal/agent/agent.go`, section=`const SystemPrompt`, chunk=`structural-02a0d96a15d2f293c21b27f2`, cosine=`0.713929`, rerank=`0.666542`
  - quote `S2` → C1: "const SystemPrompt = `Ты — агент-оркестратор трёх независимых MCP-серверов." (exact=true)
  - quote `S2` → C1: "Всегда самостоятельно решай, какие инструменты нужны и в каком порядке их вызывать, через tool_choice=auto." (exact=true)

### mcp-agent-reliability / history / turn 5

- `S2` kind=`corpus`, source=`day-18/README.md`, section=`Day 18 — Unified persistent MCP agent > Надёжность`, chunk=`structural-48f25c926c4b8f19033a5f25`, cosine=`0.579767`, rerank=`0.619585`
  - quote `S2` → C1: "незавершённые `running`-запуски восстанавливаются после падения;" (exact=true)

### mcp-agent-reliability / history / turn 10

- `S5` kind=`corpus`, source=`day-18/README.md`, section=`Day 18 — Unified persistent MCP agent > Надёжность`, chunk=`structural-48f25c926c4b8f19033a5f25`, cosine=`0.516389`, rerank=`0.597403`
  - quote `S5` → C1: "## Надёжность\n\nОба планировщика используют постоянные SQLite-расписания, атомарный claim и уникальную пару `(schedule_id, scheduled_for)`." (exact=true)

### mcp-agent-reliability / history / turn 11

- `S3` kind=`corpus`, source=`day-19/internal/agent/agent.go`, section=`const SystemPrompt`, chunk=`structural-5f7fe11f5be896804b84c790`, cosine=`0.584371`, rerank=`0.554530`
  - quote `S3` → C1: "const SystemPrompt = `Ты — агент исследовательского пайплайна." (exact=true)

### mcp-agent-reliability / history / turn 12

- `S5` kind=`corpus`, source=`day-20/internal/artifact/store_test.go`, section=`package artifact`, chunk=`structural-73aa414f6094a46dfb27686d`, cosine=`0.572437`, rerank=`0.610353`
  - quote `S5` → C1: "package artifact\n\nimport (\n\t\"ai-challenge/day-20/internal/domain\"\n\t\"crypto/sha256\"\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)" (exact=true)
  - quote `S5` → C1: "package artifact\n\nimport (\n\t\"ai-challenge/day-20/internal/domain\"\n\t\"crypto/sha256\"\n\t\"fmt\"\n\t\"os\"\n\t\"testing\"\n)" (exact=true)

### mcp-agent-reliability / task-memory / turn 1

- `U1` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 1`, chunk=`user-turn-U1`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U1` → C1: "Наша цель — понять полный tool-call и scheduler flow MCP-агента." (exact=true)

### mcp-agent-reliability / task-memory / turn 2

- `U2` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 2`, chunk=`user-turn-U2`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Термин: tool_call_id — идентификатор вызова, который надо сохранить." (exact=true)

### mcp-agent-reliability / task-memory / turn 3

- `U2` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 3`, chunk=`user-turn-U3`, cosine=`0.000000`, rerank=`0.000000`
- `U3` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 2`, chunk=`user-turn-U2`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Термин: role=tool — сообщение с результатом MCP-инструмента." (exact=true)
  - quote `U3` → C2: "Термин: tool_call_id — идентификатор вызова, который надо сохранить." (exact=true)

### mcp-agent-reliability / task-memory / turn 4

- `U2` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 4`, chunk=`user-turn-U4`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Ограничение: обязательно учитывай restart persistence scheduler-а." (exact=true)

### mcp-agent-reliability / task-memory / turn 5

- `U2` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 4`, chunk=`user-turn-U4`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Ограничение: обязательно учитывай restart persistence scheduler-а." (exact=true)

### mcp-agent-reliability / task-memory / turn 6

- `U3` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 6`, chunk=`user-turn-U6`, cosine=`0.000000`, rerank=`0.000000`
- `U4` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 3`, chunk=`user-turn-U3`, cosine=`0.000000`, rerank=`0.000000`
- `U5` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 2`, chunk=`user-turn-U2`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U3` → C1: "Термин: pagination означает повторять ListTools с NextCursor до пустого cursor." (exact=true)
  - quote `U4` → C2: "Термин: role=tool — сообщение с результатом MCP-инструмента." (exact=true)
  - quote `U5` → C3: "Термин: tool_call_id — идентификатор вызова, который надо сохранить." (exact=true)

### mcp-agent-reliability / task-memory / turn 8

- `U2` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 8`, chunk=`user-turn-U8`, cosine=`0.000000`, rerank=`0.000000`
- `U3` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 4`, chunk=`user-turn-U4`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U2` → C1: "Ограничение: не обсуждай UI." (exact=true)
  - quote `U3` → C2: "Ограничение: обязательно учитывай restart persistence scheduler-а." (exact=true)

### mcp-agent-reliability / task-memory / turn 10

- `U1` kind=`task_memory`, source=`conversation:eval-mcp-agent-reliability-task_memory`, section=`user turn 1`, chunk=`user-turn-U1`, cosine=`0.000000`, rerank=`0.000000`
  - quote `U1` → C1: "Наша цель — понять полный tool-call и scheduler flow MCP-агента." (exact=true)

### mcp-agent-reliability / task-memory / turn 12

- `S4` kind=`corpus`, source=`day-20/README.md`, section=`Day 20 — Orchestration MCP > Сервисы`, chunk=`structural-7372f1b5d2578535aef621bb`, cosine=`0.576738`, rerank=`0.626858`
  - quote `S4` → C4: "Также проверяются исходные `tool_call_id` и совпадение SHA-256." (exact=true)

## Honest conclusion

The winner is determined by the table, not assumed in advance. Task memory improves continuity only when extraction and resolver validation succeed; the 3B model can still produce malformed structured output or incomplete claims. Exact quotes prove provenance, not completeness, and the unchanged 0.55 threshold was not recalibrated on these scenarios.
