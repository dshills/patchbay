#!/usr/bin/env python3
"""Verify release integrity; --smoke explicitly executes a trusted native bundle."""

import argparse
import json
from pathlib import Path
import platform
import re
import stat
import struct
import subprocess
import sys
import tempfile
import zipfile

from release_common import (COMMANDS, MANIFEST, PLUGIN, STAMP, digest, safe_name,
                            path_key, is_license_name, validate_metadata)

MAX_FILE = 128 * 1024 * 1024
MAX_TOTAL = 256 * 1024 * 1024
MAX_MANIFEST = 1024 * 1024
CPU = {"arm64": 0x0100000C, "amd64": 0x01000007}
CONFIGS = ("example", "quickstart", "streamdeck", "developer", "bench", "plugins")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def object_pairs(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field")
        result[key] = value
    return result


def decode(data):
    return json.loads(data.decode("utf-8"), object_pairs_hook=object_pairs)


def architectures(data):
    if len(data) >= 32 and data[:4] == b"\xcf\xfa\xed\xfe":
        cpu = struct.unpack_from("<I", data, 4)[0]
        return {name for name, value in CPU.items() if value == cpu}
    if len(data) >= 48 and data[:4] == b"\xca\xfe\xba\xbe":
        require(struct.unpack_from(">I", data, 4)[0] == 2, "expected two universal slices")
        found = set()
        ranges = []
        for index in range(2):
            cpu, _, offset, size, alignment = struct.unpack_from(">IIIII", data, 8 + index * 20)
            require(offset >= 48 and size >= 32 and offset + size <= len(data) and
                    alignment <= 30 and offset % (1 << alignment) == 0, "invalid universal slice")
            require(all(offset + size <= start or offset >= end for start, end in ranges),
                    "overlapping universal slices")
            ranges.append((offset, offset + size))
            names = architectures(data[offset:offset + size]) if data[offset:offset + 4] == b"\xcf\xfa\xed\xfe" else set()
            require(len(names) == 1 and CPU[next(iter(names))] == cpu and not found.intersection(names),
                    "universal slice CPU mismatch")
            found.update(names)
        return found
    return set()


def verify_archive(path, allow_dirty=False):
    """Return verified metadata and bytes; extraction never reopens the archive."""
    require(path.stat().st_size <= MAX_TOTAL, "archive exceeds size limit")
    with zipfile.ZipFile(path) as archive:
        entries = archive.infolist()
        require(0 < len(entries) <= 1024, "invalid archive entry count")
        names = [entry.filename for entry in entries]
        normalized_paths = {path_key(name) for name in names}
        require(len(normalized_paths) == len(names), "duplicate archive path")
        require(names == sorted(names), "archive entries must be sorted")
        require(sum(entry.file_size for entry in entries) <= MAX_TOTAL, "expanded archive exceeds size limit")
        paths = set(names)
        for entry in entries:
            require(safe_name(entry.filename), "unsafe archive path")
            require(not any(path_key(str(parent)) in normalized_paths for parent in Path(entry.filename).parents),
                    "archive file/directory collision")
            require(0 <= entry.file_size <= MAX_FILE, "entry exceeds size limit")
            require(entry.create_system == 3 and entry.date_time == STAMP and not entry.flag_bits & 1,
                    "invalid archive metadata")
            mode = entry.external_attr >> 16
            require(stat.S_ISREG(mode) and stat.S_IMODE(mode) in (0o644, 0o755), "invalid entry permissions/type")
        manifests = [name for name in (MANIFEST, PLUGIN + "/" + MANIFEST) if name in paths]
        require(len(manifests) == 1, "expected exactly one release manifest")
        manifest_name = manifests[0]
        require(archive.getinfo(manifest_name).file_size <= MAX_MANIFEST, "manifest exceeds size limit")
        metadata = decode(archive.read(manifest_name))
        require(isinstance(metadata, dict), "manifest must be an object")
        require(set(metadata) == {"schema_version", "product", "version", "commit", "build_time",
                                  "source_dirty", "target", "toolchain", "modules", "files"}, "invalid manifest fields")
        require(type(metadata["schema_version"]) is int and metadata["schema_version"] == 1, "unsupported manifest version")
        product = metadata["product"]
        require(product in ("deckd", "decksd"), "unknown product")
        prefix = PLUGIN + "/" if product == "decksd" else ""
        require(manifest_name == prefix + MANIFEST, "manifest location mismatch")
        require(all(isinstance(metadata[key], str) for key in ("version", "commit", "build_time")), "invalid build metadata")
        validate_metadata(metadata["version"], metadata["commit"], metadata["build_time"], product == "decksd")
        require(len(metadata["commit"]) == 40, "manifest must record full commit")
        require(type(metadata["source_dirty"]) is bool, "invalid source state")
        require(allow_dirty or not metadata["source_dirty"], "dirty build requires --allow-dirty")
        target = metadata["target"]
        require(isinstance(target, dict) and set(target) == {"os", "arch"} and target["os"] == "darwin", "invalid target")
        arch = target["arch"]
        require(arch in (("universal",) if product == "decksd" else ("arm64", "amd64")), "invalid target architecture")
        toolchain = metadata["toolchain"]
        require(isinstance(toolchain, dict) and set(toolchain) == {"go", "python", "zlib"} and
                all(isinstance(value, str) and value for value in toolchain.values()), "invalid toolchain metadata")
        records = metadata["files"]
        require(isinstance(records, dict) and set(records) == paths - {manifest_name}, "manifest inventory mismatch")
        modules = metadata["modules"]
        require(isinstance(modules, list) and len(modules) <= 128, "invalid module inventory")
        module_paths = set()
        required = {prefix + "LICENSE", prefix + "THIRD_PARTY_NOTICES.txt", prefix + "licenses/Go-LICENSE"}
        for module in modules:
            require(isinstance(module, dict) and set(module) == {"path", "version", "sum", "notices"} and
                    all(isinstance(module[key], str) and module[key] for key in ("path", "version", "sum")),
                    "invalid dependency metadata")
            require(safe_name(module["path"]) and module["path"] not in module_paths and
                    re.fullmatch(r"v[0-9A-Za-z.+-]+", module["version"]), "invalid or duplicate dependency")
            module_paths.add(module["path"])
            base = f"licenses/{module['path']}@{module['version']}/"
            notice_paths = module["notices"]
            require(isinstance(notice_paths, list) and 1 <= len(notice_paths) <= 16 and
                    all(isinstance(name, str) and safe_name(name) and name.rsplit("/", 1)[0] + "/" == base for name in notice_paths),
                    "invalid dependency notices")
            require(any(is_license_name(name.rsplit("/", 1)[-1]) for name in notice_paths) and
                    len(set(notice_paths)) == len(notice_paths), "missing or duplicate license notice")
            required.update(prefix + name for name in notice_paths)
        binaries = {prefix + "bin/decksd"} if product == "decksd" else {"bin/" + name for name in COMMANDS}
        required.update(binaries)
        if product == "deckd":
            required.update(f"configs/{name}.yaml" for name in CONFIGS)
            required.update({"README.md", "configs/local.patchbay.deckd.plist", "specs/OPERATIONS.md",
                             "scripts/verify_v1.py", "scripts/benchmark_runtime.py", "scripts/verify_release.py",
                             "scripts/release_common.py"})
        else:
            required.update(prefix + name for name in ("manifest.json", "layout.json", "inspector.html",
                                                       "inspector.js", "README.md", "example.yaml"))
            required.update(prefix + f"assets/{name}{scale}.png" for name in ("plugin", "action", "category", "key") for scale in ("", "@2x"))
            require(all(name.startswith(prefix) for name in names), "unexpected plugin root")
        require(required <= paths, "required release file missing")
        files = {}
        for entry in entries:
            name = entry.filename
            data = archive.read(entry)
            mode = stat.S_IMODE(entry.external_attr >> 16)
            require(mode == (0o755 if name in binaries else 0o644), "unexpected executable mode")
            if name != manifest_name:
                record = records[name]
                require(isinstance(record, dict) and set(record) == {"size", "sha256", "mode"}, "invalid file record")
                require(type(record["size"]) is int and type(record["mode"]) is int and
                        record == {"size": len(data), "sha256": digest(data), "mode": mode}, "file integrity mismatch: " + name)
            if name in binaries:
                expected = {"arm64", "amd64"} if arch == "universal" else {arch}
                require(architectures(data) == expected, "binary architecture mismatch: " + name)
            files[name] = (data, mode)
        if product == "decksd":
            vendor = decode(files[prefix + "manifest.json"][0])
            require(isinstance(vendor, dict) and vendor.get("Version") == metadata["version"] + ".0" and
                    vendor.get("CodePath") == "bin/decksd" and vendor.get("UUID") == "local.patchbay.deckd", "plugin manifest mismatch")
        return metadata, files


def check_checksum(path, product):
    sidecar = path.parent / ("SHA256SUMS" if product == "deckd" else "DECKSD-SHA256SUMS")
    require(sidecar.stat().st_size <= 16384, "checksum file exceeds limit")
    matches = []
    names = set()
    for line in sidecar.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([^/\\]+)", line)
        require(match is not None, "invalid checksum record")
        checksum, name = match.groups()
        require(name not in names, "duplicate checksum record")
        names.add(name)
        if name == path.name:
            matches.append(checksum)
    require(matches == [digest(path.read_bytes())], "archive checksum mismatch or missing record")


def extract(files, destination):
    # destination is a fresh private temporary directory owned by this process.
    for name, (data, mode) in files.items():
        path = destination / name
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("xb") as output:
            output.write(data)
        path.chmod(mode)


def smoke(metadata, files):
    require(platform.system() == "Darwin", "native smoke requires macOS")
    native = {"arm64": "arm64", "x86_64": "amd64"}.get(platform.machine())
    require(metadata["target"]["arch"] in (native, "universal"), "bundle is not native to this host")
    with tempfile.TemporaryDirectory(prefix="pb-release-", dir="/tmp") as temporary:
        root = Path(temporary)
        extract(files, root)
        expected = {key: metadata[key] for key in ("version", "commit", "build_time")}
        if metadata["product"] == "decksd":
            output = subprocess.check_output([str(root / PLUGIN / "bin/decksd"), "--version"], timeout=10, text=True)
            require(all(value in output for value in expected.values()), "adapter version mismatch")
            return
        for name in ("deckd", "deckctl", "deckplugincheck"):
            output = subprocess.check_output([str(root / "bin" / name), "--version", "--json"], timeout=10)
            require(decode(output) == expected, "binary version mismatch: " + name)
        for name in CONFIGS:
            subprocess.run([str(root / "bin/deckctl"), "config", "validate", "--json",
                            "--config", str(root / f"configs/{name}.yaml")], check=True, timeout=10)
        subprocess.run([sys.executable, str(Path(__file__).with_name("verify_v1.py")),
                        "--bundle", str(root), "--samples", "5"], check=True, timeout=90)
        subprocess.run([str(root / "bin/deckplugincheck"), "--config", str(root / "configs/plugins.yaml")],
                       cwd=root, check=True, timeout=15)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("--allow-dirty", action="store_true")
    parser.add_argument("--smoke", action="store_true", help="execute the trusted bundle on native macOS")
    args = parser.parse_args()
    try:
        metadata, files = verify_archive(args.archive, args.allow_dirty)
        check_checksum(args.archive, metadata["product"])
        if args.smoke:
            smoke(metadata, files)
        print(json.dumps({"verified": True, "product": metadata["product"], "target": metadata["target"],
                          "commit": metadata["commit"], "source_dirty": metadata["source_dirty"], "smoke": args.smoke}))
    except (ValueError, OSError, zipfile.BadZipFile, subprocess.SubprocessError) as error:
        parser.exit(1, f"verify-release: {error}\n")


if __name__ == "__main__":
    main()
