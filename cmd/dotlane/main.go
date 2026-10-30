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
	"github.com/pedrodee/dotlane/internal/project"
	"github.com/pedrodee/dotlane/internal/sites"
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
  dotlane ls

  # or declare a project's sites in its dotlane.toml, and serve them all
  dotlane up`,
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
			site, err := c.AddSite(cmd.Context(), args[0], port, "")
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
					rows = append(rows, ui.SiteRow{Name: name, URL: u, Port: s.Port, Project: s.Project})
				}
			}
			out.SiteTable(rows)
			return nil
		},
	}
	ls.Flags().BoolVar(&asJSON, "json", false, "print JSON, for scripts")
	root.AddCommand(ls)

	// loadProject finds the dotlane.toml for up and down, and the registered sites.
	loadProject := func(cmd *cobra.Command) (*project.Project, *api.Client, []sites.Site, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, nil, nil, err
		}
		path, err := project.Find(cwd)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w\ncreate one in the project's root:\n[sites.<name>]\nport = <port>", err)
		}
		p, err := project.Load(path)
		if err != nil {
			return nil, nil, nil, err
		}
		c, err := client()
		if err != nil {
			return nil, nil, nil, err
		}
		list, err := c.Sites(cmd.Context())
		if err != nil {
			return nil, nil, nil, explain(err)
		}
		registered := make([]sites.Site, len(list))
		for i, s := range list {
			registered[i] = sites.Site{Name: s.Name, Port: s.Port, Project: s.Project}
		}
		return p, c, registered, nil
	}

	root.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Serve the sites in the project's dotlane.toml",
		Long: "Serves the sites listed in dotlane.toml, found in the current directory or a parent, and\n" +
			"removes this project's sites the file no longer lists. Names another project uses are\n" +
			"reported and nothing changes. A site added by hand with the same name is taken over.",
		Example: `  # dotlane.toml
  [sites.myapp]
  port = 5173`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, c, registered, err := loadProject(cmd)
			if err != nil {
				return err
			}
			out := ui.New(cmd.OutOrStdout())
			conflicts, stale := p.Up(registered)
			if len(conflicts) > 0 {
				for _, conflict := range conflicts {
					out.Fail(conflict.Name, "already used by the project in "+out.Path(conflict.Owner))
					out.Hint(fmt.Sprintf("dotlane down in that project, or dotlane rm %s", conflict.Name))
				}
				return errors.New("another project uses these names; nothing was changed")
			}
			out.Title("Dotlane up", out.Path(p.Dir))
			for _, s := range p.Sites {
				site, err := c.AddSite(cmd.Context(), s.Name, s.Port, s.Project)
				if err != nil {
					return explain(err)
				}
				for _, u := range site.URLs {
					out.SiteOK(u, site.Port)
				}
				if !site.Ready {
					out.Warn("Not answering yet", "the certificate is still being issued; give it a few seconds")
				}
			}
			for _, name := range stale {
				if err := c.RemoveSite(cmd.Context(), name); err != nil {
					return explain(err)
				}
				out.Check(fmt.Sprintf("removed %s (no longer in %s)", name, project.FileName))
			}
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "down",
		Short: "Remove the sites the project's dotlane.toml registered",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, c, registered, err := loadProject(cmd)
			if err != nil {
				return err
			}
			out := ui.New(cmd.OutOrStdout())
			names := p.Down(registered)
			if len(names) == 0 {
				out.Line("%s", out.Dim("Nothing to remove: no sites registered from "+out.Path(p.Path)))
				return nil
			}
			for _, name := range names {
				if err := c.RemoveSite(cmd.Context(), name); err != nil {
					return explain(err)
				}
				out.Check("removed " + name)
			}
			return nil
		},
	})

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
		status := func(ctx context.Context) (api.Status, error) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			return c.Status(ctx)
		}
		return install.New(cfg, configPath, status)
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

	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Move from the Bash prototype (Homebrew Caddy + dnsmasq) to Dotlane",
		Long: "Imports the prototype's sites, reuses its certificate authority (already trusted, so no\n" +
			"password), stops Homebrew's Caddy and the dnsmasq agent, and installs Dotlane in their\n" +
			"place. If the install fails, the prototype is started again. Nothing is deleted; the\n" +
			"files that can go are listed at the end. Safe to re-run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := installer()
			if err != nil {
				return err
			}
			return i.Migrate(cmd.Context())
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

	var fix bool
	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Check the installation, and what could get in its way",
		Long: "Checks the login service, the daemon, its ports, the certificate authority and DNS,\n" +
			"and names any other tool in the way, with the fix for each. --fix runs dotlane install\n" +
			"for what it can repair; other tools are left alone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			i, err := installer()
			if err != nil {
				return err
			}
			checks := i.Doctor(cmd.Context())
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(checks); err != nil {
					return err
				}
				return problems(checks)
			}
			out := ui.New(cmd.OutOrStdout())
			out.Title("Dotlane doctor", "")
			printChecks(out, checks)
			if fix && repairable(checks) {
				out.Line("")
				if err := i.Install(cmd.Context()); err != nil {
					return err
				}
				out.Line("")
				out.Title("Dotlane doctor", "after the fix")
				checks = i.Doctor(cmd.Context())
				printChecks(out, checks)
			}
			if err := problems(checks); err != nil {
				if !fix && repairable(checks) {
					return fmt.Errorf("%w\nrun dotlane doctor --fix to repair what Dotlane can", err)
				}
				return err
			}
			out.Done("All good.", "")
			return nil
		},
	}
	doctor.Flags().BoolVar(&fix, "fix", false, "repair what dotlane install can (asks for your password if needed)")
	doctor.Flags().BoolVar(&asJSON, "json", false, "print JSON, for scripts")
	root.AddCommand(doctor)

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

func printChecks(out *ui.Printer, checks []install.Check) {
	for _, c := range checks {
		switch c.Status {
		case install.Pass:
			out.OK(c.Name, c.Detail)
		case install.Warning:
			out.Warn(c.Name, c.Detail)
		case install.Skipped:
			out.Skip(c.Name, c.Detail)
		default:
			out.Fail(c.Name, c.Detail)
		}
		if c.Status != install.Pass && c.Fix != "" {
			out.Hint(c.Fix)
		}
	}
}

// repairable reports whether dotlane install would fix something, and nothing else is in the way.
func repairable(checks []install.Check) bool {
	any := false
	for _, c := range checks {
		if c.Status == install.Failed && c.Conflict {
			return false
		}
		any = any || (c.Status != install.Pass && c.Repairable)
	}
	return any
}

func problems(checks []install.Check) error {
	n := 0
	for _, c := range checks {
		if c.Status == install.Failed {
			n++
		}
	}
	switch n {
	case 0:
		return nil
	case 1:
		return errors.New("1 problem found")
	default:
		return fmt.Errorf("%d problems found", n)
	}
}
