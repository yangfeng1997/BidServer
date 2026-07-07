# 配置工具链重设计 — 计划 B（Python 三脚本）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现三个 Python 脚本：`update_bin.py`（按 svr_list 复制二进制）、`config.py`（建目录+渲染配置）、`build.py`（编译+铺二进制），构成完整的本地开发工具链。

**Architecture:** 三脚本均以 `conf/values/<env>.yaml` 中的 `svr_list` 为权威清单，`update_bin.py` 被 `config.py` 和 `build.py` 调用，`config.py` 负责目录创建和配置渲染，`build.py` 负责编译。

**Tech Stack:** Python 3.8+、PyYAML、subprocess、shutil、pathlib

**执行前提：** 计划 A 已完成（`conf/values/<env>.yaml` 含 `svr_list`，`run/` 结构已确定）。

---

## 文件变更地图

### 新建
- `update_bin.py` — 按 svr_list 将 `bin/<svr>` 复制到 `run/<svr>/bin/`
- `config.py` — 读 svr_list、建目录、调 config_build 渲染配置、调 update_bin
- `build.py` — 读 svr_list、编译各服务、调 update_bin
- `tests/test_update_bin.py` — update_bin 单元测试
- `tests/test_config.py` — config.py 单元测试
- `tests/test_build.py` — build.py 单元测试

---

### Task 1: update_bin.py

**Files:**
- Create: `update_bin.py`
- Create: `tests/test_update_bin.py`

- [ ] **Step 1: 写失败测试**

新建 `tests/test_update_bin.py`：

```python
import os
import shutil
import sys
import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import update_bin


def test_copy_binary_success(tmp_path):
    """bin/<svr> 存在，run/<svr>/bin/ 存在 → 复制成功"""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    run_dir = tmp_path / "run"
    (run_dir / "gatesvr" / "bin").mkdir(parents=True)

    # 创建假二进制
    src = bin_dir / "gatesvr"
    src.write_bytes(b"fake binary")

    warnings = []
    update_bin.copy_binaries(["gatesvr"], bin_dir, run_dir, warn_list=warnings)

    dst = run_dir / "gatesvr" / "bin" / "gatesvr"
    assert dst.exists()
    assert dst.read_bytes() == b"fake binary"
    assert len(warnings) == 0


def test_missing_binary_warns(tmp_path):
    """bin/<svr> 不存在 → 添加警告，不抛异常"""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    run_dir = tmp_path / "run"
    (run_dir / "gatesvr" / "bin").mkdir(parents=True)

    warnings = []
    update_bin.copy_binaries(["gatesvr"], bin_dir, run_dir, warn_list=warnings)

    assert len(warnings) == 1
    assert "gatesvr" in warnings[0]
    assert "build.py" in warnings[0]


def test_missing_run_bin_dir_raises(tmp_path):
    """run/<svr>/bin/ 不存在 → 抛出 FileNotFoundError"""
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    src = bin_dir / "gatesvr"
    src.write_bytes(b"fake binary")
    run_dir = tmp_path / "run"
    # 故意不创建 run/gatesvr/bin/

    with pytest.raises(FileNotFoundError, match="run/gatesvr/bin"):
        update_bin.copy_binaries(["gatesvr"], bin_dir, run_dir, warn_list=[])


def test_windows_exe_suffix(tmp_path, monkeypatch):
    """Windows 平台自动加 .exe 后缀"""
    monkeypatch.setattr(update_bin, "PLATFORM", "win32")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    run_dir = tmp_path / "run"
    (run_dir / "gatesvr" / "bin").mkdir(parents=True)

    src = bin_dir / "gatesvr.exe"
    src.write_bytes(b"win binary")

    warnings = []
    update_bin.copy_binaries(["gatesvr"], bin_dir, run_dir, warn_list=warnings)

    dst = run_dir / "gatesvr" / "bin" / "gatesvr.exe"
    assert dst.exists()
    assert len(warnings) == 0
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
python -m pytest tests/test_update_bin.py -v
```

预期：`ModuleNotFoundError: No module named 'update_bin'`

- [ ] **Step 3: 实现 `update_bin.py`**

```python
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
    values_path = conf_dir / "values" / f"{env}.yaml"
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
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
python -m pytest tests/test_update_bin.py -v
```

预期：全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add update_bin.py tests/test_update_bin.py
git commit -m "feat: 新增 update_bin.py（按 svr_list 复制二进制到 run/<svr>/bin）"
```

---

### Task 2: config.py

**Files:**
- Create: `config.py`
- Create: `tests/test_config.py`

- [ ] **Step 1: 写失败测试**

新建 `tests/test_config.py`：

```python
import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch, MagicMock

import pytest
import yaml

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import config as cfg_mod


def make_env(tmp_path, svr_list, servers=None):
    """构造最小测试目录结构"""
    conf_dir = tmp_path / "conf"
    values_dir = conf_dir / "values"
    values_dir.mkdir(parents=True)
    (values_dir / "dev.yaml").write_text(
        yaml.dump({"svr_list": svr_list, "gate_node_id": "1.1.1", "gate_addr": "0.0.0.0:8888"}),
        encoding="utf-8",
    )
    # common.yaml
    (conf_dir / "common.yaml").write_text("redis:\n  host: 127.0.0.1\n", encoding="utf-8")
    # 各服务 yaml
    for svr in (servers or svr_list):
        (conf_dir / f"{svr}.yaml").write_text(f"node_id: test\naddr: 0.0.0.0:9999\n", encoding="utf-8")
    # src/servers/<svr>/
    src_servers = tmp_path / "src" / "servers"
    for svr in (servers or svr_list):
        (src_servers / svr).mkdir(parents=True)
    return conf_dir, tmp_path / "run", tmp_path / "src" / "servers"


def test_validate_svr_list_ok(tmp_path):
    conf_dir, run_dir, servers_dir = make_env(tmp_path, ["gatesvr"])
    # 不应抛异常
    cfg_mod.validate_svr_list(["gatesvr"], servers_dir)


def test_validate_svr_list_missing_raises(tmp_path):
    conf_dir, run_dir, servers_dir = make_env(tmp_path, ["gatesvr"])
    with pytest.raises(SystemExit, match="gatesvr.*src/servers"):
        cfg_mod.validate_svr_list(["gatesvr", "unknownsvr"], servers_dir)


def test_create_run_dirs(tmp_path):
    run_dir = tmp_path / "run"
    cfg_mod.create_run_dirs(["gatesvr"], run_dir)
    assert (run_dir / "gatesvr" / "bin").exists()
    assert (run_dir / "gatesvr" / "conf").exists()
    assert (run_dir / "gatesvr" / "log").exists()
    assert (run_dir / "common" / "conf").exists()


def test_create_run_dirs_idempotent(tmp_path):
    run_dir = tmp_path / "run"
    cfg_mod.create_run_dirs(["gatesvr"], run_dir)
    cfg_mod.create_run_dirs(["gatesvr"], run_dir)  # 再次调用不应报错


def test_render_configs_calls_config_build(tmp_path):
    conf_dir, run_dir, servers_dir = make_env(tmp_path, ["gatesvr"])
    run_dir.mkdir(parents=True)

    calls = []

    def fake_run(cmd, check, cwd):
        calls.append(cmd)

    with patch("config.subprocess.run", side_effect=fake_run):
        cfg_mod.render_configs(["gatesvr"], "dev", conf_dir, run_dir)

    # 应调用 common + gatesvr 两次
    assert any("common" in " ".join(c) for c in calls), "应渲染 common"
    assert any("gatesvr" in " ".join(c) for c in calls), "应渲染 gatesvr"
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
python -m pytest tests/test_config.py -v
```

预期：`ModuleNotFoundError: No module named 'config'`

- [ ] **Step 3: 实现 `config.py`**

```python
#!/usr/bin/env python3
"""config.py — 读 svr_list，建 run 目录结构，渲染配置，铺二进制。"""

import argparse
import subprocess
import sys
from pathlib import Path

import yaml

import update_bin


def load_svr_list(env: str, conf_dir: Path) -> list[str]:
    values_path = conf_dir / "values" / f"{env}.yaml"
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
    # common 目录
    (run_dir / "common" / "conf").mkdir(parents=True, exist_ok=True)
    # 各服务目录
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

    # common 始终渲染
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

    # 铺二进制（软提示模式：缺失只警告）
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
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
python -m pytest tests/test_config.py -v
```

预期：全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add config.py tests/test_config.py
git commit -m "feat: 新增 config.py（建目录、渲染配置、铺二进制）"
```

---

### Task 3: build.py

**Files:**
- Create: `build.py`
- Create: `tests/test_build.py`

- [ ] **Step 1: 写失败测试**

新建 `tests/test_build.py`：

```python
import os
import sys
from pathlib import Path
from unittest.mock import patch, call

import pytest
import yaml

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import build as build_mod


def make_env(tmp_path, svr_list):
    conf_dir = tmp_path / "conf"
    values_dir = conf_dir / "values"
    values_dir.mkdir(parents=True)
    (values_dir / "dev.yaml").write_text(
        yaml.dump({"svr_list": svr_list}), encoding="utf-8"
    )
    servers_dir = tmp_path / "src" / "servers"
    for svr in svr_list:
        (servers_dir / svr).mkdir(parents=True)
    run_dir = tmp_path / "run"
    for svr in svr_list:
        (run_dir / svr).mkdir(parents=True)
    return conf_dir, run_dir, servers_dir, tmp_path / "bin"


def test_validate_run_dirs_ok(tmp_path):
    conf_dir, run_dir, servers_dir, bin_dir = make_env(tmp_path, ["gatesvr"])
    build_mod.validate_run_dirs(["gatesvr"], run_dir)  # 不应报错


def test_validate_run_dirs_missing_raises(tmp_path):
    conf_dir, run_dir, servers_dir, bin_dir = make_env(tmp_path, ["gatesvr"])
    with pytest.raises(SystemExit, match="config.py"):
        build_mod.validate_run_dirs(["gatesvr", "lobbysvr"], run_dir)


def test_compile_services_calls_go_build(tmp_path):
    conf_dir, run_dir, servers_dir, bin_dir = make_env(tmp_path, ["gatesvr"])
    bin_dir.mkdir(parents=True)

    calls = []

    def fake_run(cmd, check, cwd):
        calls.append(cmd)

    with patch("build.subprocess.run", side_effect=fake_run):
        build_mod.compile_services(["gatesvr"], bin_dir, servers_dir)

    assert len(calls) == 1
    assert "go" in calls[0]
    assert any("gatesvr" in part for part in calls[0])


def test_compile_failure_raises(tmp_path):
    conf_dir, run_dir, servers_dir, bin_dir = make_env(tmp_path, ["gatesvr"])
    bin_dir.mkdir(parents=True)

    def fake_run_fail(cmd, check, cwd):
        raise subprocess.CalledProcessError(1, cmd)

    import subprocess
    with patch("build.subprocess.run", side_effect=fake_run_fail):
        with pytest.raises(SystemExit):
            build_mod.compile_services(["gatesvr"], bin_dir, servers_dir)
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
python -m pytest tests/test_build.py -v
```

预期：`ModuleNotFoundError: No module named 'build'`

- [ ] **Step 3: 实现 `build.py`**

```python
#!/usr/bin/env python3
"""build.py — 按 svr_list 编译各服务，将二进制放入 bin/，然后铺到 run/<svr>/bin/。"""

import argparse
import subprocess
import sys
from pathlib import Path

import yaml

import update_bin


def load_svr_list(env: str, conf_dir: Path) -> list[str]:
    values_path = conf_dir / "values" / f"{env}.yaml"
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
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
python -m pytest tests/test_build.py -v
```

预期：全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add build.py tests/test_build.py
git commit -m "feat: 新增 build.py（编译各服务，铺二进制到 run/<svr>/bin）"
```

---

### Task 4: 集成验证

- [ ] **Step 1: 运行全部 Python 测试**

```bash
python -m pytest tests/ -v
```

预期：全部 PASS。

- [ ] **Step 2: 端到端冒烟验证（需本地 Go 环境）**

```bash
python config.py --env=dev
```

预期输出：
```
config.py 完成：env=dev，已渲染 common + ['gatesvr', 'lobbysvr', 'onlinesvr', 'routersvr']
⚠ gatesvr 尚未编译，run/gatesvr/bin 为空，可执行 ./build.py 进行编译生成
（其余服务同上）
```

检查目录结构已建立：

```bash
# Windows PowerShell
Get-ChildItem run -Recurse -Directory
```

预期：`run/common/conf/`、`run/gatesvr/{bin,conf,log}/` 等均存在。

检查配置已渲染：

```bash
# run/common/conf/config.yaml 应含 redis/etcd，不含 node_id
# run/gatesvr/conf/config.yaml 应含 node_id，不含 redis
```

- [ ] **Step 3: 推送并提 PR**

```bash
git push -u origin docs/config-toolchain-redesign
```

然后在 GitHub 上从 `docs/config-toolchain-redesign` 向 `main` 提 PR。
