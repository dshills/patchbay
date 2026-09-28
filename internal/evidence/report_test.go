package evidence

import (
	"strings"
	"testing"

	"patchbay/pkg/protocol"
)

func TestReportEscapesUntrustedTextAndStaysOffline(t *testing.T) {
	document := protocol.ExportDocument{SchemaVersion: 1, Title: `<script>alert('x')</script>`, Runs: []protocol.ExportRun{{Title: `<img src="https://evil.test/">`, Note: `</p><script>bad()</script>`, Measurements: []protocol.Measurement{}, Series: []protocol.Series{}}}}
	file, err := RenderReport(document, "html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(file.Data)
	if strings.Contains(html, "<script>") || strings.Contains(html, "<img src=") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "default-src 'none'") {
		t.Fatal("unsafe report HTML")
	}
	if Digest(file.Data) != file.SHA256 {
		t.Fatal("wrong report hash")
	}
	if _, err := RenderReport(document, "javascript"); err == nil {
		t.Fatal("unsupported export accepted")
	}
}
