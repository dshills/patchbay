package recipe

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"
	"patchbay/internal/evidence"
)

func benchmark(t testing.TB) *Package {
	t.Helper()
	p, err := Inspect(context.Background(), "../../recipes/benchmark")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func archive(t testing.TB, files map[string][]byte, extra string, mode os.FileMode) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for _, name := range sortedKeys(files) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0644)
		f, err := writer.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if extra != "" {
		h := &zip.FileHeader{Name: extra}
		h.SetMode(mode)
		f, err := writer.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func rewrite(t testing.TB, p *Package, modify func(*Manifest)) map[string][]byte {
	t.Helper()
	m := p.Manifest
	data, _ := yaml.Marshal(m)
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	modify(&m)
	m.Digest = CanonicalDigest(m)
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for k, v := range p.Files {
		files[k] = bytes.Clone(v)
	}
	files["recipe.yaml"] = data
	return files
}
func TestOfflineRoundTripAndIndependentContent(t *testing.T) {
	p := benchmark(t)
	if len(p.Samples) != 2 || p.Manifest.ID != "benchmark" {
		t.Fatal("missing curated samples")
	}
	one, err := ZIP(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	two, err := ZIP(context.Background(), p)
	if err != nil || !bytes.Equal(one, two) {
		t.Fatal("nondeterministic archive", err)
	}
	restored, err := InspectZIP(context.Background(), one)
	if err != nil || restored.Digest != p.Digest {
		t.Fatal("identity changed", err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range p.Files {
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	inspected, err := Inspect(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed after inspection"), 0600); err != nil {
		t.Fatal(err)
	}
	if inspected.Digest != p.Digest || !bytes.Equal(inspected.Files["README.md"], p.Files["README.md"]) {
		t.Fatal("source aliases inspected content")
	}
	if _, err := Inspect(context.Background(), dir); err == nil {
		t.Fatal("source mutation escaped verification")
	}
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("LICENSE", filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), dir); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestZIPAdversarialBoundaries(t *testing.T) {
	p := benchmark(t)
	for _, name := range []string{"../escape", "/absolute", "a\\b", "README.md", "readme.md", "docs/../escape", "docs/é.md", "docs/e\u0301.md", "samples", "unlisted.txt", "recipe.yaml/child"} {
		t.Run(name, func(t *testing.T) {
			if _, err := InspectZIP(context.Background(), archive(t, p.Files, name, 0644)); err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
	for _, mode := range []os.FileMode{0755, os.ModeSymlink | 0644, os.ModeNamedPipe | 0600, os.ModeDir | 0755} {
		if _, err := InspectZIP(context.Background(), archive(t, p.Files, "docs/link", mode)); err == nil {
			t.Fatal("accepted non-data payload")
		}
	}
	for _, modify := range []func(map[string][]byte){func(f map[string][]byte) { delete(f, "LICENSE") }, func(f map[string][]byte) { f["README.md"] = []byte("forged") }, func(f map[string][]byte) { f["LICENSE"] = bytes.Repeat([]byte("x"), MaxFile+1) }, func(f map[string][]byte) { f["recipe.yaml"] = bytes.Repeat([]byte("x"), MaxManifest+1) }} {
		f := rewrite(t, p, func(*Manifest) {})
		modify(f)
		if _, err := InspectZIP(context.Background(), archive(t, f, "", 0)); err == nil {
			t.Fatal("accepted oversized/missing/forged content")
		}
	}
	if _, err := InspectZIP(context.Background(), make([]byte, MaxPackage+1)); err == nil {
		t.Fatal("oversized compressed input")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectZIP(ctx, archive(t, p.Files, "", 0)); err == nil {
		t.Fatal("cancelled import accepted")
	}
	slots <- struct{}{}
	slots <- struct{}{}
	_, err := InspectZIP(context.Background(), archive(t, p.Files, "", 0))
	<-slots
	<-slots
	if err == nil {
		t.Fatal("unbounded imports")
	}
}
func TestManifestAndSampleValidation(t *testing.T) {
	p := benchmark(t)
	for _, change := range []func(*Manifest){
		func(m *Manifest) { m.SchemaVersion = 2 }, func(m *Manifest) { m.Features["unknown"] = 1 }, func(m *Manifest) { m.Schemas["series"] = 2 },
		func(m *Manifest) {
			a := m.Actions["benchmark.measure"]
			a.Tool = "missing"
			m.Actions["benchmark.measure"] = a
		},
		func(m *Manifest) {
			a := m.Actions["benchmark.measure"]
			a.Type = "plugin"
			m.Actions["benchmark.measure"] = a
		},
		func(m *Manifest) {
			a := m.Actions["benchmark.measure"]
			a.Args = []string{"--file=/Users/private"}
			m.Actions["benchmark.measure"] = a
		},
		func(m *Manifest) {
			a := m.Actions["benchmark.measure"]
			a.Safety = "dangerous"
			m.Actions["benchmark.measure"] = a
		},
		func(m *Manifest) {
			e := m.Experiments["benchmark"]
			e.Collectors[0].Action = "missing"
			m.Experiments["benchmark"] = e
		},
		func(m *Manifest) { m.Controls["run"] = Control{Action: "missing"} },
		func(m *Manifest) { m.Inventory[0].Path = "docs/evil.js" },
	} {
		files := rewrite(t, p, change)
		if _, err := InspectZIP(context.Background(), archive(t, files, "", 0)); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	for _, text := range []string{"unexpected: true\n", "schema_version: 1\n", "anything: &anchor {}\n"} {
		files := rewrite(t, p, func(*Manifest) {})
		files["recipe.yaml"] = append(files["recipe.yaml"], []byte(text)...)
		if _, err := InspectZIP(context.Background(), archive(t, files, "", 0)); err == nil {
			t.Fatal("unknown/duplicate/alias accepted")
		}
	}
	files := rewrite(t, p, func(*Manifest) {})
	name := "samples/benchmark-small.json"
	files[name] = []byte(strings.Replace(string(files[name]), `"origin": "sample"`, `"origin": "measured"`, 1))
	var m Manifest
	if err := yaml.Unmarshal(files["recipe.yaml"], &m); err != nil {
		t.Fatal(err)
	}
	for i := range m.Inventory {
		if m.Inventory[i].Path == name {
			m.Inventory[i].SHA256 = evidence.Digest(files[name])
			m.Inventory[i].Size = int64(len(files[name]))
		}
	}
	m.Digest = CanonicalDigest(m)
	files["recipe.yaml"], _ = yaml.Marshal(m)
	if _, err := InspectZIP(context.Background(), archive(t, files, "", 0)); err == nil {
		t.Fatal("sample claimed to be measured")
	}
}
func FuzzManifest(f *testing.F) {
	p := benchmark(f)
	f.Add(p.Files["recipe.yaml"])
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxManifest {
			return
		}
		_, _ = parseManifest(data)
	})
}
func FuzzZIP(f *testing.F) {
	p := benchmark(f)
	f.Add(archive(f, p.Files, "", 0))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = InspectZIP(context.Background(), data)
	})
}

func TestDirectorySpecialFileAndParentLink(t *testing.T) {
	p := benchmark(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "package")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range p.Files {
		target := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), link); err == nil {
		t.Fatal("parent link accepted")
	}
	if err := os.Remove(filepath.Join(directory, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(directory, "README.md"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), directory); err == nil {
		t.Fatal("FIFO accepted")
	}
}
func TestYAMLDepthAndAliasesRemainBounded(t *testing.T) {
	if _, err := parseManifest([]byte(strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001))); err == nil {
		t.Fatal("deep YAML accepted")
	}
	p := benchmark(t)
	data := append(bytes.Clone(p.Files["recipe.yaml"]), []byte("x: &x [a,a,a]\ny: [*x,*x,*x]\n")...)
	if _, err := parseManifest(data); err == nil {
		t.Fatal("aliases accepted")
	}
	for _, text := range []string{`C:\private`, `--file=C:\private`, `\\server\share`, `/private/file`, `~/file`} {
		if portable(text) {
			t.Fatal("nonportable path", text)
		}
	}
}
