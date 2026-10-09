package aictl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func backend(name string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, name+" "+r.URL.Path+" "+string(body))
	}))
}

func TestGatewayRoutesByModel(t *testing.T) {
	a, b := backend("a"), backend("b")
	defer a.Close()
	defer b.Close()
	g := &Gateway{}
	g.SetRoutes(map[string][]string{"qwen": {strings.TrimPrefix(a.URL, "http://"), strings.TrimPrefix(b.URL, "http://")}, "idle": nil})
	gw := httptest.NewServer(g)
	defer gw.Close()

	post := func(body string) (int, string) {
		resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}
	req := `{"model":"qwen","messages":[]}`
	_, first := post(req)
	_, second := post(req)
	if !strings.HasPrefix(first, "a /v1/chat/completions "+req) || !strings.HasPrefix(second, "b ") {
		t.Fatalf("not round robin with the body passed on: %q, %q", first, second)
	}
	if code, _ := post(`{"model":"nope"}`); code != http.StatusNotFound {
		t.Fatalf("unknown model: %d", code)
	}
	if code, _ := post(`{"model":"idle"}`); code != http.StatusServiceUnavailable {
		t.Fatalf("model with no replica: %d", code)
	}
	if code, _ := post(`not json`); code != http.StatusBadRequest {
		t.Fatalf("no model named: %d", code)
	}
	resp, err := http.Get(gw.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(list), `"id":"qwen"`) || strings.Contains(string(list), "idle") {
		t.Fatalf("models: %s", list)
	}
}
