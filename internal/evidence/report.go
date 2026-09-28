package evidence

import (
	"bytes"
	"encoding/json"
	"html/template"

	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{"json": func(v any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v)
	return buffer.String()
}}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><meta name="referrer" content="no-referrer"><title>{{.Title}}</title><style>body{font:16px system-ui,sans-serif;max-width:1000px;margin:40px auto;padding:0 24px;color:#172a31;background:#f6f7f4}h1,h2{line-height:1.15}section{background:white;border:1px solid #ccd5d2;border-radius:12px;padding:24px;margin:24px 0}table{border-collapse:collapse;width:100%}th,td{text-align:left;padding:10px;border-bottom:1px solid #ddd}pre{white-space:pre-wrap;overflow-wrap:anywhere}small{color:#485c62}</style></head><body><h1>{{.Title}}</h1><p>Saved observations from Patchbay. Timings and source revisions describe these runs; they do not establish statistical significance or complete reproducibility.</p>
{{range .Runs}}<section><h2>{{.Title}}</h2><p>{{.Origin}} · {{.State}} · {{.CreatedAt}}</p><small>Run {{.ID}} · experiment {{.Experiment}}</small><h3>Measurements</h3><table><thead><tr><th>Name</th><th>Value</th><th>Unit</th><th>Status</th><th>Reason</th></tr></thead><tbody>{{range .Measurements}}<tr><td>{{.Name}}</td><td>{{if .Value}}{{.Value}}{{else}}Unavailable{{end}}</td><td>{{.Unit}}</td><td>{{.Status}}</td><td>{{.Reason}}</td></tr>{{end}}</tbody></table>
{{if .Parameters}}<h3>Selected inputs</h3><pre>{{json .Parameters}}</pre>{{end}}{{if .Note}}<h3>Note</h3><p>{{.Note}}</p>{{end}}{{if .Logs}}<h3>Selected text outputs</h3><pre>{{json .Logs}}</pre>{{end}}{{if .SourceStart}}<h3>Observed source context</h3><pre>{{json .SourceStart}}
{{json .SourceEnd}}</pre><p>Source changed: {{.SourceChanged}}</p>{{end}}
{{range .Series}}<h3>{{.Name}}</h3><p>{{.Quality}} · X: {{.XUnit}} · Y: {{.YUnit}}</p><details><summary>Complete series data</summary><pre>{{json .}}</pre></details>{{end}}</section>{{end}}
{{if .Comparison}}<section><h2>Comparison</h2><p>Delta is candidate − baseline. Units must match exactly; a zero baseline has no percent delta.</p><pre>{{json .Comparison}}</pre></section>{{end}}<footer>Patchbay · offline report · schema {{.SchemaVersion}}</footer></body></html>`))

func RenderReport(document protocol.ExportDocument, format string) (protocol.ExportFile, error) {
	var data []byte
	var err error
	media := "application/json"
	switch format {
	case "json":
		data, err = json.MarshalIndent(document, "", "  ")
	case "html":
		var buffer bytes.Buffer
		err = reportTemplate.Execute(&buffer, document)
		data = buffer.Bytes()
		media = "text/html; charset=utf-8"
	default:
		return protocol.ExportFile{}, fault.New(protocol.InvalidRequest, "Export format must be json or html.")
	}
	if err != nil {
		return protocol.ExportFile{}, fault.New(protocol.Internal, "Cannot render export.")
	}
	if len(data) > 16<<20 {
		return protocol.ExportFile{}, fault.New(protocol.StorageFull, "Export exceeds 16 MiB.")
	}
	return protocol.ExportFile{MediaType: media, SHA256: Digest(data), Data: data}, nil
}
