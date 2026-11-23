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
	Binary               string
	Timeout              time.Duration
	Language             string
	WaitUntil            string
	Format               string
	BlockPrivateNetworks bool
	DisableAdblock       bool
	SharedAdblock        *SharedAdblockLists
}

const DefaultFetchFormat = "md"

func FetchFormats() []string { return []string{"md", "html", "semantic_tree_text"} }

// FetchContent loads an HTTP(S) page with JavaScript and returns its selected dump.
// This local CLI helper permits private network URLs; it is not an SSRF boundary.
func FetchContent(ctx context.Context, rawURL string, options FetchOptions) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("URL must be an absolute http(s) URL without credentials")
	}
	if options.Timeout < time.Second || options.Timeout > 2*time.Minute {
		return "", fmt.Errorf("timeout must be between 1s and 2m")
	}
	var dump string
	switch options.Format {
	case "", "md", "markdown":
		dump = "markdown"
	case "html", "semantic_tree_text":
		dump = options.Format
	default:
		return "", fmt.Errorf("format must be one of: %s (markdown is an alias for md)", strings.Join(FetchFormats(), ", "))
	}
	waitUntil, err := NormalizeWaitUntil(options.WaitUntil, DefaultFetchWaitUntil)
	if err != nil {
		return "", err
	}
	binary, err := exec.LookPath(options.Binary)
	if err != nil {
		return "", fmt.Errorf("Lightpanda not found: %w", err)
	}
	adblockArgs, adblockFiles, cleanup, err := prepareAdblockLists(options.DisableAdblock, options.SharedAdblock)
	if err != nil {
		return "", err
	}
	defer cleanup()
	millis := strconv.FormatInt(options.Timeout.Milliseconds(), 10)
	content, err := fetchAttempt(ctx, binary, u.String(), dump, waitUntil, millis, options, adblockArgs, adblockFiles)
	if err != nil && waitUntil != "domcontentloaded" && ctx.Err() == nil {
		return fetchAttempt(ctx, binary, u.String(), dump, "domcontentloaded", millis, options, adblockArgs, adblockFiles)
	}
	return content, err
}

func fetchAttempt(ctx context.Context, binary, rawURL, dump, waitUntil, millis string, options FetchOptions, adblockArgs []string, adblockFiles []*os.File) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, options.Timeout+3*time.Second)
	defer cancel()
	args := []string{"fetch",
		"--log-format", "pretty", "--log-level", "info", "--dump", dump,
		"--user-agent", userAgent, "--insecure-disable-tls-host-verification",
		"--locale", options.Language, "--http-timeout", millis, "--watchdog-ms", millis,
		"--wait-until", waitUntil, "--wait-ms", millis, "--terminate-ms", millis,
		"--http-max-response-size", "16777216", "--v8-max-heap-mb", "256"}
	args = append(args, adblockArgs...)
	if options.BlockPrivateNetworks {
		args = append(args, "--block-private-networks")
	}
	args = append(args, rawURL)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.ExtraFiles = adblockFiles
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
