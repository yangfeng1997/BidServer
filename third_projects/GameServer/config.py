#!/usr/bin/env python3
"""config.py — 读 svr_list，建 run 目录结构，渲染配置，铺二进制。"""

import argparse
import subprocess
import sys
from pathlib import Path

import yaml

import update_bin


def load_svr_list(env: str, conf_dir: Path) -> list[str]:
    values_path = conf_dir / "envs" / f"{env}.yaml"
    if not values_path.exists():
        sys.exit(f"错误: {values_path} 不存在")
    with open(values_path, encoding="utf-8") as f:
        data = yaml.safe_load(f)
    svr_list = data.get("svr_list")
    if not svr_list:
        sys.exit(f"错误: {values_path} 中缺少 svr_list 字段")
    return svr_list


def validate_svr_list(svr_list: list[str], servers_dir: Path) -> None:
    for svr in svr_list:
        if not (servers_dir / svr).is_dir():
            sys.exit(
                f"错误: svr_list 中的 '{svr}' 在 {servers_dir / svr} 下不存在，"
                f"请检查 svr_list 与 src/servers/ 目录是否对应"
            )


def create_run_dirs(svr_list: list[str], run_dir: Path) -> None:
    (run_dir / "common" / "conf").mkdir(parents=True, exist_ok=True)
    for svr in svr_list:
        for sub in ("bin", "conf", "log"):
            (run_dir / svr / sub).mkdir(parents=True, exist_ok=True)


def render_configs(
    svr_list: list[str],
    env: str,
    conf_dir: Path,
    run_dir: Path,
) -> None:
    def run_build(svc: str) -> None:
        cmd = [
            "go", "run", "./tools/config_build",
            f"--env={env}",
            f"--svc={svc}",
            f"--conf={conf_dir}",
            f"--run={run_dir}",
        ]
        subprocess.run(cmd, check=True, cwd=Path("."))

    run_build("common")
    for svr in svr_list:
        run_build(svr)


def main() -> None:
    parser = argparse.ArgumentParser(description="建立 run 目录并渲染服务配置")
    parser.add_argument("--env", default="dev", help="环境名，对应 conf/values/{env}.yaml")
    parser.add_argument("--conf", default="conf", help="配置模板根目录")
    parser.add_argument("--run", default="run", help="运行目录根")
    parser.add_argument("--servers", default="src/servers", help="服务源码根目录")
    parser.add_argument("--bin", default="bin", help="二进制源目录")
    args = parser.parse_args()

    conf_dir = Path(args.conf)
    run_dir = Path(args.run)
    servers_dir = Path(args.servers)
    bin_dir = Path(args.bin)

    svr_list = load_svr_list(args.env, conf_dir)
    validate_svr_list(svr_list, servers_dir)
    create_run_dirs(svr_list, run_dir)
    render_configs(svr_list, args.env, conf_dir, run_dir)

    warnings: list[str] = []
    try:
        update_bin.copy_binaries(svr_list, bin_dir, run_dir, warnings)
    except FileNotFoundError as e:
        sys.exit(f"错误: {e}")
    for w in warnings:
        print(w, file=sys.stderr)

    print(f"config.py 完成：env={args.env}，已渲染 common + {svr_list}")


if __name__ == "__main__":
    main()
