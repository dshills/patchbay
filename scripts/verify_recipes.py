#!/usr/bin/env python3
"""Offline native recipe exchange/lifecycle smoke using packaged binaries only."""
import argparse
import contextlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time

from benchmark_runtime import UnixHTTP


@contextlib.contextmanager
def daemon(bundle, root, environment):
    root.mkdir(mode=0o700)
    socket = root / "deckd.sock"
    demo = str(bundle / "bin/deckdemo")
    configuration = {"version": 1, "server": {"socket": str(socket)},
                     "state": {"path": str(root / "state.json")},
                     "context": {"defaults": {"project": "demo"}},
                     "projects": {"demo": {"name": "Recipe exchange", "path": str(root)}},
                     "actions": {name: {"type": "exec", "safety": "confirm", "command": demo,
                                          "args": ["--iterations", "10", "--repeats", "2"],
                                          "cwd": str(root)} for name in ("check", "test")}}
    config = root / "config.json"
    config.write_text(json.dumps(configuration))
    config.chmod(0o600)
    process = subprocess.Popen([str(bundle / "bin/deckd"), "--config", str(config)],
                               env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        deadline = time.monotonic() + 5
        while not socket.exists():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("recipe smoke daemon failed to start")
            time.sleep(0.02)
        yield socket
    finally:
        process.terminate()
        try:
            process.wait(timeout=7)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", type=Path, default=Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    bundle = args.bundle.resolve()
    environment = {**os.environ, "PATH": "/usr/bin:/bin"}
    environment.pop("OPENAI_API_KEY", None)

    def ctl(socket, *args):
        command = [str(bundle / "bin/deckctl"), "--json"]
        if socket:
            command += ["--socket", str(socket)]
        return json.loads(subprocess.check_output(command + list(args), env=environment, timeout=15))

    def call(socket, method, path, value=None):
        connection = UnixHTTP(str(socket))
        try:
            connection.request(method, "/v1/" + path, None if value is None else json.dumps(value),
                               {"Content-Type": "application/json"})
            response = connection.getresponse()
            body = json.loads(response.read())
            if response.status >= 300:
                raise RuntimeError(f"recipe smoke {method} {path}: {body}")
            return body
        finally:
            connection.close()

    def manage(socket, identity, operation, **options):
        preview = call(socket, "POST", f"recipes/{identity}/prepare", {"operation": operation, **options})
        request = {"preparation": preview["id"], "digest": preview["digest"], "confirmed": True,
                   "request_id": f"{int(time.time()*1000)}-{secrets.token_urlsafe(18)}"}
        result = call(socket, "POST", f"recipes/{identity}/commit", request)
        assert call(socket, "POST", f"recipes/{identity}/commit", request) == result
        return result

    def capture(socket, experiment):
        preview = call(socket, "POST", "captures/prepare", {"experiment": experiment})
        admitted = call(socket, "POST", "captures", {"preparation": preview["id"], "digest": preview["digest"],
                        "confirmed": True, "request_id": f"{int(time.time()*1000)}-{secrets.token_urlsafe(18)}"})
        deadline = time.monotonic() + 10
        while True:
            run = call(socket, "GET", "runs/" + admitted["run_id"])
            if run["state"] not in ("queued", "running"):
                assert run["state"] == "success", run.get("error")
                return run
            if time.monotonic() > deadline:
                raise RuntimeError("recipe capture timed out")
            time.sleep(0.02)

    manifests = {}
    for name in ("benchmark", "project-checkup", "rigol-capture"):
        inspected = ctl(None, "recipe", "inspect", str(bundle / "recipes" / name))
        assert len(inspected["samples"]) == 2
        manifests[name] = inspected
    assert "Physical verification pending" in manifests["rigol-capture"]["manifest"]["description"]
    with tempfile.TemporaryDirectory(prefix="pb-recipe-smoke-", dir="/tmp") as temporary:
        root = Path(temporary).resolve()
        with daemon(bundle, root / "sender", environment) as sender:
            assert call(sender, "GET", "capabilities")["features"]["outcome_collectors"] == 1
            entry = ctl(sender, "recipe", "import", str(bundle / "recipes/benchmark"))["installation"]
            identity = entry["id"]
            mappings = {"benchmark": {"project": "demo"}, "demo": {"tool": str(bundle / "bin/deckdemo")}}
            manage(sender, identity, "activate", mappings=mappings)
            run = capture(sender, f"recipe.{identity}.benchmark")
            assert run["recipe"]["content_digest"] == entry["content"]
            call(sender, "PUT", f"parameters/recipe.{identity}.iterations", {"value": 12000})
            preview = ctl(sender, "recipe", "export-preview", identity,
                          json.dumps({"defaults": ["iterations"], "samples": ["benchmark-small", "benchmark-large"]}))
            package = root / "shared.zip"
            ctl(sender, "recipe", "export-save", identity, preview["id"], preview["digest"], str(package), "--confirm")
            portable = ctl(None, "recipe", "inspect", str(package))
            assert portable["package_digest"] != entry["content"]
            ctl(sender, "recipe", "stage", identity, str(package))
            manage(sender, identity, "update")
            manage(sender, identity, "rollback")
            assert ctl(sender, "recipe", "show", identity)["installation"]["content"] == entry["content"]
            manage(sender, identity, "deactivate")
            manage(sender, identity, "remove")
            assert call(sender, "GET", "runs/" + run["id"])["recipe"]["installation"] == identity
            with daemon(bundle, root / "recipient", environment) as recipient:
                received = ctl(recipient, "recipe", "import", str(package))["installation"]
                assert received["id"] != identity
                manage(recipient, received["id"], "activate", mappings=mappings)
                local = capture(recipient, f"recipe.{received['id']}.benchmark")
                assert list(local["parameters"].values())
                comparison = call(recipient, "POST", "comparisons", {
                    "baseline": {"kind": "recipe_sample", "installation": received["id"],
                                 "content": received["content"], "id": "benchmark-small"},
                    "candidate": {"kind": "run", "id": local["id"]}})
                assert comparison["compatible"] and comparison["illustrative"]
                checkup = ctl(recipient, "recipe", "import", str(bundle / "recipes/project-checkup"))["installation"]
                manage(recipient, checkup["id"], "activate", mappings={"project": {"project": "demo"},
                       "check": {"action": "check"}, "test": {"action": "test"}})
                checked = capture(recipient, f"recipe.{checkup['id']}.checkup")
                assert len(checked["outcomes"]) == 2 and len(checked["measurements"]) == 2
                assert all(m["status"] == "valid" and m["value"] >= 0 for m in checked["measurements"])
            print(json.dumps({"recipe_smoke": True, "curated": 3, "captures": 3,
                              "second_installation_exchange": True, "update_rollback_remove": True,
                              "rigol_physical_verification": "pending"}))


if __name__ == "__main__":
    main()
