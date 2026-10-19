package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"pandaxng/internal/pandaxng"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := fang.Execute(ctx, rootCmd(), fang.WithoutVersion()); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "pandaxng",
		Short:         "Web search and page fetching through Lightpanda",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	root.AddCommand(searchCmd(), fetchCmd(), mcpCmd())
	return root
}

func searchCmd() *cobra.Command {
	var options pandaxng.Options
	var listEngines bool
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Web metasearch through Lightpanda PandaScript",
		Args: func(cmd *cobra.Command, args []string) error {
			if listEngines {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if listEngines {
				for _, engine := range pandaxng.Engines() {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), engine); err != nil {
						return err
					}
				}
				return nil
			}
			report, err := pandaxng.Search(cmd.Context(), args[0], options)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(report); err != nil {
				return err
			}
			for _, engine := range report.Engines {
				if engine.Status == "ok" || engine.Status == "empty" {
					return nil
				}
			}
			return errors.New("no search engine returned a successful response")
		},
	}
	cmd.Flags().StringSliceVar(&options.Engines, "engines", pandaxng.Engines(), "comma-separated search engines")
	cmd.Flags().IntVar(&options.Limit, "limit", 10, "results per engine (1–50)")
	cmd.Flags().DurationVar(&options.Timeout, "timeout", 20*time.Second, "timeout per engine (1s–2m)")
	cmd.Flags().IntVar(&options.Jobs, "jobs", 3, "parallel browsers (1–3)")
	cmd.Flags().StringVar(&options.Language, "language", "en-US", "browser locale")
	cmd.Flags().StringVar(&options.Binary, "lightpanda", "lightpanda", "Lightpanda executable")
	cmd.Flags().BoolVar(&listEngines, "list-engines", false, "list supported engines")
	return cmd
}

func fetchCmd() *cobra.Command {
	var options pandaxng.FetchOptions
	cmd := &cobra.Command{
		Use:   "fetch URL",
		Short: "Fetch a page through Lightpanda and print Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			content, err := pandaxng.FetchContent(cmd.Context(), args[0], options)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), content)
			return err
		},
	}
	cmd.Flags().DurationVar(&options.Timeout, "timeout", 20*time.Second, "fetch timeout (1s–2m)")
	cmd.Flags().StringVar(&options.Language, "language", "en-US", "browser locale")
	cmd.Flags().StringVar(&options.Binary, "lightpanda", "lightpanda", "Lightpanda executable")
	return cmd
}
