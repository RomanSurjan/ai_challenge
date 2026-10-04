# День 24 — сценарий видео на 6 минут

## 0:00–0:35 — от retrieval к доказательствам

Показать структуру `day-24` и README.

Сказать: Day 21 построил structural index на 1025 chunks, Day 22 добавил первый
grounded запрос, Day 23 — candidate top-20, threshold 0.45 и reranking. Day 24
не перестраивает корпус или embeddings: он читает тот же Day 21 index и повторяет
лучший Day 23 `filtered` retrieval без обязательного rewrite.

Главная проблема: `[S1]` доказывает только существование номера. Модель может
сослаться на нерелевантный чанк, пересказать несуществующий фрагмент или дать
правильную ссылку на неподтверждённое утверждение.

## 0:35–1:20 — новый контракт

Открыть `internal/evidence/evidence.go` и Mermaid-схему README.

Показать wire JSON: один компактный claim содержит собственный массив evidence
`{source_id, quote}`. Объяснить цепочку:

```text
answer → claim C1 → S1 → exact quote → source/section/chunk_id
```

Модель возвращает только `S<n>`. Полные metadata приложение берёт из final
context. Вложенная evidence устраняет рассинхронизацию между отдельными
`claim.source_ids` и `evidence.source_id`; канонический JSON всё равно содержит
и claims с source IDs, и общий citations list.

Ollama получает настоящую JSON Schema: обязательные поля, один claim, одна-две
quotes и source IDs только из S1…Sn. Это понадобилось потому, что 3B-модель при
свободном JSON пыталась копировать целую Go-функцию и обрывала ответ на 512
tokens.

## 1:20–2:05 — exact quote validation и repair

Показать `Validator.Validate`.

Цитата должна быть непрерывной Unicode-подстрокой именно связанного final chunk,
длиной 20–160 символов. Парафраз, пустая quote, другой chunk, источник только из
candidate list, неизвестный S9 и длинная quote отклоняются. Никакого молчаливого
обрезания нет.

Перед генерацией приложение предлагает короткие проверенные quote-кандидаты из
final context; модель выбирает один, а validator заново проверяет его. При ошибке
есть ровно один repair с конкретными codes. Вторая ошибка даёт
`output_validation_failed` и безопасное «Не знаю», а не слабый fallback.

## 2:05–2:40 — semantic judge

Открыть `internal/judging/judging.go`.

Judge видит только original question, один claim и validated quotes. Expected
answer, required concepts и expected sources ему не передаются. Локальный
`qwen2.5:3b`, temperature 0, максимум 128 tokens, возвращает `supported`,
`partially_supported`, `unsupported` или `unverifiable`. Malformed/error всегда
становится `unverifiable`.

Оговорить ограничение: маленькая модель проверяет близкую модель, поэтому это
proxy, а не окончательное доказательство.

## 2:40–3:25 — relevance gate и calibration

Выполнить или показать:

```bash
go run ./cmd/rag-agent calibrate-gate
```

Открыть `artifacts/gate-calibration.md`. Chunk threshold 0.45 фильтрует chunks;
answer threshold решает, вызывать ли модель. Score — максимальный cosine в final
context; empty context или score ниже threshold означает отказ до генерации.

Grid 0.45…0.80 был задан заранее. Правило: максимальный F1 при false refusal не
выше 20%, затем меньше unsafe answers и выше threshold. На 10 positives и 6 OOD
точки 0.45…0.55 дали F1 1.000, false refusal 0, negative abstention 1.000;
tie-break выбрал **0.55**. Answer model calls при calibration: 0.

Честное ограничение: все negatives уже стали empty после chunk threshold 0.45.
Значит, этот набор не доказывает отдельный прирост от 0.55; нужен новый
near-domain holdout, но менять вопросы после просмотра результата нельзя.

## 3:25–4:10 — успешный ответ и безопасный отказ

Показать команду:

```bash
go run ./cmd/rag-agent ask --mode strict \
  --question "Как минимальный Go MCP-клиент устанавливает соединение и получает список tools с пагинацией?"
```

В реальном smoke-run status был `answered`, relevance около 0.816 против 0.55,
source — `day-16/internal/mcpdemo/client.go`, section
`func ConnectAndListTools`, chunk
`structural-8b8adfcb6bcdc36d1cc5cb1d`. Quote была точной строкой:
`return nil, fmt.Errorf("list MCP tools: %w", err)`.

Затем показать OOD:

```bash
go run ./cmd/rag-agent ask --mode strict \
  --question "Как приготовить тесто для неаполитанской пиццы с холодной ферментацией 72 часа?"
```

Фактический результат: `insufficient_context`, score 0, generation 0 ms, ответ
«Не знаю: в базе недостаточно релевантных данных для надёжного ответа» и вопрос
на уточнение. Answer model не вызывалась.

## 4:10–5:05 — evaluation на 10 вопросах

Показать:

```bash
go run ./cmd/rag-agent eval
```

Открыть `artifacts/comparison.md`.

| Mode | Concepts | Answered / abstained | Final recall | Precision | MRR | Avg total ms |
|---|---:|---:|---:|---:|---:|---:|
| day23 | 0.333 | 10 / 0 | 0.458 | 0.370 | 0.820 | 16526.1 |
| grounded | 0.000 | 5 / 5 | 0.458 | 0.370 | 0.820 | 15425.1 |
| strict | 0.033 | 5 / 5 | 0.458 | 0.370 | 0.820 | 11316.5 |

Для каждого принятого grounded/strict ответа: source presence 1.000, metadata
completeness 1.000, valid final-context source ID 1.000, answer with quote 1.000,
claims with evidence 1.000 и exact substring rate 1.000. Invalid source IDs и
duplicate sources — 0.

Entailment: 1 supported, 0 partial, 0 unsupported, 4 unverifiable; fully
supported answer rate 0.200. Для grounded средние generator tokens — 3606.0 /
368.1, judge — 113.5 / 7.1; для strict — 3606.8 / 371.4 и 114.4 / 7.1.

## 5:05–5:35 — abstention evaluation

Показать:

```bash
go run ./cmd/rag-agent eval-abstention
```

Шесть OOD-вопросов: abstention rate 1.000, unsafe answered 0.000, answer model
не вызвана 6/6, средняя latency 48.0 ms. Все причины — `empty_context`; low
relevance и output validation failure на этом отрицательном наборе — 0.

## 5:35–6:15 — failure, стоимость и честный вывод

Открыть подробные примеры comparison.md. В `q04` модель дважды оборвала JSON:
`malformed_json: unexpected EOF`; strict безопасно отказал после 26.87 s и 1024
completion tokens. Всего 5 из 10 in-domain вопросов получили
`output_validation_failed`. Это end-to-end false refusal 0.500, хотя gate-only
false refusal был 0.

Даже единственный supported пример `q02` оказался слишком узким: claim «Первый
запуск назначается сразу» действительно подтверждён exact quote, но не отвечает
на весь вопрос про транзакционный claim, unique schedule и SQLite. Поэтому
strict нельзя объявить победителем: он заметно повышает проверяемость принятых
ответов, но ухудшает полноту и concept coverage.

Финальный вывод: exact substring + metadata enrichment закрывают происхождение
цитат, judge обнаруживает семантическую слабость, а gate безопасно отсекает OOD.
Следующие шаги — более сильная structured-output модель, context compression,
multi-claim generation с большим budget, независимый NLI judge и near-domain
abstention holdout.
