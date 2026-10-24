"""Render exported QA PDFs to PNG and contact sheets; no document modification."""
import json
from pathlib import Path
import pymupdf
from PIL import Image,ImageOps,ImageDraw

ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'.local-integration/template-v2-e2e'
summary=[]; tiles=[]
for path in sorted(OUT.glob('*.pdf')):
    doc=pymupdf.open(path)
    summary.append(dict(file=path.name,pages=len(doc),text_characters=sum(len(p.get_text()) for p in doc)))
    for i,page in enumerate(doc):
        image=page.get_pixmap(matrix=pymupdf.Matrix(1.5,1.5),alpha=False)
        png=OUT/f'{path.stem}-page-{i+1}.png'; image.save(png)
        tile=Image.new('RGB',(640,940),'#dddddd'); thumbnail=Image.open(png);thumbnail.thumbnail((620,900))
        tile.paste(thumbnail,((640-thumbnail.width)//2,30))
        ImageDraw.Draw(tile).text((10,8),f'{path.stem} / {i+1}',fill='black')
        tiles.append(tile)
for offset in range(0,len(tiles),4):
    sheet=Image.new('RGB',(1280,1880),'white')
    for i,tile in enumerate(tiles[offset:offset+4]):sheet.paste(tile,((i%2)*640,(i//2)*940))
    sheet.save(OUT/f'contact-{offset//4+1}.png')
(OUT/'visual-verification.json').write_text(json.dumps(summary,indent=2),encoding='utf-8')
print(json.dumps(summary,indent=2))
