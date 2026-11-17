#!/usr/bin/env python3
import argparse
import gzip
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import tarfile
import tempfile


SCRIPT_DIR = Path(__file__).resolve().parent
PREPARER_PATH = SCRIPT_DIR / "prepare-openvino-literal-source-tokenizer.py"
DEFAULT_ASSETS = SCRIPT_DIR.parent / "internal" / "modeld" / "modelstore" / "assets"
SOURCE_FILES = ("tokenizer.json", "tokenizer_config.json", "special_tokens_map.json")


def load_preparer():
    spec = importlib.util.spec_from_file_location("prepare_openvino_literal_source_tokenizer", PREPARER_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def read_requirement(path):
    requirement = json.loads(Path(path).read_text(encoding="utf-8"))
    for field in ("assetId", "model", "modelRepo", "modelRevision", "kind", "format", "version", "controls"):
        if field not in requirement:
            raise ValueError(f"requirement {path} declares no {field}")
    return requirement


def check_requirement(preparer, requirement, formats):
    controls, versions, kinds = formats
    fmt = requirement["format"]
    if fmt not in controls:
        raise ValueError(f"format {fmt!r} declares no literal-source tokenizer companion")
    for field, expected in (("kind", kinds[fmt]), ("version", versions[fmt]), ("controls", controls[fmt])):
        if requirement[field] != expected:
            raise ValueError(
                f"the curated requirement declares {field}={requirement[field]!r} for format {fmt!r}, "
                f"the format schema declares {expected!r}"
            )


def stage_snapshot(model_dir, stage):
    for name in SOURCE_FILES:
        source = model_dir / name
        if source.is_file():
            shutil.copyfile(source, stage / name)


def write_provenance(preparer, companion_dir, requirement, model_dir):
    license_path = model_dir / "LICENSE"
    license_data = license_path.read_bytes()
    (companion_dir / "LICENSE").write_bytes(license_data)
    files = {}
    for path in sorted(companion_dir.iterdir()):
        if not path.is_file():
            continue
        data = path.read_bytes()
        files[path.name] = {"sha256": preparer.digest(data), "size": len(data)}
    provenance = {
        "asset_id": requirement["assetId"],
        "model": {
            "name": requirement["model"],
            "repository": requirement["modelRepo"],
            "revision": requirement["modelRevision"],
        },
        "operation": {"kind": requirement["kind"], "format": requirement["format"], "version": requirement["version"]},
        "license": {"file": license_path.name, "sha256": preparer.digest(license_data)},
        "files": files,
    }
    (companion_dir / "PROVENANCE.json").write_text(
        json.dumps(provenance, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )


def pack(assets, asset_id, manifest_name, manifest_data, companion_dir, force):
    archive_path = assets / f"{asset_id}.tar.gz"
    if archive_path.exists() and not force:
        raise ValueError(f"{archive_path} already exists; bump the asset ID or pass --force to replace qualified bytes")
    entries = [(manifest_name, manifest_data)]
    for path in sorted(companion_dir.iterdir()):
        if path.is_file():
            entries.append((f"{companion_dir.name}/{path.name}", path.read_bytes()))
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.GNU_FORMAT) as archive:
        for name, data in entries:
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mtime = 0
            info.mode = 0o644
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            archive.addfile(info, io.BytesIO(data))
    with open(archive_path, "wb") as output:
        with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0) as compressed:
            compressed.write(raw.getvalue())
    return archive_path


def build(model_dir, requirement, assets, schema=None, force=False, converter=None, toolchain=None):
    preparer = load_preparer()
    formats = preparer.load_formats(schema or preparer.SCHEMA)
    check_requirement(preparer, requirement, formats)
    model_dir = Path(model_dir).resolve()
    assets = Path(assets).resolve()
    assets.mkdir(parents=True, exist_ok=True)
    if not (model_dir / "tokenizer.json").is_file():
        raise ValueError(f"missing model tokenizer: {model_dir / 'tokenizer.json'}")
    if not (model_dir / "LICENSE").is_file():
        raise ValueError(f"missing model license: {model_dir / 'LICENSE'}")
    stage = Path(tempfile.mkdtemp(prefix=".contenox-companion-build-", dir=assets))
    try:
        snapshot = stage / "snapshot"
        snapshot.mkdir()
        stage_snapshot(model_dir, snapshot)
        manifest_path = preparer.prepare(snapshot, requirement["format"], formats=formats, converter=converter, toolchain=toolchain)
        companion_dir = snapshot / preparer.COMPANION
        write_provenance(preparer, companion_dir, requirement, model_dir)
        return pack(assets, requirement["assetId"], manifest_path.name, manifest_path.read_bytes(), companion_dir, force)
    finally:
        shutil.rmtree(stage, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--requirement", required=True, type=Path)
    parser.add_argument("--model-dir", required=True, type=Path)
    parser.add_argument("--assets", type=Path, default=DEFAULT_ASSETS)
    parser.add_argument("--schema", type=Path)
    parser.add_argument("--force", action="store_true")
    args = parser.parse_args()
    try:
        archive = build(args.model_dir, read_requirement(args.requirement), args.assets, args.schema, args.force)
    except (OSError, ValueError, json.JSONDecodeError, ImportError) as exc:
        parser.error(str(exc))
    print(archive)


if __name__ == "__main__":
    main()
