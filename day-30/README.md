# День 30 — локальная LLM как приватный сервис

Логи, скриншоты, видео и сырые результаты прогонов удалены из репозитория.
Каталог `artifacts/` создаётся локально при запуске проверок и исключён из Git.
Исходники, тесты, входные наборы и конфигурации сохранены для воспроизведения.
Упоминания результатов прежних прогонов ниже описывают исторические измерения;
для просмотра файлов отчётов и сохранённых таблиц UI выполните соответствующие проверки заново.

Сервис фактически развёрнут на домашнем **MacBook Pro, Apple M1 Pro, 16 ГБ RAM,
macOS 26.5.1**. Сетевой адрес на момент проверки: **https://192.168.3.53:8443**.
Нативный Ollama **0.35.0**, установленная `qwen2.5:3b` (Q4_K_M).
HTTP API и прежний веб-чат работают через HTTPS и Basic Auth.

```text
Браузер / curl в LAN
  → HTTPS :8443, Caddy 2.11.7, TLS локального CA + Basic Auth
  → HTTP 127.0.0.1:8030, backend с приватным proxy token и доверенной identity
  → HTTP 127.0.0.1:11434, нативный Ollama → qwen2.5:3b / Metal
```

Backend и Ollama слушают только loopback. Caddy удаляет клиентский Authorization
перед проксированием, перезаписывает X-Private-Token и X-Authenticated-User.
Без секрета reverse proxy backend отказывает даже при обращении с того же Mac.
Порт 11434 не публикуется; Docker, CDN и внешние LLM API не используются.
Интернет не требуется для работы после установки Caddy и модели.

## Что переиспользовано

`web_server.py`, `web/index.html`, `web/styles.css`, `web/app.js` и базовые HTTP-тесты
скопированы из `day-26-27/`. Сохранены внешний вид, многоходовая история,
редактируемый systemPrompt, NDJSON meta/chunk/done/error, streaming,
«Остановить», «Новый чат», localStorage и безопасный вывод через textContent.
CSS не изменён. Ключ localStorage отделён для дня 30; история других дней не переносится.
Исправлена гонка «Новый чат» с ещё поступающим старым ответом.

Новые механизмы: защищённый вход, Origin, серверные options, контекст для всех
ходов, token bucket, FIFO-очередь, абсолютные deadlines, закрытие upstream,
health/readiness, ограничение HTTP-соединений, launchd и реальные проверки.
Из дня 29 взята идея верхней оценки byte-BPE и явного резерва ответа.
`selected.json` не используется и не объявляется улучшением; подбор/RAG не повторялись.
Предыдущие дни не изменены (сверка хешей в `artifacts/previous-files-verification.json`).

## Зависимости и воспроизводимая установка

Python 3.10+ (на этой машине 3.14.8), native Ollama с уже установленной моделью,
Caddy 2.11.7, curl. У Python только standard library, pip-зависимостей нет.
Скрипты launchd рассчитаны на macOS и пользовательскую GUI-сессию.

Официальный архив Caddy скачивается только для установки, не для запросов чата.
Установщик сверяет SHA-512 с официальным файлом и сохраняет результат локально в `artifacts/caddy-install.json`.
Для повторной установки бинарника:

```bash
cd /Users/romansurzhan/ai_challenge/day-30
python3 scripts/install_caddy.py
# Определите актуальный адрес, если DHCP его изменил:
ipconfig getifaddr en0
python3 scripts/setup.py --lan-ip 192.168.3.53
python3 scripts/service.py install
python3 scripts/export_ca.py
```

`setup.py` создаёт `.runtime/` с правами 700, секретные файлы с правами 600 и
не перезаписывает существующие настройки. Login/password находятся только в
`.runtime/access.txt`, а curl-конфигурация — `.runtime/curl.conf`.
Пароль, proxy token, CA private keys и журналы исключены из Git.
Операционные логи Caddy фильтруют весь объект request, включая заголовки;
access logging отключён. Это проверяется при сбое upstream. Пример без
реальных секретов: `config.example.json` и `deploy/Caddyfile.example`.
Сервер не сохраняет разговоры; в артефактах сохранены только специально
заданные демонстрационные запросы/ответы, это не журнал пользовательских диалогов.

## HTTPS и подключение

На другом устройстве в той же LAN перенесите **только** публичный
`artifacts/lan-root.crt`, проверьте SHA-256 отпечаток в `artifacts/deployment.json`,
установите сертификат как доверенный CA и откройте `https://192.168.3.53:8443`.
На iOS после установки профиля включите полное доверие сертификату в настройках
сертификатов. На macOS импортируйте в Keychain и настройте доверие SSL.
Не передавайте каталог `.runtime/data`: он содержит закрытый ключ CA.
Не вводите пароль через HTTP или при непроверенном сертификате.

Браузер покажет стандартное окно Basic Auth. Используйте данные из приватного
`access.txt`. Пароль в URL и frontend отсутствует. Домашний сервер не имеет
публичного домена/port forwarding; из Интернета подключение не проверялось.
Firewall и изоляция Wi-Fi могут блокировать LAN: разрешите именно Caddy:8443.
При смене LAN-IP обновите allowed_origins, адрес Caddy и приватный access.txt,
перезапустите backend/proxy и экспортируйте актуальный CA.

Авторизованный curl с реальной проверкой сертификата:

```bash
cd /Users/romansurzhan/ai_challenge/day-30
curl --config .runtime/curl.conf --no-buffer \
  https://192.168.3.53:8443/api/chat \
  -H 'Content-Type: application/json' \
  --data '{"systemPrompt":"Отвечай кратко.","messages":[{"role":"user","content":"Сколько будет 2+2?"}]}'

curl --config .runtime/curl.conf https://192.168.3.53:8443/api/status
curl --config .runtime/curl.conf https://192.168.3.53:8443/health/live
curl --config .runtime/curl.conf https://192.168.3.53:8443/health/ready
```

При переносе curl на другое устройство создайте локальный конфигурационный файл
с username/password и путём к публичному CA; выставьте ему chmod 600.
Без Origin curl работает. Браузерный POST с Origin допускается только для
явно перечисленных HTTPS origins; `null` и посторонний origin получают 403.
Unknown JSON fields (включая model/options) получают 400.

## Управление сервисами и доступность

```bash
python3 scripts/service.py status
python3 scripts/service.py restart --component backend
python3 scripts/service.py restart --component proxy
python3 scripts/service.py stop
python3 scripts/service.py start
# Удалить только установленные day-30 launchd agents:
python3 scripts/service.py uninstall
```

У backend/proxy/caffeinate включены RunAtLoad, KeepAlive и ThrottleInterval=2.
User LaunchAgents находятся в `~/Library/LaunchAgents/local.day30.*.plist`;
они запускаются при входе пользователя и перезапускаются после падения.
Это не системный сервер до входа в macOS. При выключении/выходе из аккаунта,
закрытии крышки, ручном сне, потере сети сервис недоступен.
Отдельный `caffeinate -s` предотвращает idle-сон на AC, пока сервис запущен;
на батарее такой гарантии нет. Системные pmset-настройки не изменены.

Ollama остаётся уже существующим `homebrew.mxcl.ollama`:

```bash
brew services info ollama
brew services start ollama
brew services restart ollama
brew services stop ollama
```

Последние команды относятся к общему Ollama всех дней; day-30 service.py его
не останавливает. У существующего Ollama launchd KeepAlive включён.
Для ручной отладки backend: `./run-web.sh` (сначала остановите launchd backend).
Сетевой доступ всё равно осуществляется через Caddy.

## API, лимиты и контекст

| Настройка | Значение |
|---|---:|
| Серверная модель | qwen2.5:3b |
| num_ctx / num_predict | 8192 / 512 |
| temperature / seed | 0 / 0 |
| Rate limit на authenticated user | 10/min, burst=3 |
| Активные генерации / FIFO очередь | 1 / 3 |
| Ожидание очереди | 30 s |
| Абсолютное время генерации, включая подготовку | 180 s |
| Чтение request headers/body через Caddy | 10 s / 10 s |
| Backend: чтение body / idle socket | 10 s / 10 s |
| Соединение с Ollama | 5 s |
| Body / header Caddy | 256 KiB / 16 KiB |
| Сообщение / systemPrompt | 12 000 / 4 000 символов |
| История | 64 сообщения, 60 000 символов |
| Backend HTTP-соединения | 32; лишнее соединение закрывается |

Token bucket допускает три запроса сразу, затем пополняется одним токеном
каждые 6 секунд. Ошибка 429 с Retry-After возникает до обращения к Ollama.
Валидация body/history/context выполняется раньше лимитера: ошибки ввода не
расходуют его токены. Принятый запрос расходует токен даже при отказе очереди.
При заполнении очереди и истечении ожидания — 503 с объяснением.
Параллельные HTTP-запросы обслуживаются **с последовательной генерацией**.

Контекст — **консервативная верхняя оценка, не точное число токенов**:
сумма UTF-8 байтов полной ChatML-строки (system + каждый user/assistant +
assistant prefix), плюс 256 служебных токенов, плюс резерв 512 ответа.
Для подтверждённого byte-level GPT2 BPE исходных символов не больше UTF-8 байтов;
BPE merges уменьшают число токенов. Буквальные байты ChatML special tokens также
считаются с запасом. Перед каждой генерацией проверяются family=qwen2,
GGUF tokenizer=gpt2, pre=qwen2, отсутствие встроенной истории и точное совпадение
установленного Ollama template с `model_template.txt`. При изменении шаблона/
метаданных — отказ, а не неподтверждённый подсчёт.
Даже пустой systemPrompt отправляется отдельным system-сообщением, чтобы
Ollama не добавлял заводской system prompt за пределами проверенной истории.

Граница `bound + num_predict <= num_ctx`; превышение — 413 до генерации,
без тихого удаления сообщений. Кириллица/эмодзи исчерпывают этот запас быстрее
ASCII. Подход сознательно отклоняет некоторые реально помещающиеся диалоги.
Внутренний /tokenize runner не используется; «символы / 4» не применяется.
Метод и upper bound приходят в meta. Реальный prompt_eval_count приходит в done.

`/health/live` показывает работу backend; `/health/ready` (200/503) проверяет
доступность Ollama, установку модели и метаданные. Это не обещание успешной
следующей генерации и не проверка загрузки весов в RAM; холодная загрузка возможна.
`/api/status` сохранён, содержит limits, generation и текущую очередь.
Все маршруты защищены авторизацией. До начала потока ошибки имеют HTTP-статус;
после начала — NDJSON error при уже отправленном 200.
При отмене/disconnect/deadline upstream TCP прерывается и слот освобождается.

## Проверки и сдача

```bash
python3 -m unittest discover -s tests -v
python3 -m py_compile web_server.py scripts/*.py tests/*.py
node --check web/app.js
bash -n run-web.sh
# Меняет только day-30 private config временно, всегда восстанавливает её:
python3 scripts/live_checks.py
```

Fake Ollama используется только в unit/integration tests, реальные результаты
HTTPS + модели записаны отдельно в `artifacts/live-requests.json` и
`artifacts/live-summary.json`. `artifacts/checks.md` содержит времена, конфиги,
успешные ответы, ожидаемые отказы и неожиданные ошибки отдельно.
Недоступность Ollama проверяется временной настройкой backend на закрытый
loopback-порт: общий Ollama других дней при этом продолжает работать.
Автоматический перезапуск проверяется SIGKILL **только day-30 backend**.

Проверка LAN с этого Mac не заменяет второе устройство. На момент выполнения
второе устройство не предоставлено; тест со второго устройства не выполнен.
HTTPS-чат проверен в Яндекс Браузере после настройки доверия CA пользователем:
вход, настоящий первый ответ и follow-up с историей прошли (`artifacts/browser-check.json`).
Встроенный браузер Codex вернул ERR_BLOCKED_BY_CLIENT; для записи использовать обычный браузер.
Короткие тесты не доказывают production-надёжность.

Источники: [Caddy basic_auth](https://caddyserver.com/docs/caddyfile/directives/basic_auth),
[Caddy TLS internal](https://caddyserver.com/docs/caddyfile/directives/tls),
[Caddy reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy),
[официальный Qwen tokenizer](https://huggingface.co/Qwen/Qwen2.5-3B-Instruct/blob/main/tokenizer.json).
Метаданные установленной GGUF проверены локально: `artifacts/model-verification.json`.
