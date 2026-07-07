#!/usr/bin/env python3
"""update_bin.py — 按 svr_list 将 bin/<svr> 复制到 run/<svr>/bin/。"""

import argparse
import shutil
import sys
from pathlib import Path

import yaml

PLATFORM = sys.platform


def _exe(name: str) -> str:
    return f"{name}.exe" if PLATFORM == "win32" else name


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


def copy_binaries(
    svr_list: list[str],
    bin_dir: Path,
    run_dir: Path,
    warn_list: list[str],
) -> None:
    """复制二进制到 run/<svr>/bin/。

    - run/<svr>/bin/ 不存在 → 抛 FileNotFoundError
    - bin/<svr> 不存在 → 追加警告到 warn_list，继续
    """
    for svr in svr_list:
        dst_dir = run_dir / svr / "bin"
        if not dst_dir.exists():
            raise FileNotFoundError(
                f"run/{svr}/bin 目录不存在，请先执行 config.py"
            )
        exe_name = _exe(svr)
        src = bin_dir / exe_name
        if not src.exists():
            warn_list.append(
                f"⚠ {svr} 尚未编译，run/{svr}/bin 为空，可执行 ./build.py 进行编译生成"
            )
            continue
        shutil.copy2(src, dst_dir / exe_name)


def main() -> None:
    parser = argparse.ArgumentParser(description="将 bin/ 下的二进制复制到 run/<svr>/bin/")
    parser.add_argument("--env", default="dev", help="环境名，对应 conf/values/{env}.yaml")
    parser.add_argument("--conf", default="conf", help="配置模板根目录")
    parser.add_argument("--bin", default="bin", help="二进制源目录")
    parser.add_argument("--run", default="run", help="运行目录根")
    args = parser.parse_args()

    conf_dir = Path(args.conf)
    bin_dir = Path(args.bin)
    run_dir = Path(args.run)

    svr_list = load_svr_list(args.env, conf_dir)

    warnings: list[str] = []
    try:
        copy_binaries(svr_list, bin_dir, run_dir, warnings)
    except FileNotFoundError as e:
        sys.exit(f"错误: {e}")

    for w in warnings:
        print(w, file=sys.stderr)


if __name__ == "__main__":
    main()
