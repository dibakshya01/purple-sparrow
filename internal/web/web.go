// Package web embeds the admin dashboard into the binary so releases are
// self-contained. The dashboard (M9) is a dependency-free single-page app
// (static/index.html, no build step) that talks only to the public API with an
// admin key — keeping the single static binary with no Node/CDN requirement.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var assets embed.FS

// AssetHandler returns an http.Handler serving the embedded static assets. It is
// intended to be mounted under a dedicated prefix (e.g. /assets) so it never
// shadows API routes; unknown API paths must return the error envelope, not a
// file-server 404. Used by later milestones once the SPA ships.
func AssetHandler() http.Handler {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		// The embed path is a compile-time constant; this cannot fail in a built
		// binary. Panic loudly if it somehow does.
		panic("web: embedded assets missing: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}

// Index serves the dashboard document at the root. Mounting this at exactly "/"
// keeps every other unmatched path flowing to the router's NotFound handler.
func Index() http.Handler {
	b, err := assets.ReadFile("static/index.html")
	if err != nil {
		panic("web: index.html missing: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
}
