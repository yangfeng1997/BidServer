#!/usr/bin/env python3
"""Shared helpers for local benchmark scripts."""

from __future__ import annotations

import argparse
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

import yaml

FATAL_LOG_RE = "panic|fatal|etcd node already exists|bind: address already in use|permission denied|no such file"


def project_root() -> Path:
    return Path(__file__).resolve().parents[1]


def load_yaml(path: Path) -> dict[str, Any]:
    if not path.exists():
        raise SystemExit(f"ERROR: yaml file not found: {path}")
    with path.open("r", encoding="utf-8") as file:
        data = yaml.safe_load(file) or {}
    if not isinstance(data, dict):
        raise SystemExit(f"ERROR: yaml file must be a map: {path}")
    return data


def write_yaml(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as file:
        yaml.safe_dump(data, file, sort_keys=False, allow_unicode=False)


def run_cmd(cmd: list[str], cwd: Path | None = None, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess[str]:
    print("$ " + " ".join(cmd))
    result = subprocess.run(cmd, cwd=cwd, text=True, capture_output=capture)
    if not capture:
        if result.returncode != 0 and check:
            raise SystemExit(result.returncode)
        return result
    if result.stdout:
        print(result.stdout, end="")
    if result.stderr:
        print(result.stderr, end="", file=sys.stderr)
    if result.returncode != 0 and check:
        raise SystemExit(result.returncode)
    return result


def parse_matrix(value: str) -> list[tuple[int, int]]:
    matrix: list[tuple[int, int]] = []
    for item in value.split(","):
        item = item.strip()
        if not item:
            continue
        if ":" not in item:
            raise argparse.ArgumentTypeError(f"matrix item must be clients:requests: {item}")
        clients_text, requests_text = item.split(":", 1)
        try:
            clients = int(clients_text, 10)
            requests = int(requests_text, 10)
        except ValueError:
            raise argparse.ArgumentTypeError(f"matrix item must contain integers: {item}") from None
        if clients <= 0 or requests <= 0:
            raise argparse.ArgumentTypeError(f"matrix values must be positive: {item}")
        if clients <= 1 and requests <= 1:
            raise argparse.ArgumentTypeError(f"matrix item must enter benchmark mode, use clients>1 or requests>1: {item}")
        matrix.append((clients, requests))
    if not matrix:
        raise argparse.ArgumentTypeError("matrix must not be empty")
    return matrix


def config_values(env: str) -> dict[str, Any]:
    return load_yaml(project_root() / "config" / "values" / f"{env}.yaml")


def etcd_info(env: str, world_id: int) -> tuple[list[str], str]:
    values = config_values(env)
    cluster_name = str(values.get("cluster_name") or "gameserver")
    cluster_env = str(values.get("cluster_env") or env)
    endpoints: list[str] = []
    for key in sorted(values):
        if key.startswith("etcd_endpoint_"):
            endpoints.append(str(values[key]))
    if not endpoints:
        raise SystemExit(f"ERROR: no etcd endpoints found in config/values/{env}.yaml")
    prefix = f"/{cluster_name}/{cluster_env}/worlds/{world_id}/nodes"
    return endpoints, prefix


def require_tool(name: str) -> None:
    result = subprocess.run(["bash", "-lc", f"command -v {name}"], text=True, capture_output=True)
    if result.returncode != 0:
        raise SystemExit(f"ERROR: required tool not found: {name}")


def stop_pid_files(root: Path, rel_paths: list[str], timeout_sec: int = 10) -> None:
    live_pids: list[tuple[str, int]] = []
    for rel in rel_paths:
        path = root / rel
        if not path.exists():
            continue
        text = path.read_text(encoding="utf-8").strip()
        if not text:
            continue
        try:
            pid = int(text, 10)
        except ValueError:
            print(f"WARN: invalid pid file {path}: {text}")
            continue
        try:
            os.kill(pid, signal.SIGTERM)
            print(f"stopped pid={pid} from {rel}")
        except ProcessLookupError:
            pass
        except PermissionError as exc:
            raise SystemExit(f"ERROR: cannot stop pid={pid} from {path}: {exc}") from exc
        live_pids.append((rel, pid))

    deadline = time.time() + timeout_sec
    while live_pids and time.time() < deadline:
        live_pids = [(rel, pid) for rel, pid in live_pids if process_exists(pid)]
        if live_pids:
            time.sleep(0.2)
    if live_pids:
        for rel, pid in live_pids:
            print(f"ERROR: process still running after stop: pid={pid} pid_file={rel}")
        raise SystemExit(1)


def process_exists(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True


def etcd_get_keys(endpoints: list[str], prefix: str) -> list[str]:
    result = subprocess.run(
        ["etcdctl", f"--endpoints={','.join(endpoints)}", "get", prefix, "--prefix", "--keys-only"],
        text=True,
        capture_output=True,
    )
    if result.returncode != 0:
        print(result.stdout, end="")
        print(result.stderr, end="", file=sys.stderr)
        raise SystemExit(result.returncode)
    return [line for line in result.stdout.splitlines() if line.strip()]


def wait_etcd_empty(endpoints: list[str], prefix: str, timeout_sec: int) -> None:
    deadline = time.time() + timeout_sec
    while True:
        keys = etcd_get_keys(endpoints, prefix)
        if not keys:
            print(f"etcd prefix empty: {prefix}")
            return
        if time.time() >= deadline:
            print(f"ERROR: etcd prefix still has keys after {timeout_sec}s: {prefix}")
            for key in keys:
                print(key)
            raise SystemExit(1)
        for key in keys:
            print(key)
        time.sleep(3)


def wait_etcd_nodes(endpoints: list[str], prefix: str, nodes: list[str], timeout_sec: int) -> None:
    deadline = time.time() + timeout_sec
    while True:
        keys = set(etcd_get_keys(endpoints, prefix))
        missing = [node for node in nodes if f"{prefix}/{node}" not in keys]
        if not missing:
            print("etcd nodes ready: " + ",".join(nodes))
            return
        if time.time() >= deadline:
            print("ERROR: missing etcd nodes: " + ",".join(missing))
            for key in sorted(keys):
                print(key)
            raise SystemExit(1)
        time.sleep(2)


def build_client_robot(root: Path, out: Path) -> None:
    run_cmd(["go", "build", "-o", str(out), "./tools/client_robot"], cwd=root)


def listen_to_local_addr(listen: str) -> str:
    host, sep, port = listen.rpartition(":")
    if not sep:
        raise SystemExit(f"ERROR: invalid listen address: {listen}")
    if host in ("", "0.0.0.0", "::", "[::]"):
        host = "127.0.0.1"
    return f"{host}:{port}"


def gate_addr(run_dir: Path) -> str:
    gate_cfg = load_yaml(run_dir / "gatesvr" / "conf" / "gate.yaml")
    return listen_to_local_addr(str(gate_cfg.get("listen_tcp") or "127.0.0.1:5555"))


def verify_services(run_dir: Path, services: list[tuple[str, Path, Path]], timeout_sec: int = 8) -> None:
    deadline = time.time() + timeout_sec
    while True:
        missing = [(name, pid_file) for name, pid_file, _ in services if not pid_file.exists() or not pid_file.read_text(encoding="utf-8").strip()]
        if not missing or time.time() >= deadline:
            break
        time.sleep(0.2)
    for name, pid_file, stderr_log in services:
        if not pid_file.exists() or not pid_file.read_text(encoding="utf-8").strip():
            raise SystemExit(f"ERROR: missing pid file for {name}: {pid_file}")
        pid = int(pid_file.read_text(encoding="utf-8").strip())
        if not process_exists(pid):
            raise SystemExit(f"ERROR: process not running for {name}: pid={pid}")
        if stderr_log.exists():
            result = subprocess.run(["grep", "-Eqi", FATAL_LOG_RE, str(stderr_log)])
            if result.returncode == 0:
                subprocess.run(["grep", "-Ein", FATAL_LOG_RE, str(stderr_log)])
                raise SystemExit(f"ERROR: fatal log pattern in {stderr_log}")
    print("service pid/log check passed")


def run_robot_matrix(robot: Path, addr: str, matrix: list[tuple[int, int]], timeout: str) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    for clients, requests in matrix:
        print(f"=== clients={clients} requests={requests} ===")
        result = subprocess.run(
            [str(robot), "--addr", addr, "--clients", str(clients), "--requests", str(requests), "--timeout", timeout, "--quiet", "--json"],
            text=True,
            capture_output=True,
        )
        if result.stdout:
            print(result.stdout, end="")
        if result.stderr:
            print(result.stderr, end="", file=sys.stderr)
        try:
            data = json.loads(result.stdout)
        except json.JSONDecodeError:
            raise SystemExit(f"ERROR: client_robot did not return json for clients={clients} requests={requests}") from None
        data["exit_code"] = result.returncode
        results.append(data)
        time.sleep(1)
    return results


def print_summary(results: list[dict[str, Any]]) -> None:
    print("\n| clients | requests | sent | ok | failed | qps | p50_ms | p90_ms | p99_ms | max_ms |")
    print("|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
    for item in results:
        latency = item.get("latency") or {}
        print(
            "| {clients} | {requests_per_client} | {sent} | {ok} | {failed} | {qps:.0f} | {p50:.2f} | {p90:.2f} | {p99:.2f} | {maxv:.2f} |".format(
                clients=item.get("clients", 0),
                requests_per_client=item.get("requests_per_client", 0),
                sent=item.get("sent", 0),
                ok=item.get("ok", 0),
                failed=item.get("failed", 0),
                qps=float(item.get("qps", 0.0)),
                p50=float(latency.get("p50_ns", 0)) / 1_000_000,
                p90=float(latency.get("p90_ns", 0)) / 1_000_000,
                p99=float(latency.get("p99_ns", 0)) / 1_000_000,
                maxv=float(latency.get("max_ns", 0)) / 1_000_000,
            )
        )


def write_results(path: Path | None, results: list[dict[str, Any]]) -> None:
    if path is None:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(results, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
    print(f"wrote results: {path}")
