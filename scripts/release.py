#!/usr/bin/env python3
"""Build and verify deterministic macOS ZIP bundles. No install or publish."""

import argparse
from pathlib import Path
import subprocess
import tempfile

from release_common import (ROOT, COMMANDS, arguments, provenance, notices, build,
                            add_manifest, write_archive, publish_local, tracked_files)
from verify_release import verify_archive


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    arguments(parser)
    args = parser.parse_args()
    try:
        metadata = provenance(args)
        files, modules = notices()
        # Only tracked/staged release inputs are shipped, including local dirty builds.
        files.update(tracked_files(["LICENSE", "README.md", "configs", "specs", "scripts"], root=ROOT))
        destination = Path(args.output).resolve()
        destination.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=".deckd-release-", dir=destination) as temporary:
            staging = Path(temporary)
            bundles = []
            for arch in ("arm64", "amd64"):
                contents = dict(files)
                for command, package in COMMANDS.items():
                    binary = staging / command
                    build(package, binary, arch, metadata)
                    contents[f"bin/{command}"] = (binary.read_bytes(), 0o755)
                add_manifest(contents, metadata, "deckd", arch, modules)
                name = f"deckd-{args.version}-darwin-{arch}.zip"
                write_archive(staging / name, contents)
                verify_archive(staging / name, allow_dirty=args.allow_dirty)
                bundles.append(name)
            publish_local(staging, destination, bundles, "SHA256SUMS")
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"release: {error}\n")


if __name__ == "__main__":
    main()
