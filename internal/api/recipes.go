package api

import (
	"context"
	"io"
	"net/http"
	"patchbay/internal/fault"
	"patchbay/internal/recipe"
	"patchbay/pkg/protocol"
	"time"
)

var recipeUploads = make(chan struct{}, 2)

func (h *Handler) recipeRoutes() {
	h.route("/v1/recipes/imports", "POST", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/zip" {
			failure(w, fault.New(protocol.InvalidRequest, "Expected application/zip."))
			return
		}
		select {
		case recipeUploads <- struct{}{}:
			defer func() { <-recipeUploads }()
		default:
			failure(w, fault.New(protocol.Busy, "Two recipe uploads are already in progress."))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
		defer func() { _ = http.NewResponseController(w).SetReadDeadline(time.Time{}) }()
		data, err := io.ReadAll(io.LimitReader(r.Body, recipe.MaxPackage+1))
		if err != nil || len(data) > recipe.MaxPackage {
			failure(w, fault.New(protocol.InvalidRequest, "Recipe upload exceeds 20 MiB or is incomplete."))
			return
		}
		value, err := h.runtime.ImportRecipe(ctx, data, r.URL.Query().Get("installation"), r.URL.Query().Get("alias"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/recipes", "GET", func(w http.ResponseWriter, r *http.Request) {
		value, err := h.runtime.Recipes(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/recipes/{id}", "GET", func(w http.ResponseWriter, r *http.Request) {
		value, err := h.runtime.Recipe(r.Context(), r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/recipes/{id}/prepare", "POST", func(w http.ResponseWriter, r *http.Request) {
		var request recipe.Prepare
		if !h.decode(w, r, &request) {
			return
		}
		value, err := h.runtime.PrepareRecipe(r.Context(), r.PathValue("id"), request)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
	h.route("/v1/recipes/{id}/commit", "POST", func(w http.ResponseWriter, r *http.Request) {
		var request recipe.Commit
		if !h.decode(w, r, &request) {
			return
		}
		value, err := h.runtime.CommitRecipe(r.Context(), r.PathValue("id"), request)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, value)
	})
}
