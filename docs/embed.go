// Package docs embeds the explainer topics into the binary.
//
// go:embed cannot reach a parent directory, so this file lives beside the
// documentation rather than inside internal/cli. Being embedded rather than
// installed is the point: `ratline explain` has to work on a freshly provisioned
// server reached over SSH, with no browser, no package documentation and possibly
// no network — which is exactly the situation in which someone needs it.
//
// These are the same files the documentation site renders, so there is one source
// of truth and no chance of the binary's answer differing from the website's.
package docs

import "embed"

// Topics holds one markdown file per explainer topic. The filename without its
// extension is the topic name.
//
//go:embed topics/*.md
var Topics embed.FS
