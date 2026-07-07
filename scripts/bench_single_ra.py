#!/usr/bin/env python3
"""Run a local single-RouterAgent benchmark."""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

from bench_common import (
    build_client_robot,
    etcd_info,
    gate_addr,
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
)

DEFAULT_MATRIX = "100:1000,200:1000,500:500,1000:300,1500:200,2000:200"
SERVICES = ["routeragent", "gatesvr", "lobbysvr", "onlinesvr", "matchsvr", "roomsvr"]


def main() -> None:
    parser = argparse.ArgumentParser(description="Run local single-RA benchmark")
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
            "gatesvr/bin/gatesvr.pid",
            "lobbysvr/bin/lobbysvr.pid",
            "onlinesvr/bin/onlinesvr.pid",
            "matchsvr/bin/matchsvr.pid",
            "roomsvr/bin/roomsvr.pid",
        ],
    )
    wait_etcd_empty(endpoints, prefix, args.etcd_timeout)

    services_started = False
    try:
        run_cmd([str(run_dir / "startall.sh")], cwd=run_dir)
        services_started = True
        verify_services(
            run_dir,
            [(svc, run_dir / svc / "bin" / f"{svc}.pid", run_dir / svc / "log" / f"{svc}.stderr.log") for svc in SERVICES],
        )
        wait_etcd_nodes(endpoints, prefix, [f"{args.world_id}.{server_type}.0" for server_type in (6, 1, 2, 5, 4, 3)], args.ready_timeout)

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
            run_cmd([str(run_dir / "stopall.sh")], cwd=run_dir, check=False)


if __name__ == "__main__":
    main()
