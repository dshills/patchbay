"""Shared, standard-library release tooling. No installation or publication."""

import datetime
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import subprocess
import unicodedata
import zipfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
STAMP = (1980, 1, 1, 0, 0, 0)
MANIFEST = "RELEASE.json"
COMMANDS = {"deckd": "cmd/deckd", "deckctl": "cmd/deckctl",
            "deckplugincheck": "cmd/deckplugincheck", "deckplugin-example": "examples/plugin"}
PLUGIN = "local.patchbay.deckd.sdPlugin"


def arguments(parser):
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--build-time", required=True)
    parser.add_argument("--output", default="dist")
    parser.add_argument("--allow-dirty", action="store_true",
                        help="allow local testing; records source_dirty=true in the manifest")


def validate_metadata(version, commit, build_time, adapter=False):
    suffix = "" if adapter else r"(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?"
    if not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)" + suffix, version):
        raise ValueError("version must be major.minor.patch" + ("" if adapter else " with an optional suffix"))
    if not re.fullmatch(r"[0-9a-f]{7,40}", commit):
        raise ValueError("commit must be a 7–40 character lowercase Git hash")
    if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", build_time):
        raise ValueError("build time must use UTC YYYY-MM-DDTHH:MM:SSZ")
    datetime.datetime.strptime(build_time, "%Y-%m-%dT%H:%M:%SZ")


def command_output(command, root=ROOT, env=None):
    return subprocess.check_output(command, cwd=root, env=env, text=True).strip()


def build_environment(arch="arm64"):
    # Ignore per-user build flags/workspaces that could change bundled code.
    env = {**os.environ, "GOENV": "off", "GOWORK": "off", "GOFLAGS": "",
           "GOEXPERIMENT": "", "CGO_ENABLED": "0", "GOOS": "darwin"}
    env["GOARCH"] = arch
    env["GOAMD64"] = "v1"
    env["GOARM64"] = "v8.0"
    return env


def provenance(args, adapter=False, root=ROOT):
    validate_metadata(args.version, args.commit, args.build_time, adapter)
    head = command_output(["git", "rev-parse", "HEAD"], root)
    resolved = command_output(["git", "rev-parse", "--verify", args.commit + "^{commit}"], root)
    if resolved != head:
        raise ValueError("commit must identify the checked-out HEAD")
    dirty = bool(command_output(["git", "status", "--porcelain", "--untracked-files=all"], root))
    if dirty and not args.allow_dirty:
        raise ValueError("release requires clean source; use --allow-dirty only for local verification")
    env = build_environment()
    subprocess.run(["go", "mod", "verify"], cwd=root, env=env, check=True, stdout=subprocess.DEVNULL)
    return {"schema_version": 1, "version": args.version, "commit": head,
            "build_time": args.build_time, "source_dirty": dirty,
            "toolchain": {"go": command_output(["go", "version"], root, env),
                          "python": platform.python_version(), "zlib": zlib.ZLIB_RUNTIME_VERSION}}


def build(package, target, arch, metadata, root=ROOT):
    ldflags = " ".join(f"-X patchbay/internal/version.{key}={metadata[value]}" for key, value in (
        ("Version", "version"), ("Commit", "commit"), ("BuildTime", "build_time")))
    subprocess.run(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false",
                    "-ldflags", ldflags, "-o", str(target), "./" + package],
                   cwd=root, env=build_environment(arch), check=True)


def tracked_files(patterns, root=ROOT):
    """Read tracked/staged inputs, excluding ignored and untracked local debris."""
    names = subprocess.check_output(["git", "ls-files", "-z", "--", *patterns], cwd=root).decode().split("\0")
    files = {}
    for name in filter(None, names):
        if not safe_name(name):
            raise ValueError("unsafe tracked release path")
        path = root / name
        if path.is_symlink() or any(parent.is_symlink() for parent in path.parents if parent != root and root in parent.parents):
            raise ValueError("release inputs must not contain symlinks")
        files[name] = (path.read_bytes(), 0o644)
    return files


def notices(root=ROOT):
    """Include the actual pinned dependency licenses and Go runtime license."""
    env = build_environment()
    modules = []
    files = {}
    module_paths = []
    for arch in ("arm64", "amd64"):
        module_paths.extend(command_output([
            "go", "list", "-mod=readonly", "-deps", "-f",
            "{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}{{end}}{{end}}",
            "./cmd/...", "./examples/..."], root, build_environment(arch)).splitlines())
    for module in sorted(set(filter(None, module_paths))):
        info = json.loads(command_output(["go", "list", "-mod=readonly", "-m", "-json", module], root, env))
        if info.get("Replace"):
            raise ValueError("release dependency replacements are unsupported")
        module_info = {"path": module, "version": info["Version"], "sum": info["Sum"], "notices": []}
        modules.append(module_info)
        base = Path(info["Dir"])
        for path in sorted(base.iterdir()):
            if is_license_name(path.name) or re.fullmatch(r"(?:notice|patents|copyright)(?:[._-].+)?", path.name.lower()):
                if path.is_symlink() or not path.is_file():
                    raise ValueError("dependency notices must be regular files")
                relative = f"licenses/{module}@{info['Version']}/{path.name}"
                files[relative] = (path.read_bytes(), 0o644)
                module_info["notices"].append(relative)
        if not any(is_license_name(name.rsplit("/", 1)[-1]) for name in module_info["notices"]):
            raise ValueError(f"no supported license notice found for {module}")
    goroot = Path(command_output(["go", "env", "GOROOT"], root, env))
    files.update(go_notices(goroot))
    summary = "Third-party notices\n\n" + "\n".join(f"{m['path']} {m['version']} {m['sum']}" for m in modules)
    summary += "\n\nLicense and notice texts are under licenses/. Go's runtime license is included.\n"
    files["THIRD_PARTY_NOTICES.txt"] = (summary.encode(), 0o644)
    return files, modules


def is_license_name(name):
    return re.fullmatch(r"(?:licen[cs]e|copying)(?:[._-].+)?", name.lower()) is not None


def go_notices(goroot):
    license_path = goroot / "LICENSE"
    # Homebrew installs the license beside libexec rather than inside GOROOT.
    if not license_path.is_file() and goroot.name == "libexec":
        license_path = goroot.parent / "LICENSE"
    files = {"licenses/Go-LICENSE": (license_path.read_bytes(), 0o644)}
    if (goroot / "PATENTS").is_file():
        files["licenses/Go-PATENTS"] = ((goroot / "PATENTS").read_bytes(), 0o644)
    vendor = goroot / "src/vendor"
    for path in sorted(vendor.rglob("*")):
        if path.is_file() and path.name in ("LICENSE", "PATENTS", "NOTICE"):
            files["licenses/go-vendor/" + path.relative_to(vendor).as_posix()] = (path.read_bytes(), 0o644)
    return files


def safe_name(name):
    if (not isinstance(name, str) or not name or "\\" in name or
            any(ord(char) < 32 or ord(char) == 127 for char in name)):
        return False
    path = PurePosixPath(name)
    return (bool(path.parts) and not path.is_absolute() and str(path) == name and
            all(part not in (".", "..") and ":" not in part for part in path.parts))


def path_key(name):
    # macOS commonly uses case-insensitive filesystems and decomposed Unicode.
    return unicodedata.normalize("NFD", name).casefold()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def add_manifest(files, metadata, product, arch, modules):
    prefix = PLUGIN + "/" if product == "decksd" else ""
    manifest = {**metadata, "product": product, "target": {"os": "darwin", "arch": arch},
                "modules": modules, "files": {name: {"size": len(data), "sha256": digest(data),
                                                     "mode": mode}
                                             for name, (data, mode) in sorted(files.items())}}
    files[prefix + MANIFEST] = ((json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode(), 0o644)


def write_archive(path, files):
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for name, (data, mode) in sorted(files.items()):
            if not safe_name(name) or mode not in (0o644, 0o755):
                raise ValueError("invalid release entry")
            entry = zipfile.ZipInfo(name, date_time=STAMP)
            entry.create_system = 3
            entry.external_attr = (0o100000 | mode) << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(entry, data, compresslevel=9)


def publish_local(staging, destination, names, checksum_name):
    """Build failures leave old output untouched. Publish the checksum file last.

    Each replace is atomic on this filesystem; the artifact set is not a single
    atomic transaction. A failed/interrupted replacement is detected by checksums.
    """
    hashes = "".join(f"{digest((staging / name).read_bytes())}  {name}\n" for name in names)
    (staging / checksum_name).write_text(hashes)
    for name in [*names, checksum_name]:
        if (destination / name).is_symlink():
            raise ValueError("release output must not be a symlink")
    for name in [*names, checksum_name]:
        os.replace(staging / name, destination / name)
    print(hashes, end="")
