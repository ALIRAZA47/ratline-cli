package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ALIRAZA47/ratline-cli/internal/state"
)

// Dynamic completion, because the arguments here are things an operator invented
// and cannot be expected to remember exactly. A domain, a tenant name and a
// certificate lineage are all values that exist only on this server, so a static
// completion list is worthless and the shell has to ask.
//
// Three properties matter for these to be usable rather than annoying:
//
//   - They must never block. A completion that hangs makes the shell feel broken,
//     so every lookup runs under a short timeout and returns nothing on failure.
//   - They must never print. cobra reserves stdout for the candidate list, so a log
//     line or an error message would be offered as a completion.
//   - They must degrade silently. Completion runs as whichever user pressed Tab,
//     which is frequently not root, and the state database is 0600. Returning no
//     candidates is the correct answer there.

// isCompletionRequest reports whether this invocation is cobra's completion helper.
//
// Matched by name rather than by an annotation because cobra adds `__complete`
// itself during Execute, after the tree has been built — there is no construction
// site at which to annotate it.
func isCompletionRequest(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if strings.HasPrefix(c.Name(), "__complete") {
			return true
		}
	}
	return false
}

// completionTimeout bounds every lookup. Tab completion that takes longer than this
// is worse than no completion at all.
const completionTimeout = 2 * time.Second

// completionStore opens state for a completion lookup, or reports that it cannot.
func (g *Globals) completionStore(ctx context.Context) (*state.Store, context.CancelFunc, bool) {
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	store, err := g.Store(ctx)
	if err != nil {
		cancel()
		return nil, func() {}, false
	}
	return store, cancel, true
}

// completeDomains offers every site's domain, with its runtime as the description.
func (g *Globals) completeDomains(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	store, cancel, ok := g.completionStore(cmd.Context())
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()

	sites, err := store.ListSites(cmd.Context(), state.SiteFilter{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		if !strings.HasPrefix(s.Domain, prefix) {
			continue
		}
		// The tab-separated description is cobra's convention; shells that support
		// it show it beside the candidate, and the rest ignore it.
		state := s.Runtime
		if !s.Enabled {
			state += ", disabled"
		}
		out = append(out, s.Domain+"\t"+state)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeUsers offers every tenant, with its site count.
func (g *Globals) completeUsers(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	store, cancel, ok := g.completionStore(cmd.Context())
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()

	users, err := store.ListUsers(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(users))
	for _, u := range users {
		if !strings.HasPrefix(u.Name, prefix) {
			continue
		}
		desc := "tenant"
		if n, err := store.CountSitesForUser(cmd.Context(), u.Name); err == nil {
			desc = plural(n, "site")
		}
		if u.Disabled {
			desc += ", disabled"
		}
		out = append(out, u.Name+"\t"+desc)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeCertificates offers every certificate by name.
func (g *Globals) completeCertificates(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	store, cancel, ok := g.completionStore(cmd.Context())
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()

	certs, err := store.ListCertificates(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(certs))
	for _, c := range certs {
		if !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		out = append(out, c.Name+"\t"+c.Source)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeKeyFingerprints offers the fingerprints of live keys, with their labels.
//
// A fingerprint is the one identifier in this tool nobody types from memory, so
// completion here is the difference between the command being usable and being
// preceded by `key list` every time.
func (g *Globals) completeKeyFingerprints(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	store, cancel, ok := g.completionStore(cmd.Context())
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()

	keys, err := store.ListKeys(cmd.Context(), state.KeyFilter{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !strings.HasPrefix(k.Fingerprint, prefix) {
			continue
		}
		desc := k.Scope
		if k.Label != "" {
			desc += " — " + k.Label
		}
		out = append(out, k.Fingerprint+"\t"+desc)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeFixed returns a completion function over a known set of values, for a
// flag whose choices are part of the command surface rather than the server's
// contents.
func completeFixed(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		out := make([]string, 0, len(values))
		for _, v := range values {
			if strings.HasPrefix(v, prefix) {
				out = append(out, v)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// registerCompletions attaches dynamic completion across the whole tree.
//
// Derived from each command's Use string rather than from a list of command paths.
// A path list is exactly the kind of thing that goes stale when a command is
// renamed or a subcommand is added a level deeper, and the argument placeholder is
// already the authoritative statement of what the argument is.
func registerCompletions(g *Globals, root *cobra.Command) {
	// Commands whose argument names something that does not exist yet. Offering the
	// existing names there would be actively misleading, since every one of them is
	// a value that would be refused as already taken.
	creates := map[string]bool{
		"site add": true, "user add": true, "db create": true,
		"site alias add": true, "site deploy-key create": true,
	}

	walk(root, func(cmd *cobra.Command) {
		path := strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
		if cmd.ValidArgsFunction == nil && !creates[path] {
			switch {
			case strings.Contains(cmd.Use, "<domain>"):
				cmd.ValidArgsFunction = g.completeDomains
			case strings.Contains(cmd.Use, "<username>"), strings.Contains(cmd.Use, "<user>"):
				cmd.ValidArgsFunction = g.completeUsers
			case strings.Contains(cmd.Use, "<fingerprint"):
				cmd.ValidArgsFunction = g.completeKeyFingerprints
			case strings.Contains(cmd.Use, "<cert"):
				cmd.ValidArgsFunction = g.completeCertificates
			}
		}
		// A creation command still completes nothing rather than falling back to
		// filenames: a domain is not a path, and offering the working directory's
		// contents is noise.
		if cmd.ValidArgsFunction == nil && creates[path] {
			cmd.ValidArgsFunction = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
		}

		// Flags whose values name something on this server.
		if cmd.Flags().Lookup("user") != nil {
			_ = cmd.RegisterFlagCompletionFunc("user", g.completeUsers)
		}
		if cmd.Flags().Lookup("site") != nil {
			_ = cmd.RegisterFlagCompletionFunc("site", g.completeDomains)
		}
		// Flags whose values are part of the command surface rather than the
		// server's contents. Kept here beside the flag definitions they mirror, so a
		// new accepted value is one edit away from being completable.
		for name, values := range map[string][]string{
			"runtime":   {"static", "node", "bun", "python"},
			"daemon":    {"pm2", "direct"},
			"listen":    {"socket", "port"},
			"scope":     {"global", "user", "site"},
			"ssl":       {"letsencrypt", "selfsigned", "none"},
			"challenge": {"http", "dns"},
			"key-type":  {"ecdsa", "rsa"},
			"format":    {"json", "yaml"},
		} {
			if cmd.Flags().Lookup(name) != nil {
				_ = cmd.RegisterFlagCompletionFunc(name, completeFixed(values...))
			}
		}
	})
}

// walk visits a command and every descendant.
func walk(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, c := range cmd.Commands() {
		walk(c, fn)
	}
}
