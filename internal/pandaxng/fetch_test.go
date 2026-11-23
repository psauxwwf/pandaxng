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
for arg in "$@"; do
  if [ "$previous" = '--adblock-lists' ]; then
    test -r "$arg" || exit 10
    grep -q '^\[Adblock Plus' "$arg" || exit 11
  fi
  previous="$arg"
done
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
	if len(args) < 5 {
		t.Fatalf("missing adblock arguments: %q", args)
	}
	listArgs := args[len(args)-5 : len(args)-1]
	if listArgs[0] != "--adblock-lists" || filepath.Base(listArgs[1]) != "easylist.txt" ||
		listArgs[2] != "--adblock-lists" || filepath.Base(listArgs[3]) != "easyprivacy.txt" {
		t.Fatalf("unexpected adblock arguments: %q", listArgs)
	}
	for _, path := range []string{listArgs[1], listArgs[3]} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary list was not removed: %s: %v", path, err)
		}
	}
	args = append(args[:len(args)-5], args[len(args)-1])
	want := []string{"fetch", "--log-format", "pretty", "--log-level", "info", "--dump", "markdown",
		"--user-agent", userAgent, "--insecure-disable-tls-host-verification",
		"--locale", "ru-RU", "--http-timeout", "20000", "--watchdog-ms", "20000",
		"--wait-until", "done", "--wait-ms", "20000", "--terminate-ms", "20000",
		"--http-max-response-size", "16777216", "--v8-max-heap-mb", "256",
		"https://example.com/?q=a&b=2"}
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
	options.Format = "pdf"
	if _, err := FetchContent(context.Background(), "https://example.com", options); err == nil || !strings.Contains(err.Error(), "format must") {
		t.Fatalf("expected format validation, got %v", err)
	}
	options.Format = ""
	options.WaitUntil = "invalid"
	if _, err := FetchContent(context.Background(), "https://example.com", options); err == nil || !strings.Contains(err.Error(), "wait-until must") {
		t.Fatalf("expected wait-until validation, got %v", err)
	}
	options.WaitUntil = ""
	if _, err := FetchContent(context.Background(), "https://example.com", options); err == nil || !strings.Contains(err.Error(), "Lightpanda not found") {
		t.Fatalf("expected missing binary error, got %v", err)
	}
}

func TestFetchFormats(t *testing.T) {
	for _, tc := range []struct{ format, dump string }{
		{"", "markdown"}, {"md", "markdown"}, {"markdown", "markdown"},
		{"html", "html"}, {"semantic_tree_text", "semantic_tree_text"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			binary := fakeFetchBinary(t, `while [ "$#" -gt 0 ]; do
if [ "$1" = '--dump' ]; then printf '%s' "$2"; exit 0; fi
shift
done
exit 9
`)
			content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{
				Binary: binary, Timeout: time.Second, Format: tc.format,
			})
			if err != nil || content != tc.dump {
				t.Fatalf("dump %q, want %q, error %v", content, tc.dump, err)
			}
		})
	}
}

func TestFetchWaitUntil(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("PANDAXNG_FETCH_ARGS", argsPath)
	binary := fakeFetchBinary(t, `printf '%s\n' "$@" > "$PANDAXNG_FETCH_ARGS"`)
	for _, mode := range WaitUntilModes() {
		options := FetchOptions{Binary: binary, Timeout: time.Second, WaitUntil: mode}
		if _, err := FetchContent(context.Background(), "https://example.com", options); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "--wait-until\n"+mode+"\n") {
			t.Fatalf("wait state %q missing from arguments: %s", mode, raw)
		}
	}
}

func TestFetchProcessError(t *testing.T) {
	binary := fakeFetchBinary(t, "printf 'partial page'; printf 'HTTP 403' >&2; exit 22\n")
	content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{Binary: binary, Timeout: time.Second})
	if content != "" || err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("content %q, error %v", content, err)
	}
}

func TestFetchFallback(t *testing.T) {
	for _, mode := range []string{"", "load", "networkalmostidle", "networkidle", "done", "domcontentloaded"} {
		t.Run(fmt.Sprintf("mode=%s", mode), func(t *testing.T) {
			attempts := filepath.Join(t.TempDir(), "attempts")
			t.Setenv("PANDAXNG_ATTEMPTS", attempts)
			binary := fakeFetchBinary(t, `mode=
previous=
case " $* " in *' --block-private-networks '*) ;; *) exit 9 ;; esac
case " $* " in *' --dump html '*) ;; *) exit 10 ;; esac
for arg in "$@"; do
  if [ "$previous" = '--wait-until' ]; then mode="$arg"; fi
  previous="$arg"
done
printf '%s\n' "$mode" >> "$PANDAXNG_ATTEMPTS"
if [ "$mode" = domcontentloaded ]; then printf 'fallback page'; exit 0; fi
printf 'first attempt failed' >&2
exit 1
`)
			content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{
				Binary: binary, Timeout: time.Second, WaitUntil: mode, Format: "html", BlockPrivateNetworks: true,
			})
			if err != nil || content != "fallback page" {
				t.Fatalf("content %q, error %v", content, err)
			}
			got, err := os.ReadFile(attempts)
			if err != nil {
				t.Fatal(err)
			}
			first := mode
			if first == "" {
				first = DefaultFetchWaitUntil
			}
			want := first + "\n"
			if first != "domcontentloaded" {
				want += "domcontentloaded\n"
			}
			if string(got) != want {
				t.Fatalf("attempts %q, want %q", got, want)
			}
		})
	}
}

func TestFetchFallbackError(t *testing.T) {
	attempts := filepath.Join(t.TempDir(), "attempts")
	t.Setenv("PANDAXNG_ATTEMPTS", attempts)
	binary := fakeFetchBinary(t, `mode=
previous=
for arg in "$@"; do
  if [ "$previous" = '--wait-until' ]; then mode="$arg"; fi
  previous="$arg"
done
printf '%s\n' "$mode" >> "$PANDAXNG_ATTEMPTS"
printf '%s failed' "$mode" >&2
exit 22
`)
	for _, mode := range []string{"done", "domcontentloaded"} {
		t.Run(mode, func(t *testing.T) {
			if err := os.WriteFile(attempts, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{
				Binary: binary, Timeout: time.Second, WaitUntil: mode,
			})
			if content != "" || err == nil || !strings.Contains(err.Error(), "domcontentloaded failed") {
				t.Fatalf("content %q, error %v", content, err)
			}
			got, err := os.ReadFile(attempts)
			if err != nil {
				t.Fatal(err)
			}
			want := "domcontentloaded\n"
			if mode != "domcontentloaded" {
				want = mode + "\n" + want
			}
			if string(got) != want {
				t.Fatalf("attempts %q, want %q", got, want)
			}
		})
	}
}

func TestFetchFallbackAfterAttemptDeadline(t *testing.T) {
	binary := fakeFetchBinary(t, `previous=
for arg in "$@"; do
  if [ "$previous" = '--wait-until' ] && [ "$arg" = domcontentloaded ]; then
    printf 'fallback page'
    exit 0
  fi
  previous="$arg"
done
exec sleep 30
`)
	content, err := FetchContent(context.Background(), "https://example.com", FetchOptions{
		Binary: binary, Timeout: time.Second, WaitUntil: "done",
	})
	if err != nil || content != "fallback page" {
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
		if r.URL.Path == "/dynamic" {
			time.Sleep(750 * time.Millisecond)
			fmt.Fprint(w, "JavaScript executed")
			return
		}
		if r.UserAgent() != userAgent {
			http.Error(w, "wrong user agent", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Fixture</title></head><body>
<h1>Пример</h1><p id="dynamic">before</p><a href="https://example.com">Link</a>
<script>fetch('/dynamic').then(r => r.text()).then(text => {
  setTimeout(() => document.getElementById('dynamic').textContent = text, 750);
});</script>
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
	for _, tc := range []struct{ format, marker string }{
		{"html", "<h1>Пример</h1>"},
		{"semantic_tree_text", "heading 'Пример'"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			formatted := options
			formatted.Format = tc.format
			content, err := FetchContent(ctx, server.URL, formatted)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(content, tc.marker) || !strings.Contains(content, "JavaScript executed") {
				t.Fatalf("unexpected %s content: %q", tc.format, content)
			}
		})
	}
}

func TestFetchHTTPErrorPages(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/404", http.StatusFound)
			return
		}
		status := http.StatusNotFound
		switch r.URL.Path {
		case "/403":
			status = http.StatusForbidden
		case "/500":
			status = http.StatusInternalServerError
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(w, "<!doctype html><html><body><h1>Error page %d</h1></body></html>", status)
	}))
	defer server.Close()
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/403", 403}, {"/404", 404}, {"/500", 500}, {"/redirect", 404},
	} {
		for _, format := range FetchFormats() {
			t.Run(fmt.Sprintf("%s/%s", tc.path, format), func(t *testing.T) {
				content, err := FetchContent(context.Background(), server.URL+tc.path, FetchOptions{
					Binary: binary, Timeout: 3 * time.Second, WaitUntil: "domcontentloaded",
					Language: "en-US", Format: format,
				})
				if err != nil || !strings.Contains(content, fmt.Sprintf("Error page %d", tc.status)) {
					t.Fatalf("HTTP %d: content %q, error %v", tc.status, content, err)
				}
			})
		}
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
