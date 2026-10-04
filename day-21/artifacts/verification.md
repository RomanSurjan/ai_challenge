# Verification record

Дата проверки: 2026-10-04 (Europe/Moscow).

## Среда

- Go `1.26.1 darwin/arm64`;
- Ollama `0.35.0`, endpoint `http://127.0.0.1:11434/api/embed`;
- модель `qwen3-embedding:0.6b`, локальный размер 639 MB;
- Metal compute: Apple M1 Pro;
- embedding dimension: 1024.

## Корпус

- 106 документов;
- 485 464 Unicode-символа;
- 49 738 слов;
- 13 075 строк;
- 124.345 условной страницы;
- corpus ID: `b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451`.

## Выполнено успешно

- `ollama pull qwen3-embedding:0.6b`;
- `go run ./cmd/rag corpus ...`;
- `go run ./cmd/rag index --strategy all ...`;
- semantic search по fixed и structural индексам;
- `go run ./cmd/rag compare ...` на 12 вопросах;
- `gofmt -w .`;
- `go test ./...`;
- `go vet ./...`.

## Фактические результаты

| Strategy | Chunks | Dimension | Build | JSON bytes | Hit@1 | Hit@5 | MRR@5 | Avg top-1 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| fixed | 136 | 1024 | 64.713 s | 3,605,360 | 0.250 | 1.000 | 0.540 | 0.5769 |
| structural | 1,025 | 1024 | 67.966 s | 23,407,999 | 0.417 | 0.833 | 0.611 | 0.6612 |

Structural лучше ранжировал релевантный источник в первых позициях, а fixed
дал лучший Hit@5 и существенно меньший индекс. Подробные примеры находятся в
`comparison.md`.

## Оставшееся внешнее ограничение

`pdftotext` не установлен, но текущий корпус PDF не содержит. Для будущего PDF
ввода нужен Poppler (`brew install poppler`); загрузчик уже возвращает понятную
ошибку и имеет unit-тест PDF adapter.
