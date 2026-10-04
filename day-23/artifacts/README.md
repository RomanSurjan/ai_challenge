# Day 23 generated artifacts

Это результаты реальных локальных моделей Ollama, не mocks:

- `threshold-sweep.json` / `.md` — retrieval-only sweep семи thresholds;
- `evaluation.json` — четыре режима, ответы, rewrite, candidates, rejected/final context, citations, latency, usage и метрики;
- `comparison.md` — читаемый отчёт из того же evaluation object.

Пересоздание: `make sweep` и `make eval`. Большие индексы Day 21 сюда не копируются.
