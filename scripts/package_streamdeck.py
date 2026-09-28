#!/usr/bin/env python3
"""Build a universal native Stream Deck plugin without installing it."""

import argparse
import json
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import zlib

from release_common import (ROOT, PLUGIN, arguments, provenance, notices, build,
                            add_manifest, write_archive, publish_local, tracked_files)
from verify_release import decode, verify_archive


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
    arguments(parser)
    args = parser.parse_args()
    try:
        metadata = provenance(args, adapter=True)
        output = Path(args.output).resolve()
        output.mkdir(parents=True, exist_ok=True)
        destination = output / PLUGIN
        if destination.is_symlink() or any(p.is_symlink() for p in destination.rglob("*")):
            raise ValueError("plugin output must not contain symlinks")
        with tempfile.TemporaryDirectory(prefix=".decksd-package-", dir=output) as temporary:
            temporary = Path(temporary)
            plugin = temporary / PLUGIN
            source = Path("adapters/streamdeck/plugin")
            plugin.mkdir()
            for name, (data, _) in tracked_files([str(source), "LICENSE"], root=ROOT).items():
                relative = Path("LICENSE") if name == "LICENSE" else Path(name).relative_to(source)
                path = plugin / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            (plugin / "bin").mkdir()
            thin = []
            for arch in ("arm64", "amd64"):
                binary = temporary / f"decksd-{arch}"
                build("cmd/decksd", binary, arch, metadata)
                thin.append(str(binary))
            subprocess.run(["lipo", "-create", *thin, "-output", str(plugin / "bin/decksd")], check=True)
            manifest = decode((plugin / "manifest.json").read_bytes())
            manifest["Version"] = args.version + ".0"
            (plugin / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
            icons(plugin / "assets")
            shutil.copyfile(ROOT / "specs/STREAMDECK.md", plugin / "README.md")
            shutil.copyfile(ROOT / "configs/streamdeck.yaml", plugin / "example.yaml")
            files, modules = notices()
            for path in plugin.rglob("*"):
                if path.is_file():
                    name = path.relative_to(plugin).as_posix()
                    files[name] = (path.read_bytes(), 0o755 if name == "bin/decksd" else 0o644)
            files = {PLUGIN + "/" + name: value for name, value in files.items()}
            add_manifest(files, metadata, "decksd", "universal", modules)
            name = f"decksd-{args.version}-macos.zip"
            write_archive(temporary / name, files)
            verify_archive(temporary / name, allow_dirty=args.allow_dirty)
            # Materialize exactly the verified files in the inspectable directory.
            for relative, (data, mode) in files.items():
                path = temporary / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
                path.chmod(mode)
            publish_local(temporary, output, [name], "DECKSD-SHA256SUMS")
            # The ZIP + checksum are authoritative; this directory is a convenience.
            if destination.exists():
                shutil.rmtree(destination)
            shutil.copytree(plugin, destination)
        print(f"Plugin directory: {destination}")
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"streamdeck-package: {error}\n")


if __name__ == "__main__":
    main()
