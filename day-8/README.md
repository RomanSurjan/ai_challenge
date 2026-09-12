# Day 8: работа с токенами

`day-8` показывает, как история диалога влияет на размер следующего запроса, лимит контекста и расход токенов за сессию. Отчет разделяет три уровня:

- `Question` - локальная оценка токенов нового пользовательского сообщения;
- `Turn` - токены одного API-вызова: input, output, reasoning и overall;
- `Session` - накопленный расход успешных ходов за сессию.

Агент хранит историю в JSON и разделяет источники этих чисел:

- локальная оценка до запроса;
- калиброванная локальная оценка до запроса;
- фактический `usage` DeepSeek API после ответа, если API его вернул.

Если используются локальные числа, CLI и web UI подписывают их как `estimate` / `оценка`. Фактические числа подписываются как `API usage` / `API`.

## Что такое токены

Токены - это расчетные части текста, которыми модель измеряет вход и выход. Реальный токенизатор зависит от конкретной модели, версии модели, chat-template и параметров запроса.

По официальной документации DeepSeek, фактическое количество обработанных токенов нужно смотреть в `usage`, который возвращает модель после ответа. Там же DeepSeek публикует V4 demo tokenizer zip для offline-подсчета: <https://api-docs.deepseek.com/quick_start/token_usage/>. В API reference пример ответа содержит `usage.prompt_tokens`, `usage.completion_tokens`, `usage.total_tokens`, а также cache-поля: <https://api-docs.deepseek.com/api/create-chat-completion>. На странице моделей DeepSeek сейчас рекомендует имя `deepseek-flash`; legacy `deepseek-v4-flash` принимается, но маршрутизируется на актуальную Flash-модель: <https://api-docs.deepseek.com/quick_start/pricing/>.

В этом учебном Go-проекте официальный tokenizer zip не встроен. Поэтому точный остаток контекста до запроса здесь не гарантируется. Для точного preflight-подсчета нужен локальный tokenizer и chat-template, совпадающие с текущей моделью и конфигурацией API. Без этого `usage` API является источником факта, но он приходит только после ответа.

Локальная оценка `Input` остается простой и независимой от внешних пакетов:

- слова и числа считаются фрагментами;
- знаки препинания считаются отдельными токенами;
- CJK-символы считаются по одному;
- каждое chat-сообщение получает фиксированную служебную стоимость;
- весь prompt получает небольшую служебную стоимость ответа.

Эта оценка не обязана совпадать с `usage` от DeepSeek. Ее задача - показать динамику заранее: сколько места занимает весь вход модели и когда он может превысить лимит.

## Как растут токены

Агент отправляет в модель не только новый вопрос, а полный prompt:

```text
system message + saved history + current user message
```

`Question` в отчете - это только новое сообщение пользователя. `Turn input` - это уже весь вход модели: system message, сохраненная история и новый вопрос.

После успешного ответа в историю добавляется пара `user` + `assistant`. На следующем ходе эта пара снова попадет во входной prompt. Поэтому длинный диалог увеличивает input tokens каждого следующего запроса.

Накопленная `Session`-статистика хранится в `history.json` рядом с сообщениями. Старые файлы истории с полями `version`, `messages`, `updated_at` продолжают загружаться: если `session_usage` отсутствует, счетчики начинаются с нуля.

Если API вернул `usage`, фактические `prompt_tokens`, `completion_tokens` и `total_tokens` прибавляются к `Session API`. Если `usage` отсутствует, точные API-токены не выдумываются: ход попадает только в отдельную оценочную корзину `Session estimate`.

Reasoning/thinking tokens показываются условно. Если API вернул отдельное поле `reasoning_tokens`, `thinking_tokens` или совместимое вложенное поле в completion details, агент сохранит и покажет его. Если такого поля нет, это не ошибка: в отчете будет `reasoning=unknown` / `н/д`.

## Лимит контекста

Перед обращением к API агент сравнивает калиброванную локальную оценку `Input` с `context-limit`.

Если оценка больше лимита, запрос в DeepSeek не отправляется, а история не сохраняется. Ошибка показывает:

- оценку input tokens;
- лимит;
- превышение;
- вклад system/history/current request.

Так стабильно демонстрируется сценарий переполнения без зависимости от реального API.

Автоматическая обрезка или сжатие истории для этого задания не требуется.

## Конфигурация

Приложение читает `.env` из корня проекта.

```bash
DEEPSEEK_API_KEY=<ваш DeepSeek API key>
DEEPSEEK_MODEL=deepseek-v4-flash
DEEPSEEK_BASE_URL=https://api.deepseek.com
DAY8_HISTORY_PATH=day-8/history.json
DAY8_CONTEXT_LIMIT=8192
DAY8_PROMPT_TOKEN_MULTIPLIER=1
DAY8_COMPLETION_TOKEN_MULTIPLIER=1
DAY8_INPUT_PRICE_PER_1M=0
DAY8_OUTPUT_PRICE_PER_1M=0
```

Основные значения по умолчанию:

- endpoint: `https://api.deepseek.com/chat/completions`
- model: `deepseek-v4-flash`
- history: `day-8/history.json`
- context limit: `8192`
- timeout: `60s`
- temperature: `0.7`
- max_tokens: `1024`
- thinking: disabled через `{ "type": "disabled" }`

Multiplier-ы применяются к локальной оценке до проверки `context-limit` и до расчета примерной стоимости:

- `DAY8_PROMPT_TOKEN_MULTIPLIER` / `-prompt-token-multiplier` - для `Input`;
- `DAY8_COMPLETION_TOKEN_MULTIPLIER` / `-completion-token-multiplier` - для `Output`.

Например, если калибровка показывает, что локальный `Input` обычно ниже API примерно на 20%, можно использовать `1.2`, чтобы проверка лимита была осторожнее. Это не универсальная константа, а empirical calibration for current model/config.

Флаги:

```text
-calibrate-tokens
-context-limit
-completion-token-multiplier
-demo-tokens
-history
-input-price-per-1m
-output-price-per-1m
-prompt
-prompt-token-multiplier
-serve
```

Цены не являются обязательной частью задания. В коде они оставлены только как необязательный учебный пример. По умолчанию они равны `0`, чтобы не притворяться актуальным прайсом. Для демонстрации можно передать свои значения за 1M input/output tokens.

## Запуск CLI

Через флаг:

```bash
go run ./day-8 -prompt "Ответь одним предложением: что такое токены?"
```

Через stdin:

```bash
echo "Почему длинный диалог становится дороже?" | go run ./day-8
```

С отдельной историей и маленьким лимитом:

```bash
go run ./day-8 \
  -history /tmp/day8-history.json \
  -context-limit 80 \
  -prompt "Меня зовут Роман. Сегодня я изучаю токены."
```

После ответа CLI печатает token report в `stderr`:

```text
Token report:
  question (estimate): tokens=...
  local estimate:      input=... output=... overall=...
  calibrated estimate: input=... output=... overall=... (input x1.2, output x1.1)
  turn (API usage):    input=... output=... reasoning=... overall=...
  estimate vs API:     input=... (...) output=... (...) overall=...
  session (API usage): input=... output=... reasoning=... overall=...
```

Строка `calibrated estimate` появляется только когда хотя бы один multiplier отличается от `1`.
Если API не вернул `usage`, текущий ход и накопление будут явно подписаны как `turn (estimate)` и `session (estimate)`.

## Калибровка по реальному API

Режим калибровки делает несколько коротких запросов в DeepSeek, не пишет их в историю и сравнивает локальную оценку с фактическим `usage`:

```bash
go run ./day-8 -calibrate-tokens
```

Можно сразу калибровать конкретную модель и настройки:

```bash
go run ./day-8 \
  -model deepseek-flash \
  -temperature 0.2 \
  -calibrate-tokens
```

Сценарии:

- `russian_short` - короткий русский текст;
- `english_short` - короткий английский текст;
- `punctuation` - пунктуация;
- `json_code` - JSON/code-like prompt;
- `russian_long` - длинный русский prompt;
- `multi_message_history` - prompt с историей из нескольких сообщений.

Пример формы вывода:

```text
scenario local_input api_input diff diff_% local_output api_output api_overall
russian_short                  34        41    +7  +20.6%           12        15          56
...

Suggested empirical multipliers:
  prompt_multiplier:     1.2050
  completion_multiplier: 1.1800
  mean prompt deviation: ...
  max prompt deviation:  ...
```

Полученные значения можно перенести в `.env`:

```bash
DAY8_PROMPT_TOKEN_MULTIPLIER=1.205
DAY8_COMPLETION_TOKEN_MULTIPLIER=1.18
```

Или передать флагами:

```bash
go run ./day-8 \
  -prompt-token-multiplier 1.205 \
  -completion-token-multiplier 1.18 \
  -prompt "Ответь одним предложением: что такое токены?"
```

## Demo без API

Demo не требует `DEEPSEEK_API_KEY` и не обращается к DeepSeek:

```bash
go run ./day-8 -demo-tokens
```

С примерной стоимостью:

```bash
go run ./day-8 \
  -demo-tokens \
  -input-price-per-1m 0.14 \
  -output-price-per-1m 0.28
```

Demo показывает три сценария:

- `short` - 1-2 коротких сообщения;
- `long` - несколько ходов, где история растет;
- `overflow` - искусственно длинная история и маленький лимит, чтобы показать отказ до API.

В таблице видно, что `Input` растет по мере диалога, потому что в него входит накопленная история.

## Локальный web-чат

Запуск:

```bash
go run ./day-8 -serve
```

Или helper-скрипт:

```bash
./day-8/run_web.sh
```

После запуска откройте:

```text
http://localhost:8080
```

Web API `POST /api/chat` возвращает `token_report`, `usage` от API при наличии и `session_usage` с накопленной статистикой. Страница показывает `Turn API` / `Turn оценка` и `Session API` / `Session оценка`; отдельное поле `Question` остается в JSON и CLI-отчете, но не выводится в web UI. При переполнении UI показывает понятную ошибку.

## Проверка переполнения

Простой способ увидеть локальный отказ:

```bash
go run ./day-8 -context-limit 20 -prompt "Это сообщение почти наверняка превысит маленький учебный лимит контекста."
```

Ожидаемый результат: агент вернет ошибку про превышение лимита, не отправит запрос в DeepSeek, не сохранит новое сообщение и не изменит накопленную статистику.

## Тесты

```bash
go test ./day-8
```

Если среда не разрешает писать в системный Go cache, можно указать временный кеш:

```bash
GOCACHE=/private/tmp/ai_challenge_go_cache go test ./day-8
```

Покрытые сценарии:

- токенизатор для пустых строк, английского, русского, чисел, пунктуации и CJK;
- `Agent.Ask` считает токены до API;
- multiplier применяется к локальной оценке;
- `context-limit` проверяется по калиброванной оценке;
- калибровочный режим тестируется через fake HTTP client без реального API;
- сравнение local vs API usage считается отдельно;
- превышение лимита не вызывает API и не сохраняет историю;
- успешный ответ добавляет output tokens;
- конфиг читает `DAY8_HISTORY_PATH`, `DAY8_CONTEXT_LIMIT`, multiplier-ы и примерные цены;
- demo печатает short/long/overflow;
- web API возвращает `token_report`, `usage`, если он пришел от API, и `session_usage`;
- старый `history.json` без `session_usage` загружается корректно;
- накопленные API usage значения суммируются после успешных ходов;
- reasoning tokens сохраняются и показываются, если API вернул отдельное поле;
- fallback без API usage явно подписывается как локальная оценка.
