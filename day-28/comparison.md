# День 28 — измерения локального RAG

Итоговый текстовый отчёт по ранее выполненным измерениям. Сырые результаты,
логи и материалы демонстрации удалены; упомянутые артефакты можно получить
повторным локальным прогоном, они исключены из Git.

Дата: 8 октября 2026 (Europe/Moscow). Это малый инженерный прогон, а не статистическое сравнение качества моделей.

Облачное сравнение **не выполнено**: ключ DeepSeek / deepseek-v4-flash найден в существующем server-side .env, но автоматическая проверка дважды отклонила передачу корпуса в этот внешний API. Запрошено отдельное разрешение; облачных ответов и latency нет. Состояние default UI — cloud disabled; состояние not_configured также проверено отдельно без ключа.

## Протокол и воспроизводимость

Переиспользованы компоненты Day 24 (stateless ветка Day 25): индекс, embedding client, cosine top-20, Unicode reranker, gate, structured prompt, exact-quote validator, один repair, локальный entailment judge. Новые Go tests не добавлялись; перенесены существующие tests.

14 заранее фиксированных вопросов: 10 из day-22/eval/questions.json + paraphrase/OOD/false premise/injection. Затем по 3 дополнительных повтора q01/q03/q07: **23 запроса** итоговой версии. Набор после ответов не менялся. История/memory/rewrite выключены. Temperature=0, seed=0 (Ollama), 512 tokens/answer, 128/judge, explicit num_ctx=32768; qwen2.5:3b / qwen3-embedding:0.6b; Ollama 0.35.0; Go 1.26.1 darwin/arm64.

Candidate-K 20, final-K 5, cosine chunk ≥0.45, max final cosine gate ≥0.55; rerank веса 0.70/0.20/0.10. Цитаты 20–160 Unicode символов. Judge остаётся локальным и не блокирует answer: его verdict — только вспомогательная оценка. Один короткий claim ограничивает полноту.

При compare retrieval/embedding выполняются один раз. Context hash включает упорядоченные final chunks с metadata; общий initial messages hash — system/user. Repair prompts зависят от собственного ответа и могут различаться. В raw calls сохранены каждый prompt, hash, requested/actual model, tokens и duration. Expected answers/concepts evaluator не передаёт: в benchmark structs читаются только ID/question.

Локальный API использует Day 24 JSON Schema, DeepSeek — JSON object по [официальному контракту](https://api-docs.deepseek.com/guides/json_mode/). Тексты prompts и ответный contract одинаковы, decoding различается: будущий парный результат будет сравнивать генераторы вместе с доступным им constrained decoding, а не изолированную модельную способность.

Индекс остаётся read-only. SHA-256 до и после: `febe8a3348d90d2d68b6a09073936d3999864049b1224b83f5bd722614d2cbad`. Corpus ID `b7d4f83675e1fc7c8d1c112bee933610e15ecd5aa5ade9e6d30a7bbc070cc451`, 106 документов, 1025 chunks, dimension 1024. Модели и их digests сохранены в raw metadata; индекс содержит только Day 16–20, файлы текущего Day 28 не добавляются.

File SHA-256 dataset: `62671ed838d4c3a7ee00c4db6d6c5422f875eb179fba4e3d4993a5dd1fe822a2`; protocol: `163b4fc01d291b2944ecca21225d1d52ec5977197976012686c293db0c35bca2`. Поля dataset_sha256/protocol_sha256 в raw — SHA-256 compact JSON, а не исходных байтов файла.

## Качество: исходный проход

| Вопрос | Статус | Оценка по документам |
|---|---|---|
| q01-multi-mcp-routing | error / runtime_error | Ollama HTTP 500 на generation: retrieval прошёл, ответ не получен. Это ошибка API, а не отсутствие нужных документов; raw ошибки сохранены. |
| q02-monitor-claim | answered / — | Точное, но нерелевантное сути утверждение о первом запуске. Атомарный claim/lease и защита от дублей не объяснены. Judge supported не означает правильный полный ответ. |
| q03-decimal-currency | answered / — | Направление верное: decimal избегает двоичной арифметики. Полнота низкая: отсутствуют коэффициент/scale и операции. Цитаты точны, но выбранная строка комментария обрывается до обоснования, а package/import ничего не доказывает. |
| q04-github-http-safety | insufficient_context / output_validation_failed | Ложный отказ формата: обе генерации оборвались на лимите; сведения о HTTP-ограничениях есть в найденных документах. |
| q05-agent-tool-loop | insufficient_context / output_validation_failed | Ложный отказ формата: обе генерации оборвались на лимите. Найденный контекст содержит tool loop. |
| q06-pipeline-integrity | insufficient_context / output_validation_failed | Ложный отказ формата: claim не вошёл в answer. Retrieval предоставил pipeline snippets. |
| q07-artifact-sandbox | insufficient_context / output_validation_failed | Ложный отказ формата: обе генерации исчерпали лимит 512 tokens и не дали завершённый JSON. Нужные документы есть. Предварительный прогон с усечённым контекстом цитировал только имя инструмента; это не качественный ответ. |
| q08-session-history | answered / — | Частичный ответ о кнопке нового диалога. Нет объяснения session cookie/store. DELETE handler релевантен кнопке, сообщение ошибки загрузки истории не доказывает claim. |
| q09-mcp-handshake | insufficient_context / output_validation_failed | Ложный отказ формата: обе попытки исчерпали 512 tokens и закончились незавершённым JSON. Прежний accepted ListTools ответ принадлежит предварительному прогону, не итоговой версии. |
| q10-wikipedia-search | insufficient_context / output_validation_failed | Ложный отказ формата: claim не вошёл в answer; контекст retrieval существует. |
| p01-paraphrase | insufficient_context / low_relevance | Ложный отказ retrieval: этот перефразированный вопрос относится к decimal корпуса, но max cosine ниже 0.55. Generation не вызывалась; порог не подгоняли. |
| p02-outside | insufficient_context / empty_context | Обоснованный отказ: сведения о GPU развёртывания в индексированном корпусе не найдены; final context пуст. Нынешний hardware Ollama не является источником корпуса. |
| p03-false-premise | insufficient_context / output_validation_failed | Безопасный отказ формата, но модель не продемонстрировала корректировку ложной предпосылки. Это не полноценный успешный ответ на отрицательную проверку. |
| p04-injection | insufficient_context / low_relevance | Обоснованный gate-отказ на просьбу выдумать детали. Generation не вызывалась; устойчивость генератора к injection данным опытом не проверена. |

Во всех 23 запросах: {'error': 4, 'answered': 6, 'insufficient_context': 13}; причины: {'runtime_error': 4, 'answered': 6, 'output_validation_failed': 10, 'low_relevance': 2, 'empty_context': 1}. Generation вызвана в 20 запросах; gate-skipped 3. Repair вызовов: 10.

Принятые ответы имеют 11 дословных citations, все exact_match=true. Это проверка происхождения. Не все citations достаточны для заявленного claim; semantic judge: {'supported': 1, 'unverifiable': 5}.

Модель иногда не добавляет inline [S] в answer даже при JSON Schema pattern. Унаследованный application validator требует source/quote linkage, но не требует присутствия каждого ID в тексте. UI добавляет кнопки «Открыть S…» из **валидированных** sources; сами исходные ответы и quotes не переписываются. Не называем это стопроцентной корректностью текстовых inline citations.

Нет доказательства полноты принятых ответов. Ручной разбор показывает явно нерелевантный ответ q02, неполные q03/q08 и слабое доказательство claims. q07/q09 теперь отказали; прежние accepted ответы из усечённого контекста остаются в pilot. Отклонённые raw outputs остаются в файле. Keyword coverage не использовалась как итоговый показатель; judge не заменяет этот разбор.

## Скорость

Не streaming: TTFT отсутствует. Total включает embedding+search/rerank+generation с repairs+validation+judge. Для compare per-arm total включал бы общую retrieval стоимость и собственную модельную ветку; wall_total_ms охватывает обе ветки. Числа ниже только локальные.

| Вопрос | Embedding ms | Поиск/rerank ms | Generation ms | Judge ms | Total ms | Context/answer runes | Completion tokens | Calls |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| q01-multi-mcp-routing | 842 | 2 | 23251 | 0 | 24098 | 12089/69 | 0 | 1 |
| q02-monitor-claim | 113 | 1 | 4357 | 734 | 5207 | 761/32 | 107 | 1 |
| q03-decimal-currency | 53 | 1 | 9720 | 698 | 10475 | 3933/148 | 206 | 1 |
| q04-github-http-safety | 41 | 1 | 34446 | 0 | 34491 | 3185/69 | 1024 | 2 |
| q05-agent-tool-loop | 59 | 1 | 36448 | 0 | 36511 | 5483/69 | 1024 | 2 |
| q06-pipeline-integrity | 45 | 4 | 24896 | 0 | 24949 | 15093/69 | 335 | 2 |
| q07-artifact-sandbox | 48 | 1 | 44402 | 0 | 44454 | 12160/69 | 1024 | 2 |
| q08-session-history | 70 | 1 | 26392 | 682 | 27150 | 22128/50 | 163 | 1 |
| q09-mcp-handshake | 110 | 4 | 34842 | 0 | 34957 | 3201/69 | 1024 | 2 |
| q10-wikipedia-search | 96 | 1 | 16708 | 0 | 16808 | 8672/69 | 294 | 2 |
| p01-paraphrase | 48 | 2 | skipped | 0 | 50 | 4203/69 | 0 | 0 |
| p02-outside | 38 | 1 | skipped | 0 | 39 | 0/69 | 0 | 0 |
| p03-false-premise | 34 | 1 | 22094 | 0 | 22133 | 12492/69 | 310 | 2 |
| p04-injection | 50 | 1 | skipped | 0 | 51 | 2091/69 | 0 | 0 |

Первый запрос нового процесса: 24098 ms. Ollama уже работал; первое обращение с увеличенным окном может перезагрузить runner. Это не контролируемый cold hardware start. Прогретые/последующие запросы с generation: n=19, median 22133 ms, range 5207–45565 ms. Gate-skipped запросы исключены; остальные включают attempted generation с HTTP/format failures. Это не распределение времени успешных ответов.

Tokens/sec вычислено только для Ollama по достоверным eval_count/eval_duration, на уровне отдельного call (в raw). Не делим completion tokens на total RAG latency и не оцениваем облачный throughput. Token counts включают неудачные/repair генерации; answer_runes относится только к опубликованному ответу.

## Стабильность

| Повторы (по 3, отдельно от первого прохода) | Total ms min/median/max | Разных опубликованных текстов | Разных raw generations | Статусы |
|---|---|---:|---:|---|
| q01-multi-mcp-routing | 10092/10184/21044 | 1 | не получены | {'error': 3} |
| q03-decimal-currency | 6267/6724/11007 | 1 | 2 | {'answered': 3} |
| q07-artifact-sandbox | 34986/34999/45565 | 1 | 3 | {'insufficient_context': 3} |

HTTP/transport generation errors в итоговом benchmark: 4 из 30 generation calls; runtime statuses error: 4. Ошибки формата/citations — отдельные validation_attempts; безопасный отказ не является успешным содержательным ответом. Judge errors: 0. Timeouts в этом наборе не наблюдались.

Отдельно: HTTP API errors 4, TCP/DNS errors 0. У failed HTTP calls token counts и raw generation не получены, поэтому throughput/content variability для них неизвестны.

HTTP 500 считается отказом модели/API при установленном соединении; это не TCP/DNS ошибка. Ошибки JSON/quote validation отделены от transport/API.

Temperature=0 не гарантирует идентичность. Разные raw outputs и latency надо отделять от транспортной надёжности. Три повторения не позволяют оценить общую вероятностную стабильность системы.

## Сохранённые предварительные эксперименты

pilot-json-object.json: те же 23 вопроса/повтора с обычным JSON-mode вместо constrained schema, до исправления интеграции. Этот raw файл сохранён полностью; нельзя смешивать его latency с итоговой версией. Фиксированные вопросы прежние; protocol-json-object.json хранит прежний фиксированный план; JSON-mode зафиксирован в metadata pilot. После изучения ошибок была возвращена исходная Day 24 JSON Schema для всех вопросов, без question-specific prompts/threshold tuning.

pilot-default-context.json / .md: полный предыдущий прогон с неявным окном Ollama. Для длинных prompts неоднократно возвращалось ровно 2050 prompt tokens. В финальной версии num_ctx=32768 задан явно для generation и judge; успешные длинные requests теперь имеют >2050 prompt tokens. Все результаты старого окна сохранены, но не смешиваются с итоговыми latency/качеством.

## UI, API и автономность

Пользовательский прогон и offline-проверка были выполнены локально. Их журналы, снимки экрана и материалы демонстрации удалены из репозитория.

## Файлы

- evaluation-local.json — итоговые 23 запроса, prompts, исходные ответы, candidates, quotes, judge, latency, tokens, models.
- pilot-default-context.json — результаты с неявным контекстным окном.
- pilot-json-object.json — не скрытые неудачные результаты предварительного прогона.
- index-before.json / index-after.json — метаданные и неизменность индекса.
- ui/requests.json — реальные backend traces пользовательских UI-запросов.
- offline.md и offline-* — процессный запрет исходящих подключений и локальный RAG-прогон.
- checks.md — результаты штатных проверок.
