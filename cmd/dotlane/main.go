// Command dotlane gives local dev servers real HTTPS addresses such as https://myapp.traix.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/pedrodee/dotlane/internal/api"
	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/daemon"
	"github.com/pedrodee/dotlane/internal/install"
	"github.com/pedrodee/dotlane/internal/ui"
)

// version is set at release time with -ldflags "-X main.version=…".
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	// Fang styles help and usage, and adds --version, completions and a hidden man command.
	err := fang.Execute(context.Background(), newRoot(),
		fang.WithVersion(versionString()),
		fang.WithErrorHandler(func(_ io.Writer, _ fang.Styles, err error) {
			ui.New(os.Stderr).Error(withUsageHint(err))
		}),
	)
	if err != nil {
		os.Exit(1)
	}
}

// withUsageHint points to --help when the command line itself was wrong.
func withUsageHint(err error) error {
	msg := err.Error()
	for _, prefix := range []string{"accepts ", "requires ", "unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument"} {
		if strings.HasPrefix(msg, prefix) {
			return fmt.Errorf("%w\ntry --help for usage", err)
		}
	}
	return err
}

func newRoot() *cobra.Command {
	var configPath string
	loadConfig := func() (*config.Config, error) {
		if configPath == "" {
			p, err := config.DefaultPath()
			if err != nil {
				return nil, err
			}
			configPath = p
		}
		return config.Load(configPath)
	}
	client := func() (*api.Client, error) {
		cfg, err := loadConfig()
		if err != nil {
			return nil, err
		}
		return api.NewClient(cfg.SocketPath()), nil
	}

	root := &cobra.Command{
		Use:   "dotlane",
		Short: "Real HTTPS addresses for local dev servers",
		Long: "Dotlane gives every local project a real HTTPS address, like https://myapp.traix,\n" +
			"forwarded to the dev server's port on this machine.",
		Example: `  # once: run at login, trust the certificate authority, route the domain ending
  dotlane install

  # serve a dev server at https://myapp.traix
  dotlane add myapp 5173
  dotlane ls`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "",
		"config file (default $DOTLANE_CONFIG or ~/.config/dotlane/config.toml)")

	root.AddCommand(&cobra.Command{
		Use:   "add <name> <port>",
		Short: "Serve https://<name>.<tld> from localhost:<port>",
		Long:  "Serve https://<name>.<tld> from localhost:<port>. Running it again with a new port updates the site.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("not a port: %s", args[1])
			}
			c, err := client()
			if err != nil {
				return err
			}
			site, err := c.AddSite(cmd.Context(), args[0], port)
			if err != nil {
				return explain(err)
			}
			out := ui.New(cmd.OutOrStdout())
			for _, u := range site.URLs {
				out.SiteOK(u, site.Port)
			}
			if !site.Ready {
				out.Warn("Not answering yet", "the certificate is still being issued; give it a few seconds")
			}
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Remove a site",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			if err := c.RemoveSite(cmd.Context(), args[0]); err != nil {
				return explain(err)
			}
			ui.New(cmd.OutOrStdout()).Check("removed " + args[0])
			return nil
		},
	})

	var asJSON bool
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List sites",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			list, err := c.Sites(cmd.Context())
			if err != nil {
				return explain(err)
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			out := ui.New(cmd.OutOrStdout())
			if len(list) == 0 {
				out.Line("%s", out.Dim("No sites yet. Add one: dotlane add <name> <port>"))
				return nil
			}
			var rows []ui.SiteRow
			for _, s := range list {
				for i, u := range s.URLs {
					name := s.Name
					if i > 0 { // further domain endings of the same site
						name = ""
					}
					rows = append(rows, ui.SiteRow{Name: name, URL: u, Port: s.Port})
				}
			}
			out.SiteTable(rows)
			return nil
		},
	}
	ls.Flags().BoolVar(&asJSON, "json", false, "print JSON, for scripts")
	root.AddCommand(ls)

	root.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the background service (DNS, HTTPS proxy, control socket) in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return daemon.Run(ctx, cfg, versionString())
		},
	})

	installer := func() (install.Installer, error) {
		cfg, err := loadConfig()
		if err != nil {
			return nil, err
		}
		c := api.NewClient(cfg.SocketPath())
		ready := func(ctx context.Context) error {
			for {
				_, err := c.Status(ctx)
				if err == nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return err
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
		return install.New(cfg, configPath, ready)
	}

	root.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Run Dotlane at login, trust its certificate authority and route its domain endings",
		Long: "Installs Dotlane as a login service, trusts its local certificate authority and points\n" +
			"the domain endings at its DNS. Steps already done are skipped, so it is safe to re-run.\n" +
			"Steps that need administrator rights are explained and run through sudo.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := installer()
			if err != nil {
				return err
			}
			return i.Install(cmd.Context())
		},
	})

	var purge bool
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Undo dotlane install",
		Long: "Stops the login service and removes it, the certificate authority's trust and the\n" +
			"resolver files. Sites and the CA's keys are kept unless --purge is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := installer()
			if err != nil {
				return err
			}
			return i.Uninstall(cmd.Context(), purge)
		},
	}
	uninstall.Flags().BoolVar(&purge, "purge", false, "also delete sites, the CA's keys and the log")
	root.AddCommand(uninstall)

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
		},
	})

	return root
}

func explain(err error) error {
	if errors.Is(err, api.ErrNotRunning) {
		return fmt.Errorf("%w\nset it up with: dotlane install   (or run it once: dotlane serve)", err)
	}
	return err
}
