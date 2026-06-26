#!/usr/bin/env python3
"""Build service binaries and copy them into run/{svr}/bin."""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any

import yaml

DEFAULT_SERVICES = ["gatesvr", "lobbysvr"]


def main() -> None:
    parser = argparse.ArgumentParser(description="Build runtime service binaries")
    parser.add_argument("--out", default="run", help="runtime output directory")
    parser.add_argument("--build", default="build", help="build output directory")
    parser.add_argument("--svr", default="", help="build only one service")
    args = parser.parse_args()

    root = project_root()
    out_dir = root / args.out
    build_dir = root / args.build
    env = read_env(out_dir / "ENV")
    services = read_services(root / "config" / "values" / f"{env}.yaml")
    if args.svr:
        services = [args.svr]
    if not services:
        services = DEFAULT_SERVICES[:]

    print(f"build env={env} services={','.join(services)} build={build_dir} out={out_dir}")
    validate_runtime_dirs(root, out_dir, services)
    build_dir.mkdir(parents=True, exist_ok=True)

    failed: list[str] = []
    for service in services:
        if not build_service(root, build_dir, out_dir, service):
            failed.append(service)

    if failed:
        sys.exit(f"ERROR: build failed for: {', '.join(failed)}")
    print("build done")


def project_root() -> Path:
    return Path(__file__).resolve().parents[1]


def read_env(path: Path) -> str:
    if not path.exists():
        sys.exit(f"ERROR: {path} not found, run 'make config ENV=dev' first")
    env = path.read_text(encoding="utf-8").strip()
    if not env:
        sys.exit(f"ERROR: {path} is empty")
    return env


def read_services(path: Path) -> list[str]:
    if not path.exists():
        sys.exit(f"ERROR: values file not found: {path}")
    with path.open("r", encoding="utf-8") as file:
        data: dict[str, Any] = yaml.safe_load(file) or {}
    services = data.get("svr_list") or []
    if not isinstance(services, list):
        sys.exit("ERROR: svr_list must be a yaml list")
    return sorted(str(service) for service in services)


def validate_runtime_dirs(root: Path, out_dir: Path, services: list[str]) -> None:
    for service in services:
        cmd_dir = root / "cmd" / service
        if not cmd_dir.is_dir():
            sys.exit(f"ERROR: cmd directory missing for {service}: {cmd_dir}")
        conf_dir = out_dir / service / "conf"
        if not conf_dir.is_dir():
            sys.exit(f"ERROR: runtime config missing for {service}: {conf_dir}; run 'make config' first")
        (out_dir / service / "bin").mkdir(parents=True, exist_ok=True)
        (out_dir / service / "log").mkdir(parents=True, exist_ok=True)


def build_service(root: Path, build_dir: Path, out_dir: Path, service: str) -> bool:
    exe_suffix = ".exe" if os.name == "nt" else ""
    binary = build_dir / f"{service}{exe_suffix}"
    dst = out_dir / service / "bin" / f"{service}{exe_suffix}"
    print(f"=== building {service} ===")
    result = subprocess.run(
        ["go", "build", "-o", str(binary), f"./cmd/{service}"],
        cwd=root,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        print(f"ERROR: build {service} failed", file=sys.stderr)
        print(result.stderr, file=sys.stderr)
        return False
    if dst.exists():
        dst.unlink()
    shutil.copy2(binary, dst)
    try:
        dst.chmod(0o755)
    except OSError:
        pass
    print(f"{binary} -> {dst}")
    return True


if __name__ == "__main__":
    main()
