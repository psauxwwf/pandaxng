package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"pandaxng/internal/pandaxng"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "lightpanda")
	// Every MCP browser must receive the network guard. Logs must stay out of results.
	script := `#!/bin/sh
case " $* " in *' --block-private-networks '*) ;; *) echo 'missing network guard' >&2; exit 9 ;; esac
echo 'browser diagnostic' >&2
case "$1" in
fetch)
  while [ "$#" -gt 0 ]; do
    if [ "$1" = '--dump' ]; then dump="$2"; break; fi
    shift
  done
  case "$dump" in
    markdown) printf '# Markdown\n\nPage content\n' ;;
    html) printf '<h1>Page content</h1>\n' ;;
    semantic_tree_text) printf "1 RootWebArea 'Page content'\n" ;;
    *) exit 11 ;;
  esac ;;
run)
  input=$(cat)
  case "$input" in
    *'q=blocked'*|*'www.bing.com/search?'*'q=partial'*)
      printf '%s\n' '{"status":"blocked","error":"Verification required","results":[]}'
      exit 0 ;;
  esac
  case "$input" in
    *'"url":"https://www.bing.com/search?'*)
      case "$input" in
        *'"limit":7,'*) case "$input" in *'search?count=7'* ) ;; *) exit 12 ;; esac ;;
        *'"limit":10,'*) case "$input" in *'search?count=10'* ) ;; *) exit 13 ;; esac ;;
        *) exit 14 ;;
      esac
      result_url='https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9leGFtcGxlLmNvbS8' ;;
    *) result_url='https://example.com/' ;;
  esac
  printf '{"status":"ok","results":[{"url":"%s","title":"Example","content":"Snippet"}]}\n' "$result_url" ;;
*) exit 10 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return Config{Transport: "stdio", Listen: "127.0.0.1:0", Binary: binary, Timeout: time.Second, Language: "en-US", DisablePrivateURLs: true}
}

func initialize(t *testing.T, ctx context.Context, c *client.Client) {
	t.Helper()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var req mcp.InitializeRequest
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "pandaxng-test", Version: "1"}
	result, err := c.Initialize(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerInfo.Name != "pandaxng" {
		t.Fatalf("unexpected server info: %+v", result.ServerInfo)
	}
}

func exerciseTools(t *testing.T, ctx context.Context, c *client.Client) {
	t.Helper()
	listed, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type        string   `json:"type"`
				Description string   `json:"description"`
				Minimum     int      `json:"minimum"`
				Maximum     int      `json:"maximum"`
				Default     any      `json:"default"`
				Enum        []string `json:"enum"`
				MaxItems    int      `json:"maxItems"`
				Items       struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"wait_until", "timeout"} {
			property, ok := schema.Properties[name]
			if !ok || slices.Contains(schema.Required, name) || !strings.Contains(property.Description, "Optional") || !strings.Contains(property.Description, "omit") {
				t.Fatalf("%s: %s should be explicitly optional: %s", tool.Name, name, raw)
			}
		}
		if p := schema.Properties["timeout"]; p.Type != "integer" || p.Minimum != 1 || p.Maximum != 120 {
			t.Fatalf("invalid timeout schema: %s", raw)
		}
		if tool.Name == "fetch_content" {
			p, ok := schema.Properties["format"]
			if !ok || slices.Contains(schema.Required, "format") || p.Type != "string" || p.Default != "md" ||
				!slices.Equal(p.Enum, []string{"md", "html", "semantic_tree_text"}) ||
				!strings.Contains(p.Description, "Optional") || !strings.Contains(p.Description, "omit") {
				t.Fatalf("format should be optional with md default: %s", raw)
			}
		}
		if tool.Name == "search" {
			p := schema.Properties["engines"]
			if p.MaxItems != len(pandaxng.Engines()) || !slices.Equal(p.Items.Enum, pandaxng.Engines()) {
				t.Fatalf("engine schema should include all four engines: %s", raw)
			}
		}
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "fetch_content,search" {
		t.Fatal(names)
	}
	for _, name := range names {
		var request mcp.CallToolRequest
		request.Params.Name = name
		if name == "search" {
			request.Params.Arguments = map[string]any{"query": "test", "engines": []string{"brave"}, "max_results": 1}
		} else {
			request.Params.Arguments = map[string]any{"url": "https://example.com"}
		}
		result, err := c.CallTool(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s: %+v", name, result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok || strings.Contains(text.Text, "browser diagnostic") {
			t.Fatalf("unexpected content: %+v", result.Content)
		}
		if name == "fetch_content" && text.Text != "# Markdown\n\nPage content\n" {
			t.Fatal(text.Text)
		}
		if name == "search" {
			var report pandaxng.Report
			if err := json.Unmarshal([]byte(text.Text), &report); err != nil || len(report) != 1 || report["brave"].Status != "ok" {
				t.Fatalf("unexpected search JSON: %s (%v)", text.Text, err)
			}
		}
	}
	for _, engines := range [][]string{nil, {"bing"}, pandaxng.Engines()} {
		var request mcp.CallToolRequest
		request.Params.Name = "search"
		args := map[string]any{"query": "test"}
		wantEngines := engines
		if engines == nil {
			wantEngines = pandaxng.Engines()
		} else {
			args["engines"] = engines
		}
		if len(engines) == 1 && engines[0] == "bing" {
			args["max_results"] = 7
		}
		request.Params.Arguments = args
		result, err := c.CallTool(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("engines %v: unexpected result %+v", engines, result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("unexpected search content: %+v", result.Content)
		}
		for label, content := range map[string]any{"text": json.RawMessage(text.Text), "structured": result.StructuredContent} {
			raw, err := json.Marshal(content)
			if err != nil {
				t.Fatal(err)
			}
			var output map[string]struct {
				Status  string           `json:"status"`
				Results []map[string]any `json:"results"`
			}
			if err := json.Unmarshal(raw, &output); err != nil {
				t.Fatal(err)
			}
			if len(output) != len(wantEngines) {
				t.Fatalf("%s: unexpected engine groups: %s", label, raw)
			}
			for _, name := range wantEngines {
				engine, ok := output[name]
				if !ok || engine.Status != "ok" || len(engine.Results) != 1 {
					t.Fatalf("%s: missing engine results or metadata: %s", label, raw)
				}
				row := engine.Results[0]
				if len(row) != 3 || row["url"] != "https://example.com/" || row["title"] != "Example" || row["content"] != "Snippet" {
					t.Fatalf("%s: unexpected per-engine result: %s", label, raw)
				}
			}
		}
	}
	for _, query := range []string{"blocked", "partial"} {
		var request mcp.CallToolRequest
		request.Params.Name = "search"
		request.Params.Arguments = map[string]any{"query": query, "engines": []string{"brave", "bing"}}
		result, err := c.CallTool(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != (query == "blocked") || len(result.Content) != 1 {
			t.Fatalf("%s: unexpected error status: %+v", query, result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatalf("unexpected search content: %+v", result.Content)
		}
		var report pandaxng.Report
		if err := json.Unmarshal([]byte(text.Text), &report); err != nil {
			t.Fatal(err)
		}
		if len(report) != 2 || report["bing"].Status != "blocked" || report["bing"].Error != "Verification required" || report["bing"].Results == nil {
			t.Fatalf("%s: missing engine diagnostics: %+v", query, report)
		}
	}
	for _, tc := range []struct{ format, want string }{
		{"md", "# Markdown\n\nPage content\n"},
		{"html", "<h1>Page content</h1>\n"},
		{"semantic_tree_text", "1 RootWebArea 'Page content'\n"},
	} {
		var request mcp.CallToolRequest
		request.Params.Name = "fetch_content"
		request.Params.Arguments = map[string]any{"url": "https://example.com", "format": tc.format}
		result, err := c.CallTool(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("%s: unexpected result %+v", tc.format, result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok || text.Text != tc.want {
			t.Fatalf("%s: unexpected content %+v", tc.format, result.Content)
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"search", map[string]any{}},
		{"search", map[string]any{"query": "test", "max_results": 0}},
		{"search", map[string]any{"query": "test", "max_results": 1.5}},
		{"search", map[string]any{"query": "test", "engines": []string{"google"}}},
		{"search", map[string]any{"query": "test", "engines": []string{"duckduckgo", "brave", "yahoo", "bing", "bing"}}},
		{"search", map[string]any{"query": "test", "unknown": true}},
		{"search", map[string]any{"query": "test", "wait_until": "invalid"}},
		{"search", map[string]any{"query": "test", "wait_until": ""}},
		{"search", map[string]any{"query": "test", "timeout": 0}},
		{"search", map[string]any{"query": "test", "timeout": -1}},
		{"search", map[string]any{"query": "test", "timeout": 121}},
		{"search", map[string]any{"query": "test", "timeout": 1.5}},
		{"search", map[string]any{"query": "test", "timeout": "60s"}},
		{"search", map[string]any{"query": "test", "timeout": nil}},
		{"fetch_content", map[string]any{"url": "file:///etc/passwd"}},
		{"fetch_content", map[string]any{"url": 123}},
		{"fetch_content", map[string]any{"url": "https://example.com", "format": "pdf"}},
		{"fetch_content", map[string]any{"url": "https://example.com", "format": ""}},
		{"fetch_content", map[string]any{"url": "https://example.com", "format": nil}},
		{"fetch_content", map[string]any{"url": "https://example.com", "format": 123}},
		{"fetch_content", map[string]any{"url": "https://example.com", "wait_until": "invalid"}},
		{"fetch_content", map[string]any{"url": "https://example.com", "timeout": 0}},
		{"fetch_content", map[string]any{"url": "https://example.com", "timeout": -1}},
		{"fetch_content", map[string]any{"url": "https://example.com", "timeout": 121}},
		{"fetch_content", map[string]any{"url": "https://example.com", "timeout": 1.5}},
		{"fetch_content", map[string]any{"url": "https://example.com", "timeout": "60s"}},
		{"fetch_content", map[string]any{"url": "https://example.com", "timeout": nil}},
	} {
		var request mcp.CallToolRequest
		request.Params.Name, request.Params.Arguments = tc.name, tc.args
		result, err := c.CallTool(ctx, request)
		// Schema failures may be protocol errors; execution failures are isError results.
		if err == nil && !result.IsError {
			t.Fatalf("expected failure for %s %v: %+v", tc.name, tc.args, result)
		}
	}
}

func TestStdio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	input, toServer := io.Pipe()
	fromServer, output := io.Pipe()
	defer input.Close()
	defer fromServer.Close()
	done := make(chan error, 1)
	config := testConfig(t)
	go func() {
		err := Serve(ctx, config, input, output, io.Discard)
		output.Close()
		done <- err
	}()
	c := client.NewClient(transport.NewIO(fromServer, toServer, nil))
	defer c.Close()
	initialize(t, ctx, c)
	exerciseTools(t, ctx, c)
	c.Close() // Closing stdin must stop the server without a signal.
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stdio did not stop at EOF")
	}
}

type startupWriter struct{ ready chan string }

func (w startupWriter) Write(p []byte) (int, error) {
	select {
	case w.ready <- string(p):
	default:
	}
	return len(p), nil
}

func TestHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := testConfig(t)
	config.Transport = "http"
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, config, nil, io.Discard, startupWriter{ready}) }()
	var endpoint string
	select {
	case message := <-ready:
		endpoint = strings.TrimSpace(strings.TrimPrefix(message, "MCP HTTP listening on "))
	case err := <-done:
		t.Fatalf("startup failed: %v", err)
	case <-ctx.Done():
		t.Fatal("HTTP startup timed out")
	}
	c, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	initialize(t, ctx, c)
	exerciseTools(t, ctx, c)
	c.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("HTTP server did not shut down")
	}
}

func TestHTTPOriginAndHost(t *testing.T) {
	s, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	transport := server.NewStreamableHTTPServer(s, server.WithStateLess(true))
	defer transport.Shutdown(context.Background())
	srv := httptest.NewServer(httpHandler(transport))
	defer srv.Close()
	for _, tc := range []struct{ host, origin string }{
		{"", "https://evil.example"}, {"evil.example", ""},
	} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
		if tc.host != "" {
			req.Host = tc.host
		}
		req.Header.Set("Origin", tc.origin)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("host=%s origin=%s status=%d", tc.host, tc.origin, resp.StatusCode)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	config := testConfig(t)
	config.Transport = "sse"
	if err := Serve(context.Background(), config, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("unsupported transport accepted")
	}
	config.Timeout = -time.Second
	if _, err := New(config); err == nil {
		t.Fatal("invalid timeout accepted")
	}
	config.Timeout = time.Second
	config.WaitUntil = "invalid"
	if _, err := New(config); err == nil {
		t.Fatal("invalid wait-until accepted")
	}
	config.WaitUntil = ""
	config.Binary = "/nonexistent/lightpanda"
	if _, err := New(config); err == nil {
		t.Fatal("missing binary accepted")
	}
	config = testConfig(t)
	config.Transport = "http"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config.Listen = listener.Addr().String()
	if err := Serve(context.Background(), config, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("busy listen address accepted")
	}
}

func TestWaitUntil(t *testing.T) {
	for _, tc := range []struct{ serverMode, argument, want string }{
		{"", "", ""},
		{"load", "", "load"},
		{"done", "load", "load"},
		{"done", "domcontentloaded", "domcontentloaded"},
		{"done", "networkalmostidle", "networkalmostidle"},
		{"done", "networkidle", "networkidle"},
		{"load", "done", "done"},
	} {
		for _, timeout := range []time.Duration{0, 5 * time.Second} {
			t.Run(tc.serverMode+"/"+tc.argument+"/"+timeout.String(), func(t *testing.T) {
				config := testConfig(t)
				config.WaitUntil = tc.serverMode
				config.Timeout = timeout
				capturePath := filepath.Join(t.TempDir(), "browser-input")
				t.Setenv("PANDAXNG_BROWSER_INPUT", capturePath)
				script := `#!/bin/sh
case "$1" in
fetch) printf '%s\n' "$@" > "$PANDAXNG_BROWSER_INPUT"; printf '# Page\n' ;;
run) printf '%s\n' "$@" > "$PANDAXNG_BROWSER_INPUT"; cat >> "$PANDAXNG_BROWSER_INPUT"; printf '%s\n' '{"status":"empty","results":[]}' ;;
*) exit 9 ;;
esac
`
				if err := os.WriteFile(config.Binary, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				s, err := New(config)
				if err != nil {
					t.Fatal(err)
				}
				transport := server.NewStreamableHTTPServer(s, server.WithStateLess(true))
				defer transport.Shutdown(context.Background())
				srv := httptest.NewServer(httpHandler(transport))
				defer srv.Close()
				c, err := client.NewStreamableHttpClient(srv.URL + "/mcp")
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				initialize(t, ctx, c)
				for _, name := range []string{"search", "fetch_content"} {
					for _, requestedTimeout := range []int{0, 1, 60, 120} {
						mode := tc.want
						if mode == "" {
							mode = "done"
							if name == "search" {
								mode = "domcontentloaded"
							}
						}
						args := map[string]any{"url": "https://example.com"}
						want := "--wait-until\n" + mode + "\n"
						wantTimeout := timeout
						if wantTimeout == 0 {
							wantTimeout = 40 * time.Second
							if name == "search" {
								wantTimeout = 20 * time.Second
							}
						}
						if name == "search" {
							args = map[string]any{"query": "test", "engines": []string{"yahoo"}}
							want = `"waitUntil":"` + mode + `"`
						}
						if tc.argument != "" {
							args["wait_until"] = tc.argument
						}
						if requestedTimeout != 0 {
							args["timeout"] = requestedTimeout
							wantTimeout = time.Duration(requestedTimeout) * time.Second
						}
						var request mcp.CallToolRequest
						request.Params.Name, request.Params.Arguments = name, args
						result, err := c.CallTool(ctx, request)
						if err != nil || result.IsError {
							t.Fatalf("%s: result %+v, error %v", name, result, err)
						}
						input, err := os.ReadFile(capturePath)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(string(input), want) {
							t.Fatalf("%s: expected %q in browser input: %s", name, want, input)
						}
						millis := strconv.FormatInt(wantTimeout.Milliseconds(), 10)
						if !strings.Contains(string(input), "--http-timeout\n"+millis+"\n") ||
							(name == "search" && !strings.Contains(string(input), `"timeout":`+millis+`,`)) {
							t.Fatalf("%s: missing timeout %s in browser input: %s", name, millis, input)
						}
					}
				}
			})
		}
	}
}

func TestToolDeadlines(t *testing.T) {
	handler := limitCalls(20*time.Second, 40*time.Second)(func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		deadline, ok := ctx.Deadline()
		want := 23 * time.Second
		if request.Params.Name == "fetch_content" {
			want = 43 * time.Second
		}
		if args := request.GetArguments(); args["timeout"] != nil {
			want = time.Duration(args["timeout"].(int)+3) * time.Second
		}
		if remaining := time.Until(deadline); !ok || remaining > want || remaining < want-time.Second {
			t.Fatalf("%s: incorrect tool deadline: %v", request.Params.Name, remaining)
		}
		return mcp.NewToolResultText("ok"), nil
	})
	for _, name := range []string{"search", "fetch_content"} {
		for _, timeout := range []int{0, 1, 60, 120} {
			var request mcp.CallToolRequest
			request.Params.Name = name
			request.Params.Arguments = map[string]any{}
			if timeout != 0 {
				request.Params.Arguments = map[string]any{"timeout": timeout}
			}
			if _, err := handler(context.Background(), request); err != nil {
				t.Fatal(err)
			}
		}
	}
}
