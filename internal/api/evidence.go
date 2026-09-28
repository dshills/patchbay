package api

import (
	"net/http"
	"strconv"

	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/pkg/protocol"
)

func (h *Handler) evidenceRoutes() {
	h.route("/v1/samples", "GET", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, evidence.Samples()) })
	h.route("/v1/exports/prepare", "POST", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.ExportPrepare
		if !h.decode(w, r, &req) {
			return
		}
		value, err := h.runtime.PrepareExport(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/exports", "POST", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.ExportRequest
		if !h.decode(w, r, &req) {
			return
		}
		value, err := h.runtime.Export(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})

	h.route("/v1/captures", "POST", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.CaptureRequest
		if !h.decode(w, r, &req) {
			return
		}
		value, err := h.runtime.Capture(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 202, value)
	})
	h.route("/v1/comparisons", "POST", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.ComparisonRequest
		if !h.decode(w, r, &req) {
			return
		}
		value, err := h.runtime.Compare(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})

	h.route("/v1/capabilities", "GET", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, h.runtime.Capabilities()) })
	h.route("/v1/experiments", "GET", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, h.runtime.Experiments()) })
	h.route("/v1/captures/prepare", "POST", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.CapturePrepare
		if !h.decode(w, r, &req) {
			return
		}
		value, err := h.runtime.PrepareCapture(r.Context(), req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/storage", "GET", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, h.runtime.Storage()) })
	h.route("/v1/runs", "GET", func(w http.ResponseWriter, r *http.Request) {
		store, err := h.runtime.Evidence()
		if err != nil {
			failure(w, err)
			return
		}
		q := r.URL.Query()
		limit := 0
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil || limit < 1 {
				failure(w, fault.New(protocol.InvalidRequest, "Invalid page size."))
				return
			}
		}
		value, err := store.List(q.Get("project"), q.Get("experiment"), q.Get("cursor"), limit)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/runs/{id}", "GET, DELETE", func(w http.ResponseWriter, r *http.Request) {
		store, err := h.runtime.Evidence()
		if err != nil {
			failure(w, err)
			return
		}
		if r.Method == "DELETE" {
			if err := store.Delete(r.PathValue("id"), r.URL.Query().Get("acknowledge") == "true"); err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, struct {
				Deleted bool `json:"deleted"`
			}{true})
			return
		}
		value, err := store.Get(r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/runs/{id}/annotation", "PUT", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.AnnotationUpdate
		if !h.decode(w, r, &req) {
			return
		}
		store, err := h.runtime.Evidence()
		if err != nil {
			failure(w, err)
			return
		}
		value, err := store.Annotate(r.PathValue("id"), req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/runs/{id}/artifacts/{artifact}", "GET", func(w http.ResponseWriter, r *http.Request) {
		store, err := h.runtime.Evidence()
		if err != nil {
			failure(w, err)
			return
		}
		data, artifact, err := store.Artifact(r.PathValue("id"), r.PathValue("artifact"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, protocol.ArtifactContent{Artifact: artifact, Data: data})
	})
	h.route("/v1/baselines/{experiment}", "GET, PUT", func(w http.ResponseWriter, r *http.Request) {
		store, err := h.runtime.Evidence()
		if err != nil {
			failure(w, err)
			return
		}
		project := r.URL.Query().Get("project")
		experiment := r.PathValue("experiment")
		if r.Method == "GET" {
			respond(w, 200, store.Baseline(project, experiment))
			return
		}
		var req protocol.BaselineUpdate
		if !h.decode(w, r, &req) {
			return
		}
		value, err := h.runtime.SetBaseline(project, experiment, req)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
}
