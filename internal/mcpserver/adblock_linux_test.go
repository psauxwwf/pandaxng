//go:build linux

package mcpserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestMCPAdblockHelper(t *testing.T) {
	binary := os.Getenv("PANDAXNG_MCP_HELPER")
	if binary == "" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := Serve(ctx, Config{Transport: "stdio", Binary: binary, Language: "en-US"}, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMCPSharedListsAndSignals(t *testing.T) {
	temp := t.TempDir()
	binary := filepath.Join(temp, "lightpanda")
	logPath := filepath.Join(temp, "inodes")
	script := `#!/bin/sh
prev= count=0 first= second=
for arg in "$@"; do
  if [ "$prev" = '--adblock-lists' ]; then
    test -r "$arg" || exit 9
    inode=$(stat -Lc '%d:%i' "$arg") || exit 10
    count=$((count+1))
    if [ "$count" -eq 1 ]; then first="$inode"; else second="$inode"; fi
  fi
  prev="$arg"
done
test "$count" -eq 2 || exit 11
printf '%s %s\n' "$first" "$second" >> "$PANDAXNG_LIST_LOG"
case "$1" in
  run) printf '%s\n' '{"status":"empty","results":[]}' ;;
  fetch) printf 'page' ;;
  *) exit 12 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, signalType := range []string{"eof", "term", "kill"} {
		t.Run(signalType, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.Command(self, "-test.run=^TestMCPAdblockHelper$")
			cmd.Env = append(os.Environ(), "PANDAXNG_MCP_HELPER="+binary, "PANDAXNG_LIST_LOG="+logPath, "TMPDIR="+temp)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			c := client.NewClient(transport.NewIO(stdout, stdin, nil))
			defer c.Close()
			initialize(t, ctx, c)

			entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid))
			if err != nil {
				t.Fatal(err)
			}
			var anonymous int
			for _, entry := range entries {
				target, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", cmd.Process.Pid, entry.Name()))
				if strings.Contains(target, "memfd:pandaxng-easy") {
					anonymous++
				}
			}
			if anonymous != 2 {
				t.Fatalf("expected 2 shared anonymous files, got %d", anonymous)
			}

			var group sync.WaitGroup
			errs := make(chan error, 4)
			for i := range 4 {
				group.Go(func() {
					var request mcp.CallToolRequest
					if i%2 == 0 {
						request.Params.Name = "search"
						request.Params.Arguments = map[string]any{"query": "test", "engines": []string{"bing"}}
					} else {
						request.Params.Name = "fetch_content"
						request.Params.Arguments = map[string]any{"url": "https://example.com"}
					}
					result, err := c.CallTool(ctx, request)
					if err == nil && result.IsError {
						err = fmt.Errorf("tool error: %+v", result)
					}
					errs <- err
				})
			}
			group.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(log)), "\n")
			if len(lines) < 4 {
				t.Fatalf("expected four requests, got %q", log)
			}
			current := lines[len(lines)-4:]
			for _, line := range current[1:] {
				if line != current[0] {
					t.Fatalf("requests did not reuse the same lists: %v", current)
				}
			}
			if signalType == "eof" {
				c.Close()
			} else if signalType == "term" {
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			} else if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if signalType != "kill" && err != nil {
					t.Fatalf("server exit: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("server did not exit")
			}
			if _, err := os.Stat(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid)); !os.IsNotExist(err) {
				t.Fatalf("server descriptors still exist: %v", err)
			}
			dirs, err := filepath.Glob(filepath.Join(temp, "pandaxng-adblock-*"))
			if err != nil || len(dirs) != 0 {
				t.Fatalf("unexpected adblock files left on disk: %v, %v", dirs, err)
			}
		})
	}
}
