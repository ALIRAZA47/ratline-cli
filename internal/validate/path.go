package validate

import (
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/ALIRAZA47/ratline-cli/internal/rlerr"
)

// subdirSegmentRe permits a leading underscore, because _next and _assets are
// real build-output directories, but not a leading dot (a dotfile nginx is
// configured to deny) or a leading hyphen (which git and tar would read as a
// flag).
var subdirSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// runtimeSegmentRe is the same rule with a leading dot allowed.
//
// The dot is refused above because nginx denies hidden files, so a document root inside
// one would 403 every request — a good early error. That reasoning does not reach a path
// nginx never touches. `.next/standalone/server.js` is the file node executes, and every
// Next.js standalone deployment in existence uses it; `.output/server/index.mjs` is Nuxt's
// and `.svelte-kit/output` is SvelteKit's.
//
// Applying the document-root rule to the entry point made the command in ratline's own
// Next.js guide fail with "the segment \".next\" may only contain letters, digits, dot,
// underscore and hyphen" — which lists a dot as allowed, because the rule is about where
// the dot sits rather than whether it appears.
var runtimeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9._-]*$`)

// AbsClean requires an absolute path and returns its cleaned form.
func AbsClean(p string) (string, error) {
	if p == "" {
		return "", rlerr.Usagef("the path is empty")
	}
	if strings.ContainsRune(p, 0) {
		return "", rlerr.Usagef("the path contains a NUL byte")
	}
	if !filepath.IsAbs(p) {
		return "", rlerr.Usagef("%q must be an absolute path", p)
	}
	return filepath.Clean(p), nil
}

// Subdir validates a relative directory name supplied by an operator —
// --root public, --build-output dist, --static-dir staticfiles.
//
// These land in nginx configs and unit files after being joined onto a site
// directory, so traversal, absolute paths and shell-significant characters are
// all refused here rather than being cleaned away silently.
func Subdir(name string) error { return subdir(name, subdirSegmentRe, false) }

// RuntimeSubdir is Subdir for a path only the runtime touches — an entry point, not a
// document root — where a leading dot is ordinary rather than a mistake.
func RuntimeSubdir(name string) error { return subdir(name, runtimeSegmentRe, true) }

func subdir(name string, segmentRe *regexp.Regexp, hidden bool) error {
	if name == "" {
		return rlerr.Usagef("the directory name is empty")
	}
	if len(name) > 255 {
		return rlerr.Usagef("the directory name %q is longer than 255 characters", name)
	}
	if strings.ContainsRune(name, 0) {
		return rlerr.Usagef("the directory name contains a NUL byte")
	}
	if filepath.IsAbs(name) {
		return rlerr.Usagef("%q must be relative to the site directory, not absolute", name)
	}
	if strings.ContainsAny(name, `\`) {
		return rlerr.Usagef("invalid directory name %q: use forward slashes", name)
	}
	segments := strings.Split(name, "/")
	for _, s := range segments {
		switch s {
		case "":
			return rlerr.Usagef("invalid directory name %q: it contains an empty path segment", name)
		case ".", "..":
			return rlerr.Usagef("invalid directory name %q: %q is not allowed", name, s)
		}
		if !segmentRe.MatchString(s) {
			// Naming where the character may sit, because "may only contain … dot" while
			// rejecting ".next" reads as a contradiction to anybody holding one.
			e := rlerr.Usagef("invalid directory name %q: the segment %q may only contain "+
				"letters, digits, dot, underscore and hyphen", name, s)
			if !hidden && strings.HasPrefix(s, ".") {
				return e.WithHint("it may not begin with a dot: nginx denies hidden files, so " +
					"a document root inside one would refuse every request. A build that " +
					"writes into .next or .output is served through the application instead")
			}
			if strings.HasPrefix(s, "-") {
				return e.WithHint("it may not begin with a hyphen, which git and tar read as a flag")
			}
			return e
		}
	}
	return nil
}

// WithinRoot checks containment lexically, without touching the filesystem.
// Use it on paths that do not exist yet; use ResolveWithin once they do.
func WithinRoot(root, candidate string) (string, error) {
	rootClean, err := AbsClean(root)
	if err != nil {
		return "", err
	}
	target := candidate
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootClean, target)
	}
	target = filepath.Clean(target)
	if !within(rootClean, target) {
		return "", rlerr.Preconditionf("%s is outside %s", target, rootClean).
			WithHint("a site's files must live inside its owner's home directory")
	}
	return target, nil
}

// ResolveWithin is the containment check that matters: it resolves symlinks
// before comparing, so a link planted inside a tenant's home cannot point a
// document root at /etc or at another tenant's files.
//
// The root must exist. The candidate need not: the deepest existing ancestor is
// resolved and the remainder appended, which is what lets this run before a
// directory tree has been created.
func ResolveWithin(root, candidate string) (string, error) {
	rootClean, err := AbsClean(root)
	if err != nil {
		return "", err
	}
	// The root must exist. Resolving symlinks in a root that does not is possible,
	// but it would change what this returns — on a host where /home is a symlink the
	// resolved root leaks into generated configuration — and this helper's output is
	// written into vhosts and units. A caller that legitimately runs before the tree
	// exists (a --dry-run preview) has to skip resolution rather than loosen this.
	rootReal, err := filepath.EvalSymlinks(rootClean)
	if err != nil {
		return "", rlerr.Wrap(err, rlerr.CodePrecondition, "cannot resolve %s", rootClean)
	}

	target := candidate
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootReal, target)
	}
	target = filepath.Clean(target)

	resolved, err := resolveDeepest(target)
	if err != nil {
		return "", err
	}
	if !within(rootReal, resolved) {
		return "", rlerr.Preconditionf("%s resolves to %s, which is outside %s", candidate, resolved, rootReal).
			WithHint("symlinks are followed before this check; a path may not escape the owner's home directory")
	}
	return resolved, nil
}

// resolveDeepest resolves symlinks in the longest existing prefix of p and
// re-appends whatever does not exist yet.
func resolveDeepest(p string) (string, error) {
	cur := filepath.Clean(p)
	rest := ""
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		if !isNotExist(err) {
			return "", rlerr.Wrap(err, rlerr.CodePrecondition, "cannot resolve %s", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Walked all the way to the root without finding anything that
			// exists, which on a real filesystem cannot happen.
			return filepath.Clean(p), nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// isNotExist also treats ENOTDIR as "does not exist": a path whose parent is a
// regular file reports ENOTDIR, and for containment purposes that is the same
// answer.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func within(root, p string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}
