#!/usr/bin/env python3
"""Short, live HTTPS demo. Credentials stay in the private curl config."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime
import json
from pathlib import Path
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
URL = 'https://192.168.3.53:8443'


def request(index, payload=None, authenticated=True):
    started = time.monotonic()
    with tempfile.TemporaryDirectory(dir=ROOT / '.runtime') as tmp:
        headers = Path(tmp) / 'headers'
        body = Path(tmp) / 'body'
        command = ['curl', '--silent', '--show-error', '--max-time', '180']
        if authenticated:
            command += ['--config', str(ROOT / '.runtime/curl.conf')]
        else:
            command += ['--cacert', str(ROOT / 'artifacts/lan-root.crt')]
        command += ['-D', str(headers), '-o', str(body), '-w', '%{http_code}']
        if payload is not None:
            command += ['-H', 'Content-Type: application/json', '--data-binary', '@-', URL + '/api/chat']
        else:
            command += [URL + '/health/live']
        result = subprocess.run(command, input=json.dumps(payload) if payload else None,
                                text=True, capture_output=True, check=True)
        raw = body.read_text()
        events = [json.loads(line) for line in raw.splitlines() if line.strip()]
        answer = ''.join(e.get('content', '') for e in events if e.get('type') == 'chunk')
        retry = next((line.split(':', 1)[1].strip() for line in headers.read_text().splitlines()
                      if line.lower().startswith('retry-after:')), None)
        return {'request': index, 'http': int(result.stdout), 'answer': answer,
                'done': any(e.get('type') == 'done' for e in events),
                'retryAfter': retry, 'events': events,
                'elapsedSeconds': round(time.monotonic() - started, 4)}


def show(result):
    print(f"  Запрос {result['request']} → HTTP {result['http']}", flush=True)
    if result['answer']:
        print(f"  Ответ: {result['answer']}   •   done={str(result['done']).lower()}", flush=True)
    if result['retryAfter']:
        print(f"  Retry-After: {result['retryAfter']} s", flush=True)
    for event in result['events']:
        if event.get('error'):
            print('  ' + event['error'], flush=True)
    print(flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('mode', choices=['auth', 'parallel', 'burst', 'context', 'checks'])
    args = parser.parse_args()
    print('\nДЕНЬ 30 • Локальная LLM как приватный сервис', flush=True)
    print(URL + '  |  TLS проверяется\n', flush=True)
    if args.mode == 'checks':
        checks = (ROOT / 'artifacts/checks.md').read_text()
        required = ['44/44', '10/10 с done', '3/3 с done', '22 tests OK',
                    'SIGKILL day-30 backend', 'Второе устройство не проверено']
        assert all(text in checks for text in required)
        print('Итоги artifacts/checks.md • прогон 10.10.2026, 20:53 MSK\n')
        print('  Именованные проверки: 44/44 • неожиданных ошибок: 0\n'
              '  Последовательно: 10/10 • параллельно: 3/3\n'
              '  Генерация: active=1 • FIFO очередь=3\n'
              '  Очередь: overflow / timeout → ожидаемые 503\n'
              '  Отмена: слот освобождён • следующий запрос успешен\n'
              '  Перезапуск launchd: 2.49 s • реальный ответ после рестарта\n'
              '  Автотесты: 22 OK (fake Ollama, отдельно от live)\n\n'
              '  Браузер и сервер: тот же Mac.\n'
              '  Второе устройство не проверено.')
        return
    short = {'systemPrompt': 'Отвечай одним словом.',
             'messages': [{'role': 'user', 'content': 'Сколько будет 2+2?'}]}
    if args.mode == 'auth':
        print('GET /health/live • без авторизации\n', flush=True)
        results = [request(1, authenticated=False)]
    elif args.mode == 'context':
        print('POST /api/chat • systemPrompt="" • контекст: "я" × 4000\n', flush=True)
        results = [request(1, {'systemPrompt': '', 'messages': [{'role': 'user', 'content': 'я' * 4000}]})]
    elif args.mode == 'parallel':
        print('Три HTTP-запроса одновременно • генерации последовательно\n', flush=True)
        with ThreadPoolExecutor(max_workers=3) as pool:
            futures = [pool.submit(request, i, short) for i in range(1, 4)]
            results = [future.result() for future in futures]
    else:
        print('Четыре быстрых запроса • штатные 10/min, burst=3\n', flush=True)
        results = [request(i, short) for i in range(1, 5)]
    for result in results:
        show(result)
    expected = {'auth': [401], 'parallel': [200, 200, 200],
                'burst': [200, 200, 200, 429], 'context': [413]}[args.mode]
    assert [r['http'] for r in results] == expected, 'Unexpected HTTP results'
    assert all(r['done'] for r in results if r['http'] == 200)
    if args.mode == 'burst':
        assert results[-1]['retryAfter'] is not None
    record = {'date': datetime.now().astimezone().isoformat(), 'mode': args.mode,
              'url': URL, 'source': 'fresh HTTPS requests, TLS verified', 'results': results}
    (ROOT / 'artifacts' / f'video-{args.mode}.json').write_text(json.dumps(record, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    main()
