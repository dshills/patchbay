package streamdeck

import "embed"

// PluginFiles includes the installer assets in deckctl, so setup needs no SDK,
// Python, build tool, or separately downloaded plugin archive.
//
//go:embed plugin
var PluginFiles embed.FS
