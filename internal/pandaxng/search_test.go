package pandaxng

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testOptions() Options {
	return Options{Engines: Engines(), Limit: 10, Timeout: time.Second, Jobs: 3, Language: "en-US", Binary: "lightpanda"}
}

func TestSearchURL(t *testing.T) {
	for _, engine := range Engines() {
		u, err := url.Parse(searchURL(engine, "тест & Go?", 10))
		if err != nil {
			t.Fatal(err)
		}
		parameter := "q"
		if engine == "yahoo" {
			parameter = "p"
		}
		if u.Query().Get(parameter) != "тест & Go?" {
			t.Fatal(u)
		}
		if engine == "bing" && (u.Host != "www.bing.com" || u.Path != "/search") {
			t.Fatalf("unexpected Bing endpoint: %s", u)
		}
	}
}

func TestSearchLimitInURL(t *testing.T) {
	for _, limit := range []int{1, 5, 10, 50} {
		for _, engine := range Engines() {
			t.Run(fmt.Sprintf("%s/%d", engine, limit), func(t *testing.T) {
				options := testOptions()
				options.Limit = limit
				script := makeScript(engine, "тест & Go?", options)
				requestJSON, _, _ := strings.Cut(strings.TrimPrefix(script, "const request = "), ";\n")
				var request struct {
					URL   string
					Limit int
				}
				if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(request.URL)
				if err != nil {
					t.Fatal(err)
				}
				wantCount := ""
				if engine == "bing" {
					wantCount = fmt.Sprint(limit)
				}
				if u.Query().Get("count") != wantCount || request.Limit != limit {
					t.Fatalf("unexpected limit/count in request: %s", requestJSON)
				}
			})
		}
	}
}

func TestSearchWaitUntil(t *testing.T) {
	for _, mode := range append([]string{""}, WaitUntilModes()...) {
		for _, engine := range Engines() {
			options := testOptions()
			options.WaitUntil = mode
			script := makeScript(engine, "test", options)
			requestJSON, _, _ := strings.Cut(strings.TrimPrefix(script, "const request = "), ";\n")
			var request struct{ WaitUntil string }
			if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
				t.Fatal(err)
			}
			want := mode
			if want == "" {
				want = "domcontentloaded"
			}
			if request.WaitUntil != want || !strings.Contains(script, "waitUntil: request.waitUntil") {
				t.Fatalf("%s: wait-until %q not passed to goto: %s", engine, want, requestJSON)
			}
		}
	}
}

func TestSearchLightpandaIntegration(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/results" {
			time.Sleep(750 * time.Millisecond)
			fmt.Fprint(w, `<div class="result"><a class="result__a" href="https://example.com/">Delayed result</a><p class="result__snippet">Loaded asynchronously</p></div>`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body><div class="results"></div>
<script>fetch('/results').then(r => r.text()).then(html => {
  setTimeout(() => document.querySelector('.results').innerHTML = html, 750);
});</script></body></html>`)
	}))
	defer server.Close()
	options := testOptions()
	options.Timeout = 5 * time.Second
	options.WaitUntil = "done" // This fixture explicitly waits for delayed JavaScript.
	script := strings.Replace(makeScript("duckduckgo", "test", options), searchURL("duckduckgo", "test", options.Limit), server.URL, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run", "-", "--log-level", "error")
	cmd.Env = browserEnv()
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("PandaScript failed: %v, output: %s", err, output)
	}
	var report EngineReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.Results) != 1 || report.Results[0].Title != "Delayed result" || report.Results[0].Content != "Loaded asynchronously" {
		t.Fatalf("missing asynchronous search result: %+v", report)
	}
}

func TestNormalizeURL(t *testing.T) {
	for _, tc := range []struct{ engine, raw, want string }{
		{"brave", "https://example.com#section", "https://example.com/"},
		{"duckduckgo", "https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2F%3Fq%3Da%252Fb", "https://example.com/?q=a%2Fb"},
		{"yahoo", "https://r.search.yahoo.com/a/RU=https%3A%2F%2Fexample.com%2Fa%252Fb/RK=2/RS=x", "https://example.com/a%2Fb"},
		{"duckduckgo", "https://evilduckduckgo.com/l/?uddg=https://example.com", "https://evilduckduckgo.com/l/?uddg=https://example.com"},
		{"brave", "javascript:alert(1)", ""},
		{"brave", "https://user:password@example.com/", ""},
		{"brave", "/relative", ""},
		{"brave", "https://%zz/", ""},
		{"bing", "https://example.com/a#section", "https://example.com/a"},
		{"bing", "https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9saWdodHBhbmRhLmlvLw&ntb=1", "https://lightpanda.io/"},
		{"bing", "https://www.bing.com/ck/a?u=" + url.QueryEscape("a1"+base64.URLEncoding.EncodeToString([]byte("https://example.com/a%2Fb?q=тест#section"))), "https://example.com/a%2Fb?q=тест"},
		{"bing", "https://bing.com/ck/a?u=https%3A%2F%2Fexample.com", "https://example.com/"},
		{"bing", "https://evilbing.com/ck/a?u=a1aHR0cHM6Ly9saWdodHBhbmRhLmlvLw", "https://evilbing.com/ck/a?u=a1aHR0cHM6Ly9saWdodHBhbmRhLmlvLw"},
		{"bing", "https://www.bing.com/ck/a", ""},
		{"bing", "https://www.bing.com/ck/a?u=a1%%%", ""},
		{"bing", "https://www.bing.com/ck/a?u=a1", ""},
		{"bing", "https://www.bing.com/ck/a?u=" + "a1" + base64.RawURLEncoding.EncodeToString([]byte("javascript:alert(1)")), ""},
		{"bing", "https://www.bing.com/ck/a?u=" + "a1" + base64.RawURLEncoding.EncodeToString([]byte("https://user:pass@example.com/")), ""},
		{"bing", "https://www.bing.com/ck/a?u=" + "a1" + base64.RawURLEncoding.EncodeToString([]byte("/relative")), ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := normalizeURL(tc.engine, tc.raw); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	got := aggregate([]EngineReport{
		{Engine: "duckduckgo", Results: []Result{{URL: "a"}, {URL: "b"}}},
		{Engine: "brave", Results: []Result{{URL: "a"}, {URL: "c"}}},
	})
	if len(got) != 3 || got[1].URL != "b" || got[2].URL != "c" || !reflect.DeepEqual(got[0].Engines, []string{"duckduckgo", "brave"}) {
		t.Fatalf("unexpected aggregation: %+v", got)
	}
	if aggregate(nil) == nil {
		t.Fatal("empty results must encode as []")
	}
}

func fakeBinary(t *testing.T, response string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lightpanda")
	// Validate inherited environment and the actual stdin/argv protocol.
	script := "#!/bin/sh\n" +
		"test -z \"$LP_PANDAXNG_TEST\" || exit 9\n" +
		"test \"$1 $2\" = 'run -' || exit 10\n" +
		"case \"$*\" in *--insecure-disable-tls-host-verification*) ;; *) exit 11 ;; esac\n" +
		"input=$(cat)\ncase \"$input\" in *'const request = '*'function collectResults'*) ;; *) exit 12 ;; esac\n" +
		"cat <<'JSON'\n" + response + "\nJSON\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSearch(t *testing.T) {
	t.Setenv("LP_PANDAXNG_TEST", "must not reach subprocess")
	options := testOptions()
	options.Binary = fakeBinary(t, `{"status":"ok","results":[{"url":"https://example.com#x","title":" Example "},{"url":"javascript:bad","title":"Bad"}]}`)
	options.Engines = append(Engines(), "brave")
	report, err := Search(context.Background(), "a quote: \"; return 1; //", options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Engines) != len(Engines()) || len(report.Results) != 1 || report.Results[0].URL != "https://example.com/" || report.Results[0].Title != "Example" || !reflect.DeepEqual(report.Results[0].Engines, Engines()) {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestEngineResponses(t *testing.T) {
	for _, tc := range []struct{ response, status string }{
		{`{"status":"empty","results":[]}`, "empty"},
		{`{"status":"blocked","results":[]}`, "blocked"},
		{`{"status":"ok","results":[]}`, "parse_error"},
		{`{"status":"ok"}`, "error"},
		{`{"status":"unknown","results":[]}`, "error"},
		{`not json`, "error"},
	} {
		t.Run(tc.response, func(t *testing.T) {
			options := testOptions()
			options.Binary = fakeBinary(t, tc.response)
			report := runEngine(context.Background(), "brave", "test", options)
			if report.Status != tc.status || report.Results == nil {
				t.Fatalf("unexpected report: %+v", report)
			}
		})
	}
}

func TestBingSearchNormalization(t *testing.T) {
	options := testOptions()
	options.Engines = []string{"bing"}
	options.Binary = fakeBinary(t, `{"status":"ok","results":[
{"url":"https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9leGFtcGxlLmNvbS8","title":"Example"},
{"url":"https://example.com/#fragment","title":"Duplicate"},
{"url":"https://www.bing.com/ck/a?u=a1bad!","title":"Invalid"}]}`)
	report, err := Search(context.Background(), "test", options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Engines[0].Status != "ok" || len(report.Results) != 1 || report.Results[0].URL != "https://example.com/" || !reflect.DeepEqual(report.Results[0].Engines, []string{"bing"}) {
		t.Fatalf("unexpected Bing normalization: %+v", report)
	}
}

func TestValidation(t *testing.T) {
	for _, change := range []func(*Options){
		func(o *Options) { o.Engines = nil },
		func(o *Options) { o.Engines = []string{"google"} },
		func(o *Options) { o.Limit = 0 },
		func(o *Options) { o.Jobs = 4 },
		func(o *Options) { o.Timeout = 0 },
		func(o *Options) { o.WaitUntil = "invalid" },
	} {
		options := testOptions()
		change(&options)
		if _, err := Search(context.Background(), "test", options); err == nil {
			t.Fatal("expected validation error")
		}
	}
	for _, query := range []string{" ", strings.Repeat("я", 2001)} {
		if _, err := Search(context.Background(), query, testOptions()); err == nil {
			t.Fatal("expected query error")
		}
	}
}

func TestCanceledSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := testOptions()
	options.Binary = fakeBinary(t, `{"status":"empty","results":[]}`)
	report, err := Search(ctx, "test", options)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range report.Engines {
		if engine.Status != "error" || engine.Error == "" {
			t.Fatalf("unexpected canceled report: %+v", engine)
		}
	}
}
