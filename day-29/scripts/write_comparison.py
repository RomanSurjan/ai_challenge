#!/usr/bin/env python3
"""Render the measured comparison; run report.py after manual review first."""
import json,collections,statistics,hashlib
from pathlib import Path
root=Path(__file__).resolve().parents[1]
summary=json.loads((root/'artifacts/summary.json').read_text())
selection=json.loads((root/'artifacts/selection-frozen.json').read_text())
control=json.loads((root/'artifacts/control.json').read_text())
review=json.loads((root/'eval/review-control.json').read_text())
reg=json.loads((root/'artifacts/regression.json').read_text())
regreview=json.loads((root/'eval/review-regression.json').read_text())
def fmt(s,scale=1,unit=''):
 if not s:return 'нет наблюдений (n=0)'
 return f"{s['median']/scale:.2f}{unit} [{s['min']/scale:.2f}–{s['max']/scale:.2f}], n={s['n']}"
def tab(headers,rows):
 return '| '+' | '.join(headers)+' |\n| '+' | '.join('---' for _ in headers)+' |\n'+''.join('| '+' | '.join(str(x).replace('|','/') for x in r)+' |\n' for r in rows)
out=['# День 29 — оптимизация локальной LLM\n',
'10 октября 2026, Europe/Moscow. Apple M1 Pro, 16 GiB unified memory; Ollama 0.35.0; Go 1.26.1 darwin/arm64. Все generation/embeddings/judge локальны. Полный read-only индекс Day21 по Дням 16–20.\n',
'Содержательного улучшения на tuning-наборе не получено. Поэтому baseline остаётся рекомендацией для использования. «После» — замороженный экспериментальный кандидат для честного итогового сравнения, а не заранее объявленный победитель. Ни один новый профиль не объявлялся качественным по accepted rate.\n',
'## Профили и выбор\n',
'Baseline воспроизводит финальный Day28: qwen2.5:3b, Q4_K_M, t0, seed0, 512 tokens, ctx32768, исходные prompt/schema/validator и один repair. Паритет сообщений/schema подтверждён с историческим q03: [baseline-parity.json](artifacts/baseline-parity.json). Исторические latency не использованы для итоговой пары.\n',
'Кандидат «После»: qwen2.5:3b / Q4_K_M, t0, seed0, 768 tokens, ctx16384, prompt technical-v5. LLM выводит claims/evidence/status/clarification; текст со ссылками собирается из claims, затем проходит прежние проверки exact quotes и source IDs. 1–3 утверждения и частичный ответ разрешены. Источники и цитаты не ослаблены. В исходном контракте сохранено историческое отсутствие обязательной проверки каждого inline ID; новый renderer выдаёт ID для всех claims.\n',
'Выбор зафиксирован **до** control: [selection-frozen.json](artifacts/selection-frozen.json). 768 завершает некоторые JSON, которые обрывались на 512; 1024 не улучшил содержание. 8192 не вместил длинный tuning-запрос; 16384 вмещает все tuning input/repairs и использует меньше loaded model memory. t0 сохранён: принятый ответ t0.2 оказался нерелевантным и с неподтверждёнными claims. Q8 дал точные цитаты, но не объяснил защиту пути и ошибочно приписал Artifact MCP роль оркестратора.\n',
'## Экспериментальный протокол\n',
'До ответов сохранены [tuning](eval/tuning.json), отдельный [control](eval/control.json), [regression](eval/regression.json), [protocol](eval/protocol.json). Три tuning-вопроса: Decimal, Artifact sandbox, длинная session history. Кандидаты меняют один фактор относительно опорной конфигурации, полного декартова перебора нет. Дополнительные версии prompt исправляют выявленные ошибки до control; исходный план и amendments сохранены. Generator читает только ID/question; эталоны и оценки ему не передаются.\n',
'Итоговая пара измерена заново в одной сессии: regression 14×2 ветки, control 6 вопросов ×3 повтора ×2 ветки. Warmup отдельно, не в latency. Три повтора каждого вопроса последовательны; порядок AB/BA чередуется и внутри вопроса, и между вопросами. Retrieval один раз на пару, полные chunks/порядок/IDs одинаковы. Repairs имеют собственные prompts. Настройки после control не подгонялись.\n',
'Judge одинаков: Qwen2.5 3B Q4, t0 seed0, 128 tokens, ctx32768; оценка вспомогательная. Correctness и completeness оцениваются отдельно: 0 неверно/нерелевантно/не раскрыто, 1 частично, 2 корректно/достаточно полно. Обоснованный no-data refusal получает 2/2; ложный отказ 0/0. Grounding — отдельная ручная проверка связи quote→claim. [Разбор каждого control-ответа](eval/review-control.json), [regression](eval/review-regression.json), [tuning](eval/review-tuning.json).\n',
'## Качество контрольного набора\n']
out.append(tab(['Профиль','n','Принятые','API errors','Repairs','Корректность 0–2','Полнота 0–2','Ложные отказы','Обоснованные отказы','Retrieval misses'],[[p['label'],p['n'],p['answered'],p['api_errors'],p['repairs'],p['correctness_mean'],p['completeness_mean'],p['false_refusals'],p['justified_refusals'],p['retrieval_misses']] for p in summary['profiles']]))
out.append('\nКонтрольные принятые ответы обеих версий не раскрыли существенных частей вопроса: correctness/completeness 0/0, grounding unsupported. У каждой версии 3 justified no-data refusal, 6 false refusals, 6 retrieval miss и 3 принятых ответа. c05 и c06 относятся к корпусу; запрет absolute/.. и защита пути в найденных chunks отсутствуют. c06 относится к корпусу, однако найденные chunks не содержат защиты пути и gate не вызывает генератор; невыдуманный AES здесь не доказательство устойчивости модели к атаке. Средние включают no-data вопрос; это не доля успешных содержательных ответов. Принятый validator ответ может иметь неверные или нерелевантные claims. Неподдержанные citations/исчерпанный лимит не считаются правильным отказом на answerable вопрос.\n')
ids=list(dict.fromkeys(r['question_id'] for r in review))
rows=[]
for q in ids:
 for p in ['baseline','selected']:
  rs=[r for r in review if r['question_id']==q and r['profile']==p]
  rows.append([q,p,f"{statistics.mean(r['correctness'] for r in rs):.2f}",f"{statistics.mean(r['completeness'] for r in rs):.2f}",', '.join(sorted(set(r['grounding'] for r in rs))),'; '.join(dict.fromkeys(r['reason'] for r in rs))])
out.append(tab(['Вопрос','Профиль','Correctness','Completeness','Grounding','Разбор'],rows))
out.append('\n## JSON, цитаты и отказы\n')
out.append(tab(['Профиль','Валидный JSON / attempts','Принятые validation attempts','Exact citations опубликованных','Grounding','Статусы'],[[p['label'],f"{p['json_valid_attempts']}/{p['attempts']}",f"{p['valid_attempts']}/{p['attempts']}",p['exact_citations'],p['grounding_counts'],p['status_counts']] for p in summary['profiles']]))
out.append('\nJSON validity здесь означает отсутствие parse error в attempt, не содержательную корректность. Source IDs, exact quote, правильный chunk и claim/evidence linkage проверяются независимо. Raw calls сохраняют done_reason, input counts, payload и response. API/transport errors отделены от output_validation_failed и context_window_unfit.\n')
out.append('\n## Скорость\n')
out.append(tab(['Профиль','RAG принятых, ms','Generation принятых с repairs, ms','Tokens/sec принятых','Refusal RAG, ms','API/error RAG, ms','Полные качественные ответы, ms'],[[p['label'],fmt(p['success_ms']),fmt(p['successful_generation_ms']),fmt(p['successful_tokens_sec']),fmt(p['refusal_ms']),fmt(p['error_ms']),fmt(p['quality_success_ms'])] for p in summary['profiles']]))
out.append('\n«Принятые» = status answered, не гарантия качества. Последний столбец требует одновременно correctness=2, completeness=2 и supported grounding. Быстрые gate-отказы и ошибки исключены из скорости ответов. Tokens/sec = суммарный eval_count / (суммарный eval_duration / 1e9), включая repairs конкретной ветки. Nonstreaming: TTFT не измеряется.\n')
out.append(tab(['Профиль','Gate-отказ RAG, ms','Отказ после generation/repair, ms'],[[p['label'],fmt(p['gate_refusal_ms']),fmt(p['generated_refusal_ms'])] for p in summary['profiles']]))
out.append(tab(['Профиль','Generation всех вызванных веток, ms','load_duration, ms','prompt_eval_duration, ms'],[[p['label'],fmt(p['generation_ms']),fmt(p['load_ns'],1e6),fmt(p['prompt_eval_ns'],1e6)] for p in summary['profiles']]))
out.append('\nTotal RAG = общий retrieval + собственные generation/repair/validation/judge. wall_total_ms охватывает всю пару и инструментальные probes. При смене num_ctx Ollama может перезагрузить runner; judge всегда ctx32768 и тоже влияет на latency и память. Это часть реальной конфигурации приложения.\n')
out.append('\n## Память\n')
out.append(tab(['Профиль','GGUF на диске, GiB','Loaded /api/ps при профильном ctx, MiB','RSS runner при профильном ctx, MiB','Наблюдаемый пик target runner за всю ветку, MiB','Samples'],[[p['label'],f"{p['disk_bytes']/2**30:.3f}",fmt(p['loaded_profile_window_size'],2**20),fmt(p['rss_profile_window'],2**20),f"{p['rss_max_bytes']/2**20:.2f}",p['sampling_observations']] for p in summary['profiles']]))
out.append('\nЧастота 250 ms плюс start/end, best-effort; исходные timestamps сохранены. RSS сопоставлен точному model blob и PID, другие Ollama/embedding/idle runners также сохранены. Профильный ctx выделен по /api/ps.context_length и runner -c. Пик всей ветки может включать предыдущий контекст и judge ctx32768. У ранних pilot-файлов RSS — максимум любого процесса Ollama; их не смешиваем с target RSS итоговой пары. На Apple Silicon unified memory RSS и size_vram не складываются. CPU/GPU utilization недоступны. Нет независимых наблюдений для каждого sample: это описательные медиана/диапазон/число probes.\n')
out.append('\n## Квантование\nОфициальные Q4_K_M и Q8_0 имеют разные настоящие веса/digests, одинаковые 3085938688 параметров, 36 слоёв, tokenizer и ChatML. [Metadata](artifacts/quantization-metadata.json), [сырой парный эксперимент](artifacts/tune-quantization.json). Никакого переименования или изменения семейства/размера. Скачивание только подготовка; generation локальная. Q8 не дал содержательного преимущества на tuning; GGUF/loaded memory больше.\n')
quant=json.loads((root/'artifacts/tune-quantization.json').read_text())
qreview=json.loads((root/'eval/review-tuning.json').read_text())
qrows=[]
for name in dict.fromkeys(a['provider'] for x in quant['runs'] for a in x['run']['arms']):
 arms=[a for x in quant['runs'] for a in x['run']['arms'] if a['provider']==name]
 loaded=[m['size'] for a in arms for sample in a['resources']['samples'] for m in (sample.get('api_ps') or {}).get('models',[]) if m['name']==a['model'] and m.get('context_length')==a['profile']['context_window']]
 accepted=[a['result']['latency']['total_ms'] for a in arms if a['result']['status']=='answered']
 qrows.append([name,len(arms),sum(a['result']['status']=='answered' for a in arms),f"{arms[0]['model_metadata']['size']/2**30:.3f}",fmt({'n':len(loaded),'median':statistics.median(loaded),'min':min(loaded),'max':max(loaded)} if loaded else None,2**20),fmt({'n':len(accepted),'median':statistics.median(accepted),'min':min(accepted),'max':max(accepted)} if accepted else None)])
out.append(tab(['Квантование','n','Принятые','Модель на диске / tags, GiB','Loaded ctx16384, MiB','RAG принятых, ms'],qrows))
out.append('\nОдин tuning-повтор на три вопроса; tags.size включает служебные blobs. Q8 accepted-ответ относится к Artifact, но его ручная оценка 0/0: нерелевантен и неверно называет Artifact MCP оркестратором. Одинаковые настройки и полный контекст, источники не сокращались. Сравнивать скорость разных принятых вопросов как ускорение модели нельзя.\n')
out.append('\n## Проверка окна\nExact /tokenize уже загруженного matching model blob runner, локально по 127.0.0.1; ChatML проверен на 16 pilot calls с нулевой разницей против prompt_eval_count, далее равенство проверяется на каждом exact-tokenized ответе. Резервируется весь num_predict; repair считается заново. Если runner/ps недоступен, fallback UTF-8 byte-BPE bound+256 явно записан, может отклонить fitting input. Никакого незаметного сокращения контекста. При первом control-pilot обнаружена вариация cosine до 0.00067 между uncached/repeated embedding: IDs/text/order совпали. Для итоговых повторов сохраняется полный первый snapshot, свежий retrieval выполняется и измеряется заново, записан отдельно, а генераторы всегда видят первый snapshot. Это исправление измерительного конвейера при неизменных профилях, prompt, порогах и questions. [Amendment](eval/protocol-measurement-amendment.json). Pilot `control-pilot-drift.json` сохраняет первый pair; raw второго pair потерян из-за прежнего checkpoint после guard. Он не восстановлен выдуманными данными и не включён в итог. Финальная серия полностью повторена. 8192 непригоден для длинного tuning input; такой вызов не отправляется. [Калибровка](artifacts/tokenizer-calibration.json), [audit](artifacts/audit.json). Метод зависит от внутреннего endpoint и ChatML установленной версии.\n')
out.append('\n## Регрессионный набор\n')
rows=[]
for p in ['baseline','selected']:
 rs=[r for r in regreview if r['profile']==p];arms=[a for x in reg['runs'] for a in x['run']['arms'] if a['provider']==p]
 rows.append([p,len(rs),sum(a['result']['status']=='answered' for a in arms),f"{statistics.mean(r['correctness'] for r in rs):.2f}",f"{statistics.mean(r['completeness'] for r in rs):.2f}",dict(collections.Counter(a['result']['status'] for a in arms))])
out.append(tab(['Профиль','n','Принятые','Correctness','Completeness','Статусы'],rows))
out.append('\nНовый contract помогает отдельным вопросам и ухудшает другие. Regression не является новым holdout; исторические результаты используются только для анализа проблем, свежий файл — для сравнения.\n')
out.append('\n## Ограничения и итог\nМалые tuning/control наборы, один tuning-повтор, только три control-повтора; уверенный статистический вывод о превосходстве невозможен. Retrieval фиксирован и иногда не находит детали реализации: модель не должна восполнять их догадкой. Exact citation — происхождение, а не entailment; локальный judge может ошибаться. Содержательная полнота требует отдельной оценки. Tuning не оправдал замену baseline; tradeoff кандидата — меньше профильное окно и компактнее output при новых ложных отказах. Рекомендация baseline сохранена до control; настройка под control не выполнялась.\n')
out.append('\n## Воспроизведение и файлы\n[README](README.md) содержит команды. Все `artifacts/tune-*.json`, `regression.json`, `control.json` содержат исходные ответы/запросы/ресурсы; неудачные эксперименты сохранены. `eval/review-*.json` — ручной разбор. [Checks](artifacts/checks.md) и [сценарий видео](VIDEO_SCRIPT.md). Индекс SHA-256 неизменен: `febe8a3348d90d2d68b6a09073936d3999864049b1224b83f5bd722614d2cbad`.\n')
(root/'comparison.md').write_text('\n'.join(out))
print(root/'comparison.md')
