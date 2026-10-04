# Сценарий демо Day 21 (5–7 минут)

Все команды выполняются из:

```bash
cd /Users/romansurzhan/ai_challenge/day-21
```

## 0:00–0:40 — цель и архитектура

Показать Mermaid-диаграмму в `README.md` и кратко проговорить pipeline:
документы предыдущих дней, два способа chunking, локальная Ollama, два
JSON-индекса, semantic search и одинаковый evaluation set.

## 0:40–1:25 — корпус больше 30 страниц

```bash
make corpus
python3 -m json.tool artifacts/corpus-manifest.json | sed -n '1,45p'
```

В выводе CLI показать `documents`, `words`, `estimated_pages` и `corpus_id`.
Подчеркнуть формулу `total_words / 400` и значение больше 30. В одном document
показать source, type, chars, words, lines/pages и SHA-256.

## 1:25–2:25 — две реально разные стратегии

Открыть `internal/chunk/structural.go` рядом с `internal/chunk/chunk.go`.
Пояснить:

- fixed: окно 700 слов, overlap 100, section всегда `Document`;
- structural: Markdown heading path, Go AST declaration, TXT paragraph, PDF page;
- ни один чанк не пересекает файл или структурный раздел.

Запустить реальную индексацию:

```bash
ollama list
make index
ls -lh artifacts/index-fixed.json artifacts/index-structural.json
```

## 2:25–3:10 — метаданные и manifests индексов

Показать корневые поля и первый чанк без печати всего embedding:

```bash
python3 - <<'PY'
import json
for name in ('fixed', 'structural'):
    p = f'artifacts/index-{name}.json'
    data = json.load(open(p))
    chunk = dict(data['chunks'][0])
    chunk['embedding'] = chunk['embedding'][:6] + ['…']
    chunk['text'] = chunk['text'][:180] + '…'
    print(name, {k: data[k] for k in (
        'schema_version', 'strategy', 'chunking', 'model',
        'embedding_dimension', 'corpus_id', 'stats')})
    print(chunk)
PY
```

Обратить внимание на стабильный `chunk_id`, source, section, rune offsets,
word_count и настоящие числовые значения embedding.

## 3:10–4:20 — один запрос к обоим индексам

```bash
go run ./cmd/rag search \
  --index artifacts/index-fixed.json \
  --query "Как агент маршрутизирует вызовы MCP?" --top-k 5

go run ./cmd/rag search \
  --index artifacts/index-structural.json \
  --query "Как агент маршрутизирует вызовы MCP?" --top-k 5
```

Сопоставить rank, score, source, section и snippet. У fixed section намеренно
`Document`; structural должен показывать имя заголовка или Go declaration.

## 4:20–5:35 — объективное сравнение

```bash
make compare
sed -n '1,120p' artifacts/comparison.md
```

Показать одну общую таблицу: Hit@1, Hit@5, MRR@5, chunks, min/avg/max размер,
build time, JSON size, average top-1 similarity. Затем показать 2–3 примера
выдачи ниже таблицы. Сказать, что оба индекса построены на одинаковом corpus ID
и модели, а релевантность размечена в `eval/questions.json`.

## 5:35–6:20 — тесты и честный итог

```bash
make test
```

Проговорить фактический conclusion из `artifacts/comparison.md`; не называть
structural победителем, если MRR@5/Hit@5 этого не подтверждают. В конце
упомянуть следующие шаги: hybrid BM25 + vectors, reranking, incremental index и
SQLite/FAISS вместо полного JSON scan.

## Резерв на вопросы — до 0:40

Если остаётся время, показать негативный сценарий: остановленная Ollama даёт
понятную ошибку и не создаёт индекс с фиктивными embeddings.
