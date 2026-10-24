"""Validate an explicit agent/expert review against frozen observations."""
import argparse
import hashlib
import json
from pathlib import Path

import numpy as np

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('directory',type=Path)
args=parser.parse_args()
root=args.directory
answers_path=root/'answers.json'
answers=json.loads(answers_path.read_text(encoding='utf-8'))
manifest=json.loads((root/'manifest.json').read_text(encoding='utf-8'))
review=json.loads((root/'review.json').read_text(encoding='utf-8'))
assert hashlib.sha256(answers_path.read_bytes()).hexdigest()==review['answers_sha256']
assert {(r['method'],r['query_id']) for r in answers}=={(m,q['id']) for m in ['fts','semantic','hybrid'] for q in manifest['questions'] if q['split']=='independent'}
allowed={s['chunk_id'] for s in manifest['chunks']}
summary={}
for method in ['fts','semantic','hybrid']:
    rows=[r for r in answers if r['method']==method]
    verdicts=review['methods'][method]
    assert set(verdicts)=={r['query_id'] for r in rows}
    for r in rows:
        assert set(r['used_source_ids'])<=allowed
        assert len(set(r['used_source_ids']))==len(r['used_source_ids'])
        v=verdicts[r['query_id']]
        assert type(v['grounded_correct']) is bool and type(v['false_factual_answer']) is bool
        if v['grounded_correct']:
            assert r['mode']=='generated' and r['gold_chunk_ids']
            assert set(r['used_source_ids'])==set(r['gold_chunk_ids'])
    positive=[r for r in rows if r['gold_chunk_ids']]
    negative=[r for r in rows if not r['gold_chunk_ids']]
    generated=[r for r in rows if r['mode']=='generated']
    avg=lambda values:float(np.mean(values)) if values else None
    summary[method]={
        'positive_queries':len(positive),'negative_queries':len(negative),
        'grounded_correct_count':sum(verdicts[r['query_id']]['grounded_correct'] for r in positive),
        'grounded_answer_accuracy':avg([verdicts[r['query_id']]['grounded_correct'] for r in positive]),
        'generated_answers':len(generated),
        'citation_correctness':avg([verdicts[r['query_id']]['citations_support_claims'] for r in generated]),
        'false_factual_answer_rate_on_negatives':avg([verdicts[r['query_id']]['false_factual_answer'] for r in negative]),
        'negative_answered_status_rate':avg([r['mode']=='generated' for r in negative]),
        'chat_latency_ms_p50':float(np.percentile([r['elapsed_ms'] for r in rows],50)),
        'chat_latency_ms_p95':float(np.percentile([r['elapsed_ms'] for r in rows],95)),
    }
(root/'review-summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(summary,ensure_ascii=False,indent=2))
