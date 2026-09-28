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
                  "projects": {"demo": {"name": "Agent demonstration", "path": str(root), "agent_patch_paths": ["editable.txt"]}},
                  "agents": {"proposals": {"enabled": True, "demo": True, "patches": True}},
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

        editable = root / "editable.txt"
        editable.write_text("old\n")
        (root / ".gitignore").write_text("*\n!editable.txt\n!.gitignore\n")
        for command in (["init", "-q"], ["add", "editable.txt", ".gitignore"],
                        ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"]):
            subprocess.run(["git", "-C", str(root), *command], env=env, check=True, timeout=10,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
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
            assert all(features[key] == 1 for key in ("agent_context", "agent_proposals", "agent_supervision", "agent_patches"))
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
            patch_grant = call("GET", "agents/grants/patch/workspace.apply_patch")
            assert patch_grant["target"]["paths"] == ["editable.txt"]
            config["projects"]["demo"]["agent_grants"].append(
                {"kind": "patch", "target": "workspace.apply_patch", "digest": patch_grant["target"]["digest"]})
            save()
            call("POST", "config/reload", {})
            diff = "--- a/editable.txt\n+++ b/editable.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n"
            proposal = call("POST", "agents/patches/propose", {"diff": diff,
                            "request_id": request({"id": "", "digest": ""})["request_id"]})["proposals"][0]["id"]
            full = call("POST", f"agents/proposals/{proposal}/prepare", {})
            assert full["patch"]["diff"] == diff and editable.read_text() == "old\n"
            approval = request(full)
            applied = call("POST", f"agents/proposals/{proposal}/approve", approval)
            assert call("POST", f"agents/proposals/{proposal}/approve", approval) == applied
            result = wait("runs/" + applied["run_id"], ("queued", "running"))
            assert result["state"] == "success" and result["outcomes"][0]["patch"]["files"][0]["state"] == "applied"
            assert editable.read_text() == "new\n"
            # Validation is a separate reviewed capture; patch application cannot start it.
            validation = call("POST", "captures", request(call("POST", "captures/prepare", {"experiment": "benchmark"})))
            validated = wait("runs/" + validation["run_id"], ("queued", "running"))
            assert validated["state"] == "success"
            call("POST", "comparisons", {"baseline": {"kind": "run", "id": baseline["id"]},
                                          "candidate": {"kind": "run", "id": validated["id"]}})
            history = call("GET", "agents/patches")["operations"]
            assert len(history) == 1 and history[0]["operation"] == proposal
            restoration = call("POST", f"agents/patches/{proposal}/restore", {
                "request_id": request({"id": "", "digest": ""})["request_id"]})["proposals"][0]["id"]
            restore_preview = call("POST", f"agents/proposals/{restoration}/prepare", {})
            assert editable.read_text() == "new\n"
            restored = call("POST", f"agents/proposals/{restoration}/approve", request(restore_preview))
            assert wait("runs/" + restored["run_id"], ("queued", "running"))["state"] == "success"
            assert editable.read_text() == "old\n"
            print(json.dumps({"agent_demo": "offline fixture", "generations": 1, "approved_actions": 1,
                              "measured_runs": 3, "comparison": "duration in ms", "recipes_required": False,
                              "patch_applications": 1, "separate_validations": 1, "reviewed_restorations": 1}))
        finally:
            process.terminate()
            try:
                process.wait(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == "__main__":
    main()
