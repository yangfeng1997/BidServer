import os
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import update_bin


def test_copy_binary_success(tmp_path, monkeypatch):
    """bin/<svr> 存在，run/<svr>/bin/ 存在 → 复制成功（强制 linux 路径，跨平台一致）"""
    monkeypatch.setattr(update_bin, "PLATFORM", "linux")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    run_dir = tmp_path / "run"
    (run_dir / "gatesvr" / "bin").mkdir(parents=True)

    src = bin_dir / "gatesvr"
    src.write_bytes(b"fake binary")

    warnings = []
    update_bin.copy_binaries(["gatesvr"], bin_dir, run_dir, warn_list=warnings)

    dst = run_dir / "gatesvr" / "bin" / "gatesvr"
    assert dst.exists()
    assert dst.read_bytes() == b"fake binary"
    assert len(warnings) == 0


def test_missing_binary_warns(tmp_path, monkeypatch):
    """bin/<svr> 不存在 → 添加警告，不抛异常"""
    monkeypatch.setattr(update_bin, "PLATFORM", "linux")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    run_dir = tmp_path / "run"
    (run_dir / "gatesvr" / "bin").mkdir(parents=True)

    warnings = []
    update_bin.copy_binaries(["gatesvr"], bin_dir, run_dir, warn_list=warnings)

    assert len(warnings) == 1
    assert "gatesvr" in warnings[0]
    assert "build.py" in warnings[0]


def test_missing_run_bin_dir_raises(tmp_path, monkeypatch):
    """run/<svr>/bin/ 不存在 → 抛出 FileNotFoundError"""
    monkeypatch.setattr(update_bin, "PLATFORM", "linux")
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    src = bin_dir / "gatesvr"
    src.write_bytes(b"fake binary")
    run_dir = tmp_path / "run"

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
