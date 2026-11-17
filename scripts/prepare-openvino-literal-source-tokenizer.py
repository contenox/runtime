#!/usr/bin/env python3
import argparse
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import tempfile


CONTROLS = {}
FORMAT_VERSIONS = {}
FORMAT_KINDS = {}
MANIFEST = "contenox-literal-source-tokenizer.json"
COMPANION = "literal-source-tokenizer"
COMPANION_TOKENIZER_CONFIG = {"add_bos_token": False, "add_eos_token": False}
SCHEMA = Path(__file__).resolve().parent.parent / "runtime" / "modelcontract" / "completion_formats.json"


def load_formats(schema_path):
    schema = json.loads(Path(schema_path).read_text(encoding="utf-8"))
    controls, versions, kinds = {}, {}, {}
    for entry in schema["fim"]:
        if entry["kind"] != "tokens":
            continue
        controls[entry["id"]] = list(entry["stops"])
        versions[entry["id"]] = entry["version"]
        kinds[entry["id"]] = entry["operation"]
    for entry in schema["nextEdit"]:
        controls[entry["id"]] = list(entry["controls"])
        versions[entry["id"]] = entry["version"]
        kinds[entry["id"]] = entry["operation"]
    return controls, versions, kinds


CONTROLS, FORMAT_VERSIONS, FORMAT_KINDS = load_formats(SCHEMA)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def canonical_json(value):
    encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    return encoded.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026").encode()


def identity(payload):
    return digest(canonical_json(payload))


def prepare_json(source_data, control_tokens):
    tokenizer = json.loads(source_data)
    model = tokenizer.get("model")
    if not isinstance(model, dict) or model.get("type") != "BPE" or not isinstance(model.get("vocab"), dict):
        raise ValueError("literal-source companion preparation requires a BPE tokenizer with a vocab object")
    added = tokenizer.get("added_tokens", [])
    if not isinstance(added, list):
        raise ValueError("tokenizer added_tokens must be an array")
    removed = set(control_tokens)
    removed.update(
        item["content"] for item in added
        if isinstance(item, dict) and item.get("special") is True and isinstance(item.get("content"), str)
    )
    removed = sorted(removed)
    for marker in removed:
        model["vocab"].pop(marker, None)
    tokenizer["added_tokens"] = [item for item in added if item.get("content") not in removed]
    return (json.dumps(tokenizer, ensure_ascii=False, indent=2) + "\n").encode(), removed


def convert_tokenizer_ir(companion_dir):
    from openvino import save_model, opset14 as op
    from openvino_tokenizers.hf_parser import TransformersTokenizerPipelineParser
    from openvino_tokenizers.tokenizer_pipeline import SpecialTokensSplit
    from openvino_tokenizers.utils import (
        TokenzierConversionParams, change_outputs_type,
        update_rt_info_with_params, update_rt_info_with_environment,
    )
    from tokenizers import Tokenizer
    from transformers import PreTrainedTokenizerFast

    source = companion_dir / "tokenizer.json"
    fast = PreTrainedTokenizerFast(
        tokenizer_object=Tokenizer.from_file(str(source)),
        clean_up_tokenization_spaces=False,
    )
    class LiteralSourceSplit(SpecialTokensSplit):
        def get_ov_subgraph(self, input_nodes):
            mask = op.broadcast(op.constant(False), op.shape_of(input_nodes[2]))
            return [*input_nodes, mask.output(0)]

    params = TokenzierConversionParams(
        with_detokenizer=False,
        add_special_tokens=False,
        clean_up_tokenization_spaces=False,
    )
    pipeline = TransformersTokenizerPipelineParser(fast, params).parse()
    pipeline.steps = [
        LiteralSourceSplit() if isinstance(step, SpecialTokensSplit) and not step.special_tokens else step
        for step in pipeline.steps
    ]
    tokenizer = change_outputs_type(pipeline.get_tokenizer_ov_subgraph(), params.tokenizer_output_type)
    update_rt_info_with_params(tokenizer, fast, params)
    update_rt_info_with_environment(tokenizer)
    save_model(tokenizer, str(companion_dir / "openvino_tokenizer.xml"))


def toolchain_identity():
    packages = {
        name: importlib.metadata.version(name)
        for name in ("openvino", "openvino-tokenizers", "transformers", "tokenizers")
    }
    if not packages["openvino"].startswith("2026.2.") or packages["openvino-tokenizers"] != "2026.2.0.0":
        raise ValueError("literal-source tokenizer export requires the pinned OpenVINO 2026.2 toolchain")
    if packages["transformers"] != "5.0.0":
        raise ValueError("literal-source tokenizer export requires transformers==5.0.0")
    return {"python": platform.python_version(), "packages": packages}


def source_file_hash(model_dir, name):
    path = model_dir / name
    return digest(path.read_bytes()) if path.is_file() else ""


def prepare(model_dir, fmt, converter=None, toolchain=None, script_path=None, formats=None):
    controls, versions, kinds = formats or (CONTROLS, FORMAT_VERSIONS, FORMAT_KINDS)
    if fmt not in controls:
        raise ValueError(f"format {fmt!r} declares no literal-source tokenizer companion")
    control_tokens = controls[fmt]
    model_dir = Path(model_dir).resolve()
    source_path = model_dir / "tokenizer.json"
    if not source_path.is_file():
        raise ValueError(f"missing model tokenizer: {source_path}")
    companion_dir = model_dir / COMPANION
    manifest_path = model_dir / MANIFEST
    if companion_dir.exists() or manifest_path.exists():
        raise ValueError(f"literal-source companion output already exists: {model_dir}; remove the generated companion before rebuilding")
    source_data = source_path.read_bytes()
    companion_data, removed = prepare_json(source_data, control_tokens)
    source_hash = digest(source_data)
    source_config_hash = source_file_hash(model_dir, "tokenizer_config.json")
    source_special_map_hash = source_file_hash(model_dir, "special_tokens_map.json")
    companion_hash = digest(companion_data)
    config_data = (json.dumps(COMPANION_TOKENIZER_CONFIG, sort_keys=True, indent=2) + "\n").encode()
    config_hash = digest(config_data)
    toolchain = toolchain or toolchain_identity()
    script_path = Path(script_path or __file__).resolve()
    script_hash = digest(script_path.read_bytes())

    model_dir.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix=".contenox-literal-source-", dir=model_dir))
    try:
        staged_companion = stage / COMPANION
        staged_companion.mkdir()
        (staged_companion / "tokenizer.json").write_bytes(companion_data)
        (staged_companion / "tokenizer_config.json").write_bytes(config_data)
        (converter or convert_tokenizer_ir)(staged_companion)
        tokenizer_ir_hash = digest((staged_companion / "openvino_tokenizer.xml").read_bytes())
        tokenizer_weights_hash = digest((staged_companion / "openvino_tokenizer.bin").read_bytes())
        payload = {
            "version": 1,
            "kind": kinds[fmt],
            "format": fmt,
            "format_version": versions[fmt],
            "source_tokenizer_sha256": source_hash,
            "source_tokenizer_config_sha256": source_config_hash,
            "source_special_tokens_map_sha256": source_special_map_hash,
            "companion_tokenizer_sha256": companion_hash,
            "companion_config_sha256": config_hash,
            "tokenizer_ir_sha256": tokenizer_ir_hash,
            "tokenizer_weights_sha256": tokenizer_weights_hash,
            "control_tokens": control_tokens,
            "removed_tokens": removed,
            "preparation_script_sha256": script_hash,
            "toolchain": toolchain,
        }
        manifest = dict(payload)
        manifest["identity"] = identity(payload)
        (stage / MANIFEST).write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        os.replace(staged_companion, companion_dir)
        os.replace(stage / MANIFEST, manifest_path)
    finally:
        try:
            stage.rmdir()
        except OSError:
            pass
    return manifest_path


def format_vocabulary(formats, schema_path):
    controls, versions, kinds = formats
    return {
        "schema": str(schema_path),
        "formats": {
            fmt: {"kind": kinds[fmt], "version": versions[fmt], "controls": controls[fmt]}
            for fmt in sorted(controls)
        },
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", type=Path)
    parser.add_argument("--format", choices=sorted(CONTROLS))
    parser.add_argument("--schema", type=Path, default=SCHEMA)
    parser.add_argument("--dump-formats", action="store_true")
    args = parser.parse_args()
    formats = load_formats(args.schema)
    if args.dump_formats:
        print(json.dumps(format_vocabulary(formats, args.schema), ensure_ascii=False, sort_keys=True))
        return
    if args.model_dir is None or args.format is None:
        parser.error("--model-dir and --format are required")
    try:
        path = prepare(args.model_dir, args.format, formats=formats)
    except (OSError, ValueError, json.JSONDecodeError, ImportError) as exc:
        parser.error(str(exc))
    print(path)


if __name__ == "__main__":
    main()
