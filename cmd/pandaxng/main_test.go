package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListEngines(t *testing.T) {
	cmd := rootCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"search", "--list-engines"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "duckduckgo\nbrave\nyahoo\n" {
		t.Fatal(output.String())
	}
}

func TestHelp(t *testing.T) {
	cmd := rootCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "search") || !strings.Contains(output.String(), "fetch") {
		t.Fatal(output.String())
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"search"}, {"search", "a", "b"},
		{"search", "query", "--timeout", "0s"},
		{"search", "query", "--engines", "google"},
		{"query"}, {"fetch-content", "https://example.com"}, {"fetch_content", "https://example.com"},
		{"mcp", "extra"}, {"mcp", "--transport", "sse"}, {"mcp", "--timeout", "0s"},
	} {
		cmd := rootCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestFetchCommand(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "lightpanda")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '# Markdown\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"fetch", "https://example.com", "--lightpanda", binary, "--timeout", "5s"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "# Markdown\n" {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestFetchArguments(t *testing.T) {
	for _, args := range [][]string{
		{"fetch"},
		{"fetch", "https://example.com", "extra"},
		{"fetch", "file:///etc/passwd"},
		{"fetch", "https://example.com", "--timeout", "0s"},
	} {
		cmd := rootCmd()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

func TestCommands(t *testing.T) {
	commands := rootCmd().Commands()
	var names []string
	for _, cmd := range commands {
		if cmd.Hidden {
			continue
		}
		if len(cmd.Aliases) != 0 {
			t.Fatalf("unexpected aliases for %s: %v", cmd.Name(), cmd.Aliases)
		}
		names = append(names, cmd.Name())
	}
	if strings.Join(names, ",") != "fetch,mcp,search" {
		t.Fatalf("unexpected commands: %v", names)
	}
}

func TestMCPDisablePrivateURLs(t *testing.T) {
	cmd := mcpCmd()
	disabled, err := cmd.Flags().GetBool("disable-private-urls")
	if err != nil || disabled {
		t.Fatalf("private URLs should be allowed by default: %v, %v", disabled, err)
	}
	if err := cmd.ParseFlags([]string{"--disable-private-urls"}); err != nil {
		t.Fatal(err)
	}
	disabled, err = cmd.Flags().GetBool("disable-private-urls")
	if err != nil || !disabled {
		t.Fatalf("private URLs should be blocked with the flag: %v, %v", disabled, err)
	}
	if err := cmd.ParseFlags([]string{"--disable-private-urls=false"}); err != nil {
		t.Fatal(err)
	}
	disabled, err = cmd.Flags().GetBool("disable-private-urls")
	if err != nil || disabled {
		t.Fatalf("private URLs should be allowed with explicit false: %v, %v", disabled, err)
	}
	if cmd.Flags().Lookup("allow-private-urls") != nil {
		t.Fatal("old allow-private-urls flag should not exist")
	}
}
