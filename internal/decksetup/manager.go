package decksetup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/localfs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const modelDeckPlus = "20GBD9901"
const pluginID = "local.patchbay.deckd"
const serviceID = "local.patchbay.deckd.setup"
const maxBackups = 100
const maxBackupStorage = 4 << 30

type Paths struct{ Home, Root, App, Binaries string }
type Platform interface {
	StopApp(context.Context) (bool, error)
	StartApp(context.Context) error
	ReadPreferences(context.Context) ([]byte, error)
	WritePreferences(context.Context, []byte) error
	StopService(context.Context, string) error
	StartService(context.Context, string, string) error
	VerifyBinaries(context.Context, string) error
}
type Manager struct {
	paths    Paths
	platform Platform
	Fault    func(string) error
}
type Active struct {
	Preset  string `json:"preset"`
	Device  string `json:"device"`
	Profile string `json:"profile"`
}
type Backup struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Created time.Time       `json:"created"`
	Reason  string          `json:"reason"`
	Preset  string          `json:"preset,omitempty"`
	Exists  map[string]bool `json:"exists"`
	Files   inventory       `json:"files"`
}
type Result struct {
	Preset   string `json:"preset,omitempty"`
	Backup   string `json:"backup"`
	Restored string `json:"restored,omitempty"`
	Socket   string `json:"socket,omitempty"`
}

var backupID = regexp.MustCompile(`^[0-9]{8}T[0-9]{9}Z-[A-Za-z0-9_-]{8,64}$`)

func New(paths Paths, platform Platform) (*Manager, error) {
	for _, path := range []string{paths.Home, paths.Root, paths.App, paths.Binaries} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, errors.New("setup paths must be clean absolute paths")
		}
	}
	if platform == nil {
		return nil, errors.New("setup platform is unavailable")
	}
	return &Manager{paths: paths, platform: platform}, nil
}
func (m *Manager) socket() string { return filepath.Join(m.paths.Root, "deckd.sock") }
func (m *Manager) agent() string {
	return filepath.Join(m.paths.Home, "Library", "LaunchAgents", serviceID+".plist")
}
func (m *Manager) targets() map[string]string {
	return map[string]string{
		"profiles": filepath.Join(m.paths.App, "ProfilesV3"), "data": filepath.Join(m.paths.App, "Data"),
		"plugin": filepath.Join(m.paths.App, "Plugins", pluginID+".sdPlugin"),
		"config": filepath.Join(m.paths.Root, "config.yaml"), "active": filepath.Join(m.paths.Root, "active.json"), "agent": m.agent(),
		"binaries":       filepath.Join(m.paths.Root, "bin"),
		"profiles-index": filepath.Join(m.paths.Root, "profiles.json"),
	}
}
func (m *Manager) lock() (*localfs.Lock, error) {
	if err := ensureDir(m.paths.Root); err != nil {
		return nil, err
	}
	if err := localfs.PrivateDir(m.paths.Root); err != nil {
		return nil, err
	}
	if err := ensureDir(filepath.Join(m.paths.Root, "backups")); err != nil {
		return nil, err
	}
	if err := localfs.PrivateDir(filepath.Join(m.paths.Root, "backups")); err != nil {
		return nil, err
	}
	return localfs.Acquire(filepath.Join(m.paths.Root, "setup.lock"))
}
func (m *Manager) active() Active {
	var a Active
	b, err := readFile(filepath.Join(m.paths.Root, "active.json"), 4096)
	if err == nil {
		_ = jsonstrict.Decode(b, &a)
	}
	return a
}
func (m *Manager) Backups() ([]Backup, error) {
	lock, err := m.lock()
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	return m.backups()
}
func (m *Manager) backups() ([]Backup, error) {
	entries, err := os.ReadDir(filepath.Join(m.paths.Root, "backups"))
	if err != nil {
		return nil, err
	}
	if len(entries) > maxBackups+1 {
		return nil, errors.New("backup limit reached; keep an offline copy before removing old backups")
	}
	out := []Backup{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() || !backupID.MatchString(entry.Name()) {
			return nil, errors.New("unexpected file in setup backups")
		}
		b, err := m.loadBackup(entry.Name(), false)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}
func (m *Manager) loadBackup(id string, verify bool) (Backup, error) {
	var b Backup
	if !backupID.MatchString(id) {
		return b, errors.New("invalid backup ID")
	}
	path := filepath.Join(m.paths.Root, "backups", id)
	data, err := readFile(filepath.Join(path, "manifest.json"), 8<<20)
	if err != nil {
		return b, err
	}
	if err = jsonstrict.Decode(data, &b); err != nil || b.Version != 1 || b.ID != id || len(b.Files) > maxFiles || !b.Exists["preferences.xml"] {
		return b, errors.New("invalid setup backup")
	}
	allowed := m.targets()
	allowed["preferences.xml"] = ""
	if len(b.Exists) != len(allowed) {
		return b, errors.New("backup target inventory is incomplete")
	}
	for name := range b.Exists {
		if _, ok := allowed[name]; !ok {
			return b, errors.New("unknown backup target")
		}
	}
	var total int64
	for path, f := range b.Files {
		if !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
			return b, errors.New("unsafe backup file path")
		}
		target := strings.SplitN(path, "/", 2)[0]
		if !b.Exists[target] || f.Size < 0 || f.Size > maxFile || f.Mode&^0777 != 0 || len(f.Digest) != 64 {
			return b, errors.New("invalid backup file metadata")
		}
		if _, err := hex.DecodeString(f.Digest); err != nil {
			return b, errors.New("invalid backup hash")
		}
		total += f.Size
	}
	if _, ok := b.Files["preferences.xml"]; !ok || total > maxSnapshot {
		return b, errors.New("invalid backup size or preferences")
	}
	if verify {
		err = verifyTree(path, b.Files)
	}
	return b, err
}
func (m *Manager) snapshot(ctx context.Context, reason string) (Backup, error) {
	var b Backup
	old, err := m.backups()
	if err != nil {
		return b, err
	}
	if len(old) >= maxBackups {
		return b, errors.New("backup limit reached; nothing was changed")
	}
	var used int64
	for _, v := range old {
		for _, f := range v.Files {
			used += f.Size
		}
	}
	now := time.Now().UTC()
	id := now.Format("20060102T150405") + fmt.Sprintf("%03dZ-", now.Nanosecond()/1e6) + rand.Text()
	b = Backup{1, id, now, reason, m.active().Preset, map[string]bool{}, inventory{}}
	base := filepath.Join(m.paths.Root, "backups")
	stage := filepath.Join(base, "."+id+".pending")
	if err = os.Mkdir(stage, 0700); err != nil {
		return b, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	inv := inventory{}
	for name, path := range m.targets() {
		if err = ctx.Err(); err != nil {
			return b, err
		}
		_, err = os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			b.Exists[name] = false
			continue
		}
		if err != nil {
			return b, err
		}
		b.Exists[name] = true
		if err = copyTree(path, filepath.Join(stage, name), inv); err != nil {
			return b, err
		}
	}
	prefs, err := m.platform.ReadPreferences(ctx)
	if err != nil {
		return b, err
	}
	if _, err = parsePlist(prefs); err != nil {
		return b, err
	}
	if err = writeFile(filepath.Join(stage, "preferences.xml"), prefs, 0600); err != nil {
		return b, err
	}
	h := sha256.Sum256(prefs)
	b.Exists["preferences.xml"] = true
	b.Files["preferences.xml"] = fileRecord{int64(len(prefs)), 0600, hex.EncodeToString(h[:])}
	for path, f := range inv {
		rel, err := filepath.Rel(stage, filepath.FromSlash(path))
		if err != nil {
			return b, err
		}
		b.Files[filepath.ToSlash(rel)] = f
	}
	var size int64
	for _, f := range b.Files {
		size += f.Size
	}
	if size > maxSnapshot || used+size > maxBackupStorage {
		return b, errors.New("backup storage limit reached; nothing was changed")
	}
	data, err := json.Marshal(b)
	if err != nil {
		return b, err
	}
	if len(data) > 8<<20 {
		return b, errors.New("backup manifest exceeds size limit")
	}
	if err = writeFile(filepath.Join(stage, "manifest.json"), data, 0600); err != nil {
		return b, err
	}
	if err = verifyTree(stage, b.Files); err != nil {
		return b, err
	}
	if err = syncTree(stage); err != nil {
		return b, err
	}
	if err = os.Rename(stage, filepath.Join(base, id)); err != nil {
		return b, err
	}
	r, err := localfs.OpenDirectory(base)
	if err != nil {
		return b, err
	}
	err = syncDir(r)
	_ = r.Close()
	if err != nil {
		return b, err
	}
	committed = true
	return b, nil
}
func (m *Manager) checkPending() error {
	if _, err := os.Lstat(filepath.Join(m.paths.Root, "pending.json")); err == nil {
		return errors.New("a setup switch was interrupted; run deckctl deck restore to recover before switching again")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (m *Manager) inject(phase string) error {
	if m.Fault != nil {
		return m.Fault(phase)
	}
	return nil
}
func (m *Manager) transaction(ctx context.Context, reason string, change func(context.Context) error) (Backup, error) {
	wasRunning, err := m.platform.StopApp(ctx)
	if err != nil {
		return Backup{}, err
	}
	b, err := m.snapshot(ctx, reason)
	if err != nil {
		if wasRunning {
			_ = m.platform.StartApp(context.Background())
		}
		return b, err
	}
	pending, _ := json.Marshal(struct {
		Backup string `json:"backup"`
	}{b.ID})
	if err = writeFile(filepath.Join(m.paths.Root, "pending.json"), pending, 0600); err != nil {
		if wasRunning {
			_ = m.platform.StartApp(context.Background())
		}
		return b, err
	}
	err = m.inject("backed_up")
	if err == nil {
		err = m.platform.StopService(ctx, m.agent())
	}
	if err == nil {
		err = change(ctx)
	}
	if err == nil {
		err = m.inject("changed")
	}
	if err == nil {
		err = m.start(ctx)
	}
	if err == nil {
		err = m.platform.StartApp(ctx)
	}
	if err == nil {
		err = removeTarget(filepath.Join(m.paths.Root, "pending.json"))
		return b, err
	}
	// Failure recovery uses the already durable snapshot and an independent
	// deadline, even when the caller cancelled the installation.
	recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, rollback := m.platform.StopApp(recovery)
	if rollback == nil {
		rollback = m.platform.StopService(recovery, m.agent())
	}
	if rollback == nil {
		rollback = m.restoreFiles(recovery, b)
	}
	if rollback == nil {
		rollback = m.start(recovery)
	}
	if rollback == nil && wasRunning {
		rollback = m.platform.StartApp(recovery)
	}
	if rollback == nil {
		rollback = removeTarget(filepath.Join(m.paths.Root, "pending.json"))
	}
	if rollback != nil {
		return b, fmt.Errorf("setup failed: %w; recovery needs deckctl deck restore %s: %v", err, b.ID, rollback)
	}
	return b, fmt.Errorf("setup failed; previous setup restored from %s: %w", b.ID, err)
}
func (m *Manager) start(ctx context.Context) error {
	_, err := os.Lstat(m.agent())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.platform.StartService(ctx, m.agent(), m.socket())
}
func (m *Manager) Use(ctx context.Context, name, device string) (Result, error) {
	if !knownPreset(name) {
		return Result{}, errors.New("unknown setup; run deckctl deck list")
	}
	lock, err := m.lock()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = lock.Close() }()
	if err = m.checkPending(); err != nil {
		return Result{}, err
	}
	if err = m.platform.VerifyBinaries(ctx, m.paths.Binaries); err != nil {
		return Result{}, err
	}
	configuration, err := m.configuration(name)
	if err != nil {
		return Result{}, err
	}
	prefs, err := m.platform.ReadPreferences(ctx)
	if err != nil {
		return Result{}, err
	}
	p, err := parsePlist(prefs)
	if err != nil {
		return Result{}, err
	}
	device, err = m.selectDevice(p, device)
	if err != nil {
		return Result{}, err
	}
	stage, err := os.MkdirTemp(m.paths.Root, ".install-")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err = createPlugin(filepath.Join(stage, "plugin"), filepath.Join(m.paths.Binaries, "decksd"), m.socket()); err != nil {
		return Result{}, err
	}
	index := map[string]string{}
	indexPath := filepath.Join(m.paths.Root, "profiles.json")
	if data, e := readFile(indexPath, 64<<10); e == nil {
		if jsonstrict.Decode(data, &index) != nil || index == nil || len(index) > 1000 {
			return Result{}, errors.New("invalid preset profile index")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return Result{}, e
	}
	key := name + "\n" + device
	profileID := index[key]
	if profileID == "" {
		profileID = uuid()
		index[key] = profileID
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$`).MatchString(profileID) {
		return Result{}, errors.New("invalid preset profile identity")
	}
	if err = createProfile(filepath.Join(stage, "profile"), name, device, profileID); err != nil {
		return Result{}, err
	}
	b, err := m.transaction(ctx, "before switching to "+name, func(ctx context.Context) error {
		// Read again after the app stopped; preferences may have changed while
		// staging. Preserve every field except the selected profile of this device.
		prefs, err := m.platform.ReadPreferences(ctx)
		if err != nil {
			return err
		}
		p, err := parsePlist(prefs)
		if err != nil {
			return err
		}
		if _, err = m.selectDevice(p, device); err != nil {
			return err
		}
		if err = replaceTree(filepath.Join(stage, "plugin"), m.targets()["plugin"]); err != nil {
			return err
		}
		if err = replaceTree(filepath.Join(stage, "profile"), filepath.Join(m.targets()["profiles"], profileID+".sdProfile")); err != nil {
			return err
		}
		for _, name := range []string{"deckd", "deckdemo"} {
			if err = replaceTree(filepath.Join(m.paths.Binaries, name), filepath.Join(m.paths.Root, "bin", name)); err != nil {
				return err
			}
		}
		if err = writeFile(filepath.Join(m.paths.Root, "config.yaml"), configuration, 0600); err != nil {
			return err
		}
		a := Active{name, device, profileID}
		data, _ := json.Marshal(a)
		if err = writeFile(filepath.Join(m.paths.Root, "active.json"), data, 0600); err != nil {
			return err
		}
		data, _ = json.Marshal(index)
		if err = writeFile(indexPath, data, 0600); err != nil {
			return err
		}
		if err = ensureDir(filepath.Dir(m.agent())); err != nil {
			return err
		}
		if err = writeFile(m.agent(), m.launchAgent(), 0600); err != nil {
			return err
		}
		info := p.get("Devices").get(device).get("ESDProfilesInfo")
		info.set("ESDProfilesPreferred", &plist{Kind: "string", Text: strings.ToUpper(profileID)})
		data, err = p.bytes()
		if err != nil {
			return err
		}
		return m.platform.WritePreferences(ctx, data)
	})
	return Result{Preset: name, Backup: b.ID, Socket: m.socket()}, err
}
func (m *Manager) Restore(ctx context.Context, id string) (Result, error) {
	lock, err := m.lock()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = lock.Close() }()
	if id == "" || id == "latest" {
		pending, err := readFile(filepath.Join(m.paths.Root, "pending.json"), 4096)
		if err == nil {
			var p struct {
				Backup string `json:"backup"`
			}
			if jsonstrict.Decode(pending, &p) != nil {
				return Result{}, errors.New("invalid interrupted setup journal")
			}
			id = p.Backup
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
	}
	if id == "" || id == "latest" || id == "original" {
		backups, err := m.backups()
		if err != nil {
			return Result{}, err
		}
		if len(backups) == 0 {
			return Result{}, errors.New("no backups yet")
		}
		if id == "original" {
			id = backups[0].ID
		} else {
			id = backups[len(backups)-1].ID
		}
	}
	b, err := m.loadBackup(id, true)
	if err != nil {
		return Result{}, err
	}
	before, err := m.transaction(ctx, "before restoring "+id, func(ctx context.Context) error { return m.restoreFiles(ctx, b) })
	return Result{Backup: before.ID, Restored: id, Preset: m.active().Preset}, err
}
func (m *Manager) restoreFiles(ctx context.Context, b Backup) error {
	if _, err := m.loadBackup(b.ID, true); err != nil {
		return err
	}
	base := filepath.Join(m.paths.Root, "backups", b.ID)
	for name, target := range m.targets() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.Exists[name] {
			if err := replaceTree(filepath.Join(base, name), target); err != nil {
				return err
			}
		} else {
			if err := removeTarget(target); err != nil {
				return err
			}
		}
	}
	prefs, err := readFile(filepath.Join(base, "preferences.xml"), 16<<20)
	if err != nil {
		return err
	}
	if _, err = parsePlist(prefs); err != nil {
		return err
	}
	return m.platform.WritePreferences(ctx, prefs)
}

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *Manager) Devices(ctx context.Context) ([]Device, error) {
	b, err := m.platform.ReadPreferences(ctx)
	if err != nil {
		return nil, err
	}
	p, err := parsePlist(b)
	if err != nil {
		return nil, err
	}
	devices, err := m.deviceCatalog()
	if err != nil {
		return nil, err
	}
	out := []Device{}
	for id := range devices {
		name := p.get("Devices").get(id).get("DeviceName").string()
		if name == "" {
			name = "Stream Deck+"
		}
		out = append(out, Device{id, name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *Manager) deviceCatalog() (map[string]bool, error) {
	entries, err := os.ReadDir(m.targets()["profiles"])
	if err != nil {
		return nil, errors.New("open Stream Deck once with your Deck+ connected; ProfilesV3 is missing")
	}
	devices := map[string]bool{}
	if len(entries) > 1000 {
		return nil, errors.New("too many Stream Deck profiles")
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sdProfile") {
			continue
		}
		b, err := readFile(filepath.Join(m.targets()["profiles"], entry.Name(), "manifest.json"), 1<<20)
		if err != nil {
			return nil, err
		}
		var manifest struct {
			Version string
			Device  struct{ Model, UUID string }
		}
		if json.Unmarshal(b, &manifest) != nil || manifest.Version != "3.0" {
			return nil, errors.New("unsupported Stream Deck profile format; existing setup was preserved")
		}
		if manifest.Device.Model == modelDeckPlus && manifest.Device.UUID != "" {
			devices[manifest.Device.UUID] = true
		}
	}
	return devices, nil
}
func (m *Manager) selectDevice(p *plist, requested string) (string, error) {
	devices, err := m.deviceCatalog()
	if err != nil {
		return "", err
	}
	if requested == "" {
		preferred := p.get("Devices").get("PreferredDevice").string()
		if devices[preferred] {
			requested = preferred
		}
	}
	if requested == "" {
		if len(devices) != 1 {
			return "", errors.New("run deckctl deck devices, then select a Stream Deck+ with --device")
		}
		for d := range devices {
			requested = d
		}
	}
	info := p.get("Devices").get(requested).get("ESDProfilesInfo")
	if !devices[requested] || info == nil || info.Kind != "dict" || info.get("ESDProfilesPreferred").string() == "" {
		return "", errors.New("open your Stream Deck+ profile in the app first; its selection is unavailable")
	}
	return requested, nil
}
