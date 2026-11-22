package pandaxng

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdblockBlocksTrackingRequest(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	var trackingRequests, adRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/adtracking" {
			trackingRequests.Add(1)
			fmt.Fprint(w, "tracking response")
			return
		}
		if r.URL.Path == "/ajax/banner/vpn" {
			adRequests.Add(1)
			fmt.Fprint(w, "ad response")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body><p>Test page</p>
<div class="results"><div class="result"><a class="result__a" href="https://example.com/">Example</a></div></div>
<script>fetch('/api/adtracking').catch(() => {}); fetch('/ajax/banner/vpn').catch(() => {});</script></body></html>`)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// First verify that the fixture really makes the request without filters.
	cmd := exec.CommandContext(ctx, binary, "fetch", "--dump", "markdown",
		"--wait-until", "done", "--wait-ms", "5000", server.URL)
	cmd.Env = browserEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unfiltered fetch failed: %v: %s", err, output)
	}
	if trackingRequests.Load() != 1 || adRequests.Load() != 1 {
		t.Fatalf("unfiltered page sent %d tracking and %d ad requests, want one of each", trackingRequests.Load(), adRequests.Load())
	}
	shared, err := NewSharedAdblockLists()
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	content, err := FetchContent(ctx, server.URL, FetchOptions{
		Binary: binary, Timeout: 10 * time.Second, Language: "en-US", SharedAdblock: shared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Test page") {
		t.Fatalf("missing page content: %q", content)
	}
	if trackingRequests.Load() != 1 || adRequests.Load() != 1 {
		t.Fatalf("filtered page sent requests: tracking %d, ads %d", trackingRequests.Load(), adRequests.Load())
	}

	// Exercise the actual PandaScript search path with the same embedded lists.
	args, files, cleanup, err := prepareAdblockLists(false, shared)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	options := testOptions()
	options.Timeout, options.WaitUntil = 10*time.Second, "done"
	script := strings.Replace(makeScript("duckduckgo", "test", options),
		searchURL("duckduckgo", "test", options.Limit), server.URL, 1)
	cmd = exec.CommandContext(ctx, binary, append([]string{"run", "-", "--log-level", "error"}, args...)...)
	cmd.Env = browserEnv()
	cmd.ExtraFiles = files
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("filtered search failed: %v", err)
	}
	var report EngineReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.Results) != 1 || report.Results[0].Title != "Example" {
		t.Fatalf("unexpected search results: %+v", report)
	}
	if trackingRequests.Load() != 1 || adRequests.Load() != 1 {
		t.Fatalf("filtered search sent requests: tracking %d, ads %d", trackingRequests.Load(), adRequests.Load())
	}

	var group sync.WaitGroup
	errors := make(chan error, 4)
	for range 4 {
		group.Go(func() {
			_, err := FetchContent(ctx, server.URL, FetchOptions{
				Binary: binary, Timeout: 10 * time.Second, Language: "en-US", SharedAdblock: shared,
			})
			errors <- err
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent filtered fetch failed: %v", err)
		}
	}
	if trackingRequests.Load() != 1 || adRequests.Load() != 1 {
		t.Fatalf("concurrent filtered requests leaked: tracking %d, ads %d", trackingRequests.Load(), adRequests.Load())
	}

	_, err = FetchContent(ctx, server.URL, FetchOptions{
		Binary: binary, Timeout: 10 * time.Second, Language: "en-US", DisableAdblock: true, SharedAdblock: shared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trackingRequests.Load() != 2 || adRequests.Load() != 2 {
		t.Fatalf("disabled adblock should allow requests: tracking %d, ads %d", trackingRequests.Load(), adRequests.Load())
	}
}

func TestSharedAdblockCloseWaitsForUsers(t *testing.T) {
	shared, err := NewSharedAdblockLists()
	if err != nil {
		t.Fatal(err)
	}
	_, _, release, err := prepareAdblockLists(false, shared)
	if err != nil {
		shared.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		shared.Close()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("closed shared lists while they were still in use")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shared list close did not finish")
	}
	if _, _, _, err := prepareAdblockLists(false, shared); err == nil {
		t.Fatal("expected error after shared lists were closed")
	}
	shared.Close() // Closing twice is harmless.
}

func TestAdblockDirectoryFailure(t *testing.T) {
	binary := fakeBinary(t, `{"status":"empty","results":[]}`)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	options := testOptions()
	options.Binary = binary
	if _, err := Search(context.Background(), "test", options); err == nil || !strings.Contains(err.Error(), "create adblock directory") {
		t.Fatalf("expected search adblock setup error, got %v", err)
	}
	if _, err := FetchContent(context.Background(), "https://example.com", FetchOptions{
		Binary: binary, Timeout: time.Second,
	}); err == nil || !strings.Contains(err.Error(), "create adblock directory") {
		t.Fatalf("expected fetch adblock setup error, got %v", err)
	}
}

func TestDisabledAdblockDoesNotPrepareFiles(t *testing.T) {
	binary := fakeFetchBinary(t, `case " $* " in *' --adblock-lists '*) exit 9 ;; esac
case "$1" in
  run) printf '%s\n' '{"status":"empty","results":[]}' ;;
  fetch) printf 'page' ;;
  *) exit 10 ;;
esac
`)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	options := testOptions()
	options.Binary, options.DisableAdblock = binary, true
	report, err := Search(context.Background(), "test", options)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range report {
		if engine.Status != "empty" {
			t.Fatalf("unexpected search report: %+v", report)
		}
	}
	content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{
		Binary: binary, Timeout: time.Second, DisableAdblock: true,
	})
	if err != nil || content != "page" {
		t.Fatalf("unexpected fetch content %q: %v", content, err)
	}
}
