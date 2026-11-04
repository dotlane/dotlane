package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/install"
	"github.com/dotlane/dotlane/internal/php"
	"github.com/dotlane/dotlane/internal/ui"
)

// newFrankenPHPCommand is dotlane frankenphp install: FrankenPHP is downloaded only when asked
// for, being a large file.
func newFrankenPHPCommand(loadConfig func() (*config.Config, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frankenphp",
		Short: "Get FrankenPHP, to run PHP sites with it",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Download FrankenPHP " + php.FrankenPHPVersion + " for PHP sites (runtime = \"frankenphp\")",
		Long: "Downloads FrankenPHP " + php.FrankenPHPVersion + "'s build for this machine from its GitHub release into\n" +
			"Dotlane's state folder, and checks it against the SHA-256 this Dotlane release pins before\n" +
			"keeping it. Each FrankenPHP build brings one PHP version, with a fixed set of extensions.\n" +
			"A frankenphp already on the PATH is used too; the newest FrankenPHP found wins.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			name, size, err := php.FrankenPHPDownload()
			if err != nil {
				return err
			}
			out := ui.New(cmd.OutOrStdout())
			if _, err := os.Stat(php.FrankenPHPPath(cfg.StateDir)); err == nil {
				out.Check("FrankenPHP " + php.FrankenPHPVersion + " is already installed: " + out.Path(php.FrankenPHPPath(cfg.StateDir)))
				return nil
			}
			out.Title("Installing FrankenPHP", php.FrankenPHPVersion)
			out.Line("  %s", out.Dim(fmt.Sprintf("%s, %d MB, from github.com/php/frankenphp", name, size>>20)))
			var done, shown int64
			bin, err := php.InstallFrankenPHP(cmd.Context(), cfg.StateDir, func(n int64) {
				done += n
				if pct := done * 100 / size; pct >= shown+10 {
					shown = pct - pct%10
					fmt.Fprintf(cmd.ErrOrStderr(), "  %d%%\r", shown)
				}
			})
			fmt.Fprint(cmd.ErrOrStderr(), "\r     \r")
			if err != nil {
				return err
			}
			out.OK("FrankenPHP", out.Path(bin)+", checked against its SHA-256")
			out.Done("Ready.", `PHP sites with runtime = "frankenphp" (or dotlane add --frankenphp) now use it.`)
			return nil
		},
	})
	return cmd
}

// phpChecks are doctor's lines for the PHP runtimes the daemon runs, and the PHP sites it cannot
// serve. Nothing when there are no PHP sites, or the daemon is not running (doctor says so).
func phpChecks(ctx context.Context, cfg *config.Config) []install.Check {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c := api.NewClient(cfg.SocketPath())
	st, err := c.Status(ctx)
	if err != nil {
		return nil
	}
	list, err := c.Sites(ctx)
	if err != nil {
		return nil
	}
	var checks []install.Check
	for _, rt := range st.PHP {
		check := install.Check{Name: rt.Name, Status: install.Pass,
			Detail: fmt.Sprintf("PHP %s, serves %s", rt.PHPVersion, strings.Join(rt.Sites, ", "))}
		if !rt.Running {
			check.Status, check.Detail = install.Failed, rt.Problem
			check.Fix = "see its log: " + rt.Log + "\nDotlane starts it again on its own, waiting longer after each try (up to 30 s)"
		}
		checks = append(checks, check)
	}
	for _, s := range list {
		if s.PHP != nil && s.PHP.Problem != "" {
			msg, fix, _ := strings.Cut(s.PHP.Problem, "\n")
			checks = append(checks, install.Check{Name: "PHP site " + s.Name, Status: install.Failed, Detail: msg,
				Fix: strings.TrimSpace(fix + "\nthen run dotlane up (or dotlane add) again")})
		}
	}
	return checks
}
