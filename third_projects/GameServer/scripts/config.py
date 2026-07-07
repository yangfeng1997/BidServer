#!/usr/bin/env python3
"""config.py --env=<env>

编排配置烘焙：
1. 读 conf/values/{env}.yaml 取 svr_list
2. 校验 svr_list 每项在 cmd/ 下有对应目录
3. 调用 config_build --common 一次烘焙公共配置
4. 对每个 svr 建 run/{svr}/{bin,conf,log}/ 并调 config_build --svr
5. 写 run/ENV 供后续 make build 读取环境
"""

import argparse
import os
import subprocess
import sys

import yaml


def main():
    parser = argparse.ArgumentParser(description="Build config for all services")
    parser.add_argument("--env", default="dev", help="Deployment environment (dev/test/prod)")
    args = parser.parse_args()

    env = args.env
    project_root = os.path.join(os.path.dirname(__file__), "..")
    os.chdir(project_root)

    # 1. 读 svr_list
    values_path = f"conf/values/{env}.yaml"
    if not os.path.exists(values_path):
        print(f"ERROR: values file not found: {values_path}", file=sys.stderr)
        sys.exit(1)

    with open(values_path) as f:
        values = yaml.safe_load(f)

    svr_list = values.get("svr_list", [])
    if not svr_list:
        print(f"ERROR: svr_list is empty in {values_path}", file=sys.stderr)
        sys.exit(1)

    # 2. 校验
    for svr in svr_list:
        if not os.path.isdir(f"cmd/{svr}"):
            print(f"ERROR: {svr} in svr_list but cmd/{svr}/ not found", file=sys.stderr)
            sys.exit(1)

    # 3. 烘焙 common.yaml（一次）
    print(f"=== baking common.yaml (env={env}) ===")
    run_cmd(["go", "run", "./tools/config_build", "--common", f"--env={env}"])

    # 4. 烘焙各服务
    for svr in svr_list:
        print(f"=== baking {svr} (env={env}) ===")
        for subdir in ("bin", "conf", "log"):
            os.makedirs(f"run/{svr}/{subdir}", exist_ok=True)
        run_cmd(["go", "run", "./tools/config_build", f"--svr={svr}", f"--env={env}"])

    # 5. 持久化环境名（只读）
    env_path = "run/ENV"
    with open(env_path, "w") as f:
        f.write(f"{env}\n")
    os.chmod(env_path, 0o444)

    print(f"config done (env={env})")


def run_cmd(cmd):
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"ERROR: {' '.join(cmd)} failed:", file=sys.stderr)
        print(result.stderr, file=sys.stderr)
        sys.exit(1)
    print(result.stdout.strip())


if __name__ == "__main__":
    main()
