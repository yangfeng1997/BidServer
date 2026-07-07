#!/usr/bin/env python3
"""Run a local dual-RouterAgent benchmark."""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
import time
from pathlib import Path

from bench_common import (
    build_client_robot,
    etcd_info,
    gate_addr,
    load_yaml,
    parse_matrix,
    print_summary,
    project_root,
    require_tool,
    run_cmd,
    run_robot_matrix,
    stop_pid_files,
    verify_services,
    wait_etcd_empty,
    wait_etcd_nodes,
    write_results,
    write_yaml,
)

DEFAULT_MATRIX = "100:1000,200:1000,500:500,1000:300"


def main() -> None:
    parser = argparse.ArgumentParser(description="Run local dual-RA benchmark")
    parser.add_argument("--env", default="dev")
    parser.add_argument("--world-id", default=99, type=int)
    parser.add_argument("--matrix", default=DEFAULT_MATRIX, type=parse_matrix, help="comma-separated clients:requests list")
    parser.add_argument("--timeout", default="10s", help="client_robot timeout")
    parser.add_argument("--etcd-timeout", default=15, type=int, help="seconds to wait for old etcd keys to disappear")
    parser.add_argument("--ready-timeout", default=40, type=int, help="seconds to wait for expected etcd keys")
    parser.add_argument("--skip-build", action="store_true", help="reuse existing run/ and client_robot")
    parser.add_argument("--stop-after", action="store_true", help="stop services before exiting")
    parser.add_argument("--result", default="", help="optional JSON output path")
    args = parser.parse_args()

    root = project_root()
    run_dir = root / "run"
    robot = root / "build" / "client_robot"
    endpoints, prefix = etcd_info(args.env, args.world_id)

    require_tool("go")
    require_tool("etcdctl")

    if not args.skip_build:
        run_cmd(["make", "config", f"ENV={args.env}", f"WORLDID={args.world_id}"], cwd=root)
        run_cmd(["make", "build"], cwd=root)
        build_client_robot(root, robot)

    stop_pid_files(
        run_dir,
        [
            "routeragent/bin/routeragent.pid",
            "routeragent2/bin/routeragent2.pid",
            "gatesvr/bin/gatesvr.pid",
            "lobbysvr/bin/lobbysvr.pid",
            "onlinesvr/bin/onlinesvr.pid",
            "matchsvr/bin/matchsvr.pid",
            "roomsvr/bin/roomsvr.pid",
        ],
    )
    wait_etcd_empty(endpoints, prefix, args.etcd_timeout)
    prepare_dual_ra_run(run_dir)

    services_started = False
    try:
        start_dual_ra(run_dir, args.world_id)
        services_started = True
        verify_services(
            run_dir,
            [
                ("routeragent", run_dir / "routeragent" / "bin" / "routeragent.pid", run_dir / "routeragent" / "log" / "routeragent.stderr.log"),
                ("routeragent2", run_dir / "routeragent2" / "bin" / "routeragent2.pid", run_dir / "routeragent2" / "log" / "routeragent2.stderr.log"),
                ("gatesvr", run_dir / "gatesvr" / "bin" / "gatesvr.pid", run_dir / "gatesvr" / "log" / "gatesvr.stderr.log"),
                ("lobbysvr", run_dir / "lobbysvr" / "bin" / "lobbysvr.pid", run_dir / "lobbysvr" / "log" / "lobbysvr.stderr.log"),
            ],
        )
        wait_etcd_nodes(endpoints, prefix, [f"{args.world_id}.6.0", f"{args.world_id}.6.1", f"{args.world_id}.1.0", f"{args.world_id}.2.0"], args.ready_timeout)

        addr = gate_addr(run_dir)
        run_cmd([str(robot), "--addr", addr, "--timeout", args.timeout], cwd=root)
        results = run_robot_matrix(robot, addr, args.matrix, args.timeout)
        print_summary(results)
        write_results(Path(args.result) if args.result else None, results)

        failed = [item for item in results if int(item.get("failed", 0)) > 0 or int(item.get("exit_code", 0)) != 0]
        if failed:
            sys.exit(1)
    finally:
        if args.stop_after and services_started:
            stop_dual_ra(run_dir)


def prepare_dual_ra_run(run_dir: Path) -> None:
    ra2_dir = run_dir / "routeragent2"
    (ra2_dir / "bin").mkdir(parents=True, exist_ok=True)
    (ra2_dir / "conf").mkdir(parents=True, exist_ok=True)
    (ra2_dir / "log").mkdir(parents=True, exist_ok=True)
    shutil.copy2(run_dir / "routeragent" / "bin" / "routeragent", ra2_dir / "bin" / "routeragent")

    ra1_cfg = load_yaml(run_dir / "routeragent" / "conf" / "routeragent.yaml")
    ra1_cfg["sock_path"] = "/tmp/ra1.sock"
    ra1_cfg["listen_addr"] = "127.0.0.1:7100"
    write_yaml(run_dir / "routeragent" / "conf" / "routeragent.local-cross.yaml", ra1_cfg)

    ra2_cfg = load_yaml(run_dir / "routeragent" / "conf" / "routeragent.yaml")
    ra2_cfg["sock_path"] = "/tmp/ra2.sock"
    ra2_cfg["listen_addr"] = "127.0.0.1:7101"
    rewrite_log_basename(ra2_cfg, "routeragent", "routeragent2")
    write_yaml(ra2_dir / "conf" / "routeragent.yaml", ra2_cfg)

    gate_cfg = load_yaml(run_dir / "gatesvr" / "conf" / "gate.yaml")
    gate_cfg["routeragent_sock_path"] = "/tmp/ra1.sock"
    write_yaml(run_dir / "gatesvr" / "conf" / "gate.local-cross.yaml", gate_cfg)

    lobby_cfg = load_yaml(run_dir / "lobbysvr" / "conf" / "lobby.yaml")
    lobby_cfg["routeragent_sock_path"] = "/tmp/ra2.sock"
    write_yaml(run_dir / "lobbysvr" / "conf" / "lobby.local-cross.yaml", lobby_cfg)


def rewrite_log_basename(data: object, old: str, new: str) -> None:
    if isinstance(data, dict):
        for key, value in data.items():
            if key == "basename" and isinstance(value, str):
                data[key] = value.replace(old, new)
            else:
                rewrite_log_basename(value, old, new)
    elif isinstance(data, list):
        for item in data:
            rewrite_log_basename(item, old, new)


def start_dual_ra(run_dir: Path, world_id: int) -> None:
    clean_logs(run_dir, ["routeragent", "routeragent2", "gatesvr", "lobbysvr"])
    commands = [
        (run_dir / "routeragent" / "bin", ["./routeragent", "--pid-file", "routeragent.pid", "--nodeid", f"{world_id}.6.0", "--daemon", "--common-config", "../../common/conf/common.yaml", "--routeragent-config", "../conf/routeragent.local-cross.yaml"]),
        (run_dir / "routeragent2" / "bin", ["./routeragent", "--pid-file", "routeragent2.pid", "--nodeid", f"{world_id}.6.1", "--daemon", "--common-config", "../../common/conf/common.yaml", "--routeragent-config", "../conf/routeragent.yaml"]),
        (run_dir / "gatesvr" / "bin", ["./gatesvr", "--pid-file", "gatesvr.pid", "--nodeid", f"{world_id}.1.0", "--daemon", "--common-config", "../../common/conf/common.yaml", "--gate-config", "../conf/gate.local-cross.yaml"]),
        (run_dir / "lobbysvr" / "bin", ["./lobbysvr", "--pid-file", "lobbysvr.pid", "--nodeid", f"{world_id}.2.0", "--daemon", "--common-config", "../../common/conf/common.yaml", "--lobby-config", "../conf/lobby.local-cross.yaml"]),
    ]
    for cwd, cmd in commands:
        stdout_log = cwd.parent / "log" / f"{cwd.parent.name}.stdout.log"
        stderr_log = cwd.parent / "log" / f"{cwd.parent.name}.stderr.log"
        if cwd.parent.name == "routeragent2":
            stdout_log = cwd.parent / "log" / "routeragent2.stdout.log"
            stderr_log = cwd.parent / "log" / "routeragent2.stderr.log"
        with stdout_log.open("w", encoding="utf-8") as stdout, stderr_log.open("w", encoding="utf-8") as stderr:
            print("$ " + " ".join(cmd))
            result = subprocess.run(cmd, cwd=cwd, stdout=stdout, stderr=stderr)
        if result.returncode != 0:
            raise SystemExit(result.returncode)
        time.sleep(0.5)


def stop_dual_ra(run_dir: Path) -> None:
    stop_pid_files(
        run_dir,
        [
            "lobbysvr/bin/lobbysvr.pid",
            "gatesvr/bin/gatesvr.pid",
            "routeragent2/bin/routeragent2.pid",
            "routeragent/bin/routeragent.pid",
        ],
    )


def clean_logs(run_dir: Path, services: list[str]) -> None:
    for service in services:
        log_dir = run_dir / service / "log"
        if log_dir.exists():
            shutil.rmtree(log_dir)
        log_dir.mkdir(parents=True, exist_ok=True)


if __name__ == "__main__":
    main()
