package pandaxng

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBraveLightpandaSnippets(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	// Current Brave cards use generic-snippet/content; description may belong
	// to a nested sitelink and must not override the main result's snippet.
	html := `<!doctype html><html><head><title>Brave fixture</title></head><body>
<div id="web-results">
  <div class="snippet" data-type="web">
    <a href="https://example.com/"><div class="title">Main result</div></a>
    <div class="result-cluster"><div class="snippet"><a href="https://example.com/docs">Docs</a><div class="description">Wrong sitelink description</div></div></div>
    <div class="generic-snippet"><div class="content"> Main <strong>result</strong> description </div></div>
  </div>
  <div class="snippet" data-type="web">
    <a href="https://second.example/"><div class="title">Second result</div></a>
    <div class="generic-snippet"><div class="content">Second description</div></div>
  </div>
  <div class="snippet"><a class="heading-serpresult" href="https://legacy.example/"><h3>Legacy result</h3></a><div class="snippet-description">Legacy description</div></div>
  <div class="snippet"><a href="https://fallback.example/"><h3>Fallback result</h3></a><div class="snippet-content">Fallback description</div></div>
  <div class="snippet"><a href="https://description.example/"><h3>Description result</h3></a><div class="description">Description fallback</div></div>
  <div class="snippet"><a href="https://empty.example/"><h3>No snippet</h3></a></div>
</div></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	}))
	defer srv.Close()
	options := testOptions()
	options.Timeout = 3 * time.Second
	searchURLJSON, _ := json.Marshal(searchURL("brave", "test", options.Limit))
	fixtureURLJSON, _ := json.Marshal(srv.URL)
	script := strings.Replace(makeScript("brave", "test", options), string(searchURLJSON), string(fixtureURLJSON), 1)
	if !strings.Contains(script, srv.URL) {
		t.Fatal("Brave fixture URL was not substituted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run", "-", "--log-level", "error")
	cmd.Env = browserEnv()
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Brave PandaScript failed: %v, output: %s", err, output)
	}
	var report EngineReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{URL: "https://example.com/", Title: "Main result", Content: "Main result description"},
		{URL: "https://second.example/", Title: "Second result", Content: "Second description"},
		{URL: "https://legacy.example/", Title: "Legacy result", Content: "Legacy description"},
		{URL: "https://fallback.example/", Title: "Fallback result", Content: "Fallback description"},
		{URL: "https://description.example/", Title: "Description result", Content: "Description fallback"},
		{URL: "https://empty.example/", Title: "No snippet", Content: ""},
	}
	if report.Status != "ok" || !reflect.DeepEqual(report.Results, want) {
		t.Fatalf("unexpected Brave results: %+v; want %+v", report, want)
	}
}
