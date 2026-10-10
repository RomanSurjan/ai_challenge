#!/usr/bin/env python3
"""Read-only deployment verification + one real curl generation, no secrets printed."""
import hashlib
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import time
import urllib.request

ROOT=Path(__file__).resolve().parents[1]
RUNTIME=ROOT/'.runtime'
ARTIFACTS=ROOT/'artifacts'


def main():
    os.umask(0o077)
    ARTIFACTS.mkdir(exist_ok=True)
    config=json.loads((RUNTIME/'config.json').read_text())
    url=next(o for o in config['allowed_origins'] if 'localhost' not in o)
    checks=[]
    for port in [8030,11434]:
        start=time.monotonic()
        try:
            with socket.create_connection(('192.168.3.53',port),timeout=2): reachable=True
        except OSError: reachable=False
        checks.append({'check':f'LAN_port_{port}_closed','passed':not reachable,'elapsedSeconds':round(time.monotonic()-start,4)})
    conn=http.client.HTTPConnection('127.0.0.1',8030,timeout=5)
    conn.request('GET','/health/live'); response=conn.getresponse(); response.read(); conn.close()
    checks.append({'check':'backend_direct_without_proxy_secret','status':response.status,'passed':response.status==403})
    for path in ['/','/app.js','/styles.css','/health/ready','/api/status']:
        start=time.monotonic()
        result=subprocess.run(['curl','--config',str(RUNTIME/'curl.conf'),'--silent','--show-error','--max-time','10',
                               '-o',str(RUNTIME/'last-response'),'-w','%{http_code}',url+path],capture_output=True,text=True)
        status=int(result.stdout) if result.stdout.isdigit() else 0
        raw=(RUNTIME/'last-response').read_bytes()
        checks.append({'check':path,'status':status,'bytes':len(raw),'passed':status==200,
                       'elapsedSeconds':round(time.monotonic()-start,4)})
        if path=='/api/status': (ARTIFACTS/'status-final.json').write_bytes(raw)
    data=json.dumps({'systemPrompt':'Отвечай одним словом.', 'messages':[{'role':'user','content':'Сколько будет 2+2?'}]})
    result=subprocess.run(['curl','--config',str(RUNTIME/'curl.conf'),'--silent','--show-error','--max-time','190',
                           url+'/api/chat','-H','Content-Type: application/json','--data-binary',data],capture_output=True)
    events=[json.loads(l) for l in result.stdout.splitlines()]
    (ARTIFACTS/'final-curl.ndjson').write_bytes(result.stdout)
    checks.append({'check':'final_authorized_curl','passed':bool(events) and events[-1].get('type')=='done',
                   'answer':''.join(e.get('content','') for e in events)})
    ps=json.load(urllib.request.urlopen('http://127.0.0.1:11434/api/ps',timeout=5))
    (ARTIFACTS/'ollama-ps-final.json').write_text(json.dumps(ps,indent=2)+'\n')
    (ARTIFACTS/'service-status-final.json').write_bytes(subprocess.check_output(['python3',str(ROOT/'scripts/service.py'),'status']))
    (ARTIFACTS/'listeners.txt').write_bytes(subprocess.check_output(['/usr/sbin/lsof','-nP','-iTCP:8030','-iTCP:8443','-iTCP:11434','-sTCP:LISTEN']))
    (ARTIFACTS/'pmset-final.txt').write_bytes(subprocess.check_output(['pmset','-g','assertions']))
    baseline=json.loads((ARTIFACTS/'previous-files-before.json').read_text())
    changed=[p for p,h in baseline.items() if not Path(p).exists() or hashlib.sha256(Path(p).read_bytes()).hexdigest()!=h]
    (ARTIFACTS/'previous-files-verification.json').write_text(json.dumps({'checkedFiles':len(baseline),'changed':changed,'passed':not changed},indent=2)+'\n')
    credentials=dict(l.split(': ',1) for l in (RUNTIME/'access.txt').read_text().splitlines())
    sensitive=[config['proxy_secret'].encode(),credentials['Password'].encode()]
    exposed=[]; logged=[]
    for p in ROOT.rglob('*'):
        if not p.is_file() or '.runtime' in p.parts or '__pycache__' in p.parts: continue
        raw=p.read_bytes()
        if any(value in raw for value in sensitive) or (b'BEGIN '+b'PRIVATE KEY') in raw: exposed.append(str(p.relative_to(ROOT)))
    for p in RUNTIME.glob('*.log'):
        raw=p.read_bytes()
        if any(value in raw for value in sensitive): logged.append(p.name)
    (ARTIFACTS/'secret-check.json').write_text(json.dumps({'exposedInDeliverables':exposed,'exposedInLogs':logged,
        'passed':not exposed and not logged,'runtimeExcludedByGit':subprocess.run(['git','check-ignore',str(RUNTIME/'config.json')],capture_output=True).returncode==0},indent=2)+'\n')
    (ARTIFACTS/'final-checks.json').write_text(json.dumps(checks,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps({'checks':len(checks),'passed':all(c['passed'] for c in checks),'previousFilesUnchanged':not changed,
                      'secretsAbsent':not exposed and not logged},ensure_ascii=False))
    if not all(c['passed'] for c in checks) or changed or exposed or logged: raise SystemExit(1)


if __name__=='__main__': main()
