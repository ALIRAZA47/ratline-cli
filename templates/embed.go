// Package templates holds every file ratline renders onto a server: nginx
// vhosts and snippets, systemd units, and logrotate configuration.
//
// They live in one embedded tree rather than as string literals next to the code
// that renders them, so that the exact text an operator will find in
// /etc/nginx is reviewable as a file in the repository, and diffable across
// versions.
package templates

import "embed"

//go:embed all:nginx all:systemd all:logrotate all:mongo all:tmpfiles
var FS embed.FS
