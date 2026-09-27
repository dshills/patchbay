#!/usr/bin/env python3
"""Exercise built V1 binaries in a temporary launchd-style environment."""

import argparse
import json
import os
from pathlib import Path
import plistlib
import shutil
import signal
import stat
import subprocess
import tempfile
import time

from benchmark_runtime import UnixHTTP, call, distribution


def expand(value, directory):
    if isinstance(value, str):
        return value.replace("@HOME@", str(directory))
    if isinstance(value, list):
        return [expand(item, directory) for item in value]
    if isinstance(value, dict):
        return {key: expand(item, directory) for key, item in value.items()}
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--samples", type=int, default=50)
    args = parser.parse_args()
    if args.samples < 1:
        parser.error("samples must be positive")
    root = Path(__file__).resolve().parent.parent
    template = plistlib.loads((root / "configs/local.patchbay.deckd.plist").read_bytes())
    with tempfile.TemporaryDirectory(prefix="pb-v1-", dir="/tmp") as temporary:
        directory = Path(temporary)
        agent = expand(template, directory)
        for relative in (".local/bin", ".config/deckd", ".deckd"):
            (directory / relative).mkdir(mode=0o700, parents=True)
        for executable in ("deckd", "deckctl"):
            shutil.copyfile(root / "bin" / executable, directory / ".local/bin" / executable)
            (directory / ".local/bin" / executable).chmod(0o755)
        config = directory / ".config/deckd/config.yaml"
        config.write_bytes((root / "configs/quickstart.yaml").read_bytes())
        config.chmod(0o600)
        environment = {**os.environ, **agent["EnvironmentVariables"]}
        executable = str(directory / ".local/bin/deckctl")
        socket_path = config.parent / "private/deckd.sock"
        state_path = config.parent / "private/state.json"
        subprocess.run(["git", "init", "--quiet", str(config.parent)], env=environment, check=True)
        subprocess.run(["plutil", "-lint", str(root / "configs/local.patchbay.deckd.plist")],
                       check=True, capture_output=True)
        if agent["Umask"] != 0o077 or agent["ExitTimeOut"] <= 5:
            raise RuntimeError("agent permissions or shutdown budget are inconsistent")

        def ctl(*command, expected=0, offline=False):
            options = [] if offline else ["--socket", str(socket_path)]
            result = subprocess.run([executable, "--json", *options, *command],
                                    capture_output=True, text=True, timeout=8, env=environment)
            if result.returncode != expected:
                raise RuntimeError(f"{command}: {result.returncode}: {result.stderr}")
            if expected == 0 and result.stderr:
                raise RuntimeError(f"unexpected diagnostic: {result.stderr}")
            return json.loads(result.stdout)

        def start():
            logs = []
            for key in ("StandardOutPath", "StandardErrorPath"):
                descriptor = os.open(agent[key], os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
                logs.append(os.fdopen(descriptor, "ab"))
            process = subprocess.Popen(agent["ProgramArguments"], cwd=agent["WorkingDirectory"],
                                       env=environment, stdout=logs[0], stderr=logs[1], umask=agent["Umask"])
            for log in logs:
                log.close()
            try:
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError("daemon failed in service environment")
                    result = subprocess.run([executable, "--socket", str(socket_path), "status", "--json"],
                                            capture_output=True, timeout=1, env=environment)
                    if result.returncode == 0:
                        return process
                    time.sleep(0.01)
                raise RuntimeError("daemon startup timed out")
            except BaseException:
                if process.poll() is None:
                    process.kill()
                process.wait()
                raise

        def stop(process):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise RuntimeError("daemon shutdown timed out") from None
            if process.returncode:
                raise RuntimeError(f"daemon exit {process.returncode}")

        def await_state(job_id, expected):
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                job = ctl("job", "show", job_id)
                if job["state"] == expected:
                    return
                time.sleep(0.02)
            raise RuntimeError(f"job did not reach {expected}")

        ctl("config", "validate", "--config", str(config), offline=True)
        process = start()
        try:
            for mode in ("status", "project", "action", "workflow", "param", "job"):
                ctl(*([mode] if mode == "status" else [mode, "list"]))
            ctl("project", "use", "demo")
            if ctl("project", "current")["project"] != "demo":
                raise RuntimeError("project selection failed")
            ctl("context", "set", "mode", "review")
            workflow = ctl("workflow", "run", "validate")
            if workflow["state"] != "success":
                raise RuntimeError("validation workflow failed")
            ctl("action", "run", "project.status")
            job_id = ctl("action", "run", "task.long", "--async")["job_id"]
            ctl("job", "cancel", job_id)
            await_state(job_id, "cancelled")
            previous_ids = {job["id"] for job in ctl("job", "list")["jobs"]}
            waiter = subprocess.Popen([executable, "--socket", str(socket_path), "--json",
                                       "action", "run", "task.long"], stdout=subprocess.PIPE,
                                      stderr=subprocess.PIPE, text=True, env=environment)
            try:
                deadline = time.monotonic() + 3
                interrupted_id = None
                while time.monotonic() < deadline:
                    new_jobs = [job for job in ctl("job", "list")["jobs"] if job["id"] not in previous_ids]
                    if new_jobs:
                        interrupted_id = new_jobs[0]["id"]
                        break
                    time.sleep(0.01)
                if interrupted_id is None:
                    raise RuntimeError("CLI waiter did not admit a job")
                waiter.send_signal(signal.SIGINT)
                output, diagnostic = waiter.communicate(timeout=3)
                if waiter.returncode != 130 or json.loads(output)["job_id"] != interrupted_id:
                    raise RuntimeError(f"Ctrl-C did not cancel the admitted job: {diagnostic}")
                await_state(interrupted_id, "cancelled")
            finally:
                if waiter.poll() is None:
                    waiter.kill()
                    waiter.communicate()
            ctl("param", "set", "display.level", "60")
            connection = UnixHTTP(str(socket_path))
            try:
                call(connection, "POST", "events", {"type": "control.rotated", "source": "verification",
                                                     "payload": {"control": "dial-1", "delta": 1}})
            finally:
                connection.close()
            if ctl("param", "get", "display.level")["value"] != 65:
                raise RuntimeError("rotation failed")
            generation = ctl("status")["generation"]
            original = config.read_bytes()
            config.write_text("invalid: true\n")
            ctl("config", "reload", expected=1)
            if ctl("status")["generation"] != generation:
                raise RuntimeError("invalid reload changed generation")
            config.write_bytes(original)
            ctl("config", "reload")
            timings = []
            for _ in range(args.samples):
                started = time.perf_counter()
                ctl("context", "show")
                timings.append((time.perf_counter() - started) * 1000)
            if stat.S_IMODE(socket_path.stat().st_mode) != 0o600:
                raise RuntimeError("socket is not private")
        finally:
            stop(process)
        if socket_path.exists() or stat.S_IMODE(state_path.stat().st_mode) != 0o600:
            raise RuntimeError("socket cleanup or state permissions failed")
        process = start()
        try:
            context = ctl("context", "show")
            if context["project"] != "demo" or context["mode"] != "review":
                raise RuntimeError("context did not persist")
            if ctl("param", "get", "display.level")["value"] != 65:
                raise RuntimeError("parameter did not persist")
        finally:
            stop(process)
        print(json.dumps({"verified": True, "cli_context_process_latency": distribution(timings),
                          "launchd_template": "validated and environment exercised; not installed",
                          "service_path": agent["EnvironmentVariables"]["PATH"],
                          "signal_cancellation_exit": 130, "persisted_level": 65}, indent=2))


if __name__ == "__main__":
    main()
