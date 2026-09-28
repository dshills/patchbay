package recipe

import (
	"archive/zip"
	"bytes"
	"context"
	"time"

	"go.yaml.in/yaml/v3"
	"patchbay/internal/evidence"
)

// Build is for explicitly selected portable definitions, never a host config dump.
// It inventories exact bytes and runs the same verifier used for imported packages.
func Build(m Manifest, payloads map[string][]byte) (*Package, error) {
	if len(payloads) >= MaxFiles {
		return nil, fail("$", "too many payload files")
	}
	total := 0
	for _, data := range payloads {
		total += len(data)
		if len(data) > MaxFile || total > MaxPackage-MaxManifest {
			return nil, fail("$", "payload size limit exceeded")
		}
	}
	m.Inventory = []File{}
	files := map[string][]byte{}
	for _, name := range sortedKeys(payloads) {
		data := payloads[name]
		media := "text/plain"
		if len(name) > 5 && name[:5] == "docs/" || name == "README.md" {
			media = "text/markdown"
		}
		if len(name) > 8 && name[:8] == "samples/" {
			media = "application/json"
		}
		m.Inventory = append(m.Inventory, File{Path: name, MediaType: media, Size: int64(len(data)), SHA256: evidence.Digest(data)})
		files[name] = bytes.Clone(data)
	}
	m.Digest = CanonicalDigest(m)
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	verified, err := parseManifest(data)
	if err != nil {
		return nil, err
	}
	files["recipe.yaml"] = data
	return finish(verified, files)
}

// ZIP has deterministic order, timestamp and permissions. It re-imports its
// output before returning it; no external archive program is used.
func ZIP(ctx context.Context, p *Package) ([]byte, error) {
	var output []byte
	_, err := withImport(ctx, func(ctx context.Context) (*Package, error) {
		if p == nil || len(p.Files) > MaxFiles || len(p.Files) != len(p.Manifest.Inventory)+1 {
			return nil, fail("$", "invalid package inventory")
		}
		if err := validate(p.Manifest); err != nil {
			return nil, err
		}
		if _, err := parseManifest(p.Files["recipe.yaml"]); err != nil {
			return nil, err
		}
		if _, err := verifyFiles(p.Manifest, p.Files); err != nil {
			return nil, err
		}
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for _, name := range sortedKeys(p.Files) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			header.SetMode(0644)
			header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
			stream, err := writer.CreateHeader(header)
			if err != nil {
				return nil, err
			}
			data := p.Files[name]
			for len(data) > 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				n := min(len(data), 64<<10)
				if _, err := stream.Write(data[:n]); err != nil {
					return nil, err
				}
				data = data[n:]
			}
			if buffer.Len() > MaxPackage {
				return nil, fail("$", "archive exceeds compressed limit")
			}
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		output = buffer.Bytes()
		return readZIP(ctx, output)
	})
	return output, err
}
