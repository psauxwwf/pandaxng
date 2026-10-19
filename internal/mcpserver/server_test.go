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
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "lightpanda")
	// Every MCP browser must receive the network guard. Logs must stay out of results.
	script := `#!/bin/sh
case " $* " in *' --block-private-networks '*) ;; *) echo 'missing network guard' >&2; exit 9 ;; esac
echo 'browser diagnostic' >&2
case "$1" in
fetch) printf '# Markdown\n\nPage content\n' ;;
run) cat >/dev/null; printf '%s\n' '{"status":"ok","results":[{"url":"https://example.com/","title":"Example","content":"Snippet"}]}' ;;
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
			var report struct{ Query string }
			if err := json.Unmarshal([]byte(text.Text), &report); err != nil || report.Query != "test" {
				t.Fatalf("unexpected search JSON: %s (%v)", text.Text, err)
			}
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
		{"search", map[string]any{"query": "test", "unknown": true}},
		{"fetch_content", map[string]any{"url": "file:///etc/passwd"}},
		{"fetch_content", map[string]any{"url": 123}},
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
	config.Timeout = 0
	if _, err := New(config); err == nil {
		t.Fatal("invalid timeout accepted")
	}
	config.Timeout = time.Second
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
