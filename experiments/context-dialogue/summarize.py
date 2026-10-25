"""Summarize a frozen run only AFTER reviewing actual answers and citations.

Usage: python summarize.py <run-directory>
review.json must have one {key, correct, reason} entry per observation; keys are
mode/scenario/step. Do not substitute mode=generated for factual correctness.
"""
import json
import statistics
import sys
from collections import Counter
from pathlib import Path


def percentile(values, p):
    values = sorted(values)
    pos = (len(values)-1)*p
    lo = int(pos)
    hi = min(lo+1, len(values)-1)
    return round(values[lo] + (values[hi]-values[lo])*(pos-lo), 1)


def key(row):
    return f'{row["Mode"]}/{row["Scenario"]}/{row["Step"]}'


def main():
    folder = Path(sys.argv[1])
    rows = json.loads((folder/'observations.json').read_text(encoding='utf-8'))
    corpus = json.loads((folder/'corpus.json').read_text(encoding='utf-8'))
    reviews = json.loads((folder/'review.json').read_text(encoding='utf-8'))
    review = {r['key']: r for r in reviews}
    assert len(review) == len(rows) and set(review) == {key(r) for r in rows}
    documents = {d['title']: d for d in corpus['documents']}
    # Source identity/content correctness is separate from answer correctness.
    for r in rows:
        for source in r['Turn']['assistant_message']['sources']:
            doc = documents[source['document_title']]
            assert not doc.get('private')
            assert source['version_number'] == 1
            assert doc['chunks'][source['chunk_index']] == source['content']
    result = {}
    for mode in ('baseline', 'context'):
        subset = [r for r in rows if r['Mode'] == mode]
        failures = Counter()
        resolver_ms = []
        qwen_calls = 0
        for r in subset:
            for event in r['Logs']:
                if event['event'] == 'chat context resolver completed':
                    resolver_ms.append(event['duration']/1e6)
                    qwen_calls += event.get('llm_calls', 1)
                    if event['outcome'] in ('fallback', 'timeout'):
                        failures['resolver_'+event['outcome']] += 1
                if event['event'] == 'retrieval verdict':
                    failures[event['reason']] += 1
                if event['event'] == 'chat generation completed' and event['status'] != 'answered':
                    failures['generation_'+event['status']] += 1
                if event['event'] == 'chat RAG fallback':
                    failures['generation_error_'+event['reason']] += 1
        groups = {
            'positive': [r for r in subset if r['Question']['Doc']],
            'negative': [r for r in subset if r['Question']['Expected'] == 'insufficient_information'],
            'clarification': [r for r in subset if r['Question']['Expected'] == 'clarification'],
        }
        result[mode] = {
            'reviewed': len(subset),
            'correct': sum(review[key(r)]['correct'] for r in subset),
            'groups': {g: {'total': len(rs), 'correct': sum(review[key(r)]['correct'] for r in rs)} for g, rs in groups.items()},
            'chat_ms': {'p50': percentile([r['DurationMS'] for r in subset], .5),
                        'p95': percentile([r['DurationMS'] for r in subset], .95)},
            'resolver_calls': len(resolver_ms),
            'additional_qwen_calls': qwen_calls,
            'resolver_ms': ({'p50': percentile(resolver_ms, .5), 'p95': percentile(resolver_ms, .95),
                             'total': round(sum(resolver_ms), 1)} if resolver_ms else None),
            'causes': dict(failures),
            'incorrect': [review[key(r)] for r in subset if not review[key(r)]['correct']],
        }
    before = {(r['Scenario'], r['Step']): r for r in rows if r['Mode'] == 'baseline'}
    delta = [r['DurationMS'] - before[(r['Scenario'], r['Step'])]['DurationMS'] for r in rows if r['Mode'] == 'context']
    result['paired_extra_chat_ms'] = {'p50': percentile(delta, .5), 'p95': percentile(delta, .95),
                                      'mean': round(statistics.mean(delta), 1)}
    result['all_citations_exact'] = True
    (folder/'summary.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
