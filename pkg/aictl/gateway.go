package aictl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// MaxRequest bounds a request's body, prompts and images included.
const MaxRequest = 32 << 20

// Gateway is the cluster's OpenAI-compatible endpoint: it reads the model a
// request names and passes the request to a node serving it, in turn.
type Gateway struct {
	mu       sync.Mutex
	routes   map[string][]string // model -> "address:port" of ready replicas
	next     map[string]int
	inFlight map[string]int // "address:port" -> requests it is answering
}

// SetRoutes replaces where each model is served.
func (g *Gateway) SetRoutes(routes map[string][]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.routes = routes
}

// SetModelRoutes replaces where one model is served.
func (g *Gateway) SetModelRoutes(model string, backends []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.routes == nil {
		g.routes = map[string][]string{}
	}
	g.routes[model] = backends
}

// InFlight counts the requests a replica is answering.
func (g *Gateway) InFlight(backend string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight[backend]
}

// pick is the next replica of a model, round robin, other than those
// tried already.
func (g *Gateway) pick(model string, tried map[string]bool) (string, bool, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	backends, known := g.routes[model]
	if g.next == nil {
		g.next, g.inFlight = map[string]int{}, map[string]int{}
	}
	for range backends {
		i := g.next[model] % len(backends)
		g.next[model] = i + 1
		if !tried[backends[i]] {
			g.inFlight[backends[i]]++
			return backends[i], true, true
		}
	}
	return "", known, false
}

func (g *Gateway) done(backend string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inFlight[backend]--
}

func (g *Gateway) models() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for m, b := range g.routes {
		if len(b) > 0 {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health":
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
		var data []map[string]any
		for _, m := range g.models() {
			data = append(data, map[string]any{"id": m, "object": "model", "owned_by": "kuberoot"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	case r.Method == http.MethodPost && len(r.URL.Path) > 4 && r.URL.Path[:4] == "/v1/":
		g.forward(w, r)
	default:
		apiError(w, http.StatusNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path)
	}
}

// forward passes a request to a replica of the model it names; answers
// stream back as the model writes them.
func (g *Gateway) forward(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxRequest+1))
	if err != nil || len(body) > MaxRequest {
		apiError(w, http.StatusRequestEntityTooLarge, "the request is too large")
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
		apiError(w, http.StatusBadRequest, "the request names no model")
		return
	}
	tried := map[string]bool{}
	for {
		backend, known, ok := g.pick(req.Model, tried)
		switch {
		case !known:
			apiError(w, http.StatusNotFound, fmt.Sprintf("the model %q does not exist", req.Model))
			return
		case !ok && len(tried) > 0:
			apiError(w, http.StatusBadGateway, fmt.Sprintf("no replica of the model %q answered", req.Model))
			return
		case !ok:
			apiError(w, http.StatusServiceUnavailable, fmt.Sprintf("the model %q has no replica ready", req.Model))
			return
		}
		tried[backend] = true
		// A replica that cannot be reached has written nothing back: the
		// request goes to the next one.
		unreachable := false
		target := &url.URL{Scheme: "http", Host: backend}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(p *httputil.ProxyRequest) {
				p.SetURL(target)
				p.Out.Body = io.NopCloser(bytes.NewReader(body))
				p.Out.ContentLength = int64(len(body))
			},
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				var op *net.OpError
				if errors.As(err, &op) && op.Op == "dial" {
					unreachable = true
					return
				}
				apiError(w, http.StatusBadGateway, "the replica did not answer: "+err.Error())
			},
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		proxy.ServeHTTP(rec, r)
		g.done(backend)
		if unreachable {
			klog.Warningf("%s %s model=%s replica=%s unreachable, trying another", r.Method, r.URL.Path, req.Model, backend)
			continue
		}
		klog.Infof("%s %s model=%s replica=%s status=%d %s", r.Method, r.URL.Path, req.Model, backend, rec.status, time.Since(start).Round(time.Millisecond))
		return
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// apiError answers as the OpenAI API does, for clients to show the reason.
func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"message": msg, "type": http.StatusText(code), "code": code}})
}
