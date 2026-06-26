#!/usr/bin/env python3
"""Bake one yaml template with values.

Examples:
  python tools/config_bake.py --values config/values/dev.yaml --in config/gate.yaml --out run/gatesvr/conf/gate.yaml
  python tools/config_bake.py --values config/values/dev.yaml --in config/common.yaml --out run/common/conf/common.yaml --dry-run
"""

from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path
from typing import Any

import yaml

PLACEHOLDER_RE = re.compile(r"\$\{([^}]+)\}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Bake one config yaml")
    parser.add_argument("--values", required=True, help="values yaml file")
    parser.add_argument("--in", dest="input", required=True, help="input yaml template")
    parser.add_argument("--out", required=True, help="output yaml file")
    parser.add_argument("--dry-run", action="store_true", help="print plan without writing files")
    args = parser.parse_args()

    values = load_values(Path(args.values))
    bake_file(Path(args.input), Path(args.out), values, args.dry_run)


def load_values(path: Path) -> dict[str, str]:
    if not path.exists():
        sys.exit(f"ERROR: values file not found: {path}")
    with path.open("r", encoding="utf-8") as file:
        data = yaml.safe_load(file) or {}
    if not isinstance(data, dict):
        sys.exit(f"ERROR: values file must contain a yaml map: {path}")
    return flatten_values(data)


def flatten_values(data: dict[str, Any]) -> dict[str, str]:
    out: dict[str, str] = {}

    def walk(prefix: str, value: Any) -> None:
        if isinstance(value, dict):
            for key, item in value.items():
                next_key = str(key) if not prefix else f"{prefix}.{key}"
                walk(next_key, item)
            return
        if isinstance(value, list):
            out[prefix] = ",".join(str(item) for item in value)
            return
        out[prefix] = str(value)

    for key, value in data.items():
        if key == "svr_list":
            continue
        walk(str(key), value)
    return out


def bake_file(src: Path, dst: Path, values: dict[str, str], dry_run: bool = False, name: str = "config") -> None:
    if not src.exists():
        sys.exit(f"ERROR: template not found for {name}: {src}")
    text = src.read_text(encoding="utf-8")
    baked, used = bake_text(text, values, src, name)

    if dry_run:
        print(f"would bake {name:<8} placeholders={len(used)} -> {dst}")
        return

    dst.parent.mkdir(parents=True, exist_ok=True)
    dst.write_text(baked, encoding="utf-8")
    print(f"baked {name:<8} placeholders={len(used)} -> {dst}")


def bake_text(text: str, values: dict[str, str], src: Path, name: str) -> tuple[str, set[str]]:
    missing: list[str] = []
    used: set[str] = set()

    def replace(match: re.Match[str]) -> str:
        key = match.group(1)
        if key in values:
            used.add(key)
            return values[key]
        if key in os.environ:
            used.add(key)
            return os.environ[key]
        missing.append(key)
        return match.group(0)

    baked = PLACEHOLDER_RE.sub(replace, text)
    if missing:
        unique_missing = ", ".join(sorted(set(missing)))
        sys.exit(f"ERROR: target {name} missing placeholder(s) in {src}: {unique_missing}")
    return baked, used


if __name__ == "__main__":
    main()
