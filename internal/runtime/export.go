package runtime

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"time"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/jsonstrict"
	"patchbay/pkg/protocol"
)

type exportPreparation struct {
	preview protocol.ExportPreview
	release func()
	bytes   int
}

func (r *Runtime) expireExports() {
	for id, p := range r.exports {
		if !time.Now().Before(p.preview.ExpiresAt) {
			p.release()
			delete(r.exports, id)
		}
	}
}
func (r *Runtime) PrepareExport(ctx context.Context, request protocol.ExportPrepare) (protocol.ExportPreview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireExports()
	if r.closed || r.runs == nil {
		return protocol.ExportPreview{}, fault.New(protocol.RecordingFailed, "Run storage is unavailable.")
	}
	if len(request.Runs) < 1 || len(request.Runs) > 2 || len(request.Runs) == 2 && request.Runs[0] == request.Runs[1] {
		return protocol.ExportPreview{}, fault.New(protocol.InvalidRequest, "Select one or two distinct saved runs.")
	}
	if len(r.exports) >= 16 {
		return protocol.ExportPreview{}, fault.New(protocol.Busy, "Too many export previews; wait for one to expire.")
	}
	release, err := r.runs.Hold(request.Runs...)
	if err != nil {
		return protocol.ExportPreview{}, err
	}
	retained := false
	defer func() {
		if !retained {
			release()
		}
	}()
	document := protocol.ExportDocument{SchemaVersion: 1, Title: "Patchbay experiment report", Runs: []protocol.ExportRun{}}
	sources := []protocol.Run{}
	for _, id := range request.Runs {
		if err := ctx.Err(); err != nil {
			return protocol.ExportPreview{}, fault.Safe(err)
		}
		run, err := r.runs.Get(id)
		if err != nil {
			return protocol.ExportPreview{}, err
		}
		if !evidence.Terminal(run.State) {
			return protocol.ExportPreview{}, fault.New(protocol.RequestConflict, "Wait for capture to finish before exporting.")
		}
		sources = append(sources, run)
		exported := protocol.ExportRun{ID: run.ID, Origin: run.Origin, Experiment: run.Experiment.ID, Title: run.Experiment.Title, State: run.State, CreatedAt: run.CreatedAt, Measurements: run.Measurements, Series: []protocol.Series{}}
		if request.Options.Inputs {
			exported.Parameters = run.Parameters
		}
		if request.Options.Notes {
			exported.Note = run.Annotation.Note
		}
		if request.Options.Source {
			exported.SourceStart, exported.SourceEnd, exported.SourceChanged = run.SourceStart, run.SourceEnd, run.SourceChanged
			for _, outcome := range run.Outcomes {
				if outcome.Instrument != nil {
					exported.Instruments = append(exported.Instruments, *outcome.Instrument)
				}
			}
		}
		if request.Options.Logs {
			exported.Logs = map[string]string{}
		}
		for _, artifact := range run.Artifacts {
			if err := ctx.Err(); err != nil {
				return protocol.ExportPreview{}, fault.Safe(err)
			}
			if artifact.MediaType != "application/json" && !request.Options.Logs {
				continue
			}
			data, _, err := r.runs.Artifact(id, artifact.ID)
			if err != nil {
				return protocol.ExportPreview{}, err
			}
			if artifact.MediaType == "application/json" {
				var series protocol.Series
				if jsonstrict.Decode(data, &series) == nil && evidence.ValidSeries(series) == nil {
					exported.Series = append(exported.Series, series)
				}
			} else if request.Options.Logs {
				exported.Logs[artifact.Name] = string(data)
			}
		}
		document.Runs = append(document.Runs, exported)
	}
	if len(sources) == 2 {
		comparison := evidence.Compare(sources[0], sources[1])
		if comparison.Compatible {
			for _, left := range document.Runs[0].Series {
				for _, right := range document.Runs[1].Series {
					if left.Name == right.Name {
						comparison.Series = append(comparison.Series, evidence.CompareSeries(left, right))
					}
				}
			}
		}
		document.Comparison = &comparison
	}
	data, err := json.Marshal(document)
	total := len(data)
	for _, p := range r.exports {
		total += p.bytes
	}
	if err != nil || len(data) > 8<<20 || total > 16<<20 {
		return protocol.ExportPreview{}, fault.New(protocol.StorageFull, "Export preview exceeds its memory allowance.")
	}
	preview := protocol.ExportPreview{ID: identity.New(), Digest: evidence.Digest(data), ExpiresAt: time.Now().Add(time.Minute).UTC(), Document: document}
	if r.exports == nil {
		r.exports = map[string]exportPreparation{}
	}
	r.exports[preview.ID] = exportPreparation{preview: preview, release: release, bytes: len(data)}
	retained = true
	return evidence.Clone(preview), nil
}
func (r *Runtime) Export(ctx context.Context, request protocol.ExportRequest) (protocol.ExportFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireExports()
	if r.closed {
		return protocol.ExportFile{}, fault.New(protocol.ShuttingDown, "Daemon is shutting down.")
	}
	prep, ok := r.exports[request.Preparation]
	if !ok || !hmac.Equal([]byte(prep.preview.Digest), []byte(request.Digest)) {
		return protocol.ExportFile{}, fault.New(protocol.StalePreparation, "Export preview expired or changed; review a fresh preview.")
	}
	if err := ctx.Err(); err != nil {
		return protocol.ExportFile{}, fault.Safe(err)
	}
	file, err := evidence.RenderReport(prep.preview.Document, request.Format)
	if err != nil {
		return protocol.ExportFile{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.ExportFile{}, fault.Safe(err)
	}
	prep.release()
	delete(r.exports, request.Preparation)
	return file, nil
}
