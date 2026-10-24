"""Проверка DOCX-архива и заполнение простых полей шаблона."""

import io
import json
import re
from pathlib import PurePosixPath
from zipfile import ZipFile
from zipfile import ZIP_DEFLATED
from datetime import date

from docxtpl import DocxTemplate
from jinja2 import StrictUndefined, nodes
from jinja2.sandbox import SandboxedEnvironment
from lxml import etree

from app.core.config import MAX_TEMPLATE_BYTES, MAX_VALUES_BYTES

NAME = re.compile(r"^[a-z][a-z0-9_]{0,63}$")
RENDER_PART = re.compile(r"^word/(document|header[0-9]+|footer[0-9]+|footnotes)\.xml$")
MARKERS = ("{{", "}}", "{%", "%}", "{#", "#}")


class TemplateError(Exception):
    def __init__(self, code, status=422, missing_fields=None):
        self.code, self.status = code, status
        self.missing_fields = missing_fields


def parse_values(raw: str) -> dict[str, str]:
    try:
        if len(raw.encode("utf-8")) > MAX_VALUES_BYTES:
            raise TemplateError("values_too_large", 413)

        def unique(pairs):
            output = {}
            for key, value in pairs:
                if key in output:
                    raise ValueError()
                output[key] = value
            return output

        values = json.loads(raw, object_pairs_hook=unique)
        if not isinstance(values, dict) or len(values) > 64:
            raise ValueError()
        for key, value in values.items():
            if not NAME.fullmatch(key) or not isinstance(value, str) or len(value) > 8000:
                raise ValueError()
            value.encode("utf-8")
            if any(
                not (char in "\t\n\r" or 0x20 <= ord(char) <= 0xD7FF
                     or 0xE000 <= ord(char) <= 0xFFFD or 0x10000 <= ord(char) <= 0x10FFFF)
                for char in value
            ):
                raise ValueError()
        return values
    except TemplateError:
        raise
    except (ValueError, TypeError, UnicodeError):
        raise TemplateError("invalid_values") from None


def checked_archive(content: bytes) -> dict[str, bytes]:
    if len(content) > MAX_TEMPLATE_BYTES:
        raise TemplateError("template_too_large", 413)
    if not content:
        raise TemplateError("invalid_template")
    parts = {}
    with ZipFile(io.BytesIO(content)) as archive:
        entries = archive.infolist()
        if len(entries) > 1000 or sum(e.file_size for e in entries) > 32 * 1024 * 1024:
            raise TemplateError("template_expanded_limit", 413)
        for entry in entries:
            name = entry.filename
            if (name in parts or "\\" in name or name.startswith("/")
                    or ".." in PurePosixPath(name).parts or entry.flag_bits & 1):
                raise TemplateError("invalid_template")
            lowered = name.lower()
            if any(x in lowered for x in ("vbaproject", "activex", "embeddings/")) or lowered.endswith(".bin"):
                raise TemplateError("unsafe_template")
            data = archive.read(entry)
            parts[name] = data
            if name.endswith((".xml", ".rels")):
                root = etree.fromstring(data, parser=etree.XMLParser(
                    resolve_entities=False, no_network=True, load_dtd=False,
                ))
                if root.getroottree().docinfo.doctype:
                    raise TemplateError("unsafe_template")
                for element in root.iter():
                    if not isinstance(element.tag, str):
                        continue
                    local = etree.QName(element).localname
                    if (local == "Relationship" and element.get("TargetMode", "").lower() == "external"
                            or local == "altChunk"):
                        raise TemplateError("unsafe_template")
                    if local in {"instrText", "fldSimple"}:
                        instruction = (element.text or "") if local == "instrText" else element.get(
                            "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}instr", "")
                        if not re.fullmatch(r"\s*(PAGE|NUMPAGES)\s*(?:\\\*\s*MERGEFORMAT\s*)?", instruction):
                            raise TemplateError("unsafe_template")
                    if any(marker in value for value in element.attrib.values() for marker in MARKERS):
                        raise TemplateError("unsupported_template_syntax")
                joined = "".join(root.itertext())
                if any(marker in joined for marker in MARKERS) and not RENDER_PART.fullmatch(name):
                    raise TemplateError("unsupported_template_part")
        if not {"[Content_Types].xml", "_rels/.rels", "word/document.xml"}.issubset(parts):
            raise TemplateError("invalid_template")
        types = parts["[Content_Types].xml"]
        if b"macroEnabled" in types or b"vbaProject" in types:
            raise TemplateError("unsafe_template")
    return parts


def load_template(content: bytes):
    parts = checked_archive(content)
    environment = SandboxedEnvironment(undefined=StrictUndefined, autoescape=True)
    environment.globals.clear()
    environment.filters.clear()
    environment.tests.clear()
    template = DocxTemplate(io.BytesIO(content))
    names = set()
    for name, data in parts.items():
        if not RENDER_PART.fullmatch(name):
            continue
        root = etree.fromstring(data, parser=etree.XMLParser(resolve_entities=False, no_network=True))
        text = "".join(root.itertext())
        # Разрешены только простые поля {{ name }}, без кода и выражений Jinja.
        if any(marker in text for marker in ("{%", "%}", "{#", "#}")) or re.search(r"{{\s*r\s+", text):
            raise TemplateError("unsupported_template_syntax")
        xml = template.patch_xml(data.decode("utf-8"))
        tree = environment.parse(xml)
        for node in tree.find_all(nodes.Node):
            if not isinstance(node, (nodes.Output, nodes.TemplateData, nodes.Name)):
                raise TemplateError("unsupported_template_syntax")
            if isinstance(node, nodes.Name):
                if not NAME.fullmatch(node.name):
                    raise TemplateError("unsupported_template_syntax")
                names.add(node.name)
    if len(names) > 64:
        raise TemplateError("too_many_fields")
    # Проверяем, что архив действительно является документом Word.
    template.init_docx()
    return template, environment, sorted(names)


def process_template(content: bytes, raw_values: str, render: bool, raw_schema: str | None = None):
    try:
        if raw_schema is not None:
            if raw_schema.lstrip().startswith('{'):
                from app.templates.renderer_v2 import process_profile
                return process_profile(content, raw_values, render, raw_schema)
            return process_configured_template(content, raw_values, render, raw_schema)
        values = parse_values(raw_values)
        template, environment, names = load_template(content)
        if set(values) - set(names):
            raise TemplateError("unknown_fields")
        missing = [name for name in names if not values.get(name, "").strip()]
        if not render:
            return {
                "fields": [{"name": name, "type": "string", "required": True} for name in names],
                "required_fields": names,
                "missing_fields": missing,
                "can_render": not missing,
            }
        if missing:
            raise TemplateError("missing_fields", missing_fields=missing)
        template.render(values, jinja_env=environment, autoescape=True)
        output = io.BytesIO()
        template.save(output)
        result = output.getvalue()
        if len(result) > 10 * 1024 * 1024:
            raise TemplateError("rendered_document_too_large", 413)
        return result
    except TemplateError:
        raise
    except Exception:
        # Не возвращаем клиенту содержимое XML и значения полей при ошибке.
        raise TemplateError("invalid_template") from None


W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"


def configured_schema(raw):
    from app.schemas.templates import FieldDefinition
    if len(raw.encode("utf-8")) > 65536:
        raise TemplateError("schema_too_large", 413)
    fields = [FieldDefinition.model_validate(f) for f in json.loads(raw)]
    if not 1 <= len(fields) <= 64 or len({f.key for f in fields}) != len(fields):
        raise TemplateError("invalid_schema")
    return fields


def valid_text(value, limit):
    return (isinstance(value, str) and len(value) <= limit
            and all(c in "\t\n\r" or 0x20 <= ord(c) <= 0xD7FF
                    or 0xE000 <= ord(c) <= 0xFFFD or 0x10000 <= ord(c) <= 0x10FFFF for c in value))


def process_configured_template(content, raw_values, render, raw_schema):
    fields = configured_schema(raw_schema)
    if len(raw_values.encode("utf-8")) > MAX_VALUES_BYTES:
        raise TemplateError("values_too_large", 413)
    def unique(pairs):
        result = {}
        for k, v in pairs:
            if k in result:
                raise TemplateError("invalid_values")
            result[k] = v
        return result
    values = json.loads(raw_values, object_pairs_hook=unique)
    if not isinstance(values, dict) or set(values) - {f.key for f in fields}:
        raise TemplateError("unknown_fields")
    missing, formatted = [], {}
    for field in fields:
        value = values.get(field.key)
        empty = value is None or value == [] or isinstance(value, str) and not value.strip()
        if empty:
            if field.required:
                missing.append(field.key)
            formatted[field.key] = ""
            continue
        if field.type == "person_list":
            if not isinstance(value, list) or len(value) > 50 or any(
                    not valid_text(v, field.max_length) or not v.strip() for v in value):
                raise TemplateError("invalid_values")
            formatted[field.key] = "\n".join(value)
        else:
            if not valid_text(value, field.max_length):
                raise TemplateError("invalid_values")
            if field.type == "date":
                if not re.fullmatch(r"\d{4}-\d{2}-\d{2}", value):
                    raise TemplateError("invalid_date")
                parsed = date.fromisoformat(value)
                formatted[field.key] = parsed.strftime("%d.%m.%Y")
            else:
                formatted[field.key] = value
    parts = checked_archive(content)
    # Validate the OPC/Word package as well as its ZIP/XML envelope.
    from docx import Document
    Document(io.BytesIO(content))
    # Mappings are checked against the unchanged original, before any mutation.
    mapped = any(f.bindings for f in fields)
    if mapped and not all(f.bindings for f in fields):
        raise TemplateError("invalid_schema")
    if not mapped:
        template, environment, names = load_template(content)
        if set(names) != {f.key for f in fields}:
            raise TemplateError("schema_template_mismatch")
        edits = None
    else:
        roots, edits, occupied = {}, [], {}
        for field in fields:
            for binding in field.bindings:
                if binding.part not in parts:
                    raise TemplateError("schema_template_mismatch")
                if binding.part not in roots:
                    roots[binding.part] = etree.fromstring(parts[binding.part], parser=etree.XMLParser(
                        resolve_entities=False, no_network=True, load_dtd=False))
                paragraphs = list(roots[binding.part].iter(W + "p"))
                if binding.paragraph >= len(paragraphs):
                    raise TemplateError("schema_template_mismatch")
                p = paragraphs[binding.paragraph]
                texts = list(p.iter(W + "t"))
                text = "".join(t.text or "" for t in texts)
                if "НЕ УДАЛЯТЬ" in text or text.count(binding.match) != 1:
                    raise TemplateError("schema_template_mismatch")
                start = text.index(binding.match)
                end = start + len(binding.match)
                identity = (binding.part, binding.paragraph)
                ranges = occupied.setdefault(identity, [])
                if any(start < b and a < end for a, b in ranges):
                    raise TemplateError("overlapping_bindings")
                ranges.append((start, end))
                if not valid_text(binding.suffix, 128):
                    raise TemplateError("invalid_schema")
                edits.append((identity, start, end, texts, formatted[field.key] + binding.suffix))
    if not render:
        return {"fields": [{"name": f.key, "type": "string", "required": f.required} for f in fields],
                "required_fields": [f.key for f in fields if f.required],
                "missing_fields": missing, "can_render": not missing}
    if missing:
        raise TemplateError("missing_fields", missing_fields=missing)
    output = io.BytesIO()
    if edits is None:
        template.render(formatted, jinja_env=environment, autoescape=True)
        template.save(output)
    else:
        # Descending offsets keep the positions of earlier replacements stable.
        for _, start, end, texts, value in sorted(edits, key=lambda e: (e[0], e[1]), reverse=True):
            replace_text_range(texts, start, end, value)
        with ZipFile(io.BytesIO(content)) as source, ZipFile(output, "w", ZIP_DEFLATED) as target:
            for info in source.infolist():
                data = (etree.tostring(roots[info.filename], xml_declaration=True, encoding="UTF-8", standalone=True)
                        if info.filename in roots else parts[info.filename])
                target.writestr(info, data)
    result = output.getvalue()
    if len(result) > 10 * 1024 * 1024:
        raise TemplateError("rendered_document_too_large", 413)
    checked_archive(result)
    return result


def replace_text_range(texts, start, end, value):
    position, inserted = 0, False
    for node in texts:
        text = node.text or ""
        right = position + len(text)
        if position < end and start < right:
            left_piece = text[:max(0, start - position)]
            right_piece = text[max(0, end - position):]
            replacement = value if not inserted else ""
            inserted = True
            pieces = (left_piece + replacement + right_piece).split("\n")
            node.text = pieces[0]
            node.set("{http://www.w3.org/XML/1998/namespace}space", "preserve")
            current = node
            for piece in pieces[1:]:
                br = etree.Element(W + "br")
                current.addnext(br)
                new = etree.Element(W + "t")
                new.text = piece
                new.set("{http://www.w3.org/XML/1998/namespace}space", "preserve")
                br.addnext(new)
                current = new
        position = right
