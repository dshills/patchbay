#!/usr/bin/env python3
"""Build deterministic macOS ZIP bundles. Does not install, sign, or publish."""

import argparse
import hashlib
import os
from pathlib import Path
import re
import subprocess
import tempfile
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--build-time", required=True)
    parser.add_argument("--output", default="dist")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?", args.version):
        parser.error("version must be a numeric release version, optionally with a suffix")
    if not re.fullmatch(r"[0-9a-f]{7,40}", args.commit):
        parser.error("commit must be a 7–40 character lowercase Git hash")
    if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", args.build_time):
        parser.error("build time must use UTC YYYY-MM-DDTHH:MM:SSZ format")
    root = Path(__file__).resolve().parent.parent
    destination = Path(args.output).resolve()
    destination.mkdir(parents=True, exist_ok=True)
    ldflags = " ".join(f"-X patchbay/internal/version.{key}={value}" for key, value in (
        ("Version", args.version), ("Commit", args.commit), ("BuildTime", args.build_time)
    ))
    hashes = []
    with tempfile.TemporaryDirectory(prefix="deckd-release-") as staging:
        for arch in ("arm64", "amd64"):
            environment = {**os.environ, "GOOS": "darwin", "GOARCH": arch, "CGO_ENABLED": "0"}
            files = {}
            for command in ("deckd", "deckctl"):
                binary = Path(staging) / command
                subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags,
                                "-o", str(binary), f"./cmd/{command}"], cwd=root, env=environment, check=True)
                files[f"bin/{command}"] = (binary.read_bytes(), 0o755)
            for pattern in ("README.md", "configs/*", "specs/*.md", "specs/reviews/*.md", "scripts/*.py"):
                for path in root.glob(pattern):
                    if path.is_file():
                        files[path.relative_to(root).as_posix()] = (path.read_bytes(), 0o644)
            bundle = destination / f"deckd-{args.version}-darwin-{arch}.zip"
            with zipfile.ZipFile(bundle, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
                for name, (data, mode) in sorted(files.items()):
                    entry = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
                    entry.create_system = 3
                    entry.external_attr = (0o100000 | mode) << 16
                    entry.compress_type = zipfile.ZIP_DEFLATED
                    archive.writestr(entry, data, compresslevel=9)
            hashes.append(f"{hashlib.sha256(bundle.read_bytes()).hexdigest()}  {bundle.name}")
    (destination / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    print("\n".join(hashes))


if __name__ == "__main__":
    main()
