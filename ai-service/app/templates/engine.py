import io
import json
import re
from pathlib import PurePosixPath
from zipfile import ZipFile

from docxtpl import DocxTemplate
from jinja2 import StrictUndefined, nodes
from jinja2.sandbox import SandboxedEnvironment
from lxml import etree

MAX_TEMPLATE_BYTES = 5 * 1024 * 1024
MAX_REQUEST_BYTES = MAX_TEMPLATE_BYTES + 1024 * 1024
MAX_VALUES_BYTES = 64 * 1024
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
                            or local in {"altChunk", "instrText", "fldSimple"}):
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
        # No Jinja statements/comments or docxtpl rich-text instructions.
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
    # Reject a non-DOCX OPC package even if it has plausible ZIP entries.
    template.init_docx()
    return template, environment, sorted(names)


def process_template(content: bytes, raw_values: str, render: bool):
    try:
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
        # Never disclose uploaded XML or field values in errors.
        raise TemplateError("invalid_template") from None
