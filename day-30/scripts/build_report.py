#!/usr/bin/env python3
"""Build the human report from measured JSON; no invented timings or counts."""
from collections import Counter
from datetime import datetime
import json
from pathlib import Path
import statistics
from zoneinfo import ZoneInfo

ROOT=Path(__file__).resolve().parents[1]
A=ROOT/'artifacts'
s=json.loads((A/'live-summary.json').read_text())
r=json.loads((A/'live-requests.json').read_text())
seq=[x for x in r if x['label'].startswith('sequential_')]
concurrent=[x for x in r if x['label'].startswith('concurrent_default_')]
rate=[x for x in r if x['label'].startswith('rate_burst_')]
follow=next(x for x in r if x['label']=='history_follow_up')
restart=next(x for x in r if x['label']=='launchd_automatic_restart')
zone=ZoneInfo('Europe/Moscow')
started=datetime.fromtimestamp(s['startedUnix'],zone).strftime('%Y-%m-%d %H:%M:%S')
finished=datetime.fromtimestamp(s['finishedUnix'],zone).strftime('%H:%M:%S')
lat=[x['elapsedSeconds'] for x in seq]
diagnostics=sum(x.get('httpStatus')==200 and not x.get('streamDone') and not x.get('cancelledByClient') for x in r)
probes=json.loads((A/'live-probes.json').read_text())
probe_statuses=dict(Counter(str(x.get('httpStatus',x.get('unexpectedException','no response'))) for x in probes))
rows='\n'.join(f"| {x['label']} | {x.get('httpStatus','PID')} | {x.get('elapsedSeconds')} | {'да' if x['passed'] else 'НЕТ'} |" for x in r)
text=f'''# Реальные проверки — День 30

Фактическое развёртывание: домашний MacBook Pro, M1 Pro / 16 ГБ, macOS 26.5.1.
URL **{s['url']}**, Caddy HTTPS + Basic Auth, backend loopback 8030,
нативный Ollama 0.35.0 loopback 11434, установленная qwen2.5:3b Q4_K_M.
TLS проверялся CA из файла, без отключения проверки сертификата и без изменения
macOS Keychain. Сервис остался запущен через launchd.

## Измеренный итог

Прогон **{started}–{finished}, MSK**, длительность
**{s['finishedUnix']-s['startedUnix']:.2f} s**, включая паузы и перезапуски.
`live-requests.json`: **{s['namedHttpRequests']} именованных HTTP-запроса** и
1 проверка автоматической смены PID; **{s['passed']}/{s['recordedChecks']}** ожидаемых результатов.
Отдельно `live-probes.json`: **{s['serviceProbes']} служебных HTTP-опросов** запуска/очереди.
Итого в этом прогоне **{s['httpRequestsTotal']} HTTP-запросов**.
Исходы служебных опросов: `{json.dumps(probe_statuses,ensure_ascii=False)}`;
переходные 502/503 при преднамеренном restart учитываются отдельно от тестовых отказов.

- **{s['successfulGenerations']} успешных генерации**: реальные ответы, HTTP 200 и финальный done.
- **{s['expectedRejections']} ожидаемых отказов**: auth, ввод, лимитеры и readiness при outage.
- **{diagnostics} успешных диагностических HTTP-запросов**.
- **1 намеренно отменённый поток**, после meta соединение закрыто клиентом.
- **Неожиданных ошибок именованных проверок: {len(s['unexpectedFailures'])}**.

Дополнительный разогревочный curl — `first-curl.ndjson`, ответ `4`, модельный
cold total_duration около 2.88 s. Финальная проверка после восстановления
конфигурации — `final-checks.json`, `final-curl.ndjson`, `status-final.json`.
Эти дополнительные проверки не смешиваются с количеством запросов основного прогона.

## Сценарии и конфигурации

10 последовательных коротких запросов: {sum(x.get('streamDone',False) for x in seq)}/10 с done,
ответ `4`; пауза **6.2 s** после завершения, чтобы не скрыть устойчивость rate limit.
Времена: min **{min(lat):.4f} s**, median **{statistics.median(lat):.4f} s**,
max **{max(lat):.4f} s**.

3 одновременных HTTP-запроса при штатных лимитах:
{sum(x.get('streamDone',False) for x in concurrent)}/3 с done,
времена {', '.join(str(x['elapsedSeconds'])+' s' for x in concurrent)}.
Генерации выполнялись последовательно: active=1, bounded FIFO queue=3.
Многоходовый follow-up получил предыдущие user/assistant сообщения и ответил
**{follow.get('answer','(нет ответа)')}**. Это API-проверка с сервера, не браузер второго устройства.

Штатные лимиты: num_ctx=8192, num_predict=512, temperature=0, seed=0,
rate=10/min burst=3, queue=3, queue wait=30 s, request body=10 s,
absolute generation deadline=180 s. Клиентские model/options запрещены.
4 запроса при новом bucket дали {dict(Counter(x['httpStatus'] for x in rate))};
у 429 присутствует Retry-After. Данные конфигурации, payload, размеры body,
метрики Ollama и времена каждого сценария включены в JSON.

Очередь проверялась отдельно: временно rate=600/min, burst=20, queue=1,
wait=30 s для переполнения, wait=0.15 s для таймаута ожидания.
Получены разные 503 с понятными объяснениями. Rate limit эти проверки не маскировал.
Body >256 KiB дал 413 от Caddy; большой контекст дал отдельный 413 от backend
до модели. Проверка контекста: UTF-8 byte-BPE верхняя оценка всей ChatML-истории
с system и assistant prefix, +256 служебных токенов, +512 резерв ответа.
Это не точный подсчёт; никакого молчаливого удаления истории.

Отмена HTTPS-клиента освободила слот: active=0, queued=0; следующий запрос получил done.
Недоступность Ollama: только day-30 backend временно направлен на закрытый
loopback 11435, общий Ollama других дней не остановлен. live=200, ready=503,
chat=502; после возврата 11434 ready=200 и настоящий ответ с done.
SIGKILL day-30 backend: launchd сменил PID {restart['oldPid']} → {restart['newPid']}
за **{restart['elapsedSeconds']} s**; следующий реальный запрос успешен.
После сценариев штатная private config восстановлена.

## Автоматические и завершающие проверки

**22 tests OK** в `unit-tests.txt`, только fake Ollama, отдельно от настоящих ответов.
Проверены базовый многоходовый streaming, валидация, Origin, доверенная proxy identity,
серверные параметры, весь контекст/UTF-8, token bucket/refill/per-user,
очередь/отмена/таймаут, большой и медленный body, абсолютный generation deadline,
изменённый template, error/incomplete stream после HTTP 200 и invalid Unicode.
Пустой systemPrompt отправляется явным system-сообщением, чтобы Ollama не подставлял
заводской system вне бюджета. Сверено с исходным кодом Ollama v0.35.0.
Python/JS/shell syntax checks прошли.

`final-checks.json`: доставка прежнего UI через HTTPS, прямой backend без proxy token
даёт 403, LAN-порты 8030/11434 закрыты, авторизованный curl получает реальный ответ.
`previous-files-verification.json`: все ранее существовавшие файлы дней 1–29 сверены по
SHA-256. `secret-check.json`: секретов нет в сдаваемых файлах/Git и текущих operational logs.
`log-redaction.json`: настоящий 502 при остановленном backend не записал request headers.

При промежуточной доработке проверка метаданных ошибочно запретила заводской system
модели; это вызвало ожидаемо выявленные, но незапланированные 502 в повторном прогоне.
Дефект исправлен явным system-сообщением, повторный итоговый прогон приведён выше.
Промежуточные результаты сохранены с префиксом `refinement-` и не выдаются за успех.
Также проверка логов нашла proxy token в стандартном error-логе Caddy: старое значение
редактировано, token заменён, объект request полностью исключён из operational logs.
Пользовательский пароль не менялся. Тест подтвердил отсутствие token после реального 502.

## Что не выполнено

**Второе устройство не проверено**: доступное устройство пользователем не подтверждено.
LAN-IP с того же Mac не заменяет этот тест.
**Браузерная проверка через HTTPS выполнена на том же Mac** в Яндекс Браузере:
пользователь настроил доверие CA, затем успешно проверены авторизация, первый
ответ и follow-up с историей. Оба ответа содержали САТУРН. Данные в `browser-check.json`.
Встроенный браузер Codex после настройки доверия вернул ERR_BLOCKED_BY_CLIENT.
**Видео не записано и отложено по просьбе пользователя**. Обновлённый сценарий
немой демонстрации на 60–90 секунд, 5–10 секунд на действие, со стартом с пустого
чата и промптом для отдельной сессии — `../VIDEO_SCRIPT.md`.
Интернет/VPS не проверены: реальный сервис развёрнут в домашней LAN.
Короткий прогон не доказывает production-надёжность. macOS user LaunchAgents требуют
входа пользователя; сон/закрытая крышка/выключение/потеря сети делают сервис недоступным.
Caffeinate предотвращает только idle-сон на AC.

## Все именованные проверки

| Сценарий | HTTP / PID | Время, s | Пройден |
|---|---:|---:|---|
{rows}
'''
(A/'checks.md').write_text(text)
print('Report updated from measured results')
