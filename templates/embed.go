// Package templates holds every file ratline renders onto a server: nginx
// vhosts and snippets, systemd units, and logrotate configuration.
//
// The panel subdirectory belongs to ratline-panel rather than to ratline. It is
// embedded here anyway because the two ship from one repository and one embed tree
// is easier to review than two — and because a panel vhost sitting beside the site
// vhost it will be compared against is the point.
//
// They live in one embedded tree rather than as string literals next to the code
// that renders them, so that the exact text an operator will find in
// /etc/nginx is reviewable as a file in the repository, and diffable across
// versions.
package templates

import "embed"

//go:embed all:nginx all:systemd all:logrotate all:mongo all:tmpfiles all:panel
var FS embed.FS
