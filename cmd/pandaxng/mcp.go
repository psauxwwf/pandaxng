package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"pandaxng/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	var config mcpserver.Config
	cmd := &cobra.Command{
		Use: "mcp", Short: "Serve MCP tools over stdio or Streamable HTTP",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("timeout") && config.Timeout == 0 {
				return fmt.Errorf("timeout must be between 1s and 2m")
			}
			return mcpserver.Serve(cmd.Context(), config, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&config.Transport, "transport", "stdio", "MCP transport: stdio or http")
	cmd.Flags().StringVar(&config.Listen, "listen", "127.0.0.1:8080", "HTTP listen address (/mcp endpoint)")
	cmd.Flags().StringVar(&config.Binary, "lightpanda", "lightpanda", "Lightpanda executable")
	cmd.Flags().DurationVar(&config.Timeout, "timeout", 0, "override both tool timeouts (1s–2m; defaults: search 20s, fetch 40s)")
	waitUntilFlag(cmd, &config.WaitUntil, "")
	cmd.Flags().Lookup("wait-until").Usage += "; defaults: search domcontentloaded, fetch done"
	cmd.Flags().StringVar(&config.Language, "language", "en-US", "default browser locale")
	cmd.Flags().BoolVar(&config.DisablePrivateURLs, "disable-private-urls", false, "block browser requests to private networks")
	return cmd
}
