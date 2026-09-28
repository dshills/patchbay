#!/usr/bin/env python3
"""Exercise downloaded capture/demo/export binaries without a Go toolchain or AI key."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time

from benchmark_runtime import UnixHTTP


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", type=Path, default=Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    bundle = args.bundle.resolve()
    with tempfile.TemporaryDirectory(prefix="pb-demo-smoke-", dir="/tmp") as temporary:
        root = Path(temporary)
        socket = root / "deckd.sock"
        source = (bundle / "configs/benchmark.yaml").read_text()
        source = source.replace("../bin/deckdemo", json.dumps(str(bundle / "bin/deckdemo")))
        source = source.replace("../.cache/benchmark/", str(root) + "/")
        source = source.replace("path: ..}", "path: " + json.dumps(str(root)) + "}")
        config = root / "config.yaml"
        config.write_text(source)
        config.chmod(0o600)
        environment = {**os.environ, "PATH": "/usr/bin:/bin"}
        environment.pop("OPENAI_API_KEY", None)
        process = subprocess.Popen([str(bundle / "bin/deckd"), "--config", str(config)],
                                   env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def call(method, path, body=None):
            connection = UnixHTTP(str(socket))
            try:
                data = None if body is None else json.dumps(body)
                connection.request(method, "/v1/" + path, data, {"Content-Type": "application/json"})
                response = connection.getresponse()
                value = json.loads(response.read())
                if response.status >= 300:
                    raise RuntimeError(f"{method} {path}: {value}")
                return value
            finally:
                connection.close()
        try:
            deadline = time.monotonic() + 5
            while not socket.exists():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("packaged demo daemon failed to start")
                time.sleep(0.02)
            assert call("GET", "capabilities")["features"]["capture"] == 1
            samples = call("GET", "samples")["samples"]
            assert len(samples) == 2 and all(s["origin"] == "sample" for s in samples)
            ids = []
            for iterations in (10000, 20000):
                call("PUT", "parameters/iterations", {"value": iterations})
                preview = call("POST", "captures/prepare", {"experiment": "benchmark"})
                request = {"preparation": preview["id"], "digest": preview["digest"],
                           "request_id": f"{int(time.time()*1000)}-{secrets.token_urlsafe(18)}", "confirmed": True}
                admitted = call("POST", "captures", request)
                assert call("POST", "captures", request) == admitted
                deadline = time.monotonic() + 10
                while True:
                    run = call("GET", "runs/" + admitted["run_id"])
                    if run["state"] not in ("queued", "running"):
                        break
                    if time.monotonic() > deadline:
                        raise RuntimeError("packaged capture timed out")
                    time.sleep(0.02)
                assert run["state"] == "success", run.get("error")
                assert run["measurements"][0]["repeats"] == 5
                ids.append(run["id"])
            comparison = call("POST", "comparisons", {"baseline": {"kind": "run", "id": ids[0]},
                                                       "candidate": {"kind": "run", "id": ids[1]}})
            assert comparison["compatible"] and "delta" in comparison["metrics"][0]
            preview = call("POST", "exports/prepare", {"runs": ids, "options": {}})
            exported = call("POST", "exports", {"preparation": preview["id"], "digest": preview["digest"], "format": "html"})
            report = base64.b64decode(exported["data_base64"], validate=True)
            assert hashlib.sha256(report).hexdigest() == exported["sha256"]
            assert b"<script" not in report and b"Patchbay experiment report" in report
            print(json.dumps({"workbench_smoke": True, "captures": 2, "samples": len(samples), "offline_export": True}))
        finally:
            process.terminate()
            try:
                process.wait(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == "__main__":
    main()
