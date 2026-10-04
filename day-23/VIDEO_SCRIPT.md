# Сценарий видео — Day 23 (5–7 минут)

## 0:00–0:35 — от Day 21 и 22 к новой проблеме

Показать README и коротко напомнить: Day 21 собрал корпус, сделал structural chunks и индекс из 1025 чанков; Day 22 добавил cosine top-5 и grounded answer с `[S1]`. В Day 23 эти части не строятся заново: модуль читает `../day-21/artifacts/index-structural.json` только для чтения.

Сформулировать проблему: обычный top-5 берёт пять ближайших vectors, даже если пятый слабый, и не умеет поднять чанк с точным lexical/metadata совпадением.

## 0:35–1:15 — четыре режима и схема

Показать Mermaid pipeline:

```text
original → optional rewrite → embedding → candidate top-K
         → similarity threshold → heuristic rerank → final top-K
         → answer на original question
```

Назвать режимы: baseline, filtered, rewritten, enhanced. Подчеркнуть: rewrite используется только для retrieval, финальная LLM всегда отвечает на исходный вопрос; в evaluation один rewrite переиспользуется между rewritten и enhanced.

## 1:15–1:55 — query rewrite

Выполнить:

```bash
go run ./cmd/rag-agent ask --mode enhanced \
  --question "Как Artifact MCP безопасно сохраняет отчёты и предотвращает выход пути за разрешённый каталог?" \
  --candidate-k 20 --final-k 5 --min-similarity 0.45 --json
```

Показать `original_question` и фактический rewrite:

```text
Artifact MCP безопасное сохранение отчётов и предотвращение выхода за разрешённый каталог
```

Открыть `rewrite`: `fallback=false`, latency **822 ms** в проверенной отдельной
демонстрационной команде (среднее в evaluation — **952.7 ms**). Объяснить
строгий JSON, temperature 0, max 96 tokens и явный fallback на исходный вопрос
при malformed/empty JSON, error или timeout.

## 1:55–2:45 — candidate-K, threshold и reranking

В JSON показать 20 `candidates_before_filtering`: исходный rank, cosine, lexical, metadata и rerank score. Формула использует веса 0.70 / 0.20 / 0.10. Токены Unicode-aware; tie-break всегда идёт по chunk ID.

Затем показать `rejected_candidates`: отдельная причина либо `cosine below threshold`, либо `outside final-k=5 after reranking`. В `final_context_after_reranking` номера назначены заново как S1…S5; citation validation работает именно по ним.

Уточнить политику: если threshold удалил всё, нет скрытого возврата кандидатов — `insufficient_context=true`, генератор не вызывается.

## 2:45–3:25 — как выбран threshold

Выполнить или показать готовый результат:

```bash
go run ./cmd/rag-agent sweep
```

Открыть `artifacts/threshold-sweep.md`. Sweep retrieval-only и реально использует `qwen3-embedding:0.6b`. Правило: оставить не менее 95% максимального recall, затем максимизировать MRR и precision.

Назвать фактические точки:

- при 0.30: recall 0.458, precision 0.340, MRR 0.820;
- при выбранном 0.45: recall 0.458, precision 0.370, MRR 0.820, отсеяно threshold-ом 16%, empty 0;
- при 0.60: recall падает до 0.325, empty-context rate становится 0.200.

Поэтому default — 0.45, candidate-K 20, final-K 5.

## 3:25–4:05 — baseline и enhanced на одном вопросе

Показать две команды:

```bash
make ask-baseline
make ask-enhanced
```

На `q07-artifact-sandbox` baseline снова ставит первым только декларацию `SaveTool` и получает concept coverage 0. Enhanced увеличивает expected-source recall с 0.333 до 0.667, но coverage остаётся 0. Это хороший антипример: лучший source recall не гарантирует, что маленькая LLM извлечёт нужные path traversal/atomic write детали.

## 4:05–5:05 — общая таблица evaluation

Показать команду и `artifacts/comparison.md`:

```bash
go run ./cmd/rag-agent eval
```

Назвать результаты единого реального запуска из 10 вопросов:

| Mode | Concepts | Final recall | Precision | Hit@1 | MRR | Cited recall | Invalid |
|---|---:|---:|---:|---:|---:|---:|---:|
| baseline | 0.400 | 0.442 | 0.340 | 0.500 | 0.700 | 0.358 | 0 |
| filtered | 0.350 | 0.458 | 0.370 | 0.700 | 0.820 | 0.242 | 0 |
| rewritten | 0.283 | 0.442 | 0.320 | 0.500 | 0.667 | 0.392 | 0 |
| enhanced | 0.183 | 0.425 | 0.360 | 0.400 | 0.567 | 0.200 | 12 |

Ошибок вызовов — 0, rewrite fallbacks — 0. Baseline свежий, а не взят из старого Day 22 JSON.

## 5:05–5:40 — где помогло

Открыть подробный `q04-github-http-safety`. Enhanced поднял answer concept coverage с 0.250 до 0.500; filtered без rewrite достиг 0.750. В финальном контексте появились разделы GitHub REST API, MCP tool и ограничения. На уровне retrieval именно filtered дал лучший общий эффект: MRR 0.700 → 0.820, precision 0.340 → 0.370 и Hit@1 0.500 → 0.700.

## 5:40–6:20 — где навредило и цена

Открыть `q06-pipeline-integrity`: enhanced снизил coverage 0.500 → 0.000. Для `q08` rewrite стал английским набором ключевых слов и потерял часть ограничений. В целом enhanced coverage упал до 0.183 и получил 12 ссылок вне финального S1…S5, которые validator честно пометил invalid.

Назвать стоимость: baseline total latency **5858.2 ms** против enhanced
**18758.8 ms**; enhanced rewrite **952.7 ms**, generation **17746.7 ms**, prompt
**3075.0** tokens против **1959.8**. Причина — более длинные reranked chunks и
повторные попытки citation, а не вычисление heuristic: сам rerank занимает около
**0.5 ms**.

## 6:20–6:50 — вывод

Вывод сформулировать без объявления enhanced победителем: второй этап прозрачно улучшил retrieval, особенно filtered без rewrite, но текущий LLM rewrite и длинный контекст ухудшили answer quality. Threshold 0.45 обоснован sweep-ом, все потери и invalid citations видны в JSON.

Следующий шаг: cross-encoder или hybrid BM25+dense reranker, morphology-aware lexical score, context compression, claim-level citation checking и отдельный holdout dataset.
