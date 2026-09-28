package api

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"patchbay/internal/fault"
	"patchbay/internal/job"
	"patchbay/internal/jsonstrict"
	runtimecore "patchbay/internal/runtime"
	"patchbay/pkg/protocol"
)

type Handler struct {
	runtime *runtimecore.Runtime
	mux     *http.ServeMux
}

func NewHandler(runtime *runtimecore.Runtime) http.Handler {
	h := &Handler{runtime: runtime, mux: http.NewServeMux()}
	h.evidenceRoutes()
	h.recipeRoutes()
	h.route("/v1/status", "GET", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, runtime.Status()) })
	h.route("/v1/context", "GET, PATCH", h.context)
	h.route("/v1/context/project", "PUT", h.projectSelection)
	h.route("/v1/projects", "GET", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, protocol.ProjectList{Projects: runtime.Projects()})
	})
	h.route("/v1/projects/{id}", "GET", h.project)
	h.route("/v1/actions", "GET", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, protocol.ActionList{Actions: runtime.Actions()})
	})
	h.route("/v1/actions/{name}", "GET, POST", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			h.invoke(w, r, false)
			return
		}
		value, err := runtime.Action(r.PathValue("name"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/workflows", "GET", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, protocol.WorkflowList{Workflows: runtime.Workflows()})
	})
	h.route("/v1/workflows/{name}", "POST", func(w http.ResponseWriter, r *http.Request) { h.invoke(w, r, true) })
	h.route("/v1/jobs", "GET", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, wireJobs(runtime.Jobs().List()))
	})
	h.route("/v1/jobs/{id}", "GET, DELETE", h.job)
	h.route("/v1/parameters", "GET", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, wireParameters(runtime.Parameters()))
	})
	h.route("/v1/parameters/{name}", "GET, PUT", h.parameter)
	h.route("/v1/events", "POST", h.control)
	h.route("/v1/controls/snapshot", "POST", h.controls)
	h.route("/v1/config/reload", "POST", h.reload)
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		failure(w, fault.New(protocol.NotFound, "Route not found."))
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) route(path, methods string, handler http.HandlerFunc) {
	h.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		allowed := false
		for _, method := range strings.Split(methods, ", ") {
			if method == r.Method {
				allowed = true
			}
		}
		if !allowed {
			w.Header().Set("Allow", methods)
			respond(w, 405, protocol.ErrorResponse{Error: protocol.Error{Code: protocol.InvalidRequest, Message: "Unsupported HTTP method."}})
			return
		}
		if (r.Method == "GET" || r.Method == "DELETE") && (r.ContentLength > 0 || len(r.TransferEncoding) > 0) {
			failure(w, fault.New(protocol.InvalidRequest, "This operation does not accept a body."))
			return
		}
		handler(w, r)
	})
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			failure(w, fault.New(protocol.InvalidRequest, "Expected application/json."))
			return false
		}
	}
	limit := h.runtime.Settings().MaxRequestBytes
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		failure(w, fault.New(protocol.InvalidRequest, "Cannot read request body."))
		return false
	}
	if int64(len(data)) > limit {
		respond(w, 413, protocol.ErrorResponse{Error: protocol.Error{Code: protocol.InvalidRequest, Message: "Request body exceeds limit."}})
		return false
	}
	if err := jsonstrict.Decode(data, value); err != nil {
		failure(w, fault.New(protocol.InvalidRequest, "Malformed or unsupported JSON request."))
		return false
	}
	// Zero means omitted internally; an explicitly supplied timeout must be positive.
	if invocation, ok := value.(*protocol.Invocation); ok && invocation.TimeoutMS == 0 {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(data, &fields)
		if _, exists := fields["timeout_ms"]; exists {
			failure(w, fault.New(protocol.InvalidRequest, "Timeout must be positive when supplied."))
			return false
		}
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		status = 500
		data = []byte(`{"error":{"code":"internal","message":"Cannot encode response."}}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
func failure(w http.ResponseWriter, err error) {
	safe := fault.Safe(err)
	respond(w, safe.Code.HTTPStatus(), protocol.ErrorResponse{Error: *safe})
}

func (h *Handler) context(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		respond(w, 200, wireContext(h.runtime.Context()))
		return
	}
	var patch protocol.ContextPatch
	if !h.decode(w, r, &patch) {
		return
	}
	value, err := h.runtime.PatchContext(r.Context(), patch)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, wireContext(value))
}
func (h *Handler) projectSelection(w http.ResponseWriter, r *http.Request) {
	var selection struct {
		Project *string `json:"project"`
	}
	if !h.decode(w, r, &selection) {
		return
	}
	if selection.Project == nil {
		failure(w, fault.New(protocol.InvalidRequest, "Project field is required."))
		return
	}
	value, err := h.runtime.PatchContext(r.Context(), protocol.ContextPatch{Project: selection.Project})
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, wireContext(value))
}
func (h *Handler) project(w http.ResponseWriter, r *http.Request) {
	for _, project := range h.runtime.Projects() {
		if project.ID == r.PathValue("id") {
			respond(w, 200, project)
			return
		}
	}
	failure(w, fault.New(protocol.ProjectNotFound, "Project not found."))
}
func (h *Handler) parameter(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if r.Method == "GET" {
		value, err := h.runtime.Parameter(name)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, wireParameter(value))
		return
	}
	var request protocol.ParameterSet
	if !h.decode(w, r, &request) {
		return
	}
	value, err := h.runtime.SetParameter(r.Context(), name, request.Value)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, wireParameter(value))
}
func (h *Handler) invoke(w http.ResponseWriter, r *http.Request, isWorkflow bool) {
	var request protocol.Invocation
	if !h.decode(w, r, &request) {
		return
	}
	handle, err := h.runtime.Invoke(r.Context(), r.PathValue("name"), isWorkflow, request)
	if err != nil {
		failure(w, err)
		return
	}
	if request.Mode != protocol.Sync {
		respond(w, 202, protocol.InvocationResponse{JobID: handle.ID})
		return
	}
	completed, err := handle.Wait(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	status := 200
	if completed.Error != nil {
		status = completed.Error.Code.HTTPStatus()
	}
	respond(w, status, protocol.InvocationResponse{JobID: handle.ID, Result: wireResult(completed.Result), Error: completed.Error})
}
func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	var value job.Job
	var err error
	if r.Method == "GET" {
		value, err = h.runtime.Jobs().Get(r.PathValue("id"))
	} else {
		value, err = h.runtime.Jobs().Cancel(r.PathValue("id"))
	}
	if err != nil {
		failure(w, err)
		return
	}
	status := 200
	if r.Method == "DELETE" && value.State == job.Running {
		status = 202
	}
	respond(w, status, wireJob(value))
}
func (h *Handler) control(w http.ResponseWriter, r *http.Request) {
	var request protocol.EventRequest
	if !h.decode(w, r, &request) {
		return
	}
	value, err := h.runtime.Control(r.Context(), request)
	if err != nil {
		failure(w, err)
		return
	}
	status := 200
	if value.JobID != "" {
		status = 202
	}
	respond(w, status, value)
}
func (h *Handler) controls(w http.ResponseWriter, r *http.Request) {
	var request protocol.ControlSnapshotRequest
	if !h.decode(w, r, &request) {
		return
	}
	value, err := h.runtime.ControlSnapshot(r.Context(), request)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, value)
}
func (h *Handler) reload(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if !h.decode(w, r, &empty) {
		return
	}
	generation, err := h.runtime.Reload(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, protocol.ReloadResponse{Generation: generation})
}
