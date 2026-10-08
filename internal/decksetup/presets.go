// Package decksetup manages explicitly requested Stream Deck setup switches.
package decksetup

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"patchbay/adapters/streamdeck"
	"patchbay/internal/config"
	"path/filepath"
	"strings"
)

//go:embed demo.yaml benchmark.yaml
var presets embed.FS

type Preset struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func Presets() []Preset {
	return []Preset{{"demo", "Try a button, a confirmation, and an adjustable dial."}, {"benchmark", "Capture benchmarks; set a baseline and adjust the workload."}}
}
func knownPreset(name string) bool {
	for _, p := range Presets() {
		if p.Name == name {
			return true
		}
	}
	return false
}
func quoted(s string) string { b, _ := json.Marshal(s); return string(b) }
func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func (m *Manager) configuration(name string) ([]byte, error) {
	b, err := presets.ReadFile(name + ".yaml")
	if err != nil {
		return nil, errors.New("unknown setup; run deckctl deck list")
	}
	text := string(b)
	workspace := filepath.Join(m.paths.Root, "workspaces", name)
	if err = ensureDir(workspace); err != nil {
		return nil, err
	}
	if name == "demo" {
		text = strings.ReplaceAll(text, "private/deckd.sock", quoted(m.socket()))
		text = strings.ReplaceAll(text, "private/state.json", quoted(filepath.Join(workspace, "state.json")))
		text = strings.Replace(text, "path: .}", "path: "+quoted(workspace)+"}", 1)
	} else {
		text = strings.ReplaceAll(text, "../.cache/benchmark/deckd.sock", quoted(m.socket()))
		text = strings.ReplaceAll(text, "../.cache/benchmark/state.json", quoted(filepath.Join(workspace, "state.json")))
		text = strings.Replace(text, "path: ..}", "path: "+quoted(workspace)+"}", 1)
		text = strings.ReplaceAll(text, "../bin/deckdemo", quoted(filepath.Join(m.paths.Root, "bin", "deckdemo")))
		text += `
bindings:
  - control: key-1
    press: {action: benchmark.capture}
  - control: key-2
    press: {baseline: benchmark}
  - control: key-3
    press: {result: benchmark}
  - control: dial-1
    rotate: {parameter: iterations}
  - control: dial-2
    rotate: {parameter: repeats}
`
	}
	if len([]byte(m.socket())) > 100 {
		return nil, errors.New("home path is too long for the private Stream Deck socket")
	}
	if _, err = config.Parse([]byte(text), m.paths.Root, m.paths.Home); err != nil {
		return nil, err
	}
	return []byte(text), nil
}

func createPlugin(path, binary, socket string) error {
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	if err := fs.WalkDir(streamdeck.PluginFiles, "plugin", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := streamdeck.PluginFiles.ReadFile(name)
		if err != nil {
			return err
		}
		return writeFile(filepath.Join(path, filepath.Base(name)), data, 0600)
	}); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(path, "bin"), 0700); err != nil {
		return err
	}
	if err := copyTree(binary, filepath.Join(path, "bin", "decksd"), nil); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(path, "bin", "decksd"), 0700); err != nil {
		return err
	}
	setup, _ := json.Marshal(struct {
		Socket string `json:"socket"`
	}{socket})
	if err := writeFile(filepath.Join(path, "patchbay-setup.json"), setup, 0600); err != nil {
		return err
	}
	assets := filepath.Join(path, "assets")
	if err := os.Mkdir(assets, 0700); err != nil {
		return err
	}
	for _, icon := range []struct {
		name string
		size int
		mono bool
	}{{"plugin", 256, false}, {"plugin@2x", 512, false}, {"action", 20, true}, {"action@2x", 40, true}, {"category", 28, true}, {"category@2x", 56, true}, {"key", 72, false}, {"key@2x", 144, false}} {
		img := image.NewNRGBA(image.Rect(0, 0, icon.size, icon.size))
		for y := 0; y < icon.size; y++ {
			for x := 0; x < icon.size; x++ {
				px, py := float64(x)/float64(icon.size), float64(y)/float64(icon.size)
				fill := color.NRGBA{17, 25, 35, 255}
				if icon.mono {
					fill = color.NRGBA{}
				}
				for _, left := range []float64{.18, .6} {
					for _, top := range []float64{.18, .6} {
						if px >= left && px <= left+.22 && py >= top && py <= top+.22 {
							fill = color.NRGBA{82, 200, 152, 255}
							if icon.mono {
								fill = color.NRGBA{255, 255, 255, 255}
							}
						}
					}
				}
				img.SetNRGBA(x, y, fill)
			}
		}
		f, err := os.OpenFile(filepath.Join(assets, icon.name+".png"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = png.Encode(f, img)
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return err
		}
	}
	return syncTree(path)
}

func createProfile(path, name, device, profileID string) error {
	page := uuid()
	defaultPage := uuid()
	pagePath := filepath.Join(path, "Profiles", page)
	if err := ensureDir(pagePath); err != nil {
		return err
	}
	manifest := map[string]any{"Version": "3.0", "Name": "Patchbay — " + name, "Device": map[string]string{"Model": modelDeckPlus, "UUID": device}, "Pages": map[string]any{"Current": page, "Default": defaultPage, "Pages": []string{page}}}
	keys, dials := map[string]any{}, map[string]any{}
	labels := map[string]string{"key-1": "Try button", "key-2": "Confirm", "key-3": "Policy demo", "dial-1": "Level"}
	if name == "benchmark" {
		labels = map[string]string{"key-1": "Capture", "key-2": "Baseline", "key-3": "Result", "dial-1": "Iterations", "dial-2": "Repeats"}
	}
	for control, label := range labels {
		var n int
		if _, err := fmt.Sscanf(control, "key-%d", &n); err == nil {
			n--
			keys[fmt.Sprintf("%d,%d", n%4, n/4)] = profileAction(control, label)
		} else {
			_, _ = fmt.Sscanf(control, "dial-%d", &n)
			dials[fmt.Sprintf("%d,0", n-1)] = profileAction(control, label)
		}
	}
	pageManifest := map[string]any{"Name": "Patchbay", "Icon": "", "Controllers": []any{map[string]any{"Type": "Keypad", "Actions": keys}, map[string]any{"Type": "Encoder", "Actions": dials}}}
	defaultPath := filepath.Join(path, "Profiles", defaultPage, "manifest.json")
	if err := ensureDir(filepath.Dir(defaultPath)); err != nil {
		return err
	}
	if err := writeFile(defaultPath, []byte(`{"Name":"","Icon":"","Controllers":null}`), 0600); err != nil {
		return err
	}
	for p, v := range map[string]any{filepath.Join(path, "manifest.json"): manifest, filepath.Join(pagePath, "manifest.json"): pageManifest} {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if err = writeFile(p, b, 0600); err != nil {
			return err
		}
	}
	return syncTree(path)
}
func profileAction(control, label string) map[string]any {
	return map[string]any{"ActionID": uuid(), "UUID": "local.patchbay.deckd.control", "Name": "Patchbay Control", "Plugin": map[string]string{"Name": "Patchbay", "UUID": pluginID, "Version": "0.2.0.0"}, "Settings": map[string]string{"control": control, "label": label}, "LinkedTitle": false, "State": 0, "States": []any{map[string]any{"ShowTitle": false}}, "Resources": nil}
}
