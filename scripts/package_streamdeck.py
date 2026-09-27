#!/usr/bin/env python3
"""Build a universal native Stream Deck plugin without installing it."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tempfile
import zipfile
import zlib


def icons(directory):
    """Draw a small, deterministic control-grid icon; no external image tools."""
    directory.mkdir(exist_ok=True)
    for name, size, monochrome in (("plugin", 256, False), ("plugin@2x", 512, False),
                                    ("action", 20, True), ("action@2x", 40, True),
                                    ("category", 28, True), ("category@2x", 56, True),
                                    ("key", 72, False), ("key@2x", 144, False)):
        rows = bytearray()
        for y in range(size):
            rows.append(0)
            for x in range(size):
                px, py = x / size, y / size
                key = any(left <= px <= left + .22 and top <= py <= top + .22
                          for left in (.18, .6) for top in (.18, .6))
                if key:
                    rgba = (255, 255, 255, 255) if monochrome else (82, 200, 152, 255)
                else:
                    rgba = (0, 0, 0, 0) if monochrome else (17, 25, 35, 255)
                rows.extend(rgba)

        def chunk(kind, data):
            return struct.pack("!I", len(data)) + kind + data + struct.pack("!I", zlib.crc32(kind + data))

        png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack("!IIBBBBB", size, size, 8, 6, 0, 0, 0))
               + chunk(b"IDAT", zlib.compress(rows, 9)) + chunk(b"IEND", b""))
        (directory / f"{name}.png").write_bytes(png)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--build-time", required=True)
    parser.add_argument("--output", default="dist")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must be major.minor.patch")
    if not re.fullmatch(r"[0-9a-f]{7,40}", args.commit):
        parser.error("commit must be a Git hash")
    if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", args.build_time):
        parser.error("build time must use UTC YYYY-MM-DDTHH:MM:SSZ")
    root = Path(__file__).resolve().parent.parent
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    name = "local.patchbay.deckd.sdPlugin"
    destination = output / name
    if destination.is_symlink() or any(p.is_symlink() for p in destination.rglob("*")):
        parser.error("plugin output must not contain symlinks")
    ldflags = " ".join(f"-X patchbay/internal/version.{key}={value}" for key, value in (
        ("Version", args.version), ("Commit", args.commit), ("BuildTime", args.build_time)))
    with tempfile.TemporaryDirectory(prefix="decksd-package-") as temporary:
        temporary = Path(temporary)
        plugin = temporary / name
        shutil.copytree(root / "adapters/streamdeck/plugin", plugin)
        (plugin / "bin").mkdir()
        thin = []
        for arch in ("arm64", "amd64"):
            binary = temporary / f"decksd-{arch}"
            env = {**os.environ, "GOOS": "darwin", "GOARCH": arch, "CGO_ENABLED": "0"}
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags,
                            "-o", str(binary), "./cmd/decksd"], cwd=root, env=env, check=True)
            thin.append(str(binary))
        subprocess.run(["lipo", "-create", *thin, "-output", str(plugin / "bin/decksd")], check=True)
        (plugin / "bin/decksd").chmod(0o755)
        manifest = json.loads((plugin / "manifest.json").read_text())
        manifest["Version"] = args.version + ".0"
        (plugin / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        icons(plugin / "assets")
        shutil.copyfile(root / "specs/STREAMDECK.md", plugin / "README.md")
        shutil.copyfile(root / "configs/streamdeck.yaml", plugin / "example.yaml")
        for path in plugin.rglob("*"):
            if path.is_file():
                path.chmod(0o755 if path == plugin / "bin/decksd" else 0o644)
        archive_path = output / f"decksd-{args.version}-macos.zip"
        with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for path in sorted(plugin.rglob("*")):
                if not path.is_file():
                    continue
                entry = zipfile.ZipInfo(path.relative_to(temporary).as_posix(), (1980, 1, 1, 0, 0, 0))
                entry.create_system = 3
                mode = 0o755 if path == plugin / "bin/decksd" else 0o644
                entry.external_attr = (0o100000 | mode) << 16
                entry.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(entry, path.read_bytes(), compresslevel=9)
        # Replace only this generated artifact after a successful build. A fresh
        # directory removes obsolete assets and handles prior read-only copies.
        if destination.exists():
            shutil.rmtree(destination)
        shutil.copytree(plugin, destination)
    checksum = hashlib.sha256(archive_path.read_bytes()).hexdigest()
    (output / "DECKSD-SHA256SUMS").write_text(f"{checksum}  {archive_path.name}\n")
    print(f"{checksum}  {archive_path.name}")
    print(f"Plugin directory: {destination}")


if __name__ == "__main__":
    main()
