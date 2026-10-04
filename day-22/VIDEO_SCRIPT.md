# Сценарий видео — Day 22 (5–7 минут)

## 0:00–0:35 — цель

Показать схему из README: один вопрос идёт либо прямо в локальную chat-модель,
либо через embedding, top-k готового индекса и grounded prompt. Подчеркнуть:
сравниваются не разные LLM, а два режима одной модели с temperature 0.

## 0:35–1:10 — Day 21 остаётся готовым backend

Открыть заголовок `../day-21/artifacts/index-structural.json`: schema 1,
`structural`, corpus ID `b7d4f…cc451`, embedding `qwen3-embedding:0.6b`, dimension
1024, 1 025 chunks. Показать, что в `day-22` нет corpus loader, PDF extraction,
chunker или команды index; индекс только читается и валидируется.

## 1:10–1:35 — CLI и модели

Показать `ollama list`, `.env.example` и один CLI `cmd/rag-agent`. Объяснить выбор
`qwen2.5:3b` для стабильного instruction-чата на M1 Pro. Указать, что оба режима
используют одну модель, system prompt, temperature 0 и max tokens 512.

## 1:35–2:05 — plain

Выполнить:

```bash
make ask-plain QUESTION="Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?"
```

Прочитать ключевую часть ответа и отметить отсутствие retrieved chunks и citations.

## 2:05–3:10 — RAG на том же вопросе

Выполнить `make ask-rag` с тем же `QUESTION`. Показать порядок до генерации:
query embedding → cosine → stable top-5. В выводе остановиться на rank/score,
`source`, `section`, `chunk_id`, затем показать citations и обе latency.

Открыть функцию grounded prompt: полный текст находится внутри
`BEGIN/END_UNTRUSTED_CONTEXT`, документы объявлены данными, требуются `[S1]` и
честный отказ при нехватке контекста. Показать, что invalid ID не принимается.

## 3:10–3:35 — both

Выполнить `make ask` и показать чётко разделённые блоки PLAIN/RAG. Обратить внимание,
что `both` запускает plain первым, затем RAG, а generation settings не меняются.

## 3:35–5:40 — evaluation из 10 вопросов

Открыть `eval/questions.json`: human expectations используются только после ответа.
Затем открыть `artifacts/comparison.md` и показать общую таблицу.

<!-- VIDEO_ACTUAL_START -->
Назвать точные числа финального запуска:

- concept coverage: plain **0.250**, RAG **0.417**;
- все concepts: plain **0.000**, RAG **0.100**;
- expected-source recall: top-5 **0.442**, citations **0.333**;
- invalid citations **0**, ошибок **0**;
- средняя retrieval latency **148.5 ms**;
- generation latency: plain **3040.0 ms**, RAG **8525.0 ms**;
- средняя длина: plain **418.5**, RAG **654.3** Unicode-символа.

Случай улучшения — `q05-agent-tool-loop`: coverage 0.667 → 1.000. Показать
`role=tool`, исходный `tool_call_id`, следующий sampling и пять валидных citations.

Случай без улучшения — `q07-artifact-sandbox`: 0.000 → 0.000. Первый retrieved
chunk содержит только константу SaveTool, а нужный `artifact/store.go` не попал в
top-5. Ещё один наглядный промах — `q10-wikipedia-search`: размеченные Day 19
sources отсутствуют в top-5, оба source recall равны 0.
<!-- VIDEO_ACTUAL_END -->

Показать один вопрос, где RAG повысил concept coverage, вместе с retrieved/cited
sources. Затем показать один вопрос, где RAG не помог, потерял concept или получил
нерелевантный top-k: это обязательный компромисс, а не скрываемая ошибка.

## 5:40–6:30 — честный вывод

Проговорить, что substring coverage не равно semantic correctness, source recall не
гарантирует поддержку каждого claim, а локальные latency зависят от прогрева. Итог
формулировать строго по фактической таблице, не объявляя RAG победителем заранее.
Завершить следующими шагами: query rewriting, hybrid retrieval, reranking, context
compression, claim-level citation verification и blinded LLM-as-judge.
