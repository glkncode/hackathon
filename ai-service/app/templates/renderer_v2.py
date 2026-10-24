"""Structural rendering of SHA-pinned, explicitly configured original modules."""
import hashlib
import io
import json
import re
from copy import deepcopy
from datetime import date
from zipfile import ZipFile, ZIP_DEFLATED
from lxml import etree
from app.templates.profile import load_profile, validate, test_condition
from app.templates.engine import W, TemplateError, checked_archive, replace_text_range


def text_of(node):
    return ''.join(t.text or '' for t in node.iter(W+'t'))


def segments(items, values, dates):
    out = []
    for s in items:
        if s.literal is not None: out.append(s.literal)
        elif s.field is not None:
            v = values.get(s.field, '')
            if v is None: v = ''
            if s.field in dates and v: v = date.fromisoformat(v).strftime('%d.%m.%Y')
            if isinstance(v, list): v = s.join.join(v)
            if not isinstance(v, str): raise TemplateError('invalid_values')
            out.append(v)
        else: out.append(segments(s.then if test_condition(s.when, values) else s.otherwise, values, dates))
    value = ''.join(out)
    if len(value) > 32000: raise TemplateError('rendered_text_limit', 413)
    return value


def process_profile(content, raw_values, render, raw_profile):
    p = load_profile(raw_profile)
    if hashlib.sha256(content).hexdigest() != p.source_sha256:
        raise TemplateError('profile_source_mismatch')
    if len(raw_values.encode('utf-8')) > 65536: raise TemplateError('values_too_large', 413)
    def unique(pairs):
        result = {}
        for k,v in pairs:
            if k in result: raise TemplateError('invalid_values')
            result[k] = v
        return result
    values = json.loads(raw_values, object_pairs_hook=unique)
    if not isinstance(values, dict) or len(values) > 64: raise TemplateError('invalid_values')
    validation = validate(p, values)
    # Invalid values are reported, not interpolated or evaluated as document text.
    parts = checked_archive(content)
    from docx import Document
    Document(io.BytesIO(content))
    root = etree.fromstring(parts['word/document.xml'])
    body = root.find(W+'body')
    paragraphs = list(root.iter(W+'p'))
    tables = list(root.iter(W+'tbl'))
    def anchor(a):
        if a.paragraph >= len(paragraphs) or text_of(paragraphs[a.paragraph]) != a.text:
            raise TemplateError('profile_anchor_mismatch')
        return paragraphs[a.paragraph]
    def top(node):
        while node.getparent() is not body:
            node = node.getparent()
            if node is None: raise TemplateError('profile_anchor_mismatch')
        return node
    cleanup = p.document_cleanup
    protected = [anchor(a) for a in cleanup.protected]
    expected_protected = [n for n in paragraphs if text_of(n) in (
        'ДАТА / РЕГИСТРАЦИОННЫЙ НОМЕР (НЕ УДАЛЯТЬ)', 'ВИЗУАЛИЗАЦИЯ ЭП (НЕ УДАЛЯТЬ)')]
    if len(expected_protected)!=2 or set(protected)!=set(expected_protected): raise TemplateError('protected_block_configuration')
    protected_xml = [etree.tostring(n) for n in protected]
    def safe_remove(node):
        if node.tag == W+'sectPr' or any(n is node or node in n.iterancestors() for n in protected):
            raise TemplateError('protected_block')
        if node.getparent() is not None: node.getparent().remove(node)
    if cleanup.approval_table >= len(tables): raise TemplateError('profile_anchor_mismatch')
    approval = tables[cleanup.approval_table]
    if text_of(approval) != cleanup.approval_text or approval.getparent() is not body:
        raise TemplateError('profile_anchor_mismatch')
    marker = anchor(cleanup.marker)
    if text_of(marker)!='МОДУЛЬНЫЙ ДОКУМЕНТ' or 'УТВЕРЖДАЮ' not in cleanup.approval_text:
        raise TemplateError('profile_anchor_mismatch')
    first, last = anchor(cleanup.notes_start), anchor(cleanup.notes_end)
    if not text_of(first).startswith('Примечания по заполнению'): raise TemplateError('profile_anchor_mismatch')
    children = list(body)
    first_i, last_i = children.index(top(first)), children.index(top(last))
    if first_i > last_i: raise TemplateError('profile_anchor_mismatch')
    removals = [approval, marker] + children[first_i:last_i+1] + [anchor(a) for a in cleanup.remove_paragraphs]
    for n in removals:
        if any(n is x or n in x.iterancestors() for x in protected): raise TemplateError('protected_block')
    edits, occupied = [], {}
    for edit in p.bindings:
        if edit.paragraph >= len(paragraphs): raise TemplateError('profile_anchor_mismatch')
        node = paragraphs[edit.paragraph]
        text = text_of(node)
        if 'НЕ УДАЛЯТЬ' in text or text.count(edit.match) != 1: raise TemplateError('profile_anchor_mismatch')
        a = text.index(edit.match); b = a + len(edit.match)
        ranges = occupied.setdefault(edit.paragraph, [])
        if any(a < y and x < b for x,y in ranges): raise TemplateError('overlapping_bindings')
        ranges.append((a,b))
        edits.append((edit.paragraph, a, b, edit.value))
    blocks = []
    block_nodes = set()
    for block in p.conditional_blocks:
        start, end = anchor(block.start), anchor(block.end)
        if start.getparent() is not end.getparent(): raise TemplateError('profile_anchor_mismatch')
        siblings = list(start.getparent())
        a,b = siblings.index(start), siblings.index(end)
        if a > b: raise TemplateError('profile_anchor_mismatch')
        nodes = siblings[a:b+1]
        if any(n is x or n in x.iterancestors() for n in nodes for x in protected): raise TemplateError('protected_block')
        if any(n in block_nodes for n in nodes): raise TemplateError('overlapping_blocks')
        block_nodes.update(nodes)
        blocks.append((block,nodes))
    row_sets, row_plans = set(), []
    for r in p.repeaters:
        if r.table >= len(tables): raise TemplateError('profile_anchor_mismatch')
        table = tables[r.table]
        rows = table.findall(W+'tr')
        if not 0 <= r.start_row <= r.end_row < len(rows): raise TemplateError('profile_anchor_mismatch')
        proto = rows[r.start_row]
        if text_of(proto) != r.anchor or len(proto.findall(W+'tc')) != len(r.cells): raise TemplateError('profile_anchor_mismatch')
        affected = rows[r.start_row:r.end_row+1]
        if any(n in row_sets for n in affected): raise TemplateError('overlapping_repeaters')
        row_sets.update(affected)
        # Horizontal merges are copied intact; vertical merges need a separately reviewed profile.
        if any(list(n.iter(W+'vMerge')) for n in affected): raise TemplateError('unsupported_vertical_merge')
        if any(n is x or n in x.iterancestors() for n in affected for x in protected): raise TemplateError('protected_block')
        row_plans.append((r,table,proto,affected))
    numbered = [(a, anchor(a)) for a in p.numbered_paragraphs]
    for a,n in numbered:
        if not re.match(r'^[1-9][0-9]*\. ', a.text): raise TemplateError('invalid_numbering_anchor')
        # No opaque field/bookmark references may be silently invalidated by literal renumbering.
        if list(n.iter(W+'fldChar')) or list(n.iter(W+'bookmarkStart')): raise TemplateError('unsupported_numbering_reference')
    if not render:
        validation['checks'].append(dict(rule_id='structure.source',category='blocking',source=p.field_source.model_dump(),
            description='SHA-256 исходника, структурные якоря и защищённые зоны соответствуют профилю.',result='passed',method='sha256_and_xml_anchors'))
        return dict(fields=[dict(name=f.key, type='string', required=f.key in validation['required_fields']) for f in p.fields],
                    required_fields=validation['required_fields'], missing_fields=[f['key'] for f in validation['missing_fields']],
                    can_render=validation['ready'], validation=validation)
    if not validation['ready']: raise TemplateError('validation_failed', missing_fields=[f['key'] for f in validation['missing_fields']])
    dates = {f.key for f in p.fields if f.type=='date'}
    for index,a,b,ss in sorted(edits, key=lambda e:(e[0],e[1]), reverse=True):
        replace_text_range(list(paragraphs[index].iter(W+'t')), a,b,segments(ss,values,dates))
    for r,table,proto,affected in row_plans:
        if r.repeat_header and r.start_row>0:
            header=table.findall(W+'tr')[0]
            props=header.find(W+'trPr')
            if props is None: props=etree.Element(W+'trPr'); header.insert(0,props)
            if props.find(W+'tblHeader') is None: etree.SubElement(props,W+'tblHeader')
        index = list(table).index(proto)
        for item in values.get(r.field, []):
            row = deepcopy(proto)
            if r.keep_rows_together:
                props=row.find(W+'trPr')
                if props is None: props=etree.Element(W+'trPr'); row.insert(0,props)
                if props.find(W+'cantSplit') is None: etree.SubElement(props,W+'cantSplit')
            for cell,ss in zip(row.findall(W+'tc'),r.cells):
                ps = list(cell.iter(W+'p'))
                if not ps: raise TemplateError('profile_anchor_mismatch')
                text = text_of(ps[0])
                if not text: raise TemplateError('empty_repeater_prototype')
                replace_text_range(list(ps[0].iter(W+'t')),0,len(text),segments(ss,item,set()))
                for extra in ps[1:]:
                    if text_of(extra): raise TemplateError('unsupported_repeater_cell')
            table.insert(index,row); index += 1
        for row in affected: safe_remove(row)
    for block,nodes in blocks:
        if not test_condition(block.include_if,values):
            for n in nodes: safe_remove(n)
    for n in removals: safe_remove(n)
    number = 1
    for _,n in numbered:
        if n.getparent() is None: continue
        old = re.match(r'^[1-9][0-9]*\.', text_of(n))
        if old is None: raise TemplateError('invalid_numbering')
        replace_text_range(list(n.iter(W+'t')),0,len(old.group()),str(number)+'.'); number += 1
    if [etree.tostring(n) for n in protected] != protected_xml or any(n.getparent() is None for n in protected):
        raise TemplateError('protected_block')
    output = io.BytesIO()
    with ZipFile(io.BytesIO(content)) as source, ZipFile(output,'w',ZIP_DEFLATED) as target:
        for info in source.infolist():
            data = etree.tostring(root,xml_declaration=True,encoding='UTF-8',standalone=True) if info.filename=='word/document.xml' else parts[info.filename]
            target.writestr(info,data)
    result = output.getvalue()
    if len(result) > 10*1024*1024: raise TemplateError('rendered_document_too_large',413)
    checked_archive(result); Document(io.BytesIO(result))
    return result
