# Day 24 real artifacts

Все результаты получены локальными `qwen3-embedding:0.6b` и `qwen2.5:3b`, не mocks:

- `gate-calibration.json` / `.md` — retrieval-only calibration на 10 positive + 6 OOD;
- `evaluation.json` — 10 вопросов в `day23`, `grounded`, `strict` с raw outputs, validation, sources, exact quotes, judge, latency и tokens;
- `comparison.md` — readable main report и три подробных примера;
- `abstention-evaluation.json` / `abstention-comparison.md` — strict на 6 OOD-вопросах.

Пересоздание: `make calibrate-gate`, `make eval`, `make eval-abstention`.
Structural index Day 21 не копируется и открывается только для чтения.
