package pandaxng

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fakeFetchBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lightpanda")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFetchContent(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("PANDAXNG_FETCH_ARGS", argsPath)
	t.Setenv("LP_PANDAXNG_TEST", "must not be inherited")
	binary := fakeFetchBinary(t, `test -z "$LP_PANDAXNG_TEST" || exit 9
printf '%s\n' "$@" > "$PANDAXNG_FETCH_ARGS"
printf 'browser log\n' >&2
printf '# Заголовок\n\n[Ссылка](https://example.com/)\n'
`)
	options := FetchOptions{Binary: binary, Timeout: 20 * time.Second, Language: "ru-RU"}
	content, err := FetchContent(context.Background(), "https://example.com/?q=a&b=2", options)
	if err != nil {
		t.Fatal(err)
	}
	if content != "# Заголовок\n\n[Ссылка](https://example.com/)\n" {
		t.Fatalf("unexpected Markdown: %q", content)
	}
	raw, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"fetch", "--log-format", "pretty", "--log-level", "info", "--dump", "markdown",
		"--user-agent", userAgent, "--insecure-disable-tls-host-verification",
		"--locale", "ru-RU", "--http-timeout", "20000", "--watchdog-ms", "20000",
		"--wait-until", "networkalmostidle", "--wait-ms", "20000", "--terminate-ms", "20000",
		"--http-max-response-size", "16777216", "--v8-max-heap-mb", "256",
		"--fail-on-http-error", "https://example.com/?q=a&b=2"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected arguments: %q", args)
	}
}

func TestFetchValidation(t *testing.T) {
	options := FetchOptions{Binary: "/nonexistent/lightpanda", Timeout: time.Second}
	for _, rawURL := range []string{"", "example.com", "/relative", "file:///etc/passwd", "javascript:alert(1)", "https://user:pass@example.com", "https://%zz", "--help"} {
		_, err := FetchContent(context.Background(), rawURL, options)
		if err == nil || !strings.Contains(err.Error(), "URL must") {
			t.Fatalf("expected URL error for %q, got %v", rawURL, err)
		}
	}
	for _, timeout := range []time.Duration{0, -time.Second, 3 * time.Minute} {
		options.Timeout = timeout
		if _, err := FetchContent(context.Background(), "https://example.com", options); err == nil || !strings.Contains(err.Error(), "timeout must") {
			t.Fatalf("expected timeout validation, got %v", err)
		}
	}
	options.Timeout = time.Second
	if _, err := FetchContent(context.Background(), "https://example.com", options); err == nil || !strings.Contains(err.Error(), "Lightpanda not found") {
		t.Fatalf("expected missing binary error, got %v", err)
	}
}

func TestFetchProcessError(t *testing.T) {
	binary := fakeFetchBinary(t, "printf 'partial page'; printf 'HTTP 403' >&2; exit 22\n")
	content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{Binary: binary, Timeout: time.Second})
	if content != "" || err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("content %q, error %v", content, err)
	}
}

func TestFetchCancellation(t *testing.T) {
	binary := fakeFetchBinary(t, "exec sleep 30\n")
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			defer cancel()
			_, err := FetchContent(ctx, "https://example.com", FetchOptions{Binary: binary, Timeout: time.Second})
			if !errors.Is(err, want) {
				t.Fatalf("expected %v, got %v", want, err)
			}
		})
	}
}

func TestFetchLightpandaIntegration(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "missing page", http.StatusNotFound)
			return
		}
		if r.UserAgent() != userAgent {
			http.Error(w, "wrong user agent", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Fixture</title></head><body>
<h1>Пример</h1><p id="dynamic">before</p><a href="https://example.com">Link</a>
<script>setTimeout(() => document.getElementById('dynamic').textContent = 'JavaScript executed', 50);</script>
</body></html>`)
	}))
	defer server.Close()
	options := FetchOptions{Binary: binary, Timeout: 5 * time.Second, Language: "ru-RU"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	content, err := FetchContent(ctx, server.URL, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"# Пример", "JavaScript executed", "[Link](https://example.com"} {
		if !strings.Contains(content, text) {
			t.Fatalf("missing %q in Markdown: %q", text, content)
		}
	}
	if content, err := FetchContent(ctx, server.URL+"/missing", options); err == nil || content != "" {
		t.Fatalf("HTTP 404 should fail: content %q, error %v", content, err)
	}
}

func TestFetchPrivateNetworkBlocked(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private-network request reached the server")
		fmt.Fprint(w, "private data")
	}))
	defer server.Close()
	content, err := FetchContent(context.Background(), server.URL, FetchOptions{
		Binary: binary, Timeout: time.Second, Language: "en-US", BlockPrivateNetworks: true,
	})
	if err == nil || content != "" {
		t.Fatalf("private URL was not blocked: %q, %v", content, err)
	}
}
