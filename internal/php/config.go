package php

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dotlane/dotlane/internal/sites"
)

// fpmConfig is a PHP-FPM configuration for one PHP version's pool: in the foreground, listening
// on socket, starting workers only while requests come in. The install's own php.ini applies,
// with its extensions.
func fpmConfig(minor, socket, log string) string {
	return fmt.Sprintf(`; Written by Dotlane for its PHP %s sites, and rewritten each time it starts the pool.
[global]
error_log = %s
daemonize = no

[dotlane]
listen = %s
listen.mode = 0600
pm = ondemand
pm.max_children = 10
pm.process_idle_timeout = 60s
catch_workers_output = yes
decorate_workers_output = no
`, minor, iniQuote(log), iniQuote(socket))
}

// iniQuote quotes a path for PHP-FPM's INI files (macOS's state folder has a space in it).
func iniQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, ``) + `"` }

// frankenPHPConfig is a Caddyfile for one site's FrankenPHP: plain HTTP on socket, with no
// admin endpoint and nothing of its own done for TLS, which Dotlane's Caddy handles in front.
// PHP still sees HTTPS (HTTPS, SERVER_PORT), so apps build https:// URLs. In worker mode every
// request that is not a file goes to the worker script, as Laravel Octane sets it up. Hidden
// files (.env, .git) are never sent, as with PHP-FPM sites.
func frankenPHPConfig(site sites.Site, socket, dataDir string, httpsPort int) string {
	var worker string
	if w := site.PHP.Worker; w != "" {
		rel := filepath.ToSlash(w)
		worker = fmt.Sprintf(`
		index %s
		try_files {path} %s
		worker %s`, caddyQuote(rel), caddyQuote(rel), caddyQuote(filepath.Join(site.PHP.Root, w)))
	}
	return fmt.Sprintf(`# Written by Dotlane for its site %s, and rewritten each time it starts FrankenPHP.
{
	admin off
	auto_https off
	persist_config off
	skip_install_trust
	storage file_system %s
}

http:// {
	bind %s
	root %s
	@hidden {
		path */.*
		not path /.well-known/*
	}
	respond @hidden 404
	php_server {
		env HTTPS on
		env SERVER_PORT %d%s
	}
}
`, site.Name, caddyQuote(dataDir), caddyQuote("unix/"+filepath.ToSlash(socket)), caddyQuote(site.PHP.Root), httpsPort, worker)
}

// caddyQuote quotes a Caddyfile token.
func caddyQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
