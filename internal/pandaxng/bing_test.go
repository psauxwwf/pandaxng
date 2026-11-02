package pandaxng

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBingLightpandaIntegration(t *testing.T) {
	binary, err := exec.LookPath("lightpanda")
	if err != nil {
		t.Skip("Lightpanda is not installed")
	}
	organic := `<ol id="b_results">
<li class="b_ad"><h2><a href="https://ads.example/">Advertisement</a></h2></li>
<li class="b_algo"><h2><a href="https://example.com/"> Example title </a></h2><div class="b_caption"><p>Useful snippet</p></div></li>
<li class="b_algo"><h2><a href="https://example.com/">Duplicate</a></h2></li>
<li class="b_algo b_ad"><h2><a href="https://ads.example/">Sponsored card</a></h2></li>
<li class="b_algo"><h2><a href="https://second.example/">Second</a></h2><div class="b_snippet">Fallback snippet</div></li>
</ol>`
	for _, tc := range []struct {
		name, html, status string
		limit, count       int
	}{
		{"organic", organic, "ok", 10, 2},
		{"limit", organic, "ok", 1, 1},
		{"empty", "<p>There are no results for this query.</p>", "empty", 10, 0},
		{"captcha", `<div id="b_captcha"></div>` + organic, "blocked", 10, 0},
		{"verification", "<p>Please verify you are a human</p>", "blocked", 10, 0},
		{"unknown", "<p>Unexpected layout</p>", "parse_error", 10, 0},
		{"ads_only", `<ol id="b_results"><li class="b_ad"><h2><a href="https://ads.example/">Ad</a></h2></li></ol>`, "parse_error", 10, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, "<!doctype html><html><head><title>Bing fixture</title></head><body>"+tc.html+"</body></html>")
			}))
			defer srv.Close()
			options := testOptions()
			options.Limit = tc.limit
			options.Timeout = 3 * time.Second
			searchURLJSON, _ := json.Marshal(searchURL("bing", "test", options.Limit))
			fixtureURLJSON, _ := json.Marshal(srv.URL)
			script := strings.Replace(makeScript("bing", "test", options), string(searchURLJSON), string(fixtureURLJSON), 1)
			if !strings.Contains(script, srv.URL) {
				t.Fatal("Bing fixture URL was not substituted")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "run", "-", "--log-level", "error")
			cmd.Env = browserEnv()
			cmd.Stdin = strings.NewReader(script)
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Bing PandaScript failed: %v, output: %s", err, output)
			}
			var report EngineReport
			if err := json.Unmarshal(output, &report); err != nil {
				t.Fatal(err)
			}
			if report.Status != tc.status || len(report.Results) != tc.count || report.Results == nil {
				t.Fatalf("unexpected report: %+v", report)
			}
			if tc.count > 0 && (report.Results[0].URL != "https://example.com/" || report.Results[0].Title != "Example title" || report.Results[0].Content != "Useful snippet") {
				t.Fatalf("unexpected first result: %+v", report.Results[0])
			}
			if tc.count > 1 && report.Results[1].Content != "Fallback snippet" {
				t.Fatalf("missing fallback snippet: %+v", report.Results[1])
			}
		})
	}
}
