package decksetup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"patchbay/internal/config"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const originalProfile = "b41d27a4-0000-4000-8000-000000000001"
const testDevice = "fixture-deck-plus"
const preferences = `<?xml version="1.0"?><plist version="1.0"><dict>
<key>Devices</key><dict><key>PreferredDevice</key><string>fixture-deck-plus</string>
<key>fixture-deck-plus</key><dict><key>ESDProfilesInfo</key><dict><key>ESDProfilesPreferred</key><string>B41D27A4-0000-4000-8000-000000000001</string></dict><key>Brightness</key><integer>37</integer></dict></dict>
<key>Opaque</key><data>AQIDBA==</data><key>Updated</key><date>2026-10-08T12:00:00Z</date>
<key>Float</key><real>0.5</real><key>OtherPlugin</key><dict><key>Enabled</key><true/></dict>
</dict></plist>`

type fixturePlatform struct {
	prefs            []byte
	running          bool
	service          bool
	stopped, started int
	failStart        bool
	paths            Paths
}

func (f *fixturePlatform) StopApp(context.Context) (bool, error) {
	was := f.running
	f.running = false
	f.stopped++
	return was, nil
}
func (f *fixturePlatform) StartApp(context.Context) error { f.running = true; f.started++; return nil }
func (f *fixturePlatform) ReadPreferences(context.Context) ([]byte, error) {
	return append([]byte(nil), f.prefs...), nil
}
func (f *fixturePlatform) WritePreferences(_ context.Context, b []byte) error {
	if f.running {
		return errors.New("preferences changed while app running")
	}
	f.prefs = append([]byte(nil), b...)
	return nil
}
func (f *fixturePlatform) StopService(context.Context, string) error { f.service = false; return nil }
func (f *fixturePlatform) StartService(_ context.Context, path, socket string) error {
	if f.failStart {
		f.failStart = false
		return errors.New("injected startup failure")
	}
	data, err := readFile(path, 1<<20)
	if err != nil {
		return err
	}
	if _, err = parsePlist(data); err != nil {
		return err
	}
	c, err := config.Load(filepath.Join(f.paths.Root, "config.yaml"))
	if err != nil {
		return err
	}
	if c.Server.Socket != socket {
		return errors.New("socket mismatch")
	}
	f.service = true
	return nil
}
func (f *fixturePlatform) VerifyBinaries(context.Context, string) error { return nil }

func fixture(t *testing.T) (*Manager, *fixturePlatform) {
	t.Helper()
	// Keep Unix socket fixtures below macOS's socket path limit; its default
	// per-user temporary directory is too long for the managed socket path.
	temporary, err := os.MkdirTemp("/tmp", "pb-deck-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temporary) })
	home, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		t.Fatal(err)
	}
	p := Paths{Home: home, Root: filepath.Join(home, ".deckd", "deck"), App: filepath.Join(home, "Library", "Application Support", "com.elgato.StreamDeck"), Binaries: filepath.Join(home, "bundle", "bin")}
	for _, path := range []string{p.Binaries, filepath.Join(p.App, "Data"), filepath.Join(p.App, "Plugins", pluginID+".sdPlugin")} {
		if err = ensureDir(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"deckd", "deckdemo", "decksd"} {
		if err = os.WriteFile(filepath.Join(p.Binaries, name), []byte("fixture-binary-"+name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = createProfile(filepath.Join(p.App, "ProfilesV3", originalProfile+".sdProfile"), "original", testDevice, originalProfile); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{filepath.Join(p.App, "Data", "settings.db"): "private original plugin settings", filepath.Join(p.App, "Plugins", pluginID+".sdPlugin", "original.txt"): "prior plugin"} {
		if err = os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixturePlatform{prefs: []byte(preferences), running: true, paths: p}
	m, err := New(p, f)
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func assertOriginal(t *testing.T, m *Manager, f *fixturePlatform) {
	t.Helper()
	if string(f.prefs) != preferences {
		t.Fatal("original preferences changed")
	}
	if string(mustRead(t, filepath.Join(m.paths.App, "Data", "settings.db"))) != "private original plugin settings" {
		t.Fatal("original plugin settings changed")
	}
	if string(mustRead(t, filepath.Join(m.targets()["plugin"], "original.txt"))) != "prior plugin" {
		t.Fatal("prior Patchbay plugin not restored")
	}
	entries, err := os.ReadDir(m.targets()["profiles"])
	if err != nil || len(entries) != 1 || entries[0].Name() != originalProfile+".sdProfile" {
		t.Fatal("original profile tree not restored", err)
	}
	for _, path := range []string{m.agent(), filepath.Join(m.paths.Root, "config.yaml"), filepath.Join(m.paths.Root, "active.json"), filepath.Join(m.paths.Root, "bin")} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("originally absent setup remained", path, err)
		}
	}
	if f.service {
		t.Fatal("new service remained after restoring original")
	}
}
func TestUseSwitchRestoreAndUndoRestore(t *testing.T) {
	m, f := fixture(t)
	first, err := m.Use(context.Background(), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if !backupID.MatchString(first.Backup) || !f.running || !f.service {
		t.Fatal(first, f)
	}
	if _, err = m.loadBackup(first.Backup, true); err != nil {
		t.Fatal(err)
	}
	a := m.active()
	if a.Preset != "demo" || a.Device != testDevice {
		t.Fatal(a)
	}
	profile := filepath.Join(m.targets()["profiles"], a.Profile+".sdProfile")
	var manifest struct {
		Pages struct {
			Current string
			Default string
			Pages   []string
		}
		Device struct{ UUID string }
	}
	if err = json.Unmarshal(mustRead(t, filepath.Join(profile, "manifest.json")), &manifest); err != nil || manifest.Device.UUID != testDevice {
		t.Fatal(manifest, err)
	}
	if manifest.Pages.Current == manifest.Pages.Default || len(manifest.Pages.Pages) != 1 || manifest.Pages.Pages[0] != manifest.Pages.Current {
		t.Fatal("invalid native page identities", manifest.Pages)
	}
	var defaultPage struct{ Controllers []any }
	if err = json.Unmarshal(mustRead(t, filepath.Join(profile, "Profiles", manifest.Pages.Default, "manifest.json")), &defaultPage); err != nil || defaultPage.Controllers != nil {
		t.Fatal("invalid reserved default page", err)
	}
	var page struct {
		Controllers []struct {
			Type    string
			Actions map[string]struct {
				UUID     string
				Settings struct{ Control string }
			}
		}
	}
	if err = json.Unmarshal(mustRead(t, filepath.Join(profile, "Profiles", manifest.Pages.Current, "manifest.json")), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Controllers) != 2 || page.Controllers[0].Actions["0,0"].Settings.Control != "key-1" || page.Controllers[1].Actions["0,0"].Settings.Control != "dial-1" {
		t.Fatal(page)
	}
	p, err := parsePlist(f.prefs)
	if err != nil {
		t.Fatal(err)
	}
	if p.get("Devices").get(testDevice).get("ESDProfilesInfo").get("ESDProfilesPreferred").string() != strings.ToUpper(a.Profile) {
		t.Fatal("profile not selected")
	}
	original, _ := parsePlist([]byte(preferences))
	for _, key := range []string{"Opaque", "Updated", "Float", "OtherPlugin"} {
		if !reflect.DeepEqual(original.get(key), p.get(key)) {
			t.Fatal("unrelated typed preference changed", key)
		}
	}
	if string(mustRead(t, filepath.Join(m.paths.App, "Data", "settings.db"))) != "private original plugin settings" {
		t.Fatal("unrelated data overwritten")
	}
	second, err := m.Use(context.Background(), "benchmark", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Backup == first.Backup || m.active().Preset != "benchmark" {
		t.Fatal(second)
	}
	if _, err = m.Restore(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if m.active().Preset != "demo" {
		t.Fatal("latest backup did not restore previous preset")
	}
	if _, err = m.Restore(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if m.active().Preset != "benchmark" {
		t.Fatal("restoration was not undoable")
	}
	if _, err = m.Restore(context.Background(), "original"); err != nil {
		t.Fatal(err)
	}
	assertOriginal(t, m, f)
	backups, err := m.Backups()
	if err != nil || len(backups) != 5 {
		t.Fatal(len(backups), err)
	}
}
func TestSwitchFailureRestoresAndRetainsBackup(t *testing.T) {
	for _, phase := range []string{"backed_up", "changed", "startup"} {
		t.Run(phase, func(t *testing.T) {
			m, f := fixture(t)
			m.Fault = func(p string) error {
				if p == phase {
					return errors.New("injected failure")
				}
				return nil
			}
			f.failStart = phase == "startup"
			result, err := m.Use(context.Background(), "demo", "")
			if err == nil || result.Backup == "" {
				t.Fatal(result, err)
			}
			assertOriginal(t, m, f)
			if !f.running {
				t.Fatal("app not reopened after failure")
			}
			if _, err = m.loadBackup(result.Backup, true); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(m.paths.Root, "pending.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}
func TestBackupFailureAndInvalidPresetNeverChangeSetup(t *testing.T) {
	m, f := fixture(t)
	if _, err := m.Use(context.Background(), "missing", ""); err == nil || f.stopped != 0 {
		t.Fatal("invalid preset stopped app")
	}
	if err := os.Symlink(filepath.Join(m.paths.App, "Data", "settings.db"), filepath.Join(m.targets()["profiles"], "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Use(context.Background(), "demo", ""); err == nil {
		t.Fatal("accepted linked profile backup")
	}
	if string(f.prefs) != preferences || !f.running || f.service {
		t.Fatal("backup failure changed setup")
	}
	if _, err := os.Stat(m.agent()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("service installed before backup")
	}
}
func TestCorruptBackupRejectedBeforeStoppingApp(t *testing.T) {
	m, f := fixture(t)
	r, err := m.Use(context.Background(), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.paths.Root, "backups", r.Backup, "preferences.xml")
	if err = os.WriteFile(path, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	stopped := f.stopped
	if _, err = m.Restore(context.Background(), r.Backup); err == nil || f.stopped != stopped {
		t.Fatal("corrupt restore modified live setup")
	}
}
func TestPendingRecoveryAndConcurrentSwitch(t *testing.T) {
	m, f := fixture(t)
	ready, release := make(chan struct{}), make(chan struct{})
	m.Fault = func(phase string) error {
		if phase == "backed_up" {
			close(ready)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := m.Use(context.Background(), "demo", ""); done <- err }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal("switch stopped before its backup boundary", err)
	case <-time.After(10 * time.Second):
		t.Fatal("backup boundary timed out")
	}
	other, err := New(m.paths, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Use(context.Background(), "benchmark", ""); err == nil {
		t.Error("concurrent switch bypassed lock")
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	m.Fault = nil
	backups, err := m.Backups()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(struct {
		Backup string `json:"backup"`
	}{backups[0].ID})
	if err = writeFile(filepath.Join(m.paths.Root, "pending.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Use(context.Background(), "benchmark", ""); err == nil {
		t.Fatal("ignored interrupted switch")
	}
	if _, err = m.Restore(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	assertOriginal(t, m, f)
}
func TestNativeAgentAndPlistTypedValues(t *testing.T) {
	m, _ := fixture(t)
	if err := ensureDir(filepath.Dir(m.agent())); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(m.agent(), m.launchAgent(), 0600); err != nil {
		t.Fatal(err)
	}
	n := native{paths: m.paths}
	if err := n.ownedAgent(m.agent()); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(m.agent(), []byte(strings.Replace(string(m.launchAgent()), serviceID, "someone.else", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := n.ownedAgent(m.agent()); err == nil {
		t.Fatal("accepted foreign service")
	}
	p, err := parsePlist([]byte(preferences))
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.bytes()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := parsePlist(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Opaque", "Updated", "Float", "OtherPlugin"} {
		if !reflect.DeepEqual(p.get(key), p2.get(key)) {
			t.Fatal(key)
		}
	}
}

func TestRepeatedPresetAndCancelledSwitch(t *testing.T) {
	m, f := fixture(t)
	if _, err := m.Use(context.Background(), "demo", ""); err != nil {
		t.Fatal(err)
	}
	id := m.active().Profile
	if _, err := m.Use(context.Background(), "demo", ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(m.targets()["profiles"])
	if err != nil || len(entries) != 2 || m.active().Profile != id {
		t.Fatal("repeated preset added duplicate profiles", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Fault = func(phase string) error {
		if phase == "changed" {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if _, err = m.Use(ctx, "benchmark", ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if m.active().Preset != "demo" || !f.running || !f.service {
		t.Fatal("cancelled switch did not restore running prior setup")
	}
}

func TestNativeServiceInspectionFailuresDoNotStopUnknownJobs(t *testing.T) {
	for _, status := range []int{113, 1, 0} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			m, _ := fixture(t)
			calls := 0
			n := native{paths: m.paths, command: func(_ context.Context, path string, args ...string) ([]byte, error) {
				calls++
				if path != "/bin/launchctl" || args[0] != "print" {
					t.Fatal("unexpected native mutation", path, args)
				}
				if status == 0 {
					return nil, nil
				}
				return nil, exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(status)).Run()
			}}
			err := n.StopService(context.Background(), m.agent())
			if calls != 1 || (status == 113 && err != nil) || (status != 113 && err == nil) {
				t.Fatal(status, calls, err)
			}
		})
	}
}

func TestUnsafeManifestPathsRejectedBeforeStoppingApp(t *testing.T) {
	m, f := fixture(t)
	r, err := m.Use(context.Background(), "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.loadBackup(r.Backup, true)
	if err != nil {
		t.Fatal(err)
	}
	stopped := f.stopped
	path := filepath.Join(m.paths.Root, "backups", r.Backup, "manifest.json")
	for _, unsafe := range []string{"../outside", "profiles/../../outside", "/tmp/outside", "profiles/./file"} {
		modified := b
		modified.Files = inventory{}
		for name, record := range b.Files {
			modified.Files[name] = record
		}
		modified.Files[unsafe] = b.Files["preferences.xml"]
		data, err := json.Marshal(modified)
		if err != nil {
			t.Fatal(err)
		}
		if err = writeFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = m.Restore(context.Background(), r.Backup); err == nil || f.stopped != stopped {
			t.Fatal("unsafe backup reached live operation", unsafe, err)
		}
	}
}
