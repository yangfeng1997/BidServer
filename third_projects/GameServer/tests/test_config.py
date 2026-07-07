import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch

import pytest
import yaml

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import config as cfg_mod


def make_env(tmp_path, svr_list, servers=None):
    """构造最小测试目录结构"""
    conf_dir = tmp_path / "conf"
    values_dir = conf_dir / "envs"
    values_dir.mkdir(parents=True)
    (values_dir / "dev.yaml").write_text(
        yaml.dump({"svr_list": svr_list, "gate_node_id": "1.1.1", "gate_addr": "0.0.0.0:8888"}),
        encoding="utf-8",
    )
    (conf_dir / "common.yaml").write_text("redis:\n  host: 127.0.0.1\n", encoding="utf-8")
    for svr in (servers or svr_list):
        (conf_dir / f"{svr}.yaml").write_text("node_id: test\naddr: 0.0.0.0:9999\n", encoding="utf-8")
    src_servers = tmp_path / "src" / "servers"
    for svr in (servers or svr_list):
        (src_servers / svr).mkdir(parents=True)
    return conf_dir, tmp_path / "run", tmp_path / "src" / "servers"


def test_validate_svr_list_ok(tmp_path):
    conf_dir, run_dir, servers_dir = make_env(tmp_path, ["gatesvr"])
    cfg_mod.validate_svr_list(["gatesvr"], servers_dir)


def test_validate_svr_list_missing_raises(tmp_path):
    conf_dir, run_dir, servers_dir = make_env(tmp_path, ["gatesvr"])
    with pytest.raises(SystemExit, match="unknownsvr"):
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
    cfg_mod.create_run_dirs(["gatesvr"], run_dir)


def test_render_configs_calls_config_build(tmp_path):
    conf_dir, run_dir, servers_dir = make_env(tmp_path, ["gatesvr"])
    run_dir.mkdir(parents=True)

    calls = []

    def fake_run(cmd, check, cwd):
        calls.append(cmd)

    with patch("config.subprocess.run", side_effect=fake_run):
        cfg_mod.render_configs(["gatesvr"], "dev", conf_dir, run_dir)

    assert any("common" in " ".join(c) for c in calls), "应渲染 common"
    assert any("gatesvr" in " ".join(c) for c in calls), "应渲染 gatesvr"
