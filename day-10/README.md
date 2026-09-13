# Day 10: управление контекстом разными стратегиями

`day-10` развивает агента с файловой памятью и добавляет отдельный слой стратегий управления контекстом. Стратегия управляет тем, какие сообщения реально отправить в prompt, какой хвост истории оставить в долговременном JSON-файле после успешного ответа и нужны ли дополнительные блоки памяти.

Текущий срез реализации: готовы стратегии `sliding`, `facts` и `branching`. Архитектура выделяет стратегии отдельно от агента, поэтому окно сообщений, sticky-память и ветки развиваются независимо.

## Архитектура

- `agent.go` - основной агент: загрузка состояния, вызов стратегии, HTTP-запрос к DeepSeek, сохранение успешного хода.
- `context_strategy.go` - общий интерфейс `ContextStrategy`, типы входа/выхода и реализации `SlidingWindowStrategy`, `FactsStrategy` и `BranchingStrategy`.
- `facts.go` - rule-based извлечение sticky facts из явных user-сообщений.
- `branches.go` - нормализация branching-состояния, активная ветка и видимая история.
- `settings.go` - runtime-настройки стратегии для web/API.
- `memory.go` - JSON-хранилище состояния разговора: история, sticky facts и ветки.
- `server.go` - локальный web/API.
- `chat_page.html` и `frontend.go` - интерфейс web-чата.
- `main.go` - CLI-флаги, env-настройки и запуск web-режима.

В JSON хранится состояние разговора. Актуальный формат `version: 3` содержит обычную историю, отдельный блок facts и состояние веток:

```json
{
  "version": 3,
  "messages": [],
  "facts": {
    "цель": "сравнить стратегии"
  },
  "branches": {
    "active_branch_id": "main",
    "checkpoint": [],
    "items": [
      {
        "id": "main",
        "title": "Main",
        "messages": []
      }
    ]
  },
  "updated_at": "2026-09-12T00:00:00Z"
}
```

Старые форматы `version: 1` и `version: 2` читаются как раньше. При следующем сохранении приложение пишет актуальный `version: 3`, который оно же умеет читать. Если в старом файле уже есть `messages`, при первом использовании `branching` они становятся историей ветки `main`, а checkpoint остается пустым. В модель отправляется подготовленный стратегией контекст. В ответе агента возвращаются метаданные:

- активная стратегия;
- размер окна для `sliding`, количество facts для `facts` или активная ветка для `branching`;
- сколько сообщений реально сохранено в истории после текущего хода;
- сколько сообщений ушло в текущий запрос.

## Sliding Window

Стратегия `sliding` отправляет в модель только последние `N` сообщений из истории, затем текущий вопрос пользователя. После успешного ответа она сохраняет в `history.json` только последние `N` сообщений уже обновленной истории. `system` message добавляется отдельно и в историю не записывается.

Пример при `-window 2`:

```text
system
последнее user-сообщение из истории
последний assistant-ответ из истории
текущий user prompt
```

`-window 0` допустим: тогда в запрос уходит только `system` и текущий prompt, а после ответа файл истории остается пустым.

## Sticky Facts

Стратегия `facts` хранит историю полностью, но не использует окно сообщений как основной параметр. В каждый запрос она отправляет обычный `system` message, отдельный `system`-блок `Sticky facts` при наличии facts и текущий user prompt. Старая история остается в JSON для аудита и будущих стратегий, но не отправляется как sliding window.

Facts обновляются после каждого успешного user-сообщения в режиме `facts`. Извлечение intentionally простое и без LLM-вызова: поддерживаются явные строки `ключ: значение` для ключей:

- `цель`
- `ограничение`
- `предпочтение`
- `решение`
- `имя`
- `проект`

Пример сообщения:

```text
цель: сравнить стратегии контекста
ограничение: не использовать SQLite
предпочтение: короткие ответы
```

Если ключ уже существовал, новое значение заменяет старое. Если API-вызов завершился ошибкой, ни история, ни новые facts не сохраняются.

В Web UI sticky facts показываются в отдельной сворачиваемой панели `Sticky Facts`. Панель отображает key-value список фактов, которые запомнил агент, показывает их количество и обновляется после каждого успешного ответа агента. Если facts пока нет, в раскрытой панели видно `Пока нет facts`. Если backend не смог прочитать историю, панель показывает ошибку загрузки facts вместо пустого списка.

Пример контекста при `-strategy facts`:

```text
system
Sticky facts:
- ограничение: не использовать SQLite
- цель: сравнить стратегии контекста
текущий user prompt
```

## Branching

Стратегия `branching` хранит общий checkpoint и набор альтернативных веток. Контекст запроса строится так:

```text
system
checkpoint messages
messages активной ветки
текущий user prompt
```

После успешного ответа новый ход `user/assistant` сохраняется только в активную ветку. `branching` не использует `window_messages`, не обрезает историю ветки и не обновляет sticky facts.

Checkpoint создается из текущей видимой истории: предыдущий checkpoint + сообщения активной ветки. После этого активная ветка очищается, и от checkpoint можно создавать новые альтернативные ветки.

## Конфигурация

Приложение читает `.env` из корня проекта.

```bash
DEEPSEEK_API_KEY=<ваш DeepSeek API key>
DEEPSEEK_MODEL=deepseek-v4-flash
DEEPSEEK_BASE_URL=https://api.deepseek.com
DAY10_HISTORY_PATH=day-10/history.json
DAY10_CONTEXT_STRATEGY=sliding
DAY10_CONTEXT_WINDOW=8
```

Значения по умолчанию:

- endpoint: `https://api.deepseek.com/chat/completions`
- model: `deepseek-v4-flash`
- history: `day-10/history.json`
- strategy: `sliding`; также доступны `facts` и `branching`
- window: `8` для `sliding`; `facts` и `branching` игнорируют этот параметр
- timeout: `60s`
- temperature: `0.7`
- max_tokens: `1024`
- thinking: disabled через `{ "type": "disabled" }`

## Запуск CLI

Через флаг:

```bash
go run ./day-10 -strategy sliding -window 4 -prompt "Сформулируй краткий план проекта"
```

Sticky Facts:

```bash
go run ./day-10 \
  -strategy facts \
  -prompt "цель: сравнить стратегии контекста"
```

Branching:

```bash
go run ./day-10 \
  -strategy branching \
  -prompt "Предложи альтернативный план решения"
```

Через stdin:

```bash
echo "Объясни, чем sliding window отличается от полной истории" | go run ./day-10 -window 6
```

С отдельным файлом истории:

```bash
go run ./day-10 \
  -history /tmp/day10-history.json \
  -strategy facts \
  -prompt "имя: Роман
цель: сравнить стратегии контекста"
```

После ответа CLI печатает метаданные контекста в stderr:

```text
Context: strategy=sliding window=3 request_messages=5 stored_history_messages=3
```

Для `facts` метаданные не показывают window:

```text
Context: strategy=facts facts=2 request_messages=3 stored_history_messages=12
```

Для `branching` метаданные показывают активную ветку и размеры checkpoint/ветки:

```text
Context: strategy=branching branch=Main checkpoint=4 branch_messages=2 request_messages=8 stored_history_messages=6
```

## Web UI

Запуск на адресе по умолчанию `:8080`:

```bash
go run ./day-10 -serve
```

Или через helper-скрипт:

```bash
./day-10/run_web.sh
```

После запуска открыть:

```text
http://localhost:8080
```

В интерфейсе есть:

- селектор стратегии `Sliding Window` / `Sticky Facts` / `Branching`;
- поле размера окна `N` только для `Sliding Window`;
- отдельная сворачиваемая панель `Sticky Facts` для key-value sticky-памяти при выбранной стратегии `facts`;
- отдельная панель `Branching` для checkpoint, создания, переключения и удаления веток;
- отображение активной стратегии, окна для `sliding`, количества facts для `facts` или активной ветки для `branching`, размера реально сохраненной истории (`stored`) и размера отправленного контекста (`sent`).

Настройки web-чата меняются через `POST /api/settings` без перезапуска сервера.

Для проверки на порту `8082`:

```bash
go run ./day-10 -serve -addr :8082 -strategy facts
```

Если `DAY10_HISTORY_PATH` не задан и флаг `-history` не передан, используется файл `day-10/history.json` относительно рабочей папки запуска. Для запуска из корня репозитория это `/Users/romansurzhan/ai_challenge/day-10/history.json`.

Диагностика facts:

```bash
curl http://localhost:8082/api/settings
curl http://localhost:8082/api/facts
```

`/api/settings` должен вернуть `strategy=facts`, а `/api/facts` должен вернуть сохраненные facts из history-файла. Ошибка вида `неподдерживаемая версия истории: 3` означает, что на порту работает старая сборка, которая не умеет читать актуальный history format; остановите старый процесс и запустите свежий `go run ./day-10`.

## API

Отправить сообщение:

```http
POST /api/chat
Content-Type: application/json

{"message":"Привет"}
```

Получить историю:

```http
GET /api/history
```

Получить sticky facts:

```http
GET /api/facts
```

Ответ:

```json
{
  "facts": {
    "цель": "сравнить стратегии контекста"
  }
}
```

Получить branching-состояние:

```http
GET /api/branches
```

Ответ:

```json
{
  "active_branch_id": "main",
  "checkpoint_messages": 2,
  "branches": [
    {"id": "main", "title": "Main", "message_count": 4, "active": true}
  ]
}
```

Создать checkpoint из текущего активного диалога:

```http
POST /api/branches/checkpoint
```

Создать новую ветку от checkpoint и сделать ее активной:

```http
POST /api/branches
Content-Type: application/json

{"title":"Alternative"}
```

Переключить активную ветку:

```http
POST /api/branches/active
Content-Type: application/json

{"id":"main"}
```

Удалить ветку:

```http
DELETE /api/branches/branch-1
```

Получить или обновить настройки:

```http
GET /api/settings
POST /api/settings
Content-Type: application/json

{"strategy":"facts"}
```

Старые клиенты могут прислать `window_messages` вместе с `facts`; сервер примет поле для совместимости, но стратегия `facts` его игнорирует. То же верно для `branching`: поле может прийти в JSON, но на контекст веток оно не влияет.

## Проверка

Автотесты:

```bash
go test ./day-10
```

Покрытые сценарии:

- успешный ответ отправляется в модель и парсится;
- история сохраняется, обрезается активным окном и загружается после перезапуска;
- ошибка API не сохраняет незавершенный ход;
- битая история останавливает запрос до обращения к API;
- `GET /api/history` возвращает сохраненные сообщения;
- `GET /api/facts` возвращает сохраненные sticky facts;
- `GET /api/branches` возвращает checkpoint, active branch и список веток со счетчиками сообщений;
- `SlidingWindowStrategy` обрезает контекст и сохраненную историю до последних `N` сообщений;
- `FactsStrategy` добавляет facts-блок, не применяет окно к истории и сохраняет полную историю;
- `BranchingStrategy` строит контекст из `system + checkpoint + active branch + current prompt`;
- extractor извлекает явные facts и обновляет существующие ключи;
- JSON-хранилище сохраняет/загружает `messages + facts + branches` и читает старые `version: 1/2`;
- агент реально использует sliding window при сборке запроса;
- агент в режиме `facts` обновляет facts после успешного ответа, отправляет sticky facts в модель и не сохраняет новые facts при API-ошибке;
- агент в режиме `branching` сохраняет ход только в активную ветку, не применяет sliding window, не меняет facts и не сохраняет ход при API-ошибке;
- checkpoint переносит текущий активный диалог в checkpoint и очищает активную ветку;
- создание новой ветки делает ее активной, переключение меняет дальнейшую историю, удаление не позволяет удалить последнюю ветку;
- `/api/history` в режиме `branching` возвращает видимую историю: checkpoint + messages активной ветки;
- явный `window=0` отправляет только system и текущий prompt и сохраняет пустую историю;
- `POST /api/settings` обновляет runtime-настройки;
- CLI читает `-strategy sliding|facts|branching`; `-window` управляет только `sliding` и отклоняет отрицательное окно для этой стратегии.

## Сравнение стратегий

| Стратегия | Качество ответа | Стабильность | Расход токенов | Удобство |
| --- | --- | --- | --- | --- |
| Sliding Window | Хорошо держит недавний контекст, но может забыть ранние требования | Предсказуемая: один параметр `N` | Ограничен размером окна | Простая настройка в CLI и web |
| Sticky Facts | Лучше удерживает явно записанные цели, ограничения и предпочтения даже через длинную историю | Предсказуемая при явных `ключ: значение`; не угадывает скрытые факты | Зависит от размера facts-блока, не от окна истории | Доступна в CLI, env, API и web; facts видны в отдельной панели |
| Branching | Позволяет сравнивать разные продолжения от общего checkpoint без потери исходного состояния | Предсказуемая: общий checkpoint + выбранная активная ветка | Растет от размера checkpoint и активной ветки; `window_messages` не используется | Доступна в CLI, env, API и web; ветки видны в отдельной панели |

Для финального сравнения можно прогнать общий сценарий из 10-15 сообщений через все три стратегии и сравнить ответы/токены на одинаковых запросах.

## Следующие шаги

1. Добавить общий сценарий сравнения и сохранить результаты по токенам для всех трех стратегий.
2. При необходимости добавить экспорт/импорт веток для ручного анализа альтернативных диалогов.
