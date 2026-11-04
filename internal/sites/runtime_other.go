//go:build !windows

package sites

// DefaultRuntime is the PHP runtime a site gets when it names none: PHP-FPM, which uses the PHP
// versions already installed, with their extensions.
const DefaultRuntime = RuntimeFPM
