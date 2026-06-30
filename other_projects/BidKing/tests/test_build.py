import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch

import pytest
import yaml

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import build as build_mod


def make_env(tmp_path, svr_list):
    conf_dir = tmp_path / "conf"
    values_dir = conf_dir / "envs"
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
    build_mod.validate_run_dirs(["gatesvr"], run_dir)


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

    with patch("build.subprocess.run", side_effect=fake_run_fail):
        with pytest.raises(SystemExit):
            build_mod.compile_services(["gatesvr"], bin_dir, servers_dir)
