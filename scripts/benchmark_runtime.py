#!/usr/bin/env python3
"""Measure a built deckd on a private temporary socket, without external services."""

import argparse
import http.client
import json
import os
import platform
import socket
import statistics
import subprocess
import tempfile
import time
from pathlib import Path


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("deckd", timeout=3)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


def call(client, method, route, payload=None):
    body = None if payload is None else json.dumps(payload)
    start = time.perf_counter()
    client.request(method, "/v1/" + route, body, {"Content-Type": "application/json"})
    response = client.getresponse()
    data = json.loads(response.read())
    elapsed = (time.perf_counter() - start) * 1000
    if response.status not in (200, 202):
        raise RuntimeError(f"{method} {route}: {response.status}: {data}")
    return data, elapsed


def distribution(values):
    values = sorted(values)
    return {
        "samples": len(values),
        "median_ms": round(statistics.median(values), 4),
        "p95_ms": round(values[min(len(values) - 1, int(len(values) * 0.95))], 4),
        "max_ms": round(max(values), 4),
    }


def process_usage(pid):
    fields = subprocess.check_output(
        ["ps", "-p", str(pid), "-o", "time=,rss="], text=True
    ).split()
    cpu = 0.0
    for component in fields[0].split(":"):
        cpu = cpu * 60 + float(component)
    return cpu, int(fields[1])


def stop(process, client):
    client.close()
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        raise RuntimeError("daemon shutdown exceeded five seconds") from None
    if process.returncode:
        raise RuntimeError(f"daemon exited {process.returncode}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/deckd")
    parser.add_argument("--samples", type=int, default=1000)
    parser.add_argument("--startup-samples", type=int, default=20)
    parser.add_argument("--idle-seconds", type=float, default=10)
    args = parser.parse_args()
    if args.samples < 1 or args.startup_samples < 1 or args.idle_seconds <= 0:
        parser.error("sample counts and idle duration must be positive")
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix="pb-bench-", dir="/tmp") as directory:
        path = Path(directory)
        config = path / "config.yaml"
        config.write_text("""version: 1
server: {socket: deckd.sock, shutdown_grace: 2s}
state: {path: state.json, flush_interval: 250ms}
jobs: {concurrency: 4, queue_capacity: 64, history_limit: 100}
actions:
  noop: {type: exec, safety: safe, command: /usr/bin/true}
parameters:
  level: {type: integer, value: 50, min: 0, max: 100, persistent: true}
bindings:
  - control: dial
    rotate: {parameter: level}
""")
        os.chmod(config, 0o600)
        startups = []
        with (path / "daemon.log").open("w") as log:
            for index in range(args.startup_samples):
                start = time.perf_counter()
                process = subprocess.Popen(
                    [binary, "--config", str(config)],
                    stdout=subprocess.DEVNULL, stderr=log,
                )
                client = UnixHTTP(str(path / "deckd.sock"))
                try:
                    while True:
                        try:
                            call(client, "GET", "status")
                            break
                        except (OSError, http.client.HTTPException):
                            client.close()
                            if process.poll() is not None or time.perf_counter() - start > 3:
                                raise RuntimeError("daemon did not become ready") from None
                            time.sleep(0.002)
                    startups.append((time.perf_counter() - start) * 1000)
                    if index + 1 < args.startup_samples:
                        continue
                    before_cpu, _ = process_usage(process.pid)
                    idle_start = time.perf_counter()
                    time.sleep(args.idle_seconds)
                    after_cpu, idle_rss = process_usage(process.pid)
                    idle_elapsed = time.perf_counter() - idle_start
                    dispatch, rotation, admission = [], [], []
                    for sample in range(args.samples):
                        _, latency = call(client, "GET", "context")
                        dispatch.append(latency)
                        _, latency = call(client, "POST", "events", {
                            "type": "control.rotated", "source": "benchmark",
                            "payload": {"control": "dial", "delta": 1 if sample % 2 else -1},
                        })
                        rotation.append(latency)
                        job, latency = call(client, "POST", "actions/noop", {})
                        admission.append(latency)
                        while True:
                            value, _ = call(client, "GET", "jobs/" + job["job_id"])
                            if value["state"] not in ("queued", "running"):
                                if value["state"] != "success":
                                    raise RuntimeError(f"noop failed: {value}")
                                break
                            time.sleep(0.001)
                    report = {
                        "platform": platform.platform(),
                        "machine": platform.machine(),
                        "startup": distribution(startups),
                        "context_dispatch": distribution(dispatch),
                        "persistent_rotation": distribution(rotation),
                        "async_admission": distribution(admission),
                        "idle_seconds": round(idle_elapsed, 3),
                        "idle_cpu_percent": round(100 * (after_cpu - before_cpu) / idle_elapsed, 3),
                        "idle_rss_kib": idle_rss,
                        "loaded_rss_kib": process_usage(process.pid)[1],
                    }
                finally:
                    stop(process, client)
        print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
