#!/usr/bin/env python3
"""Print recorded answers and quotes for manual scoring; no model calls."""
import json,sys
for file in sys.argv[1:]:
 d=json.load(open(file));print('\nFILE',file)
 for x in d['runs']:
  print('\n',x['question_id'],x['repetition'],x['run']['question'])
  for a in x['run']['arms']:
   r=a['result'];print(a['provider'],r['status'],r.get('abstention_reason',''),r['latency']['total_ms'],'ms');print(r['final_answer']);print('clarification:',r['clarification_question'])
   for c in r['citations']: print(c['claim_ids'],c['source_id'],repr(c['quote']))
   print('judge',[(v['verdict'],v['reason']) for v in r['entailment_verdicts']]);print('errors',r.get('error',''),r['validation_errors'])
