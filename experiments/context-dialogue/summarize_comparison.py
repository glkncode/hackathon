"""Aggregate manually reviewed factual answers, not generated status counts."""
import argparse
from collections import Counter
import json
from pathlib import Path
import statistics

from summarize import percentile


ROOT = Path(__file__).resolve().parents[2]/'.local-integration/context-dialogue'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--run', required=True)
    parser.add_argument('--modes', default='baseline,context')
    args = parser.parse_args()
    modes = args.modes.split(',')
    assert modes[0] == 'baseline' and len(set(modes)) == len(modes)
    assert all(m in {'baseline','context'} for m in modes)
    result = {}
    observations = {}
    for mode in modes:
        folder = ROOT/f'{args.run}-{mode}'
        rows = json.loads((folder/'observations.json').read_text(encoding='utf-8'))
        corpus = json.loads((folder/'corpus.json').read_text(encoding='utf-8'))
        reviews = json.loads((folder/'review.json').read_text(encoding='utf-8'))
        review = {r['key']: r for r in reviews}
        keys = {f"{r['Scenario']}/{r['Step']}" for r in rows}
        assert keys == set(review) and len(rows) == len(review)
        documents = {d['title']: d for d in corpus['documents']}
        for r in rows:
            trace = r.get('Trace') or {}
            candidates = trace.get('gate_candidates', [])
            for s in [*(r['Turn']['assistant_message']['sources'] or []), *candidates]:
                doc = documents[s['document_title']]
                assert not doc.get('private')
                assert s['version_number'] == 1 and doc['chunks'][s['chunk_index']] == s['content']
            if 'gate_candidates' in trace and not trace.get('gate_error_code'):
                by_id = {s['chunk_id']: s for s in candidates}
                selected = trace.get('gate_selected_ids') or []
                assert len(selected) == len(set(selected)) and set(selected) <= set(by_id)
                for s in trace.get('generation_sources', []):
                    assert s['chunk_id'] in selected
                    original = by_id[s['chunk_id']]
                    for field in ('document_title', 'version_number', 'content'):
                        assert s[field] == original[field]
        reviewed = list(review.values())
        def metric(field):
            applicable = [r[field] for r in reviewed if r[field] is not None]
            return {'passed': sum(applicable), 'total': len(applicable)}
        events = [e for r in rows for e in r['Logs']]
        resolver = [e for e in events if e['event'] == 'chat context resolver completed']
        durations = [e['duration']/1e6 for e in resolver]
        result[mode] = {
            'questions': len(rows),
            **{field: metric(field) for field in (
                'status_correct', 'factual', 'complete', 'conditions_preserved',
                'clarification_appropriate', 'refusal_correct', 'citation_support', 'direct_answer')},
            'correct_and_complete': sum(r['status_correct'] and r['factual'] and r['complete'] is not False
                                        and r['conditions_preserved'] is not False and r['citation_support'] is not False
                                        and r['clarification_appropriate'] and r['refusal_correct'] is not False
                                        for r in reviewed),
            'readability_mean_1_to_5': round(statistics.mean(r['readability'] for r in reviewed if r['readability'] is not None), 2),
            'readability_scored': sum(r['readability'] is not None for r in reviewed),
            'response_modes': dict(Counter(r['Turn']['assistant_message']['mode'] for r in rows)),
            'chat_ms': {'p50': percentile([r['DurationMS'] for r in rows], .5),
                        'p95': percentile([r['DurationMS'] for r in rows], .95)},
            'resolver_calls': len(resolver),
            'additional_qwen_calls': sum(e.get('llm_calls',1) for e in resolver),
            # Both clients make exactly one attempt. Resolver's own count is
            # recorded separately, including failed/invalid plans.
            'gate_qwen_calls': sum('gate_query' in r['Trace'] for r in rows),
            'generation_qwen_calls': sum('generation_query' in r['Trace'] for r in rows),
            'source_boundary_omissions': sum(e.get('omitted_source_count', 0) for e in events
                                             if e['event'] == 'chat generation input'),
            'multi_source_generation_turns': sum(len(r['Trace'].get('generation_sources', [])) > 1 for r in rows),
            'resolver_ms': {'p50': percentile(durations,.5), 'p95': percentile(durations,.95)} if durations else None,
            'resolver_outcomes': dict(Counter(e['outcome'] for e in resolver)),
            'resolver_failure_codes': dict(Counter(e['reason'] for e in resolver if e['reason'])),
            'refusal_causes': dict(Counter(e['reason'] for e in events if e['event']=='retrieval verdict')),
            'generation_statuses': dict(Counter(e['status'] for e in events if e['event']=='chat generation completed')),
            'generation_failures': dict(Counter(e['reason'] for e in events if e['event']=='chat RAG fallback')),
            'semantic_fallback_codes': dict(Counter(e['reason'] for e in events if e['event']=='semantic search FTS fallback')),
            'evidence_error_codes': dict(Counter(e['error_code'] for e in events if e['event']=='evidence gate completed' and not e['success'])),
            'technical_errors': dict(Counter(r['ErrorCode'] for r in rows if r.get('ErrorCode'))),
            'all_citations_exact': True,
            'issues': [r for r in reviewed if not r['status_correct'] or not r['factual'] or r['complete'] is False
                       or r['conditions_preserved'] is False or r['citation_support'] is False
                       or not r['clarification_appropriate'] or r['refusal_correct'] is False],
        }
        controls_path = folder/'controls.json'
        result[mode]['controls'] = json.loads(controls_path.read_text(encoding='utf-8')) if controls_path.exists() else json.loads((folder/'control-failure.json').read_text(encoding='utf-8'))
        observations[mode] = rows
    before = {(r['Scenario'],r['Step']): r['DurationMS'] for r in observations['baseline']}
    for mode in modes[1:]:
        delta = [r['DurationMS']-before[(r['Scenario'],r['Step'])] for r in observations[mode]]
        result[mode]['paired_extra_chat_ms'] = {'p50': percentile(delta,.5), 'p95': percentile(delta,.95),
                                                'mean': round(statistics.mean(delta),1)}
        baseline = {(r['Scenario'],r['Step']): r for r in observations['baseline']}
        both = [r['DurationMS']-baseline[(r['Scenario'],r['Step'])]['DurationMS'] for r in observations[mode]
                if r['Turn']['assistant_message']['mode']=='generated'
                and baseline[(r['Scenario'],r['Step'])]['Turn']['assistant_message']['mode']=='generated']
        result[mode]['extra_ms_both_generated'] = {'n':len(both),'p50':percentile(both,.5),'p95':percentile(both,.95)} if both else None
    # Compare language on identical answered turns, without rewarding a variant
    # merely because it replaced an empty reply with a substantive answer.
    common = set.intersection(*[
        {f"{r['Scenario']}/{r['Step']}" for r in rows if r['Turn']['assistant_message']['mode']=='generated'}
        for rows in observations.values()])
    for mode in modes:
        reviewed = json.loads((ROOT/f'{args.run}-{mode}'/'review.json').read_text(encoding='utf-8'))
        result[mode]['readability_common_generated'] = {'n':len(common), 'mean':round(statistics.mean(r['readability'] for r in reviewed if r['key'] in common),2)}
    out = ROOT/f'{args.run}-summary.json'
    out.write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps(result,ensure_ascii=False,indent=2))


if __name__ == '__main__':
    main()
