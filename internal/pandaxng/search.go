// Package pandaxng runs the embedded search parsers through Lightpanda.
package pandaxng

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"pandaxng/scripts"
)

const userAgent = "AppleWebKit/537.36 (X11; Linux x86_64) (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"

func Engines() []string { return []string{"duckduckgo", "brave", "yahoo"} }

type Options struct {
	Engines              []string
	Limit                int
	Timeout              time.Duration
	Jobs                 int
	Language             string
	Binary               string
	BlockPrivateNetworks bool
}

type Result struct {
	URL     string   `json:"url"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Engines []string `json:"engines,omitempty"`
}

type EngineReport struct {
	Engine  string   `json:"engine"`
	Status  string   `json:"status"`
	Results []Result `json:"results"`
	URL     string   `json:"url,omitempty"`
	Title   string   `json:"title,omitempty"`
	Error   string   `json:"error,omitempty"`
	Elapsed float64  `json:"elapsed"`
}

type Report struct {
	Query   string         `json:"query"`
	Results []Result       `json:"results"`
	Engines []EngineReport `json:"engines"`
}

func Search(ctx context.Context, query string, options Options) (Report, error) {
	report := Report{Query: query, Results: []Result{}}
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 2000 {
		return report, fmt.Errorf("query must contain 1–2000 characters")
	}
	if options.Limit < 1 || options.Limit > 50 || options.Jobs < 1 || options.Jobs > 3 {
		return report, fmt.Errorf("limit must be 1–50 and jobs must be 1–3")
	}
	if options.Timeout < time.Second || options.Timeout > 2*time.Minute {
		return report, fmt.Errorf("timeout must be between 1s and 2m")
	}
	var engines []string
	for _, name := range options.Engines {
		name = strings.ToLower(strings.TrimSpace(name))
		if !slices.Contains(Engines(), name) {
			return report, fmt.Errorf("unknown engine %q; use --list-engines", name)
		}
		if !slices.Contains(engines, name) {
			engines = append(engines, name)
		}
	}
	if len(engines) == 0 {
		return report, fmt.Errorf("select at least one engine")
	}
	binary, err := exec.LookPath(options.Binary)
	if err != nil {
		return report, fmt.Errorf("Lightpanda not found: %w", err)
	}
	options.Binary = binary
	report.Engines = make([]EngineReport, len(engines))
	semaphore := make(chan struct{}, options.Jobs)
	var group sync.WaitGroup
	for i, engine := range engines {
		group.Go(func() {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
				report.Engines[i] = runEngine(ctx, engine, query, options)
			case <-ctx.Done():
				report.Engines[i] = EngineReport{Engine: engine, Status: "error", Results: []Result{}, Error: ctx.Err().Error()}
			}
		})
	}
	group.Wait()
	report.Results = aggregate(report.Engines)
	return report, nil
}

func searchURL(engine, query string) string {
	endpoint, parameter := "https://html.duckduckgo.com/html/", "q"
	values := url.Values{}
	switch engine {
	case "brave":
		endpoint = "https://search.brave.com/search"
		values.Set("source", "web")
	case "yahoo":
		endpoint, parameter = "https://search.yahoo.com/search", "p"
	}
	values.Set(parameter, query)
	return endpoint + "?" + values.Encode()
}

func makeScript(engine, query string, options Options) string {
	request, _ := json.Marshal(struct {
		URL     string `json:"url"`
		Limit   int    `json:"limit"`
		Timeout int64  `json:"timeout"`
	}{searchURL(engine, query), options.Limit, options.Timeout.Milliseconds()})
	// Engine names are validated before reading these embedded files.
	common, _ := scripts.Files.ReadFile("common.js")
	parser, _ := scripts.Files.ReadFile("engines/" + engine + ".js")
	return "const request = " + string(request) + ";\n" + string(common) + "\n" + string(parser)
}

func runEngine(ctx context.Context, engine, query string, options Options) (report EngineReport) {
	started := time.Now()
	report = EngineReport{Engine: engine, Status: "error", Results: []Result{}}
	defer func() { report.Elapsed = time.Since(started).Seconds() }()
	ctx, cancel := context.WithTimeout(ctx, options.Timeout+3*time.Second)
	defer cancel()
	millis := strconv.FormatInt(options.Timeout.Milliseconds(), 10)
	cmd := exec.CommandContext(ctx, options.Binary, "run", "-",
		"--user-agent", userAgent, "--insecure-disable-tls-host-verification",
		"--locale", options.Language, "--http-timeout", millis, "--watchdog-ms", millis,
		"--http-max-response-size", "16777216", "--v8-max-heap-mb", "256", "--log-level", "error")
	if options.BlockPrivateNetworks {
		cmd.Args = append(cmd.Args, "--block-private-networks")
	}
	cmd.WaitDelay = time.Second
	cmd.Env = browserEnv()
	cmd.Stdin = strings.NewReader(makeScript(engine, query, options))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		report.Error = strings.TrimSpace(stderr.String())
		if len(report.Error) > 2000 {
			report.Error = report.Error[len(report.Error)-2000:]
		}
		if report.Error == "" {
			report.Error = err.Error()
		}
		if ctx.Err() == context.DeadlineExceeded || strings.Contains(strings.ToLower(report.Error), "navigation timed out") {
			report.Status = "timeout"
		}
		return report
	}
	var data EngineReport
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		report.Error = "invalid PandaScript response: " + err.Error()
		return report
	}
	if !slices.Contains([]string{"ok", "empty", "blocked", "parse_error", "error"}, data.Status) || data.Results == nil {
		report.Error = "invalid PandaScript status or results"
		return report
	}
	report.Status, report.URL, report.Title, report.Error = data.Status, data.URL, data.Title, data.Error
	if data.Status != "ok" {
		return report
	}
	var seen []string
	for _, row := range data.Results {
		row.URL = normalizeURL(engine, row.URL)
		row.Title = strings.TrimSpace(row.Title)
		row.Engines = nil
		if row.URL == "" || row.Title == "" || slices.Contains(seen, row.URL) {
			continue
		}
		seen = append(seen, row.URL)
		report.Results = append(report.Results, row)
		if len(report.Results) >= options.Limit {
			break
		}
	}
	if len(report.Results) == 0 {
		report.Status, report.Error = "parse_error", "no valid web results after URL validation"
	}
	return report
}

func normalizeURL(engine, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	var target string
	if engine == "duckduckgo" && (host == "duckduckgo.com" || strings.HasSuffix(host, ".duckduckgo.com")) && strings.HasPrefix(u.Path, "/l/") {
		target = u.Query().Get("uddg")
	} else if engine == "yahoo" && host == "r.search.yahoo.com" {
		if _, tail, ok := strings.Cut(u.EscapedPath(), "/RU="); ok {
			target, _, _ = strings.Cut(tail, "/RK=")
			target, err = url.PathUnescape(target)
			if err != nil {
				return ""
			}
		}
	}
	if target != "" {
		u, err = url.Parse(target)
		if err != nil {
			return ""
		}
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	u.Fragment, u.RawFragment = "", ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func aggregate(reports []EngineReport) []Result {
	results := []Result{}
	indexed := map[string]int{}
	for rank := 0; ; rank++ {
		found := false
		for _, report := range reports {
			if rank >= len(report.Results) {
				continue
			}
			found = true
			row := report.Results[rank]
			if i, ok := indexed[row.URL]; ok {
				results[i].Engines = append(results[i].Engines, report.Engine)
			} else {
				row.Engines = []string{report.Engine}
				indexed[row.URL] = len(results)
				results = append(results, row)
			}
		}
		if !found {
			return results
		}
	}
}
