package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bensyverson/jobs/internal/web/handlers"
	"github.com/bensyverson/jobs/internal/web/server"
	"github.com/spf13/cobra"
)

func newPreviewCmd() *cobra.Command {
	var bindFlag, format string
	var list bool
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Serve the dashboard's component catalog (no database needed)",
		Long: "Serve the dashboard's preview catalog: every component in each of its\n" +
			"representative states, rendered through the real page shell from the\n" +
			"production view-models. It needs no database, credentials or network,\n" +
			"and writes nothing. /preview lists components, /preview/<component>\n" +
			"stacks its states, /preview/<component>/<state> is one state whole.\n\n" +
			"--list prints the catalog instead of serving it (--format=json for\n" +
			"agents). --bind works as it does for `job serve`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				return writePreviewList(cmd, format)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			addr := resolveServeAddr(bindFlag)
			ln, _, err := bindForServe(addr, cmd.Flags().Changed("bind"))
			if err != nil {
				return fmt.Errorf("bind %s: %w", addr, err)
			}
			srv := &http.Server{Handler: server.NewPreviewMux(), ReadHeaderTimeout: 10 * time.Second}
			fmt.Fprintf(cmd.OutOrStdout(), "Jobs preview catalog: http://%s/preview\n", ln.Addr())
			fmt.Fprintln(cmd.OutOrStdout(), "Press Ctrl-C to stop.")
			return server.Serve(ctx, srv, ln)
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "print the catalog instead of serving it")
	cmd.Flags().StringVar(&format, "format", "md", "with --list: output format (md|json)")
	cmd.Flags().StringVar(&bindFlag, "bind", "", "address to bind (default 127.0.0.1:7823, walking up when taken)")
	return cmd
}

func writePreviewList(cmd *cobra.Command, format string) error {
	index := handlers.PreviewIndex()
	out := cmd.OutOrStdout()
	switch format {
	case "json":
		b, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", b)
		return err
	case "md":
		for _, c := range index {
			fmt.Fprintf(out, "%s — %s (%s)\n", c.Component, c.Title, c.Source)
			for _, s := range c.States {
				fmt.Fprintf(out, "  %s  %s\n", s.URL, s.Name)
			}
		}
		return nil
	default:
		return fmt.Errorf("preview: --format must be md or json (got %q)", format)
	}
}
