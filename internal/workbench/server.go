// Package workbench serves an explicitly launched browser client on loopback.
// The daemon remains reachable only over its private Unix socket.
package workbench

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"patchbay/internal/client"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/recipe"
	"patchbay/pkg/protocol"
)

//go:embed assets/*
var assets embed.FS

type Server struct {
	ownedDaemon bool
	socket      string
	uploads     chan struct{}
	listener    net.Listener
	http        *http.Server
	client      *client.Client
	token       string
	origin      string
	quit        chan struct{}
	once        sync.Once
	cancel      context.CancelFunc
}

func Start(socket string) (*Server, error) { return StartOwned(socket, false) }
func StartOwned(socket string, owned bool) (*Server, error) {
	c, err := client.New(client.Options{Socket: socket, Timeout: 5 * time.Second, MaxResponseBytes: 32 << 20})
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		c.Close()
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		_ = listener.Close()
		c.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{socket: socket, uploads: make(chan struct{}, 2), ownedDaemon: owned, listener: listener, client: c, token: base64.RawURLEncoding.EncodeToString(secret), origin: "http://" + listener.Addr().String(), quit: make(chan struct{}), cancel: cancel}
	s.http = &http.Server{Handler: s, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = s.http.Serve(listener); s.once.Do(func() { close(s.quit) }) }()
	return s, nil
}
func (s *Server) URL() string           { return s.origin + "/#" + s.token }
func (s *Server) Origin() string        { return s.origin }
func (s *Server) Done() <-chan struct{} { return s.quit }
func (s *Server) Close() error {
	s.cancel()
	s.once.Do(func() { close(s.quit) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.http.Shutdown(ctx)
	if err != nil {
		_ = s.http.Close()
	}
	s.client.Close()
	return err
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	fail := func(code int, message string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: protocol.Error{Code: protocol.InvalidRequest, Message: message}})
	}
	if r.Host != strings.TrimPrefix(s.origin, "http://") {
		fail(403, "Unexpected Host.")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
		fail(403, "Unexpected Origin.")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		fail(403, "Cross-site requests are disabled.")
		return
	}
	static := map[string]struct{ file, media string }{"/": {"index.html", "text/html; charset=utf-8"}, "/app.js": {"app.js", "text/javascript; charset=utf-8"}, "/agents.js": {"agents.js", "text/javascript; charset=utf-8"}, "/recipes.js": {"recipes.js", "text/javascript; charset=utf-8"}, "/style.css": {"style.css", "text/css; charset=utf-8"}}
	if asset, ok := static[r.URL.Path]; ok {
		if r.Method != "GET" || r.URL.RawQuery != "" {
			fail(405, "Unsupported asset request.")
			return
		}
		data, err := assets.ReadFile("assets/" + asset.file)
		if err != nil {
			fail(500, "Workbench asset is unavailable.")
			return
		}
		w.Header().Set("Content-Type", asset.media)
		_, _ = w.Write(data)
		return
	}
	if len(r.Header.Values("Authorization")) != 1 || !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) {
		fail(401, "Launch a new workbench session to authorize this tab.")
		return
	}
	upload := r.Method == "POST" && r.URL.Path == "/api/recipes/imports"
	media := "application/json"
	if upload {
		media = "application/zip"
	}
	mutation := r.Method != "GET"
	if mutation && (r.Header.Get("Origin") != s.origin || r.Header.Get("Content-Type") != media) {
		fail(403, "Mutations require the session origin and the route-specific content type.")
		return
	}
	if r.URL.Path == "/api/session" && r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Owned bool `json:"owned_daemon"`
		}{s.ownedDaemon})
		return
	}
	if r.URL.Path == "/session/quit" && r.Method == "POST" {
		if r.ContentLength != 0 {
			fail(400, "Quit accepts no body.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"closed":true}`))
		s.once.Do(func() { close(s.quit) })
		return
	}
	path, ok := allowed(r.Method, r.URL.Path)
	if !ok {
		fail(404, "Route is not exposed by the workbench.")
		return
	}
	if upload {
		s.uploadRecipe(w, r, fail)
		return
	}
	var request any
	if mutation && r.Method != "DELETE" {
		data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			fail(413, "Request exceeds workbench limit.")
			return
		}
		var object map[string]any
		if jsonstrict.Decode(data, &object) != nil {
			fail(400, "Invalid JSON request.")
			return
		}
		request = object
	} else if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		fail(400, "This request accepts no body.")
		return
	}
	var value map[string]any
	if err := s.client.CallQuery(r.Context(), r.Method, path, r.URL.Query(), request, &value); err != nil {
		var known *protocol.Error
		w.Header().Set("Content-Type", "application/json")
		if errors.As(err, &known) {
			w.WriteHeader(known.Code.HTTPStatus())
			_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: *known})
		} else {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: protocol.Error{Code: protocol.ProviderUnavailable, Message: "The daemon is unavailable. Reconnect after checking its socket and status."}})
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func allowed(method, path string) ([]string, bool) {
	if !strings.HasPrefix(path, "/api/") {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	for _, part := range parts {
		if part == "" || len(part) > 128 || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			return nil, false
		}
	}
	key := strings.Join(parts, "/")
	if len(parts) == 1 {
		if method == "GET" && contains([]string{"capabilities", "status", "context", "projects", "parameters", "experiments", "storage", "runs", "samples", "recipes", "actions"}, key) {
			return parts, true
		}
		if method == "POST" && contains([]string{"captures", "comparisons", "exports"}, key) {
			return parts, true
		}
	}
	if len(parts) == 2 {
		if method == "POST" && (key == "captures/prepare" || key == "exports/prepare" || key == "recipes/imports") {
			return parts, true
		}
		if method == "PUT" && key == "context/project" {
			return parts, true
		}
		if parts[0] == "recipes" && method == "GET" {
			return parts, true
		}
		if parts[0] == "parameters" && (method == "GET" || method == "PUT") {
			return parts, true
		}
		if parts[0] == "runs" && (method == "GET" || method == "DELETE") {
			return parts, true
		}
		if parts[0] == "baselines" && (method == "GET" || method == "PUT") {
			return parts, true
		}
		if parts[0] == "jobs" && (method == "GET" || method == "DELETE") {
			return parts, true
		}
	}
	if key == "agents/catalog" && method == "GET" || key == "agents/context/prepare" && method == "POST" || key == "agents/sessions" && (method == "GET" || method == "POST") {
		return parts, true
	}
	if len(parts) == 3 && parts[0] == "agents" && parts[1] == "sessions" && (method == "GET" || method == "DELETE") {
		return parts, true
	}
	if len(parts) == 4 && parts[0] == "agents" && parts[1] == "sessions" && parts[3] == "forget" && method == "POST" {
		return parts, true
	}
	if len(parts) == 3 && parts[0] == "recipes" && method == "POST" && contains([]string{"prepare", "commit", "export"}, parts[2]) {
		return parts, true
	}
	if len(parts) == 4 && parts[0] == "recipes" && parts[2] == "export" && parts[3] == "prepare" && method == "POST" {
		return parts, true
	}
	if len(parts) == 3 && parts[0] == "runs" && parts[2] == "annotation" && method == "PUT" {
		return parts, true
	}
	if len(parts) == 4 && parts[0] == "runs" && parts[2] == "artifacts" && method == "GET" {
		return parts, true
	}
	return nil, false
}
func contains(items []string, item string) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

func (s *Server) uploadRecipe(w http.ResponseWriter, r *http.Request, fail func(int, string)) {
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		fail(503, "Two recipe uploads are already in progress.")
		return
	}
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(30 * time.Second)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	defer func() { _ = controller.SetReadDeadline(time.Time{}); _ = controller.SetWriteDeadline(time.Time{}) }()
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	data, err := io.ReadAll(io.LimitReader(r.Body, recipe.MaxPackage+1))
	if err != nil || len(data) > recipe.MaxPackage {
		fail(413, "Recipe upload must be a complete ZIP of at most 20 MiB.")
		return
	}
	c, err := client.New(client.Options{Socket: s.socket, Timeout: 30 * time.Second, MaxResponseBytes: 32 << 20})
	if err != nil {
		fail(503, "Cannot connect to daemon.")
		return
	}
	defer c.Close()
	var value recipe.ImportResult
	if err := c.Upload(ctx, []string{"recipes", "imports"}, r.URL.Query(), data, &value); err != nil {
		var known *protocol.Error
		if errors.As(err, &known) {
			fail(known.Code.HTTPStatus(), known.Message)
		} else {
			fail(503, "Upload response lost. Inspect installed recipes before retrying; identical content is idempotent.")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
