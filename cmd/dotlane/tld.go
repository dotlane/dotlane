package main

// dotlane tld: list, add, remove and choose the primary domain ending. Each change is written to
// config.toml, then, when Dotlane is installed, applied to the machine: the DNS routes follow the
// file (Installer.SyncRoutes) and the daemon restarts to answer on the new list (Refresh).

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/install"
	"github.com/dotlane/dotlane/internal/tld"
	"github.com/dotlane/dotlane/internal/ui"
)

// withTLD is list with t in it: moved or added to the front when primary, else added at the end
// (and left where it is when listed already).
func withTLD(list []string, t string, primary bool) []string {
	rest := slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == t })
	if primary {
		return append([]string{t}, rest...)
	}
	if slices.Contains(list, t) {
		return slices.Clone(list)
	}
	return append(rest, t)
}

// withoutTLD is list without t. The last ending cannot go: sites need one.
func withoutTLD(list []string, t string) ([]string, error) {
	rest := slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == t })
	if len(rest) == 0 {
		return nil, fmt.Errorf("%s is the only domain ending; add another first: dotlane tld add <ending>", t)
	}
	return rest, nil
}

// checkNew refuses an ending Dotlane must not route, and one that may hide the network's own
// names unless force is set.
func checkNew(t string, force bool) error {
	warning, err := tld.Check(t)
	if err != nil {
		return err
	}
	if warning != "" && !force {
		return fmt.Errorf("%s\nadd it anyway with --force, or pick one of: %s", warning, strings.Join(tld.Suggested, ", "))
	}
	return nil
}

// tldEnv is what the tld commands need from the root command.
type tldEnv struct {
	loadConfig func() (*config.Config, error)
	configPath func() string // the file loadConfig read; valid after it ran
	installer  func() (install.Installer, error)
	lookup     func(ctx context.Context, host string) ([]string, error)
}

// apply writes list to the config file and, when Dotlane is installed, routes it and restarts
// the daemon, then checks that the added endings resolve. Run with the list unchanged, it still
// syncs, so a route a declined password prompt left behind is cleaned up by running it again.
func (e tldEnv) apply(ctx context.Context, out *ui.Printer, before, list []string) error {
	if !slices.Equal(before, list) {
		if err := config.SetTLDs(e.configPath(), list); err != nil {
			return err
		}
		out.OK("Config", "tlds = ["+strings.Join(list, ", ")+"] · "+out.Path(e.configPath()))
	}
	i, err := e.installer() // reloads the file, so it sees the new list
	if err != nil {
		return err
	}
	if !i.Installed(ctx) {
		out.Done("Saved.", out.Dim("Dotlane is not installed; dotlane install routes these endings"))
		return nil
	}
	if err := i.SyncRoutes(ctx); err != nil {
		return err
	}
	if !slices.Equal(before, list) {
		if err := i.Refresh(ctx); err != nil {
			return err
		}
	}
	for _, t := range list {
		if !slices.Contains(before, t) {
			e.verify(ctx, out, t)
		}
	}
	return nil
}

// verify looks up a name under t through the system's resolver, as a browser would. A failure is
// reported, not fatal: the OS can take a few seconds to pick up a new route.
func (e tldEnv) verify(ctx context.Context, out *ui.Printer, t string) {
	host := "dotlane-check." + t
	for i := 0; i < 20; i++ {
		lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
		addrs, err := e.lookup(lookupCtx, host)
		cancel()
		if err == nil && slices.ContainsFunc(addrs, func(a string) bool { ip := net.ParseIP(a); return ip != nil && ip.IsLoopback() }) {
			out.OK("Lookup ."+t, host+" → this machine")
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	out.Warn("Lookup ."+t, host+" does not resolve yet; the OS can take a few seconds (check with: dotlane doctor)")
}

// tldRow is one ending in dotlane tld ls --json.
type tldRow struct {
	TLD        string `json:"tld"`
	Primary    bool   `json:"primary"`
	Configured bool   `json:"configured"`
	Routed     bool   `json:"routed"`
	Warning    string `json:"warning,omitempty"`
}

func newTLDCommand(e tldEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tld",
		Short: "List, add and remove domain endings, and choose the primary one",
		Long: "Every site answers on each domain ending; the first is the primary, whose URL add, up and\n" +
			"ls print first. Changes are saved in config.toml and, when Dotlane is installed, applied:\n" +
			"the DNS routes follow (sudo, or an administrator prompt on Windows), and the daemon restarts.\n\n" +
			"Endings that would hide real websites (.com, .dev and every other public one, two-letter\n" +
			"ones), or that resolvers treat specially (.local, .localhost), are refused. Ones a router\n" +
			"or company network may already answer (.lan, .home, .corp) need --force.",
		Example: `  dotlane tld ls
  dotlane tld add test
  dotlane tld use dotlane
  dotlane tld rm traix`,
	}

	var asJSON bool
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the domain endings, which is primary, and which are routed to Dotlane",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := e.loadConfig()
			if err != nil {
				return err
			}
			var routed []string
			installed := false
			if i, err := e.installer(); err == nil {
				installed = i.Installed(cmd.Context())
				if routed, err = i.Routed(cmd.Context()); err != nil {
					return err
				}
			}
			var rows []tldRow
			for n, t := range union(cfg.TLDs, routed) {
				warning, err := tld.Check(t)
				if err != nil {
					warning = err.Error()
				}
				rows = append(rows, tldRow{TLD: t, Primary: n == 0, Configured: slices.Contains(cfg.TLDs, t),
					Routed: slices.Contains(routed, t), Warning: warning})
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			out := ui.New(cmd.OutOrStdout())
			for _, r := range rows {
				label := "." + r.TLD
				switch {
				case !r.Configured:
					out.Warn(label, fmt.Sprintf("still sent to Dotlane, but not in config.toml (dotlane tld rm %s)", r.TLD))
				case !r.Routed && installed:
					out.Warn(label, "not routed to Dotlane yet (dotlane install)")
				case r.Primary:
					out.OK(label, "primary")
				default:
					out.OK(label, "")
				}
				if r.Warning != "" {
					out.Hint(r.Warning)
				}
			}
			return nil
		},
	}
	ls.Flags().BoolVar(&asJSON, "json", false, "print JSON, for scripts")
	cmd.AddCommand(ls)

	var force bool
	add := &cobra.Command{
		Use:     "add <ending>",
		Short:   "Add a domain ending; every site then answers on it too",
		Example: "  dotlane tld add test",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimPrefix(args[0], ".")
			cfg, err := e.loadConfig()
			if err != nil {
				return err
			}
			if !slices.Contains(cfg.TLDs, t) {
				if err := checkNew(t, force); err != nil {
					return err
				}
			}
			out := ui.New(cmd.OutOrStdout())
			out.Title("Adding ."+t, "")
			return e.apply(cmd.Context(), out, cfg.TLDs, withTLD(cfg.TLDs, t, false))
		},
	}
	add.Flags().BoolVar(&force, "force", false, "add an ending a router or company network may already answer")
	cmd.AddCommand(add)

	rm := &cobra.Command{
		Use:     "rm <ending>",
		Aliases: []string{"remove"},
		Short:   "Remove a domain ending and its DNS route; sites stay, on the other endings",
		Example: "  dotlane tld rm traix",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimPrefix(args[0], ".")
			cfg, err := e.loadConfig()
			if err != nil {
				return err
			}
			list, err := withoutTLD(cfg.TLDs, t)
			if err != nil {
				return err
			}
			out := ui.New(cmd.OutOrStdout())
			out.Title("Removing ."+t, "")
			if !slices.Contains(cfg.TLDs, t) {
				out.Skip("Config", "."+t+" is not in config.toml; removing its route if Dotlane still has one")
			}
			return e.apply(cmd.Context(), out, cfg.TLDs, list)
		},
	}
	cmd.AddCommand(rm)

	var useForce bool
	use := &cobra.Command{
		Use:     "use <ending>",
		Short:   "Make a domain ending the primary one, adding it if needed",
		Long:    "Moves the ending to the front of the list, adding it first if it is not there. The other\nendings keep working until removed.",
		Example: "  dotlane tld use dotlane",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimPrefix(args[0], ".")
			cfg, err := e.loadConfig()
			if err != nil {
				return err
			}
			if !slices.Contains(cfg.TLDs, t) {
				if err := checkNew(t, useForce); err != nil {
					return err
				}
			}
			out := ui.New(cmd.OutOrStdout())
			out.Title("Using ."+t, "as the primary domain ending")
			return e.apply(cmd.Context(), out, cfg.TLDs, withTLD(cfg.TLDs, t, true))
		},
	}
	use.Flags().BoolVar(&useForce, "force", false, "add an ending a router or company network may already answer")
	cmd.AddCommand(use)

	return cmd
}

// union is a, then the entries of b that a lacks.
func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
