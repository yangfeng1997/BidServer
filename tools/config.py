#!/usr/bin/env python3
"""Prepare runtime config directory for one environment.

Examples:
  python tools/config.py --env dev
  python tools/config.py --env prod --svr gatesvr
  python tools/config.py --env dev --dry-run
"""

from __future__ import annotations

import argparse
import stat
import sys
from pathlib import Path
from typing import Any

import yaml

from config_bake import bake_file, load_values

DEFAULT_SERVICES = ["gatesvr", "lobbysvr"]


def main() -> None:
    parser = argparse.ArgumentParser(description="Prepare baked runtime configs")
    parser.add_argument("--env", default="dev", help="environment name under config/values")
    parser.add_argument("--conf", default="config", help="config source directory")
    parser.add_argument("--out", default="run", help="runtime output directory")
    parser.add_argument("--svr", default="", help="prepare only one service")
    parser.add_argument("--dry-run", action="store_true", help="print plan without writing files")
    args = parser.parse_args()

    root = project_root()
    conf_dir = root / args.conf
    out_dir = root / args.out
    values_path = conf_dir / "values" / f"{args.env}.yaml"

    values_raw = load_yaml(values_path)
    values = load_values(values_path)
    services = services_from_values(values_raw)
    if args.svr:
        services = [args.svr]
    if not services:
        services = DEFAULT_SERVICES[:]

    validate_services(root, conf_dir, services)
    targets = build_targets(conf_dir, out_dir, services)
    print_plan(args.env, values_path, out_dir, values, targets, args.dry_run)

    for name, src, dst in targets:
        bake_file(src, dst, values, args.dry_run, name=name)

    if not args.dry_run:
        prepare_runtime_dirs(out_dir, services)
        write_env(out_dir / "ENV", args.env)

    print(f"config done env={args.env} services={','.join(services)}")


def project_root() -> Path:
    return Path(__file__).resolve().parents[1]


def load_yaml(path: Path) -> dict[str, Any]:
    if not path.exists():
        sys.exit(f"ERROR: values file not found: {path}")
    with path.open("r", encoding="utf-8") as file:
        data = yaml.safe_load(file) or {}
    if not isinstance(data, dict):
        sys.exit(f"ERROR: values file must contain a yaml map: {path}")
    return data


def services_from_values(data: dict[str, Any]) -> list[str]:
    services = data.get("svr_list") or []
    if not isinstance(services, list):
        sys.exit("ERROR: svr_list must be a yaml list")
    return sorted(str(service) for service in services)


def validate_services(root: Path, conf_dir: Path, services: list[str]) -> None:
    for service in services:
        cmd_dir = root / "cmd" / service
        if not cmd_dir.is_dir():
            sys.exit(f"ERROR: {service} in svr_list but {cmd_dir} not found")
        template_base = service.removesuffix("svr")
        if not (resolve_template(conf_dir, template_base).exists()):
            sys.exit(f"ERROR: config template for {service} not found")


def build_targets(conf_dir: Path, out_dir: Path, services: list[str]) -> list[tuple[str, Path, Path]]:
    targets: list[tuple[str, Path, Path]] = [
        ("common", resolve_template(conf_dir, "common"), out_dir / "common" / "conf" / "common.yaml")
    ]
    for service in services:
        base = service.removesuffix("svr")
        targets.append((service, resolve_template(conf_dir, base), out_dir / service / "conf" / f"{base}.yaml"))
    return targets


def resolve_template(conf_dir: Path, base: str) -> Path:
    for candidate in (conf_dir / f"{base}.yaml.tmpl", conf_dir / f"{base}.yaml"):
        if candidate.exists():
            return candidate
    return conf_dir / f"{base}.yaml.tmpl"


def print_plan(env: str, values_path: Path, out_dir: Path, values: dict[str, str], targets: list[tuple[str, Path, Path]], dry_run: bool) -> None:
    mode = "dry-run" if dry_run else "write"
    print(f"config mode={mode} env={env} values={values_path} out={out_dir} values_count={len(values)} targets={len(targets)}")
    for name, src, dst in targets:
        print(f"target {name:<8} {src} -> {dst}")


def prepare_runtime_dirs(out_dir: Path, services: list[str]) -> None:
    (out_dir / "common" / "conf").mkdir(parents=True, exist_ok=True)
    for service in services:
        for dirname in ("bin", "conf", "log"):
            (out_dir / service / dirname).mkdir(parents=True, exist_ok=True)


def write_env(path: Path, env: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"{env}\n", encoding="utf-8")
    try:
        path.chmod(stat.S_IREAD | stat.S_IRGRP | stat.S_IROTH)
    except OSError:
        pass


if __name__ == "__main__":
    main()
