// Command dotlane gives local dev servers real HTTPS addresses such as https://myapp.traix.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pedrodee/dotlane/internal/api"
	"github.com/pedrodee/dotlane/internal/config"
	"github.com/pedrodee/dotlane/internal/daemon"
	"github.com/pedrodee/dotlane/internal/install"
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
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "dotlane:", err)
		os.Exit(1)
	}
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
			printSite(cmd, site)
			if !site.Ready {
				fmt.Fprintf(cmd.ErrOrStderr(), "dotlane: warning: %s is not answering yet; give it a few seconds\n", site.URLs[0])
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
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", args[0])
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
			for _, s := range list {
				printSite(cmd, s)
			}
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

func printSite(cmd *cobra.Command, s api.Site) {
	for _, u := range s.URLs {
		fmt.Fprintf(cmd.OutOrStdout(), "%s -> localhost:%d\n", u, s.Port)
	}
}

func explain(err error) error {
	if errors.Is(err, api.ErrNotRunning) {
		return fmt.Errorf("%w (set it up with: dotlane install, or run it once with: dotlane serve)", err)
	}
	return err
}
