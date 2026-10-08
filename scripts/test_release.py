"""Release artifact regression tests; no compiler, network or hardware required."""

import argparse
import contextlib
import io
import json
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest import mock
import warnings
import zipfile

import release
import release_common as common
import verify_release as verify
import package_streamdeck as streamdeck


METADATA = {"schema_version": 1, "version": "0.3.0", "commit": "a" * 40,
            "build_time": "2026-09-28T00:00:00Z", "source_dirty": False,
            "toolchain": {"go": "go1.27.1", "python": "3.14.7", "zlib": "1.2.13"}}
MODULES = [{"path": "go.yaml.in/yaml/v3", "version": "v3.0.5", "sum": "h1:test"},
           {"path": "github.com/gorilla/websocket", "version": "v1.5.3", "sum": "h1:test"}]
for module in MODULES:
    module["notices"] = [f"licenses/{module['path']}@{module['version']}/LICENSE"]
MODULES[0]["notices"].append("licenses/go.yaml.in/yaml/v3@v3.0.5/NOTICE")


def thin(arch):
    return struct.pack("<IIIIIIII", 0xFEEDFACF, verify.CPU[arch], 0, 2, 0, 0, 0, 0)


def universal():
    header = struct.pack(">II", 0xCAFEBABE, 2)
    header += struct.pack(">IIIII", verify.CPU["arm64"], 0, 48, 32, 0)
    header += struct.pack(">IIIII", verify.CPU["amd64"], 0, 80, 32, 0)
    return header + thin("arm64") + thin("amd64")


def fixture():
    files = {"bin/" + name: (thin("arm64"), 0o755) for name in common.COMMANDS}
    names = ["LICENSE", "README.md", "THIRD_PARTY_NOTICES.txt", "licenses/Go-LICENSE",
             "share/workbench/index.html", "share/workbench/app.js", "share/workbench/style.css", "share/workbench/recipes.js", "share/workbench/agents.js", "scripts/verify_agents.py", "specs/AGENT_CONTROL.md", "scripts/verify_recipes.py", "specs/RECIPES.md", "scripts/verify_workbench.py", "specs/DECK_SETUP.md",
             "configs/local.patchbay.deckd.plist", "specs/OPERATIONS.md", "specs/WORKBENCH.md", "specs/EVIDENCE.md",
             "scripts/verify_v1.py", "scripts/benchmark_runtime.py", "scripts/verify_release.py",
             "scripts/release_common.py"]
    names += [f"configs/{name}.yaml" for name in verify.CONFIGS]
    names += [f"recipes/{name}/{file}" for name in verify.RECIPES for file in ("recipe.yaml", "LICENSE", "README.md")]
    names += [f"licenses/{m['path']}@{m['version']}/LICENSE" for m in MODULES]
    names += ["licenses/go.yaml.in/yaml/v3@v3.0.5/NOTICE"]
    files.update({name: (b"example\n", 0o644) for name in names})
    files["Patchbay.command"] = (b"#!/bin/sh\nexec ./bin/deckctl demo\n", 0o755)
    return files


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.path = self.root / "deckd-0.3.0-darwin-arm64.zip"

    def write(self, files=None, metadata=None):
        files = fixture() if files is None else dict(files)
        common.add_manifest(files, metadata or METADATA, "deckd", "arm64", MODULES)
        common.write_archive(self.path, files)
        return files

    def rewrite(self, transform):
        with zipfile.ZipFile(self.path) as archive:
            entries = [(info, archive.read(info)) for info in archive.infolist()]
        with zipfile.ZipFile(self.path, "w") as archive:
            for info, data in entries:
                info, data = transform(info, data)
                archive.writestr(info, data)

    def test_deterministic_roundtrip_permissions_and_checksum(self):
        self.write()
        before = self.path.read_bytes()
        self.write()
        self.assertEqual(before, self.path.read_bytes())
        metadata, files = verify.verify_archive(self.path)
        self.assertEqual(metadata["commit"], METADATA["commit"])
        destination = self.root / "unpacked"
        destination.mkdir()
        verify.extract(files, destination)
        self.assertEqual((destination / "bin/deckctl").stat().st_mode & 0o777, 0o755)
        (self.root / "SHA256SUMS").write_text(f"{common.digest(before)}  {self.path.name}\n")
        verify.check_checksum(self.path, "deckd")
        with self.path.open("ab") as output:
            output.write(b"tampered")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            verify.check_checksum(self.path, "deckd")

    def test_dirty_build_requires_explicit_opt_in(self):
        self.write(metadata={**METADATA, "source_dirty": True})
        with self.assertRaisesRegex(ValueError, "dirty build"):
            verify.verify_archive(self.path)
        verify.verify_archive(self.path, allow_dirty=True)

    def test_required_files_and_architecture(self):
        for name in ("bin/decksd", "specs/DECK_SETUP.md", "recipes/project-checkup/recipe.yaml", "recipes/rigol-capture/LICENSE", "share/workbench/recipes.js", "share/workbench/agents.js", "scripts/verify_agents.py", "specs/AGENT_CONTROL.md", "scripts/verify_recipes.py", "bin/deckplugin-example", "configs/bench.yaml", "bin/deckdemo", "configs/benchmark.yaml", "Patchbay.command", "LICENSE", "licenses/Go-LICENSE"):
            with self.subTest(name=name):
                files = fixture()
                del files[name]
                self.write(files)
                with self.assertRaisesRegex(ValueError, "required release file"):
                    verify.verify_archive(self.path)
        files = fixture()
        files["bin/deckctl"] = (thin("amd64"), 0o755)
        self.write(files)
        with self.assertRaisesRegex(ValueError, "architecture mismatch"):
            verify.verify_archive(self.path)

    def test_module_inventory_can_grow_and_requires_each_notice(self):
        files = fixture()
        module = {"path": "example.com/third", "version": "v1.0.0", "sum": "h1:test",
                  "notices": ["licenses/example.com/third@v1.0.0/LICENSE"]}
        files[module["notices"][0]] = (b"third license", 0o644)
        common.add_manifest(files, METADATA, "deckd", "arm64", MODULES + [module])
        common.write_archive(self.path, files)
        verify.verify_archive(self.path)
        del files[common.MANIFEST]
        del files[module["notices"][0]]
        common.add_manifest(files, METADATA, "deckd", "arm64", MODULES + [module])
        common.write_archive(self.path, files)
        with self.assertRaisesRegex(ValueError, "required release file"):
            verify.verify_archive(self.path)

    def test_tampering_inventory_and_modes(self):
        self.write()
        self.rewrite(lambda info, data: (info, b"bad") if info.filename == "README.md" else (info, data))
        with self.assertRaisesRegex(ValueError, "integrity mismatch"):
            verify.verify_archive(self.path)
        files = fixture()
        files["README.md"] = (b"example", 0o755)
        self.write(files)
        with self.assertRaisesRegex(ValueError, "executable mode"):
            verify.verify_archive(self.path)
        self.write()
        def change_inventory(info, data):
            if info.filename == common.MANIFEST:
                manifest = json.loads(data)
                del manifest["files"]["README.md"]
                data = json.dumps(manifest).encode()
            return info, data
        self.rewrite(change_inventory)
        with self.assertRaisesRegex(ValueError, "inventory mismatch"):
            verify.verify_archive(self.path)

    def test_unsafe_paths_duplicates_and_symlinks(self):
        for name in (".", "../escape", "/absolute", "a/../../escape", "a\\escape", "a/./escape", "a//escape", "C:escape", "a\nb"):
            with self.subTest(name=name):
                self.assertFalse(common.safe_name(name))
                self.write()
                def rename(info, data):
                    if info.filename == "README.md":
                        info.filename = name
                    return info, data
                self.rewrite(rename)
                with self.assertRaises(ValueError):
                    verify.verify_archive(self.path)
        self.write()
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", UserWarning)
            with zipfile.ZipFile(self.path, "a") as archive:
                archive.writestr("README.md", b"duplicate")
        with self.assertRaisesRegex(ValueError, "duplicate archive path"):
            verify.verify_archive(self.path)
        self.write()
        def symlink(info, data):
            if info.filename == "README.md":
                info.external_attr = 0o120777 << 16
            return info, data
        self.rewrite(symlink)
        with self.assertRaisesRegex(ValueError, "permissions/type"):
            verify.verify_archive(self.path)

    def test_case_and_parent_collisions(self):
        for name in ("readme.md", "README.md/child", "readme.md/child"):
            files = fixture()
            files[name] = (b"data", 0o644)
            self.write(files)
            with self.assertRaises(ValueError):
                verify.verify_archive(self.path)
        files = fixture()
        files["\u00e9"] = (b"one", 0o644)
        files["e\u0301"] = (b"two", 0o644)
        self.write(files)
        with self.assertRaisesRegex(ValueError, "duplicate archive path"):
            verify.verify_archive(self.path)

    def test_bounds_and_duplicate_json(self):
        self.write()
        for field, limit in (("MAX_MANIFEST", 1), ("MAX_FILE", 1), ("MAX_TOTAL", 1)):
            with mock.patch.object(verify, field, limit), self.assertRaisesRegex(ValueError, "(limit|size)"):
                verify.verify_archive(self.path)
        def duplicate(info, data):
            if info.filename == common.MANIFEST:
                data = data.replace(b'"schema_version": 1', b'"schema_version": 1, "schema_version": 1')
            return info, data
        self.rewrite(duplicate)
        with self.assertRaisesRegex(ValueError, "duplicate JSON"):
            verify.verify_archive(self.path)

    def test_universal_cpu_and_offset_checks(self):
        binary = universal()
        self.assertEqual(verify.architectures(binary), {"arm64", "amd64"})
        invalid = bytearray(binary)
        struct.pack_into(">I", invalid, 36, 48)  # overlapping second slice
        with self.assertRaisesRegex(ValueError, "overlapping"):
            verify.architectures(invalid)

    def test_metadata_rejects_impossible_dates_and_unsafe_versions(self):
        for value in ("2026-02-30T00:00:00Z", "2026-09-28T25:00:00Z", "yesterday"):
            with self.assertRaises(ValueError):
                common.validate_metadata("0.3.0", "a" * 40, value)
        for value in ("../x", "01.2.3", "1.2.3-", "1.2.3;echo"):
            with self.assertRaises(ValueError):
                common.validate_metadata(value, "a" * 40, METADATA["build_time"])
        common.validate_metadata("0.3.0-rc.1", "a" * 40, METADATA["build_time"])

    def test_standard_and_homebrew_go_license_layouts(self):
        for goroot in (self.root / "standard", self.root / "homebrew/libexec"):
            goroot.mkdir(parents=True)
            license_path = goroot.parent / "LICENSE" if goroot.name == "libexec" else goroot / "LICENSE"
            license_path.write_bytes(b"Go license")
            (goroot / "PATENTS").write_bytes(b"patents")
            self.assertEqual(common.go_notices(goroot)["licenses/Go-LICENSE"][0], b"Go license")
            self.assertEqual(common.go_notices(goroot)["licenses/Go-PATENTS"][0], b"patents")

    def test_notices_discover_dependencies_for_both_target_architectures(self):
        (self.root / "LICENSE").write_bytes(b"license")
        module_dir = self.root / "module"
        module_dir.mkdir()
        (module_dir / "COPYING.md").write_bytes(b"module license")
        (module_dir / "NOTICE.txt").write_bytes(b"module notice")
        seen = []
        def output(command, root, env):
            if "-deps" in command:
                seen.append(env["GOARCH"])
                return "example.com/" + env["GOARCH"]
            if "-json" in command:
                return json.dumps({"Path": command[-1], "Version": "v1.0.0",
                                   "Sum": "h1:test", "Dir": str(module_dir)})
            return str(self.root)
        with mock.patch.object(common, "command_output", side_effect=output):
            files, modules = common.notices()
        self.assertEqual(seen, ["arm64", "amd64"])
        self.assertEqual({m["path"] for m in modules}, {"example.com/arm64", "example.com/amd64"})
        self.assertIn("licenses/example.com/amd64@v1.0.0/COPYING.md", files)
        self.assertIn("licenses/example.com/amd64@v1.0.0/NOTICE.txt", files)

    def test_common_license_names_are_collected_and_verified(self):
        for name in ("LICENSE", "LICENSE.txt", "license.md", "COPYING", "LICENCE", "LICENSE-APACHE"):
            self.assertTrue(common.is_license_name(name))
        self.assertFalse(common.is_license_name("source.go"))
        files = fixture()
        modules = json.loads(json.dumps(MODULES))
        old = modules[0]["notices"][0]
        new = old + ".md"
        modules[0]["notices"][0] = new
        files[new] = files.pop(old)
        common.add_manifest(files, METADATA, "deckd", "arm64", modules)
        common.write_archive(self.path, files)
        verify.verify_archive(self.path)

    def test_wrong_commit_and_dirty_source_refused_before_go(self):
        args = argparse.Namespace(version="0.3.0", commit="a" * 40,
                                  build_time=METADATA["build_time"], allow_dirty=False)
        with mock.patch.object(common, "command_output", side_effect=["a" * 40, "b" * 40]), mock.patch.object(common.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "checked-out HEAD"):
                common.provenance(args)
            run.assert_not_called()

        with mock.patch.object(common, "command_output", side_effect=["a" * 40, "a" * 40, " M source.go"]), mock.patch.object(common.subprocess, "run") as run:
            with self.assertRaisesRegex(ValueError, "clean source"):
                common.provenance(args)
            run.assert_not_called()

    def test_tracked_input_collection_rejects_links_and_excludes_debris(self):
        (self.root / "tracked").write_bytes(b"input")
        (self.root / "untracked").write_bytes(b"debris")
        with mock.patch.object(common.subprocess, "check_output", return_value=b"tracked\0"):
            self.assertEqual(set(common.tracked_files(["*"], self.root)), {"tracked"})
        (self.root / "link").symlink_to(self.root / "tracked")
        with mock.patch.object(common.subprocess, "check_output", return_value=b"link\0"):
            with self.assertRaisesRegex(ValueError, "symlinks"):
                common.tracked_files(["*"], self.root)
        (self.root / "real").mkdir()
        (self.root / "real/input").write_bytes(b"input")
        (self.root / "parent").symlink_to(self.root / "real", target_is_directory=True)
        with mock.patch.object(common.subprocess, "check_output", return_value=b"parent/input\0"):
            with self.assertRaisesRegex(ValueError, "symlinks"):
                common.tracked_files(["*"], self.root)

    def test_failed_second_architecture_preserves_existing_outputs(self):
        source = self.root / "source"
        output = self.root / "output"
        output.mkdir()
        names = ["deckd-0.3.0-darwin-arm64.zip", "deckd-0.3.0-darwin-amd64.zip", "SHA256SUMS"]
        for name in names:
            (output / name).write_bytes(b"previous release")
        paths = []
        for name, (data, _) in fixture().items():
            if name.startswith("bin/"):
                continue
            path = source / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
            paths.append(name)
        def build(package, target, arch, metadata):
            if arch == "amd64":
                raise subprocess.CalledProcessError(1, "go build")
            target.write_bytes(thin(arch))
        args = ["release.py", "--version", "0.3.0", "--commit", "a" * 40,
                "--build-time", METADATA["build_time"], "--output", str(output)]
        with mock.patch("sys.argv", args), mock.patch.object(release, "ROOT", source), mock.patch.object(release, "provenance", return_value=METADATA), mock.patch.object(release, "notices", return_value=({}, MODULES)), mock.patch.object(release.subprocess, "check_output", return_value="\0".join(paths).encode()), mock.patch.object(release, "build", side_effect=build), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as error:
                release.main()
            self.assertEqual(error.exception.code, 1)
        self.assertEqual(sorted(p.name for p in output.iterdir()), sorted(names))
        for name in names:
            self.assertEqual((output / name).read_bytes(), b"previous release")

    def test_no_execution_without_smoke_and_wrong_host_rejected(self):
        self.write()
        (self.root / "SHA256SUMS").write_text(f"{common.digest(self.path.read_bytes())}  {self.path.name}\n")
        with mock.patch("sys.argv", ["verify_release.py", str(self.path)]), mock.patch.object(verify, "smoke") as smoke, contextlib.redirect_stdout(io.StringIO()):
            verify.main()
            smoke.assert_not_called()
        with mock.patch.object(verify.platform, "system", return_value="Linux"):
            with self.assertRaisesRegex(ValueError, "macOS"):
                verify.smoke({}, {})

    def package_adapter(self, fail=False, duplicate_manifest=False):
        source = self.root / "source"
        plugin = source / "adapters/streamdeck/plugin"
        plugin.mkdir(parents=True, exist_ok=True)
        vendor = {"UUID": "local.patchbay.deckd", "CodePath": "bin/decksd"}
        (plugin / "manifest.json").write_text(json.dumps(vendor))
        if duplicate_manifest:
            (plugin / "manifest.json").write_text('{"UUID":"one","UUID":"two"}')
        for name in ("layout.json", "inspector.html", "inspector.js", "WEBSOCKET-LICENSE.txt"):
            (plugin / name).write_bytes(b"content")
        (plugin / "WEBSOCKET-LICENSE.txt").chmod(0o444)
        (source / "LICENSE").write_bytes(b"project license")
        tracked = [path.relative_to(source).as_posix() for path in plugin.iterdir()] + ["LICENSE"]
        (plugin / ".DS_Store").write_bytes(b"local debris")
        for name in ("specs/STREAMDECK.md", "configs/streamdeck.yaml"):
            path = source / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"content")
        output = self.root / "output"
        output.mkdir(exist_ok=True)
        files = {name: value for name, value in fixture().items()
                 if name.startswith("licenses/") or name == "THIRD_PARTY_NOTICES.txt"}
        def build(package, target, arch, metadata):
            if fail and arch == "amd64":
                raise subprocess.CalledProcessError(1, "go build")
            target.write_bytes(thin(arch))
        def icons(directory):
            directory.mkdir()
            for name in ("plugin", "action", "category", "key"):
                for scale in ("", "@2x"):
                    (directory / f"{name}{scale}.png").write_bytes(b"icon")
        def lipo(command, **kwargs):
            Path(command[-1]).write_bytes(universal())
        args = ["package_streamdeck.py", "--version", "0.3.0", "--commit", "a" * 40,
                "--build-time", METADATA["build_time"], "--output", str(output)]
        with mock.patch("sys.argv", args), mock.patch.object(streamdeck, "ROOT", source), mock.patch.object(streamdeck, "provenance", return_value=METADATA), mock.patch.object(streamdeck, "notices", return_value=(files, MODULES)), mock.patch.object(streamdeck, "build", side_effect=build), mock.patch.object(streamdeck, "icons", side_effect=icons), mock.patch.object(streamdeck.subprocess, "run", side_effect=lipo), mock.patch.object(common.subprocess, "check_output", return_value="\0".join(tracked).encode()), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            streamdeck.main()
        return output

    def test_adapter_handles_read_only_license_and_verifies_universal_bundle(self):
        output = self.package_adapter()
        archive = output / "decksd-0.3.0-macos.zip"
        metadata, files = verify.verify_archive(archive)
        verify.check_checksum(archive, "decksd")
        self.assertEqual(metadata["target"]["arch"], "universal")
        self.assertEqual(files[common.PLUGIN + "/LICENSE"][0], b"project license")
        self.assertNotIn(common.PLUGIN + "/.DS_Store", files)
        for name, (data, mode) in files.items():
            self.assertEqual((output / name).read_bytes(), data)
            self.assertEqual((output / name).stat().st_mode & 0o777, mode)

    def test_duplicate_source_manifest_is_refused(self):
        with self.assertRaises(SystemExit) as error:
            self.package_adapter(duplicate_manifest=True)
        self.assertEqual(error.exception.code, 1)
        self.assertEqual(list((self.root / "output").iterdir()), [])

    def test_failed_adapter_build_preserves_prior_artifacts_and_directory(self):
        output = self.root / "output"
        (output / common.PLUGIN).mkdir(parents=True)
        names = ["decksd-0.3.0-macos.zip", "DECKSD-SHA256SUMS", common.PLUGIN + "/old"]
        for name in names:
            (output / name).write_bytes(b"previous release")
        with self.assertRaises(SystemExit):
            self.package_adapter(fail=True)
        for name in names:
            self.assertEqual((output / name).read_bytes(), b"previous release")
        self.assertFalse(any(path.name.startswith(".decksd-") for path in output.iterdir()))


if __name__ == "__main__":
    unittest.main()
