package api

import (
	"net/http"
	"patchbay/internal/fault"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
)

func (h *Handler) agentRoutes() {
	h.route("/v1/agents/catalog", "GET", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, h.runtime.AgentCatalog(r.Context())) })
	h.route("/v1/agents/context/prepare", "POST", func(w http.ResponseWriter, r *http.Request) {
		var request supervisor.Selection
		if !h.decode(w, r, &request) {
			return
		}
		value, err := h.runtime.PrepareAgentContext(r.Context(), request)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/agents/sessions", "GET, POST", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			value, err := h.runtime.AgentSessions(r.URL.Query().Get("project"), r.URL.Query().Get("cursor"))
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, value)
			return
		}
		var request supervisor.Start
		if !h.decode(w, r, &request) {
			return
		}
		value, err := h.runtime.StartAgent(r.Context(), request)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/agents/sessions/{id}", "GET, DELETE", func(w http.ResponseWriter, r *http.Request) {
		var value supervisor.Session
		var err error
		if r.Method == "DELETE" {
			value, err = h.runtime.CancelAgent(r.Context(), r.PathValue("id"))
		} else {
			value, err = h.runtime.AgentSession(r.PathValue("id"))
		}
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/agents/sessions/{id}/forget", "POST", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Confirmed bool `json:"confirmed"`
		}
		if !h.decode(w, r, &request) {
			return
		}
		if !request.Confirmed {
			failure(w, fault.New(protocol.ConfirmationRequired, "Explicitly confirm forgetting this terminal session; linked runs remain."))
			return
		}
		if err := h.runtime.ForgetAgent(r.Context(), r.PathValue("id")); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]string{"forgotten": r.PathValue("id")})
	})
}
