#!/usr/bin/env python3
"""Offline explain/review/approve/compare smoke using native packaged binaries."""
import argparse
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
    bundle = parser.parse_args().bundle.resolve()
    env = {**os.environ, "PATH": "/usr/bin:/bin"}
    env.pop("OPENAI_API_KEY", None)
    with tempfile.TemporaryDirectory(prefix="pb-agent-", dir="/tmp") as temporary:
        root = Path(temporary).resolve()
        socket = root / "sock"
        selected = root / "selected.txt"
        selected.write_text("Offline illustrative context. Only native measurements establish timing.")
        config = {"version": 1, "server": {"socket": str(socket)},
                  "state": {"path": str(root / "state.json")},
                  "context": {"defaults": {"project": "demo"}},
                  "projects": {"demo": {"name": "Agent demonstration", "path": str(root)}},
                  "agents": {"proposals": {"enabled": True, "demo": True}},
                  "actions": {"measure": {"type": "exec", "command": str(bundle / "bin/deckdemo"),
                                            "args": ["--iterations", "100", "--repeats", "2"], "safety": "safe"}},
                  "experiments": {"benchmark": {"schema_version": 1, "title": "Benchmark Playground",
                                                   "action": "measure", "projects": ["demo"], "collectors": [
                      {"name": "duration", "step": 0, "action": "measure", "kind": "measurement", "source": "json_stdout",
                       "path": ["duration"], "unit": "ms", "quantity": "duration", "direction": "lower"}]}}}
        config_path = root / "config.json"

        def save():
            config_path.write_text(json.dumps(config))
            config_path.chmod(0o600)

        def call(method, path, value=None):
            connection = UnixHTTP(str(socket))
            try:
                connection.request(method, "/v1/" + path, None if value is None else json.dumps(value),
                                   {"Content-Type": "application/json"})
                response = connection.getresponse()
                result = json.loads(response.read())
                if response.status >= 300:
                    raise RuntimeError(f"agent smoke {method} {path}: {result}")
                return result
            finally:
                connection.close()

        def request(preview):
            return {"preparation": preview["id"], "digest": preview["digest"], "confirmed": True,
                    "request_id": f"{int(time.time()*1000)}-{secrets.token_hex(16)}"}

        def wait(path, active):
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                result = call("GET", path)
                if result["state"] not in active:
                    return result
                time.sleep(0.02)
            raise RuntimeError("agent fixture timed out")

        save()
        process = subprocess.Popen([str(bundle / "bin/deckd"), "--config", str(config_path)],
                                   env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 5
            while not socket.exists():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("agent daemon failed to start")
                time.sleep(0.02)
            catalog = call("GET", "agents/catalog")
            assert catalog["available"] and catalog["destination"].startswith("local:") and not catalog["targets"]
            features = call("GET", "capabilities")["features"]
            assert all(features[key] == 1 for key in ("agent_context", "agent_proposals", "agent_supervision"))
            assert not call("GET", "recipes")["installations"]
            grant = call("GET", "agents/grants/experiment/benchmark")
            assert grant["experiment"]["action"] == "measure"
            config["projects"]["demo"]["agent_grants"] = [
                {"kind": "experiment", "target": "benchmark", "digest": grant["target"]["digest"]}]
            save()
            call("POST", "config/reload", {})
            first = call("POST", "captures", request(call("POST", "captures/prepare", {"experiment": "benchmark"})))
            baseline = wait("runs/" + first["run_id"], ("queued", "running"))
            assert baseline["state"] == "success"
            preview = call("POST", "agents/context/prepare", {"prompt": "Suggest the demo experiment.",
                           "files": ["selected.txt"], "request_id": request({"id": "", "digest": ""})["request_id"]})
            assert selected.read_text() in preview["input"] and preview["destination"].startswith("local:")
            consent = request(preview)
            consent["request_id"] = preview["request_id"]
            session = call("POST", "agents/sessions", consent)
            assert call("POST", "agents/sessions", consent)["id"] == session["id"]
            session = wait("agents/sessions/" + session["id"], ("generating",))
            assert session["state"] == "awaiting_review" and len(session["proposals"]) == 1
            proposal = session["proposals"][0]["id"]
            full = call("POST", f"agents/proposals/{proposal}/prepare", {})
            assert full["capture"]["steps"][0]["executable"] == str(bundle / "bin/deckdemo")
            approval = request(full)
            admitted = call("POST", f"agents/proposals/{proposal}/approve", approval)
            assert call("POST", f"agents/proposals/{proposal}/approve", approval) == admitted
            run = wait("runs/" + admitted["run_id"], ("queued", "running"))
            assert run["state"] == "success" and run["agent"]["proposal"] == proposal
            comparison = call("POST", "comparisons", {"baseline": {"kind": "run", "id": baseline["id"]},
                                                       "candidate": {"kind": "run", "id": run["id"]}})
            assert comparison["metrics"][0]["unit"] == "ms" and "delta" in comparison["metrics"][0]
            print(json.dumps({"agent_demo": "offline fixture", "generations": 1, "approved_actions": 1,
                              "measured_runs": 2, "comparison": "duration in ms", "recipes_required": False}))
        finally:
            process.terminate()
            try:
                process.wait(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == "__main__":
    main()
