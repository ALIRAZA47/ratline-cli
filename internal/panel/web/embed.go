// Package web carries the built single-page interface inside the binary.
//
// Embedded rather than installed for the same reason ratline embeds its explainer
// topics: one file to copy onto a server, nothing to keep in step with the binary
// beside it, and no directory an operator can half-upgrade. A panel is administered
// by whoever runs it, and "the interface is from the previous release" is a bug that
// only exists if there is somewhere for the interface to live separately.
//
// Two directories, and the second is why a checkout without Node still compiles.
// `dist` is the build's output and holds nothing but a .gitkeep in a fresh clone;
// `placeholder` is a committed page that says the interface was not built and how to
// build it. Serving that beats serving a blank screen, and committing the real bundle
// to keep the embed happy would put a build artefact in the history that changes on
// every build.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var files embed.FS

//go:embed placeholder
var placeholder embed.FS

// Assets returns the built interface, or the placeholder when there is none.
func Assets() (fs.FS, error) {
	built, err := fs.Sub(files, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(built, "index.html"); err == nil {
		return built, nil
	}
	return fs.Sub(placeholder, "placeholder")
}

// Built reports whether the real interface is in this binary, so `doctor` can say so
// rather than leaving somebody to wonder why the page looks like that.
func Built() bool {
	built, err := fs.Sub(files, "dist")
	if err != nil {
		return false
	}
	_, err = fs.Stat(built, "index.html")
	return err == nil
}

// Handler serves the interface, with the routing a single-page application needs.
//
// The rule is: a request that looks like a file is served or 404s, and everything
// else gets index.html. That is what makes /sites/example.com work when somebody
// pastes the link rather than clicking through to it — React owns the path, and the
// server has to hand it the page before it can.
//
// Hashed assets are immutable and told so; index.html is never cached, because a
// cached shell pointing at last release's bundle is how a panel breaks after an
// upgrade in a way that clears itself only on a hard refresh nobody thinks to do.
func Handler() (http.Handler, error) {
	assets, err := Assets()
	if err != nil {
		return nil, err
	}
	server := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + r.URL.Path)
		if clean == "/" {
			serveIndex(w, r, assets)
			return
		}
		f, err := assets.Open(strings.TrimPrefix(clean, "/"))
		if err != nil {
			// Not a file, so it is either a route or a genuinely missing asset.
			// An asset request that misses must be a real 404: serving index.html
			// for a missing .js hands the browser HTML where it expected a script,
			// for a blank page and a console error that says nothing useful.
			if looksLikeAnAsset(clean) {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, r, assets)
			return
		}
		_ = f.Close()
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		server.ServeHTTP(w, r)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	body, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "the panel interface is missing from this binary", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "index.html", modTime(assets), strings.NewReader(string(body)))
}

// staticExtensions is the closed list of things the browser fetches as files.
//
// A closed list rather than "has an extension", and the difference is not academic:
// every site detail page in this panel is /sites/<domain>, and every domain ends in
// something that looks exactly like a file extension. The first version of this
// returned 404 for /sites/example.com on a refresh — a deep link that worked when
// clicked and broke when pasted.
var staticExtensions = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".map": true,
	".ico": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".webp": true, ".avif": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".json": true, ".txt": true, ".webmanifest": true, ".xml": true,
}

func looksLikeAnAsset(p string) bool {
	// Everything the build emits lives here, so a miss under it is always a miss.
	if strings.HasPrefix(p, "/assets/") {
		return true
	}
	return staticExtensions[strings.ToLower(path.Ext(p))]
}

// modTime is zero, which makes ServeContent skip Last-Modified entirely. An embedded
// file has no meaningful modification time — every file in the binary would claim the
// zero time — and a wrong one is worse than none.
func modTime(fs.FS) time.Time { return time.Time{} }
