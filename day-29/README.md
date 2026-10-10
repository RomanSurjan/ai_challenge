# День 29 — оптимизация локальной LLM

Логи, скриншоты, видео и сырые результаты прогонов удалены из репозитория.
Каталог `artifacts/` создаётся локально при запуске проверок и исключён из Git.
Исходники, тесты, входные наборы и конфигурации сохранены для воспроизведения.
Упоминания результатов прежних прогонов ниже описывают исторические измерения;
для просмотра файлов отчётов и сохранённых таблиц UI выполните соответствующие проверки заново.

Самостоятельный Go-модуль на основе Day 28: локальный технический RAG по Дням 16–20, profiles «До», «После», «Сравнить», строгие цитаты, один repair и одинаковый локальный judge. История, облако и другой корпус отсутствуют. Индекс Day 21 только читается.

## Запуск

Нужны Go 1.26+, Node для проверки JS, Python 3 для отчёта, локальный Ollama. Подготовка весов:

```bash
ollama pull qwen3-embedding:0.6b
ollama pull qwen2.5:3b
ollama pull qwen2.5:3b-instruct-q8_0
ollama serve # если Ollama ещё не запущен
cd /Users/romansurzhan/ai_challenge/day-29
./run-web.sh
```

Открыть http://127.0.0.1:8089. Все browser assets встроены в Go; CDN отсутствуют. Произвольный вопрос задаётся в форме. Сравнение выполняет retrieval один раз и generation последовательно. Текущий запрос и сохранённая таблица benchmark подписаны отдельно. Отказы, ошибки, реальные pending, параметры, источники/цитаты и trace видны в UI.

```bash
curl http://127.0.0.1:8089/api/status
curl http://127.0.0.1:8089/api/ask -H 'Content-Type: application/json' \
  -d '{"question":"Как Artifact Store проверяет путь отчёта?","mode":"compare"}'
```

mode = baseline / selected / compare. Доступ только по literal loopback HTTP; прокси/redirects отключены. POST cross-origin отклоняется, 64 KiB body, вопрос до 12000 Unicode символов. Запросы серверного процесса сериализованы. Не запускайте benchmark одновременно с живыми вопросами: отдельные процессы не используют общий mutex.

## Воспроизведение

Протокол и наборы зафиксированы до генераций в `eval/`. Generator получает только question: expected answers, facets и ручные labels не передаются. Сначала tuning, затем замороженный selected, regression и отдельный control. После control настройки не меняются.

```bash
./run-web.sh --eval --dataset eval/tuning.json \
  --profiles configs/baseline.json,configs/technical512v3.json \
  --repeats 1 --output artifacts/reproduce-prompt.json
./run-web.sh --eval --dataset eval/tuning.json \
  --profiles configs/technical512v3.json,configs/technical768.json,configs/technical1024.json \
  --repeats 1 --output artifacts/reproduce-tokens.json
# Профили temperature / context / quantization сохранены после выбора опорных параметров.
./run-web.sh --eval --dataset eval/regression.json \
  --profiles configs/baseline.json,configs/selected.json \
  --repeats 1 --output artifacts/reproduce-regression.json
./run-web.sh --eval --dataset eval/control.json \
  --profiles configs/baseline.json,configs/selected.json \
  --repeats 3 --output artifacts/reproduce-control.json
python3 scripts/inspect.py artifacts/control.json
python3 scripts/report.py
```

Для точного повторения этапов используйте тот же CLI с `--dataset eval/tuning.json --repeats 1`, отдельным `--output artifacts/reproduce-NAME.json` и следующими `--profiles` в этом порядке:

| Этап | Profiles |
| --- | --- |
| tune-prompt | `configs/baseline.json,configs/technical512.json` |
| tune-tokens | `configs/technical512v3.json,configs/technical768.json,configs/technical1024.json` |
| tune-v4-tokens | `configs/technical512v4.json,configs/technical768v4.json,configs/technical1024v4.json` |
| tune-temperature | `configs/technical768v4.json,configs/temperature02.json` |
| tune-draft-tokens | `configs/draft512.json,configs/draft768.json,configs/draft1024.json` |
| tune-draft-temperature | `configs/draft768.json,configs/draft-temp02.json` |
| tune-context | `configs/draft768.json,configs/draft-ctx8192.json,configs/draft-ctx16384.json` |
| tune-quantization | `configs/quant-q4-ref.json,configs/quant-q8-peer.json` |

Файлы всех фактически проверенных candidates находятся в `configs/`, результаты — в `artifacts/tune-*.json`. Замена ровно одного temperature/context/model относительно опорного профиля воспроизводится через тот же `--profiles` CLI. Исходные v2–v5 prompt/schema версии доступны для повторения; baseline неизменен.

Последняя команда агрегирует `artifacts/control.json` и ручной разбор `eval/review-control.json`. При новых ответах сначала оцените их заново по рубрике; старые оценки нельзя автоматически присвоить другим ответам. Исходные файлы экспериментов сохраняйте, не перезаписывайте контрольные результаты. Каждая генерация сохраняет реальные payload, prompt/schema hashes, raw JSON API, модель, duration, token counts, done_reason и ошибки. Warmup model-only отдельно; минимум три измеряемых повтора control, порядок профилей чередуется.

## Контракты и контекст

Baseline: qwen2.5:3b Q4_K_M, t0, seed0, 512 tokens, num_ctx32768, исходный prompt/schema и исходный validator Day 28 (включая его историческую слабость проверки inline citations). Prompt перенесён дословно в `internal/agent/prompts/baseline-v1.txt`. Новые версии сохранены рядом. Pilot v2 оставлен в артефактах; v3/v4 ограничивают clarification и claims; компактный v5 убирает дублирование answer: модель генерирует claims/evidence, приложение собирает текст со ссылками и затем запускает тот же validator. Новые контракты разрешают 1–3 утверждения, частичный ответ и no-data refusal. Связь claim ↔ source ↔ exact quote, 20–160 Unicode символов, wrong-chunk checks сохранена. V3–V5 дополнительно требует inline ID для каждого связанного источника. Schema разрешает любую дословную цитату из полного контекста; application validator её проверяет независимо.

Перед generation и repair используется локальный `/tokenize` уже загруженного **точно совпадающего model blob** Ollama llama-server. Port обнаруживается через `ps`, принимается только ChatML runner без embedding и loopback 127.0.0.1. Полные system/user сообщения оформляются по сохранённому шаблону Qwen2.5 (без tools/history). 16 pilot-вызовов сверены: token count точно совпал с prompt_eval_count, включая repair и длинный контекст. Все последующие успешные exact-tokenized вызовы тоже проверяют равенство; несовпадение даёт `tokenizer_mismatch` и не публикуется как успешный ответ. В окно должны поместиться вход и весь num_predict. Repair проверяется отдельно.

Это внутренний endpoint установленного llama.cpp/Ollama, поэтому метод зависит от версии runner, вывода ps и ChatML-шаблона. Если matching runner не доступен, безопасный fallback — сумма UTF-8 bytes двух сообщений +256 на оформление. Для подтверждённого GPT2 byte-level BPE слияния уменьшают исходный byte-token count. Такой fallback консервативен и может отклонить вход, который поместился бы после BPE. Method, count и причина fallback сохраняются в каждом call. Недостаточное окно — `context_window_unfit`: вход не отправляется и не усечён. Методы проверены только для Qwen2.5, max context 32768. Schema — decoding constraint, в сообщения не добавляется.

Основное сравнение сохраняет полный retrieved context, порядок и S IDs. В итоговом benchmark полный snapshot первого retrieval каждого вопроса (включая scores) фиксируется для всех повторов. При этом каждый измеряемый pair заново выполняет и измеряет embedding/retrieval; свежие results отдельно сохранены как observed_retrieval_context/hash, input генераторов берётся из первого полного snapshot. Live compare делает обычный fresh retrieval один раз. Так устранена обнаруженная вариация cosine между uncached/repeated embeddings без изменения модели, порогов, источников или prompt. [Measurement amendment](eval/protocol-measurement-amendment.json) описывает pilot-прерывание; в первом pilot потерян raw второго pair из-за порядка checkpoint, первый сохранён. Полная финальная серия выполнена заново; после исправления raw сохраняется до audit guard. Hash итогового input проверяется между повторами и отдельно между stages. Сокращения контекста нет. Retrieval Day28 неизменен: embedding qwen3-embedding:0.6b, candidate20, final5, cosine0.45, gate0.55, rerank0.7/0.2/0.1. Judge всегда Qwen2.5 3B Q4 t0 seed0 128 tokens ctx32768, вспомогательный.

## Измерения и ограничения

Total RAG включает retrieval, generation/repairs, validation и judge; wall сравнения — обе ветки. Generation — сумма попыток. tokens/sec вычисляется через eval_count/eval_duration, не через RAG total. TTFT не измеряется: streaming отсутствует. `/api/ps` и RSS каждого Ollama/runner наблюдаются каждые 250 ms плюс начало/конец; реальные timestamps и ошибки сохранены. Частота best-effort, медленный probe увеличивает интервал. Disk model size, loaded model size и максимальный наблюдаемый RSS процесса разделены. На M1 Pro память unified: RSS и size_vram не складываются. CPU/GPU utilization недоступны. Время загрузки и изменение окна входят в реальный latency; judge с постоянным окном может вызвать повторную загрузку после маленького генераторного окна.

Отказ validator не означает отсутствие документов, а accepted не означает полноту или entailment. Ручной разбор использует рубрику 0–2 по отдельным осям корректности/полноты. Быстрые gate-отказы и ошибки отделены от успешных ответов. Малый набор и один tuning-повтор ограничивают выводы; ресурсы разделяются с другими программами компьютера.

Tuning не подтвердил содержательного улучшения. Для использования рекомендован исходный baseline (`configs/recommended.json`), а «После» и основной парный benchmark показывают последний экспериментальный кандидат v5, t0 / 768 / 16384 / Q4_K_M. Это исследовательский профиль, не объявленный победитель. Решение зафиксировано до control в `artifacts/selection-frozen.json`. Q8 не дал качественного преимущества; память его весов выше.

В v5 ответ собран из model-generated claims после разбора JSON: это устраняет повторную генерацию тех же предложений и ссылок. Каждое утверждение проходит прежнюю exact-quote/source validation; вывод не содержит шаблонных ответов на вопросы. RSS конкретного runner сопоставляется с model blob; все остальные процессы также сохраняются. Часть ранних pilot-измерений хранит максимум по всем Ollama процессам — это явно иной показатель, не смешиваемый с target runner RSS.

Результаты и выбранные параметры: [comparison.md](comparison.md). Проверки и браузерная демонстрация: artifacts/checks.md (локальный отчёт).
