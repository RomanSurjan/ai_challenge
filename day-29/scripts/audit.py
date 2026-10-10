#!/usr/bin/env python3
"""Audit raw payloads, shared full contexts, token bounds and index immutability."""
import json,hashlib,sys
from pathlib import Path
root=Path(__file__).resolve().parents[1]
seen={};observations=0;checks=[]
for name in sys.argv[1:]:
 d=json.loads(Path(name).read_text())
 for x in d['runs']:
  run=x['run'];key=x['question_id'];contexts=[a['result']['final_context'] for a in run['arms']]
  assert all(c==contexts[0] for c in contexts),f'arm context drift: {key}'
  if key in seen:assert contexts[0]==seen[key],f'stage context drift: {key}'
  seen[key]=contexts[0]
  for a in run['arms']:
   p=a['profile']
   for c in a['calls']:
    opts=c['payload']['options']
    assert opts=={'temperature':p['temperature'],'num_predict':p['max_tokens'],'num_ctx':p['context_window'],'seed':p['seed']}
    assert c['payload']['model']==p['model']
    assert c['payload']['messages']==c['messages']
    assert c['prompt_eval_count']<=c['input_token_upper_bound']
    if c.get('token_budget_method')=='exact_loaded_runner_chatml' and not c.get('error'):assert c['prompt_eval_count']==c['input_tokens_budget']
    if not c.get('error'):assert c.get('input_tokens_budget',c['input_token_upper_bound'])+p['max_tokens']<=p['context_window']
    observations+=1
    assert len(a['calls'])<=2,'more than one repair'
 checks.append({'file':name,'runs':len(d['runs']),'completed':bool(d.get('completed_at'))})
frozen=json.loads((root/'artifacts/selection-frozen.json').read_text())
for file,h in frozen['hashes'].items():assert hashlib.sha256((root/file).read_bytes()).hexdigest()==h,f'Frozen configuration/labels changed: {file}'
if (root/'artifacts/control.json').exists():
 ctrl=json.loads((root/'artifacts/control.json').read_text())
 if ctrl.get('completed_at'):
  assert len(ctrl['runs'])==18,'missing measured control pairs'
  grouped={}
  for x in ctrl['runs']:grouped.setdefault(x['question_id'],[]).append(x)
  assert len(grouped)==6 and all({x['repetition'] for x in xs}=={1,2,3} for xs in grouped.values())
  for xs in grouped.values():
   assert len({x['run']['context_sha256'] for x in xs})==1,'control context drift'
   assert [a['provider'] for a in xs[0]['run']['arms']]==[a['provider'] for a in xs[2]['run']['arms']]
   assert [a['provider'] for a in xs[0]['run']['arms']]==[a['provider'] for a in reversed(xs[1]['run']['arms'])]
before=json.loads((root/'artifacts/previous-files-before.json').read_text());changed=[]
for name,h in before.items():
 p=root.parent/name
 if hashlib.sha256(p.read_bytes()).hexdigest()!=h:changed.append(name)
assert not changed,f'previous files changed: {changed}'
result={'files':checks,'generation_calls_audited':observations,'shared_context_questions':len(seen),'previous_files_unchanged':len(before),'index_sha256':hashlib.sha256((root.parent/'day-21/artifacts/index-structural.json').read_bytes()).hexdigest()}
(root/'artifacts/audit.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n');print(json.dumps(result,ensure_ascii=False,indent=2))
