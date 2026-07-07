#!/usr/bin/env python3
"""build.py — 编译所有服务二进制并铺到 run/ 目录。

环境从 run/ENV 读取（由 config.py 写入），无需参数。
"""

import os
import shutil
import subprocess
import sys

import yaml


def read_env() -> str:
    env_path = "run/ENV"
    if not os.path.exists(env_path):
        sys.exit(f"ERROR: {env_path} not found, run 'make config' first")
    with open(env_path) as f:
        return f.read().strip()


def main():
    os.chdir(os.path.join(os.path.dirname(__file__), ".."))
    env = read_env()

    # 1. 读 svr_list
    values_path = f"conf/values/{env}.yaml"
    if not os.path.exists(values_path):
        sys.exit(f"ERROR: values file not found: {values_path}")

    with open(values_path) as f:
        values = yaml.safe_load(f)
    svr_list = values.get("svr_list", [])

    # 2. 校验
    for svr in svr_list:
        if not os.path.isdir(f"run/{svr}"):
            sys.exit(f"ERROR: run/{svr}/ not found, run 'make config' first")

    # 3. 编译
    failed = []
    for svr in svr_list:
        print(f"=== building {svr} ===")
        result = subprocess.run(
            ["go", "build", "-o", f"build/{svr}", f"./cmd/{svr}"],
            capture_output=True, text=True,
        )
        if result.returncode != 0:
            failed.append(svr)
            print(f"ERROR: build {svr} failed:", file=sys.stderr)
            print(result.stderr, file=sys.stderr)
        else:
            print(f"build/{svr} OK")

    if failed:
        sys.exit(f"ERROR: build failed for: {failed}")

    # 4. 铺二进制到 run/{svr}/bin/
    missing = []
    for svr in svr_list:
        src = f"build/{svr}"
        dst = f"run/{svr}/bin/{svr}"
        if not os.path.exists(src):
            print(f"WARNING: {src} not found, skipping")
            missing.append(svr)
            continue
        # cp --remove-destination: avoid ETXTBUSY when target is running
        if os.path.exists(dst):
            os.remove(dst)
        shutil.copy2(src, dst)
        os.chmod(dst, 0o755)
        print(f"{src} → {dst}")

    if missing:
        print(f"WARNING: binaries not found for: {missing}")
    print("build done")


if __name__ == "__main__":
    main()
