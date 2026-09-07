#!/usr/bin/env python3
"""Sample CPU and memory for Postgres, Redis, and the auth API while a command runs."""

from __future__ import annotations

import argparse
import csv
import json
import os
import signal
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from typing import Any


CONTAINERS = (
    ("postgres", "tech-internal-db"),
    ("redis", "tech-internal-redis"),
    ("jaeger", "tech-internal-jaeger"),
)


def to_bytes(raw: str) -> float | None:
    s = raw.strip().replace(",", "")
    if not s or s == "--":
        return None
    units = (
        ("GiB", 1024**3),
        ("MiB", 1024**2),
        ("KiB", 1024),
        ("GB", 1000**3),
        ("MB", 1000**2),
        ("KB", 1000),
        ("B", 1),
    )
    for suffix, mul in units:
        if s.endswith(suffix):
            return float(s[: -len(suffix)].strip()) * mul
    try:
        return float(s)
    except ValueError:
        return None


def parse_pct(raw: str) -> float | None:
    s = raw.strip().replace("%", "")
    if not s or s == "--":
        return None
    try:
        return float(s)
    except ValueError:
        return None


def docker_stats() -> dict[str, dict[str, Any]]:
    try:
        out = subprocess.check_output(
            [
                "docker",
                "stats",
                "--no-stream",
                "--format",
                "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}",
                "tech-internal-db",
                "tech-internal-redis",
                "tech-internal-jaeger",
            ],
            text=True,
            stderr=subprocess.DEVNULL,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        return {}
    found: dict[str, dict[str, Any]] = {}
    for line in out.splitlines():
        parts = line.split("\t")
        if len(parts) < 4:
            continue
        name, cpu, mem_usage, mem_pct = parts[0], parts[1], parts[2], parts[3]
        used = mem_usage.split("/")[0].strip() if "/" in mem_usage else mem_usage
        found[name] = {
            "cpu_pct": parse_pct(cpu),
            "mem_bytes": to_bytes(used),
            "mem_pct": parse_pct(mem_pct),
        }
    return found


def api_pid(port: int) -> int | None:
    try:
        out = subprocess.check_output(
            ["lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN", "-t"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None
    for line in out.splitlines():
        line = line.strip()
        if line.isdigit():
            return int(line)
    return None


def proc_stats(pid: int) -> dict[str, Any] | None:
    try:
        out = subprocess.check_output(
            ["ps", "-p", str(pid), "-o", "pcpu=", "-o", "rss="],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except subprocess.CalledProcessError:
        return None
    if not out:
        return None
    parts = out.split()
    if len(parts) < 2:
        return None
    try:
        cpu = float(parts[0])
        rss_kb = float(parts[1])
    except ValueError:
        return None
    return {"cpu_pct": cpu, "mem_bytes": rss_kb * 1024, "mem_pct": None}


def sample_once(port: int) -> list[dict[str, Any]]:
    now = datetime.now(timezone.utc).isoformat()
    ts = time.time()
    rows: list[dict[str, Any]] = []
    stats = docker_stats()
    for label, name in CONTAINERS:
        row = stats.get(name) or {"cpu_pct": None, "mem_bytes": None, "mem_pct": None}
        rows.append(
            {
                "ts": now,
                "unix": ts,
                "target": label,
                "source": name,
                **row,
            }
        )
    pid = api_pid(port)
    api = proc_stats(pid) if pid else None
    rows.append(
        {
            "ts": now,
            "unix": ts,
            "target": "auth_api",
            "source": f"pid:{pid}" if pid else "missing",
            "cpu_pct": None if not api else api["cpu_pct"],
            "mem_bytes": None if not api else api["mem_bytes"],
            "mem_pct": None,
        }
    )
    return rows


def summarize(rows: list[dict[str, Any]]) -> dict[str, Any]:
    by_target: dict[str, list[dict[str, Any]]] = {}
    for row in rows:
        by_target.setdefault(row["target"], []).append(row)

    def series(values: list[float | None]) -> dict[str, float] | None:
        nums = [v for v in values if v is not None]
        if not nums:
            return None
        nums.sort()
        n = len(nums)

        def pct(p: float) -> float:
            if n == 1:
                return nums[0]
            idx = min(n - 1, max(0, int(round((p / 100) * (n - 1)))))
            return nums[idx]

        return {
            "n": n,
            "min": nums[0],
            "avg": sum(nums) / n,
            "p95": pct(95),
            "max": nums[-1],
        }

    out: dict[str, Any] = {}
    for target, target_rows in by_target.items():
        cpu = series([r.get("cpu_pct") for r in target_rows])
        mem = series([r.get("mem_bytes") for r in target_rows])
        out[target] = {
            "cpu_pct": cpu,
            "mem_bytes": mem,
            "mem_mib": None
            if not mem
            else {k: (v / (1024**2) if k != "n" else v) for k, v in mem.items()},
            "samples": len(target_rows),
            "source": target_rows[-1].get("source"),
        }
    return out


def write_outputs(out_prefix: str, rows: list[dict[str, Any]]) -> None:
    os.makedirs(os.path.dirname(out_prefix) or ".", exist_ok=True)
    csv_path = f"{out_prefix}.csv"
    json_path = f"{out_prefix}.summary.json"
    with open(csv_path, "w", newline="") as f:
        w = csv.DictWriter(
            f, fieldnames=["ts", "unix", "target", "source", "cpu_pct", "mem_bytes", "mem_pct"]
        )
        w.writeheader()
        w.writerows(rows)
    summary = summarize(rows)
    with open(json_path, "w") as f:
        json.dump(summary, f, indent=2)
        f.write("\n")
    print(f"infra samples: {csv_path}", file=sys.stderr)
    print(f"infra summary: {json_path}", file=sys.stderr)
    print_human(summary)


def print_human(summary: dict[str, Any]) -> None:
    print("\n==> infra CPU / memory", file=sys.stderr)
    print(
        f"{'target':12} {'cpu min':>8} {'cpu avg':>8} {'cpu p95':>8} {'cpu max':>8} "
        f"{'rss min':>10} {'rss avg':>10} {'rss p95':>10} {'rss max':>10}",
        file=sys.stderr,
    )
    for target in ("postgres", "redis", "jaeger", "auth_api"):
        block = summary.get(target) or {}
        cpu = block.get("cpu_pct") or {}
        mem = block.get("mem_mib") or {}

        def fmt(d: dict[str, Any], key: str, suffix: str) -> str:
            if key not in d:
                return f"{'n/a':>8}"
            return f"{d[key]:.2f}{suffix}"

        print(
            f"{target:12} {fmt(cpu, 'min', '%'):>8} {fmt(cpu, 'avg', '%'):>8} "
            f"{fmt(cpu, 'p95', '%'):>8} {fmt(cpu, 'max', '%'):>8} "
            f"{fmt(mem, 'min', 'Mi'):>10} {fmt(mem, 'avg', 'Mi'):>10} "
            f"{fmt(mem, 'p95', 'Mi'):>10} {fmt(mem, 'max', 'Mi'):>10}",
            file=sys.stderr,
        )


def sampler_loop(port: int, interval: float, rows: list[dict[str, Any]], stop: threading.Event) -> None:
    while not stop.is_set():
        rows.extend(sample_once(port))
        stop.wait(interval)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True, help="Output prefix (writes .csv and .summary.json)")
    parser.add_argument("--interval", type=float, default=1.0)
    parser.add_argument("--port", type=int, default=int(os.environ.get("API_PORT", "8080")))
    parser.add_argument("cmd", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    cmd = args.cmd[1:] if args.cmd and args.cmd[0] == "--" else args.cmd
    if not cmd:
        parser.error("pass a command after --")

    rows: list[dict[str, Any]] = []
    stop = threading.Event()
    t = threading.Thread(target=sampler_loop, args=(args.port, args.interval, rows, stop), daemon=True)
    t.start()

    proc = subprocess.Popen(cmd)

    def handle_signal(signum: int, _frame: Any) -> None:
        proc.send_signal(signum)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)
    rc = proc.wait()
    stop.set()
    t.join(timeout=5)
    if rows:
        write_outputs(args.out, rows)
    return rc


if __name__ == "__main__":
    sys.exit(main())
