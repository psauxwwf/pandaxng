package pandaxng

import (
	"context"
	"net/url"
	"os"
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
		u, err := url.Parse(searchURL(engine, "тест & Go?"))
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
	options.Engines = []string{"duckduckgo", "brave", "yahoo", "brave"}
	report, err := Search(context.Background(), "a quote: \"; return 1; //", options)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Engines) != 3 || len(report.Results) != 1 || report.Results[0].URL != "https://example.com/" || report.Results[0].Title != "Example" || !reflect.DeepEqual(report.Results[0].Engines, Engines()) {
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

func TestValidation(t *testing.T) {
	for _, change := range []func(*Options){
		func(o *Options) { o.Engines = nil },
		func(o *Options) { o.Engines = []string{"google"} },
		func(o *Options) { o.Limit = 0 },
		func(o *Options) { o.Jobs = 4 },
		func(o *Options) { o.Timeout = 0 },
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
