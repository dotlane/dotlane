### Added

- **PHP sites**: `dotlane add shop --php ~/code/shop/public [--php-version 8.4]`, or
  `root = "public"` and `php = "8.4"` (or `"latest"`) in `dotlane.toml`, serve a folder of PHP
  files with the PHP versions already installed (Homebrew, mise, asdf, the system's packages)
  through PHP-FPM, one pool per version, so each site can have its own. Dotlane starts, stops and
  restarts the pools itself; front controllers (Laravel, Symfony) work, and `.env` and other
  hidden files are never served.
- **FrankenPHP** as a PHP runtime per site (`runtime = "frankenphp"`, or `dotlane add
  --frankenphp`), worker mode included (`worker = "frankenphp-worker.php"`). `dotlane frankenphp
  install` downloads it, checked against the SHA-256 this release pins. On Windows it is the PHP
  runtime, as PHP-FPM does not exist there.
- `dotlane ls` shows a PHP site's folder and PHP, `dotlane doctor` checks each PHP runtime in
  use, and Dotlane's own pages say when a PHP site's PHP is missing or not answering.

### Changed

- `dotlane ls`'s third column is now **SERVED FROM**: the port a site forwards to, or a PHP
  site's folder and PHP.
