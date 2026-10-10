#!/usr/bin/env python3
"""Post-process only saved outputs. Evaluation labels never enter model prompts."""
import collections, hashlib, json, statistics
from pathlib import Path
root = Path(__file__).resolve().parents[1]
data = json.loads((root/'artifacts/evaluation-local.json').read_text())
rows=data['runs']; initial=rows[:14]
def mean(vals): return statistics.mean(vals) if vals else 0
def state(row): return row['run']['arms'][0]['result']
def arm(row): return row['run']['arms'][0]
def h(v): return hashlib.sha256(json.dumps(v,ensure_ascii=False,sort_keys=True).encode()).hexdigest()
quality = {
'q01-multi-mcp-routing':'Ollama HTTP 500 на generation: retrieval прошёл, ответ не получен. Это ошибка API, а не отсутствие нужных документов; raw ошибки сохранены.',
'q02-monitor-claim':'Точное, но нерелевантное сути утверждение о первом запуске. Атомарный claim/lease и защита от дублей не объяснены. Judge supported не означает правильный полный ответ.',
'q03-decimal-currency':'Направление верное: decimal избегает двоичной арифметики. Полнота низкая: отсутствуют коэффициент/scale и операции. Цитаты точны, но выбранная строка комментария обрывается до обоснования, а package/import ничего не доказывает.',
'q04-github-http-safety':'Ложный отказ формата: обе генерации оборвались на лимите; сведения о HTTP-ограничениях есть в найденных документах.',
'q05-agent-tool-loop':'Ложный отказ формата: обе генерации оборвались на лимите. Найденный контекст содержит tool loop.',
'q06-pipeline-integrity':'Ложный отказ формата: claim не вошёл в answer. Retrieval предоставил pipeline snippets.',
'q07-artifact-sandbox':'Ложный отказ формата: обе генерации исчерпали лимит 512 tokens и не дали завершённый JSON. Нужные документы есть. Предварительный прогон с усечённым контекстом цитировал только имя инструмента; это не качественный ответ.',
'q08-session-history':'Частичный ответ о кнопке нового диалога. Нет объяснения session cookie/store. DELETE handler релевантен кнопке, сообщение ошибки загрузки истории не доказывает claim.',
'q09-mcp-handshake':'Ложный отказ формата: обе попытки исчерпали 512 tokens и закончились незавершённым JSON. Прежний accepted ListTools ответ принадлежит предварительному прогону, не итоговой версии.',
'q10-wikipedia-search':'Ложный отказ формата: claim не вошёл в answer; контекст retrieval существует.',
'p01-paraphrase':'Ложный отказ retrieval: этот перефразированный вопрос относится к decimal корпуса, но max cosine ниже 0.55. Generation не вызывалась; порог не подгоняли.',
'p02-outside':'Обоснованный отказ: сведения о GPU развёртывания в индексированном корпусе не найдены; final context пуст. Нынешний hardware Ollama не является источником корпуса.',
'p03-false-premise':'Безопасный отказ формата, но модель не продемонстрировала корректировку ложной предпосылки. Это не полноценный успешный ответ на отрицательную проверку.',
'p04-injection':'Обоснованный gate-отказ на просьбу выдумать детали. Generation не вызывалась; устойчивость генератора к injection данным опытом не проверена.'}
lines=['# День 28 — измерения локального RAG', '', 'Дата: 8 октября 2026 (Europe/Moscow). Это малый инженерный прогон, а не статистическое сравнение качества моделей.', '',
'Облачное сравнение **не выполнено**: ключ DeepSeek / deepseek-v4-flash найден в существующем server-side .env, но автоматическая проверка дважды отклонила передачу корпуса в этот внешний API. Запрошено отдельное разрешение; облачных ответов и latency нет. Состояние default UI — cloud disabled; состояние not_configured также проверено отдельно без ключа.', '',
'## Протокол и воспроизводимость', '',
'Переиспользованы компоненты Day 24 (stateless ветка Day 25): индекс, embedding client, cosine top-20, Unicode reranker, gate, structured prompt, exact-quote validator, один repair, локальный entailment judge. Новые Go tests не добавлялись; перенесены существующие tests.', '',
'14 заранее фиксированных вопросов: 10 из day-22/eval/questions.json + paraphrase/OOD/false premise/injection. Затем по 3 дополнительных повтора q01/q03/q07: **23 запроса** итоговой версии. Набор после ответов не менялся. История/memory/rewrite выключены. Temperature=0, seed=0 (Ollama), 512 tokens/answer, 128/judge, explicit num_ctx=32768; qwen2.5:3b / qwen3-embedding:0.6b; Ollama 0.35.0; Go 1.26.1 darwin/arm64.', '',
'Candidate-K 20, final-K 5, cosine chunk ≥0.45, max final cosine gate ≥0.55; rerank веса 0.70/0.20/0.10. Цитаты 20–160 Unicode символов. Judge остаётся локальным и не блокирует answer: его verdict — только вспомогательная оценка. Один короткий claim ограничивает полноту.', '',
'При compare retrieval/embedding выполняются один раз. Context hash включает упорядоченные final chunks с metadata; общий initial messages hash — system/user. Repair prompts зависят от собственного ответа и могут различаться. В raw calls сохранены каждый prompt, hash, requested/actual model, tokens и duration. Expected answers/concepts evaluator не передаёт: в benchmark structs читаются только ID/question.', '',
'Локальный API использует Day 24 JSON Schema, DeepSeek — JSON object по [официальному контракту](https://api-docs.deepseek.com/guides/json_mode/). Тексты prompts и ответный contract одинаковы, decoding различается: будущий парный результат будет сравнивать генераторы вместе с доступным им constrained decoding, а не изолированную модельную способность.', '',
'Индекс остаётся read-only. SHA-256 до и после: `'+data['metadata']['index_sha256']+'`. Corpus ID `'+data['metadata']['index']['corpus_id']+'`, 106 документов, 1025 chunks, dimension 1024. Модели и их digests сохранены в raw metadata; индекс содержит только Day 16–20, файлы текущего Day 28 не добавляются.', '',
'File SHA-256 dataset: `'+hashlib.sha256((root/'eval/questions.json').read_bytes()).hexdigest()+'`; protocol: `'+hashlib.sha256((root/'eval/protocol.json').read_bytes()).hexdigest()+'`. Поля dataset_sha256/protocol_sha256 в raw — SHA-256 compact JSON, а не исходных байтов файла.', '',
'## Качество: исходный проход', '', '| Вопрос | Статус | Оценка по документам |','|---|---|---|']
for row in initial: lines.append(f"| {row['question_id']} | {state(row)['status']} / {state(row).get('abstention_reason','—')} | {quality[row['question_id']]} |")
counts=collections.Counter(state(row)['status'] for row in rows); reasons=collections.Counter(state(row).get('abstention_reason','answered') for row in rows)
answered=[state(row) for row in rows if state(row)['status']=='answered']; called=[state(row) for row in rows if state(row)['answer_model_called']]
allcalls=[c for row in rows for c in arm(row)['calls']]
lines += ['',f"Во всех 23 запросах: {dict(counts)}; причины: {dict(reasons)}. Generation вызвана в {len(called)} запросах; gate-skipped {len(rows)-len(called)}. Repair вызовов: {sum(max(0,len(arm(row)['calls'])-1) for row in rows)}.", '',
f"Принятые ответы имеют {sum(len(r['citations']) for r in answered)} дословных citations, все exact_match=true. Это проверка происхождения. Не все citations достаточны для заявленного claim; semantic judge: {dict(collections.Counter(j['verdict'] for r in answered for j in r['entailment_verdicts']))}.", '',
'Модель иногда не добавляет inline [S] в answer даже при JSON Schema pattern. Унаследованный application validator требует source/quote linkage, но не требует присутствия каждого ID в тексте. UI добавляет кнопки «Открыть S…» из **валидированных** sources; сами исходные ответы и quotes не переписываются. Не называем это стопроцентной корректностью текстовых inline citations.', '',
'Нет доказательства полноты принятых ответов. Ручной разбор показывает явно нерелевантный ответ q02, неполные q03/q08 и слабое доказательство claims. q07/q09 теперь отказали; прежние accepted ответы из усечённого контекста остаются в pilot. Отклонённые raw outputs остаются в файле. Keyword coverage не использовалась как итоговый показатель; judge не заменяет этот разбор.', '',
'## Скорость', '',
'Не streaming: TTFT отсутствует. Total включает embedding+search/rerank+generation с repairs+validation+judge. Для compare per-arm total включал бы общую retrieval стоимость и собственную модельную ветку; wall_total_ms охватывает обе ветки. Числа ниже только локальные.', '',
'| Вопрос | Embedding ms | Поиск/rerank ms | Generation ms | Judge ms | Total ms | Context/answer runes | Completion tokens | Calls |','|---|---:|---:|---:|---:|---:|---:|---:|---:|']
for row in initial:
 r=state(row);t=r['latency'];lines.append(f"| {row['question_id']} | {t['embedding_ms']} | {t['retrieval_ms']+t['filter_rerank_ms']} | {t['generation_ms'] if r['answer_model_called'] else 'skipped'} | {t['judge_ms']} | {t['total_ms']} | {r['context_runes']}/{r['answer_runes']} | {r['token_usage']['generator']['completion_tokens']} | {len(arm(row)['calls'])} |")
first=state(rows[0]);warmcalled=[state(row)['latency']['total_ms'] for row in rows[1:] if state(row)['answer_model_called']]
lines += ['',f"Первый запрос нового процесса: {first['latency']['total_ms']} ms. Ollama уже работал; первое обращение с увеличенным окном может перезагрузить runner. Это не контролируемый cold hardware start. Прогретые/последующие запросы с generation: n={len(warmcalled)}, median {statistics.median(warmcalled):.0f} ms, range {min(warmcalled)}–{max(warmcalled)} ms. Gate-skipped запросы исключены; остальные включают attempted generation с HTTP/format failures. Это не распределение времени успешных ответов.", '',
'Tokens/sec вычислено только для Ollama по достоверным eval_count/eval_duration, на уровне отдельного call (в raw). Не делим completion tokens на total RAG latency и не оцениваем облачный throughput. Token counts включают неудачные/repair генерации; answer_runes относится только к опубликованному ответу.', '',
'## Стабильность', '', '| Повторы (по 3, отдельно от первого прохода) | Total ms min/median/max | Разных опубликованных текстов | Разных raw generations | Статусы |','|---|---|---:|---:|---|']
for q in ['q01-multi-mcp-routing','q03-decimal-currency','q07-artifact-sandbox']:
 subset=[row for row in rows if row['question_id']==q and row['phase']=='repeat'];times=[state(row)['latency']['total_ms'] for row in subset]
 lines.append(f"| {q} | {min(times)}/{statistics.median(times):.0f}/{max(times)} | {len({h(state(row)['final_answer']) for row in subset})} | {len({h(state(row)['raw_structured_model_outputs']) for row in subset}) if any(state(row)['raw_structured_model_outputs'] for row in subset) else 'не получены'} | {dict(collections.Counter(state(row)['status'] for row in subset))} |")
lines += ['',f"HTTP/transport generation errors в итоговом benchmark: {sum(bool(c.get('error')) for c in allcalls)} из {len(allcalls)} generation calls; runtime statuses error: {sum(state(row)['status']=='error' for row in rows)}. Ошибки формата/citations — отдельные validation_attempts; безопасный отказ не является успешным содержательным ответом. Judge errors: {sum(bool(j.get('error')) for row in rows for j in state(row)['entailment_verdicts'])}. Timeouts в этом наборе не наблюдались.", '',
f'Отдельно: HTTP API errors {sum("HTTP" in c.get("error","") for c in allcalls)}, TCP/DNS errors {sum(bool(c.get("error")) and "HTTP" not in c.get("error","") for c in allcalls)}. У failed HTTP calls token counts и raw generation не получены, поэтому throughput/content variability для них неизвестны.', '', 'HTTP 500 считается отказом модели/API при установленном соединении; это не TCP/DNS ошибка. Ошибки JSON/quote validation отделены от transport/API.', '', 'Temperature=0 не гарантирует идентичность. Разные raw outputs и latency надо отделять от транспортной надёжности. Три повторения не позволяют оценить общую вероятностную стабильность системы.', '',
'## Сохранённые предварительные эксперименты', '',
'pilot-json-object.json: те же 23 вопроса/повтора с обычным JSON-mode вместо constrained schema, до исправления интеграции. Этот raw файл сохранён полностью; нельзя смешивать его latency с итоговой версией. Фиксированные вопросы прежние; protocol-json-object.json хранит прежний фиксированный план; JSON-mode зафиксирован в metadata pilot. После изучения ошибок была возвращена исходная Day 24 JSON Schema для всех вопросов, без question-specific prompts/threshold tuning.', '',
'pilot-default-context.json / .md: полный предыдущий прогон с неявным окном Ollama. Для длинных prompts неоднократно возвращалось ровно 2050 prompt tokens. В финальной версии num_ctx=32768 задан явно для generation и judge; успешные длинные requests теперь имеют >2050 prompt tokens. Все результаты старого окна сохранены, но не смешиваются с итоговыми latency/качеством.', '', '## UI, API и автономность', '',
'Подробный журнал пользовательского прогона: ui-testing.md; raw UI requests и screenshots: artifacts/ui/. Offline-проверка и её границы: offline.md. Видео не записано: сохранены настоящие screenshots/DOM snapshots и точный сценарий VIDEO_SCRIPT.md.', '',
'## Файлы', '',
'- evaluation-local.json — итоговые 23 запроса, prompts, исходные ответы, candidates, quotes, judge, latency, tokens, models.',
'- pilot-default-context.json — результаты с неявным контекстным окном.',
'- pilot-json-object.json — не скрытые неудачные результаты предварительного прогона.',
'- index-before.json / index-after.json — метаданные и неизменность индекса.',
'- ui/requests.json — реальные backend traces пользовательских UI-запросов.',
'- offline.md и offline-* — процессный запрет исходящих подключений и локальный RAG-прогон.',
'- checks.md — результаты штатных проверок.']
(root/'artifacts/comparison.md').write_text('\n'.join(lines)+'\n')
summary={'runs':len(rows),'status_counts':dict(counts),'reason_counts':dict(reasons),'generator_calls':len(allcalls),'generation_requests':len(called),'repair_calls':sum(max(0,len(arm(row)['calls'])-1) for row in rows),'cloud_comparison':'blocked_pending_specific_approval','manual_review':quality}
(root/'artifacts/summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(summary,ensure_ascii=False,indent=2))
