#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
import re
import sys


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def parse_name(name):
    base = name
    kind = "binary"
    if base.endswith(".tar.gz"):
        base = base[: -len(".tar.gz")]
        kind = "archive"
    elif base.endswith(".zip"):
        base = base[: -len(".zip")]
        kind = "archive"
    if base.endswith(".exe"):
        base = base[: -len(".exe")]
    m = re.match(r"^contenox-runtime-([a-z0-9]+)-([a-z0-9]+)$", base)
    if not m:
        return None
    return m.group(1), m.group(2), kind


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", required=True)
    ap.add_argument("--version", required=True)
    ap.add_argument("--tag", required=True)
    ap.add_argument("--commit", required=True)
    ap.add_argument("--output", required=True)
    args = ap.parse_args()

    assets = []
    for name in sorted(os.listdir(args.dir)):
        if not name.startswith("contenox-runtime-"):
            continue
        parsed = parse_name(name)
        if parsed is None:
            continue
        osname, arch, kind = parsed
        path = os.path.join(args.dir, name)
        assets.append(
            {
                "os": osname,
                "arch": arch,
                "kind": kind,
                "file": name,
                "sha256": sha256(path),
                "size": os.path.getsize(path),
            }
        )

    doc = {
        "schema": 1,
        "version": args.version,
        "tag": args.tag,
        "commit": args.commit,
        "assets": assets,
    }
    with open(args.output, "w", encoding="utf-8") as f:
        json.dump(doc, f, indent=2)
        f.write("\n")


if __name__ == "__main__":
    main()
