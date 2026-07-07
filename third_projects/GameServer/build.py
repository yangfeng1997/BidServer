#!/usr/bin/env python3
"""build.py — 按 svr_list 编译各服务，将二进制放入 bin/，然后铺到 run/<svr>/bin/。"""

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


def validate_run_dirs(svr_list: list[str], run_dir: Path) -> None:
    for svr in svr_list:
        if not (run_dir / svr).exists():
            sys.exit(
                f"错误: run/{svr}/ 目录不存在，请先执行 config.py 创建目录结构"
            )


def compile_services(
    svr_list: list[str],
    bin_dir: Path,
    servers_dir: Path,
) -> None:
    bin_dir.mkdir(parents=True, exist_ok=True)
    exe_suffix = ".exe" if sys.platform == "win32" else ""
    for svr in svr_list:
        out = bin_dir / f"{svr}{exe_suffix}"
        cmd = [
            "go", "build",
            f"-o={out}",
            f"./src/servers/{svr}",
        ]
        try:
            subprocess.run(cmd, check=True, cwd=Path("."))
        except subprocess.CalledProcessError as e:
            sys.exit(f"错误: {svr} 编译失败（exit {e.returncode}），请检查编译错误")


def main() -> None:
    parser = argparse.ArgumentParser(description="编译各服务并铺二进制到 run/<svr>/bin/")
    parser.add_argument("--env", default="dev", help="环境名，对应 conf/values/{env}.yaml")
    parser.add_argument("--conf", default="conf", help="配置模板根目录")
    parser.add_argument("--run", default="run", help="运行目录根")
    parser.add_argument("--servers", default="src/servers", help="服务源码根目录")
    parser.add_argument("--bin", default="bin", help="二进制输出目录")
    args = parser.parse_args()

    conf_dir = Path(args.conf)
    run_dir = Path(args.run)
    servers_dir = Path(args.servers)
    bin_dir = Path(args.bin)

    svr_list = load_svr_list(args.env, conf_dir)
    validate_svr_list(svr_list, servers_dir)
    validate_run_dirs(svr_list, run_dir)
    compile_services(svr_list, bin_dir, servers_dir)

    warnings: list[str] = []
    try:
        update_bin.copy_binaries(svr_list, bin_dir, run_dir, warnings)
    except FileNotFoundError as e:
        sys.exit(f"错误: {e}")
    for w in warnings:
        print(w, file=sys.stderr)

    print(f"build.py 完成：env={args.env}，已编译并铺二进制 {svr_list}")


if __name__ == "__main__":
    main()
