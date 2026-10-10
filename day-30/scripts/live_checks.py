#!/usr/bin/env python3
"""Real HTTPS/LAN + real Ollama checks. No fake responses; no credentials in artifacts.

Temporarily adjusts ONLY the private Day 30 backend config for isolated limit
scenarios; always restores it and restarts backend in finally. Run on server.
"""
import base64
import concurrent.futures
import http.client
import hashlib
import json
import os
from pathlib import Path
import signal
import ssl
import subprocess
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT/'.runtime'
ARTIFACTS = ROOT/'artifacts'


def main():
    os.umask(0o077)
    ARTIFACTS.mkdir(exist_ok=True)
    original = (RUNTIME/'config.json').read_text()
    base_config = json.loads(original)
    url = next(x for x in base_config['allowed_origins'] if 'localhost' not in x)
    parsed = urllib.parse.urlparse(url)
    access = dict(l.split(': ',1) for l in (RUNTIME/'access.txt').read_text().splitlines())
    auth = 'Basic '+base64.b64encode(f"{access['Username']}:{access['Password']}".encode()).decode()
    context = ssl.create_default_context(cafile=RUNTIME/'root.crt')
    records = []
    probes = []
    current_public = {k:v for k,v in base_config.items() if k!='proxy_secret'}
    start_wall = time.time()

    def req(label, payload=None, *, path='/api/chat', authorization=True, wrong=False,
            origin=None, expected=(200,), method='POST', raw=None, cancel=False, headers=None):
        start = time.monotonic()
        c = http.client.HTTPSConnection(parsed.hostname,parsed.port,context=context,timeout=230)
        h = {'Content-Type':'application/json'}
        if authorization: h['Authorization'] = 'Basic ZGVtbzp3cm9uZw==' if wrong else auth
        if origin is not None: h['Origin'] = origin
        h.update(headers or {})
        data = raw if raw is not None else (json.dumps(payload,ensure_ascii=False).encode() if payload else None)
        record = {'label':label,'startedUnix':time.time(),'expectedStatuses':list(expected),'config':dict(current_public),
                  'requestPayload':payload if payload else None,
                  'requestBodyBytes':len(data) if data else 0,
                  'requestBodySha256':hashlib.sha256(data).hexdigest() if data else None,
                  'transport':'HTTPS verified LAN from SAME Mac (not second device)'}
        try:
            c.request(method,path,data,h)
            response = c.getresponse()
            record['httpStatus'] = response.status
            record['headerSeconds'] = round(time.monotonic()-start,4)
            record['retryAfter'] = response.getheader('Retry-After')
            if cancel:
                record['firstEvent'] = json.loads(response.readline())
                # Closing the body explicitly closes TCP even after HTTP Connection: close.
                response.close(); c.close()
                record['cancelledByClient'] = True
            else:
                body = response.read()
                if response.getheader('Content-Type','').startswith('application/x-ndjson'):
                    events = [json.loads(l) for l in body.splitlines() if l.strip()]
                    record['events'] = events
                    record['answer'] = ''.join(e.get('content','') for e in events)
                    record['streamDone'] = bool(events) and events[-1].get('type')=='done'
                    record['streamError'] = any(e.get('type')=='error' for e in events)
                else:
                    try: record['response'] = json.loads(body)
                    except ValueError: record['response'] = body.decode(errors='replace')[:1000]
            record['passed'] = (response.status in expected and
                (response.status!=200 or method=='GET' or cancel or record.get('streamDone',False)))
        except Exception as exc:
            record['unexpectedException'] = type(exc).__name__+': '+str(exc)
            record['passed'] = False
        finally:
            c.close()
        record['elapsedSeconds'] = round(time.monotonic()-start,4)
        if label in ('startup','poll'): probes.append(record)
        return record

    def save(record):
        records.append(record)
        (ARTIFACTS/'live-requests.json').write_text(json.dumps(records,ensure_ascii=False,indent=2)+'\n')
        print(json.dumps({k:record.get(k) for k in ['label','httpStatus','elapsedSeconds','passed']},ensure_ascii=False),flush=True)
        return record

    def restart(changes=None):
        nonlocal current_public
        config = dict(base_config); config.update(changes or {})
        (RUNTIME/'config.json').write_text(json.dumps(config,indent=2)+'\n')
        current_public = {k:v for k,v in config.items() if k!='proxy_secret'}
        subprocess.run(['python3',str(ROOT/'scripts/service.py'),'restart','--component','backend'],
                       check=True,stdout=subprocess.DEVNULL)
        deadline=time.monotonic()+15
        while time.monotonic()<deadline:
            r=req('startup',path='/health/live',method='GET',expected=(200,502,503))
            if r.get('httpStatus')==200: return
            time.sleep(.15)
        raise RuntimeError('backend restart did not become live')

    short={'systemPrompt':'Отвечай одним словом.','messages':[{'role':'user','content':'Сколько будет 2+2?'}]}
    long={'systemPrompt':'Продолжай до конца, без сокращений.', 'messages':[{'role':'user','content':'Напиши по одному числу на строке от 1 до 200, полностью.'}]}
    try:
        save(req('unauthorized',short,authorization=False,expected=(401,)))
        save(req('wrong_credentials',short,wrong=True,expected=(401,)))
        save(req('spoofed_proxy_headers',short,authorization=False,
                 headers={'X-Private-Token':'fake','X-Authenticated-User':'admin'},expected=(401,)))
        save(req('origin_denied',short,origin='https://evil.example',expected=(403,)))
        save(req('invalid_json',raw=b'{broken',expected=(400,)))
        save(req('client_model_denied',{**short,'model':'qwen3:4b'},expected=(400,)))
        save(req('context_overflow',{'systemPrompt':'','messages':[{'role':'user','content':'я'*4000}]},expected=(413,)))
        save(req('body_overflow',raw=b'x'*(256*1024+1),expected=(413,)))
        save(req('live',path='/health/live',method='GET'))
        save(req('ready',path='/health/ready',method='GET'))
        restart()
        for i in range(10):
            save(req(f'sequential_{i+1:02}',short))
            # Pacing respects deployed 10/minute rate. Delay is included in protocol.
            if i<9: time.sleep(6.2)
        restart()
        first=save(req('history_first',{'systemPrompt':'Отвечай кратко.', 'messages':[{'role':'user','content':'Запомни: кодовое слово — САТУРН. Подтверди одним словом.'}]}))
        follow={'systemPrompt':'Отвечай кратко.','messages':[{'role':'user','content':'Запомни: кодовое слово — САТУРН. Подтверди одним словом.'},
                {'role':'assistant','content':first['answer']},{'role':'user','content':'Какое кодовое слово? Ответь одним словом.'}]}
        save(req('history_follow_up',follow,origin=url))
        restart()
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            for r in pool.map(lambda i:req(f'concurrent_default_{i}',short),range(1,4)): save(r)
        restart()
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            rate_records = list(pool.map(lambda i:req(f'rate_burst_{i}',short,expected=(200,429)),range(1,5)))
            assert sorted(r['httpStatus'] for r in rate_records)==[200,200,200,429]
            assert next(r for r in rate_records if r['httpStatus']==429)['retryAfter'] is not None
            for r in rate_records: save(r)
        restart({'rate_per_minute':600,'burst':20,'queue_size':1,'queue_timeout':30})
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            a=pool.submit(req,'queue_active',long)
            time.sleep(.25)
            b=pool.submit(req,'queue_waiting',short)
            time.sleep(.25)
            save(req('queue_overflow',short,expected=(503,)))
            save(a.result()); save(b.result())
        restart({'rate_per_minute':600,'burst':20,'queue_size':1,'queue_timeout':.15})
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            a=pool.submit(req,'queue_timeout_active',long)
            time.sleep(.25)
            save(req('queue_timeout',short,expected=(503,)))
            save(a.result())
        restart()
        save(req('cancel',long,cancel=True))
        deadline=time.monotonic()+5
        while time.monotonic()<deadline:
            r=req('poll',path='/api/status',method='GET')
            if r.get('response',{}).get('queue',{}).get('active')==0: break
            time.sleep(.05)
        save(r | {'label':'slot_after_cancel','passed':r.get('response',{}).get('queue',{}).get('active')==0})
        save(req('after_cancel',short))
        # Make Ollama unreachable for THIS backend; leave other days' shared service running.
        restart({'ollama_url':'http://127.0.0.1:11435'})
        save(req('outage_live',path='/health/live',method='GET'))
        save(req('outage_ready',path='/health/ready',method='GET',expected=(503,)))
        save(req('outage_generation',short,expected=(502,)))
        restart()
        save(req('recovery_ready',path='/health/ready',method='GET'))
        save(req('recovery_generation',short))
        status=json.loads(subprocess.check_output(['python3',str(ROOT/'scripts/service.py'),'status']))
        old_pid=status['backend']['PID']
        restart_started=time.monotonic()
        os.kill(old_pid,signal.SIGKILL)
        deadline=time.monotonic()+20
        new_pid=None
        while time.monotonic()<deadline:
            status=json.loads(subprocess.check_output(['python3',str(ROOT/'scripts/service.py'),'status']))
            new_pid=status['backend'].get('PID')
            if new_pid and new_pid!=old_pid and req('poll',path='/health/live',method='GET').get('httpStatus')==200: break
            time.sleep(.25)
        save({'label':'launchd_automatic_restart','oldPid':old_pid,'newPid':new_pid,
              'passed':bool(new_pid and new_pid!=old_pid),'elapsedSeconds':round(time.monotonic()-restart_started,4)})
        save(req('after_backend_restart',short))
    finally:
        (RUNTIME/'config.json').write_text(original)
        subprocess.run(['python3',str(ROOT/'scripts/service.py'),'restart','--component','backend'],check=True,stdout=subprocess.DEVNULL)
        named_starts={r.get('startedUnix') for r in records}
        probes[:]=[p for p in probes if p.get('startedUnix') not in named_starts]
        summary={'startedUnix':start_wall,'finishedUnix':time.time(),'url':url,
                 'secondDevice':{'performed':False,'reason':'No second device confirmed'},
                 'video':{'recorded':False,'reason':'Browser CA not trusted; no interstitial bypass'},
                 'sequentialCount':10,'sequentialPacingSeconds':6.2,
                 'recordedChecks':len(records),'namedHttpRequests':sum('httpStatus' in r for r in records),'serviceProbes':len(probes),
                 'httpRequestsTotal':sum('httpStatus' in r for r in records)+len(probes),'passed':sum(r.get('passed',False) for r in records),
                 'unexpectedFailures':[r['label'] for r in records if not r.get('passed')],
                 'successfulGenerations':sum(r.get('streamDone',False) for r in records),
                 'expectedRejections':sum(r.get('passed',False) and r.get('httpStatus',0)>=400 for r in records),
                 'activeFinalConfig':{k:v for k,v in base_config.items() if k!='proxy_secret'}}
        (ARTIFACTS/'live-probes.json').write_text(json.dumps(probes,ensure_ascii=False,indent=2)+'\n')
        (ARTIFACTS/'live-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
        print(json.dumps(summary,ensure_ascii=False),flush=True)
    if not all(r.get('passed') for r in records): raise SystemExit(1)


if __name__=='__main__': main()
