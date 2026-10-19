package pandaxng

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type FetchOptions struct {
	Binary   string
	Timeout  time.Duration
	Language string
}

// FetchContent loads an HTTP(S) page with JavaScript and returns its Markdown dump.
// This local CLI helper permits private network URLs; it is not an SSRF boundary.
func FetchContent(ctx context.Context, rawURL string, options FetchOptions) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("URL must be an absolute http(s) URL without credentials")
	}
	if options.Timeout < time.Second || options.Timeout > 2*time.Minute {
		return "", fmt.Errorf("timeout must be between 1s and 2m")
	}
	binary, err := exec.LookPath(options.Binary)
	if err != nil {
		return "", fmt.Errorf("Lightpanda not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout+3*time.Second)
	defer cancel()
	millis := strconv.FormatInt(options.Timeout.Milliseconds(), 10)
	cmd := exec.CommandContext(ctx, binary, "fetch",
		"--log-format", "pretty", "--log-level", "info", "--dump", "markdown",
		"--user-agent", userAgent, "--insecure-disable-tls-host-verification",
		"--locale", options.Language, "--http-timeout", millis, "--watchdog-ms", millis,
		"--wait-until", "networkalmostidle", "--wait-ms", millis, "--terminate-ms", millis,
		"--http-max-response-size", "16777216", "--v8-max-heap-mb", "256",
		"--fail-on-http-error", u.String())
	cmd.Env = browserEnv()
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("fetch content: %w", ctx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 2000 {
			detail = detail[len(detail)-2000:]
		}
		if detail != "" {
			return "", fmt.Errorf("fetch content: %w: %s", err, detail)
		}
		return "", fmt.Errorf("fetch content: %w", err)
	}
	return stdout.String(), nil
}

func browserEnv() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "LP_") {
			env = append(env, entry)
		}
	}
	return env
}
