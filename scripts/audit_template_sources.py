"""Read original sources without modifying them; emit indexed XML and PDF text."""
import hashlib
from pathlib import Path
from zipfile import ZipFile
from lxml import etree
import pymupdf

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.local-integration' / 'template-v2-audit'
OUT.mkdir(parents=True, exist_ok=True)
W = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'
for path in (ROOT / 'data/demo').glob('*'):
    if path.suffix == '.docx':
        with ZipFile(path) as z:
            root = etree.fromstring(z.read('word/document.xml'))
        paragraphs = list(root.iter(W + 'p'))
        lines = [path.name, 'SHA256 ' + hashlib.sha256(path.read_bytes()).hexdigest()]
        lines += [f'P{i}: ' + ''.join(t.text or '' for t in p.iter(W+'t')) for i,p in enumerate(paragraphs)]
        for i, table in enumerate(root.iter(W+'tbl')):
            lines.append(f'TABLE {i}')
            for j,row in enumerate(table.findall(W+'tr')):
                lines.append(f' R{j}: ' + ' | '.join(''.join(t.text or '' for t in cell.iter(W+'t')) for cell in row.findall(W+'tc')))
        (OUT / (path.stem + '.txt')).write_text('\n'.join(lines), encoding='utf-8')
    elif path.suffix == '.pdf':
        doc = pymupdf.open(path)
        (OUT / (path.stem + '.txt')).write_text('\n'.join(f'PAGE {i+1}\n{p.get_text()}' for i,p in enumerate(doc)), encoding='utf-8')
print(OUT)
