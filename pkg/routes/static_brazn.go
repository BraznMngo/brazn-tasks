// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-present Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package routes

import (
	"net/http"
	"path"
	"strings"

	"code.vikunja.io/api/pkg/config"

	"github.com/labstack/echo/v5"
)

// The restricted-UI lockout, brazn.restricteduionly. Fork-only, and it lives in
// its own file for the same reason managed_gate.go, managed_rules_*.go and
// admin_gate.go do: static.go is upstream-derived, so every line we leave in it
// is re-resolved by hand on each upstream merge. What static.go carries of this
// is three changed lines and nothing else: the two calls that stand in for
// serveIndexFile, and one extra term on the condition above the second of them.
const (
	// Settings is the general entry point and its own document; the task detail
	// is a deep link and its own. A settings screen answering to a URL called
	// task.html is a URL nobody can hand to anyone.
	restrictedUIPage       = `/one/settings.html`
	restrictedUITaskPage   = `/one/task.html`
	restrictedUITaskPrefix = `/tasks/`
	restrictedUIHTMLSuffix = `.html`

	// The commercial service's surface: /v1 with no /api segment, same host,
	// different codebase. static()'s early return covers "/api/" only, so this
	// prefix has to be named here or the lockout swallows it.
	restrictedUICommercialPrefix = `/v1/`

	// The OIDC round trip returns to /auth/openid/{provider}, so this one is a
	// prefix rather than an exact path.
	restrictedUIAuthPrefix = `/auth/`

	// The application's service worker, which the lockout has to replace rather
	// than merely stop serving. See braznEvictingServiceWorker.
	restrictedUIServiceWorkerPath = `/sw.js`
	restrictedUIServiceWorkerFile = `sw.js`
)

// braznEvictingServiceWorker is what /sw.js answers while the lockout is on.
//
// INTERCEPTING SERVER READS OF dist/index.html IS NOT ENOUGH, and this is the
// hole that closes. frontend/src/sw.ts precaches the built assets
// (`precacheAndRoute(self.__WB_MANIFEST)`), answers HTML with
// StaleWhileRevalidate, and calls `clientsClaim()`. So a browser that visited
// the application before the key was turned on keeps a controlled navigation
// served FROM ITS OWN CACHE: it never reaches this server, the cached chunks run
// normally, and the deliberately-unblocked APIs keep the whole SPA working.
// Flipping a server-side config evicts nothing.
//
// A service worker is replaced by BYTES, not by configuration: the browser
// re-fetches this script on navigation and whenever it checks for an update,
// and installs it if it differs from the one it holds. So the fix is to serve a
// different script — one whose entire job is to delete every cache, unregister
// itself, and reload the windows it controls. `skipWaiting` and the
// unconditional activate are what make that happen on the first update check
// rather than after every tab is closed.
//
// Written as a literal rather than assembled, so what a browser executes is
// reviewable here in one piece.
const braznEvictingServiceWorker = `// Brazn Tasks: the restricted-UI lockout is on (brazn.restricteduionly).
// This replaces the application's service worker so an installed one evicts
// itself, its caches, and the offline copy of the Vue application.
self.addEventListener('install', function () {
  self.skipWaiting();
});
self.addEventListener('activate', function (event) {
  event.waitUntil(
    caches.keys()
      .then(function (names) {
        return Promise.all(names.map(function (name) { return caches.delete(name); }));
      })
      .then(function () { return self.registration.unregister(); })
      .then(function () { return self.clients.matchAll({type: 'window'}); })
      .then(function (windows) {
        windows.forEach(function (w) { w.navigate(w.url); });
      })
  );
});
`

// restrictedUIAuthPaths are the vue-router paths that must keep reaching the app
// shell while the lockout is on.
//
// WITHOUT THIS THE LOCKOUT LOCKS EVERYONE OUT. The sign-in form is part of the
// Vue application; there is no separate login document. So a signed-out visitor
// is redirected to the restricted page, the page finds no session and hands off
// to /login exactly as the SPA does, /login is a vue-router path that matches no
// file and no route, and the fallback redirects it back to the restricted page.
// The browser gives up with ERR_TOO_MANY_REDIRECTS and nobody can ever sign in.
//
// This is a DELIBERATE, NARROW HOLE in "the SPA is never delivered", and it is
// worth being honest about its size: serving the shell at /login serves the whole
// application, because the router is client-side. Someone who signs in and then
// types a path can stay in it. The alternative is building a second sign-in form,
// which the ticket forbids (bar 4, do not touch auth) and which would be a worse
// answer anyway — a second credential surface to keep correct.
//
// What the lockout still buys with this hole open: every ORDINARY route in is
// closed. The root, every task path, every settings path and every deep link a
// user actually holds land on the restricted page, and a successful sign-in
// returns to "/", which is redirected. The SPA stops being where people are sent
// and becomes somewhere they must deliberately go.
var restrictedUIAuthPaths = map[string]bool{
	"/login":              true,
	"/register":           true,
	"/password-reset":     true,
	"/get-password-reset": true,
}

// braznBlocksAppShell reports whether a request that resolved to a REAL FILE in
// the embedded dist/ must be diverted to braznServeAppShell rather than served.
//
// It exists because the premise this feature was first built on was false. The
// claim was that the Vue SPA is delivered by exactly one function,
// serveIndexFile, so intercepting its two call sites was the whole job. But
// dist/index.html is itself a real file in the embed FS, and static() serves
// real files verbatim, so with the key on:
//
//	GET /index.html
//	-> name = path.Join("dist/", path.Clean("//index.html")) = "dist/index.html"
//	-> assetFs.Open succeeds — the very file serveIndexFile opens
//	-> info.IsDir() is false
//	-> generateEtag, then serveFile (static.go:189): HTTP 200 carrying the SPA,
//	   and braznServeAppShell never runs.
//
// And it is a working SPA rather than a husk: the document carries
// <div id="app"></div> and the module script for the hashed /assets/*.js chunks,
// which are served normally, and the router is client-side, so every route in
// the application then works with no further round trip. One guessable URL
// defeated the entire lockout.
//
// IT RETURNS FALSE WHENEVER THE KEY IS OFF, and that is the reason this is a
// function at all rather than a comparison written inline in static.go. Inline
// — `info.IsDir() || name == path.Join(rootPath, indexFile)` — would divert
// /index.html into serveIndexFile on every instance, lockout or not, and that is
// a deviation from upstream with nothing to buy it: serveIndexFile passes an
// empty etag (static.go:130) so the file would lose the ETag serveFile gives it,
// and it would gain the injected SPA config that upstream does not put there.
// With the key off this fork must be byte-identical to upstream, so the key is
// checked here, and first.
//
// The second clause blocks any other .html under dist/ that is not under the
// restricted page's own directory. Nothing in the build emits one today — Vite
// has a single HTML entry, frontend/index.html, and frontend/public/ holds
// exactly one .html, the restricted page — so it costs nothing today, and it is
// what stops an .html dropped into frontend/public/ later from silently
// reopening the hole.
//
// THE /one/ EXEMPTION IS LOAD-BEARING, not a nicety. dist/one/task.html is the
// page this whole feature exists to serve. Without the exemption it would be
// diverted here, braznRestrictedUITarget would compute it as its own target, and
// the redirect-loop guard would answer 404 — a locked-down instance with no
// interface at all, which is the failure the lockout is supposed to prevent.
func braznBlocksAppShell(name string) bool {
	if !config.BraznRestrictedUIOnly.GetBool() {
		return false
	}

	if name == path.Join(rootPath, indexFile) {
		return true
	}

	// The service worker is a real file too, so it would otherwise be served
	// verbatim and keep handing out the cached application. Diverted here and
	// answered with the evicting script instead.
	if name == path.Join(rootPath, restrictedUIServiceWorkerFile) {
		return true
	}

	if !strings.HasSuffix(name, restrictedUIHTMLSuffix) {
		return false
	}

	// "dist/one/", derived from restrictedUIPage rather than spelled out, so the
	// exemption cannot drift away from the page it exists for.
	exemptDir := path.Join(rootPath, path.Dir(restrictedUIPage)) + `/`

	return !strings.HasPrefix(name, exemptDir)
}

// braznServeAppShell stands in for serveIndexFile at its two call sites in
// static(): the not-found fallback and the directory case. Together with
// braznBlocksAppShell on the second of those conditions, those are every way the
// Vue SPA can leave this server — serveIndexFile is the function that injects
// the SPA config into <div id="app"></div>, the raw dist/index.html is the only
// other document that loads the SPA's entry module, and the hashed asset chunks
// are inert on their own because nothing remains to load them.
//
// With the key off this is byte-identical to calling serveIndexFile directly.
//
// Intercepting LATE rather than before the file lookup is the whole design.
// next(c) has already run by the time the not-found fallback is reached, so
// every registered handler still answers for itself: /dav, /.well-known,
// /health, /feeds and the API keep working, and they keep working by
// construction rather than through an allowlist somebody has to maintain. An
// earlier revision of this intercepted before the lookup and 302'd all four of
// those at the restricted page, which blinds monitoring and breaks every CalDAV
// client.
//
// The other call site does run ahead of next(c), and braznBlocksAppShell widened
// it, so it is worth saying why that costs no handler anything: it is reached
// only for a path that resolves to a real directory or a real file in dist/, and
// upstream static() already shadows a registered route with a file of the same
// name at that same point. No route this application registers is a directory or
// a .html in dist/, so the set of requests that reach a handler is unchanged.
//
// /one/task.html needs no special case either. It is a real file and
// braznBlocksAppShell exempts its directory, so static() serves it by the
// ordinary path with the same ETag and cache headers every other asset gets, and
// it never reaches this function at all.
func braznServeAppShell(c *echo.Context, assetFs http.FileSystem) error {
	if !config.BraznRestrictedUIOnly.GetBool() {
		return serveIndexFile(c, assetFs)
	}

	requested := path.Clean("/" + c.Request().URL.Path)

	// Answered before anything else: a redirect here would leave the installed
	// worker in place, and the worker is what keeps serving the cached
	// application without ever reaching this server.
	if requested == restrictedUIServiceWorkerPath {
		// no-store, or the browser can satisfy its own update check from cache
		// and never see the replacement.
		c.Response().Header().Set("Content-Type", "text/javascript; charset=utf-8")
		c.Response().Header().Set("Cache-Control", "no-store")
		c.Response().WriteHeader(http.StatusOK)
		_, err := c.Response().Write([]byte(braznEvictingServiceWorker))

		return err
	}

	// THE COMMERCIAL SERVICE IS NOT UI AND MUST NOT BE REDIRECTED. static()
	// returns early for "/api/" (static.go:140-142) but that prefix does not
	// cover "/v1/", which is the commercial service's own surface — no /api
	// segment, same host, different codebase. Without this, turning the key on
	// answers an unrouted /v1 call with 302 -> /one/task.html -> 200 text/html,
	// which is a refusal wearing a success code: exactly the shape the page's
	// commercial guard exists to reject, manufactured by us rather than by the
	// service. Answer 404, which is what "this server does not serve /v1" means.
	if strings.HasPrefix(requested, restrictedUICommercialPrefix) {
		return echo.ErrNotFound
	}

	target := braznRestrictedUITarget(requested)

	// THE REDIRECT-LOOP GUARD. It is not paranoia; write the loop out before
	// touching it. On any build where the page was not copied into dist/ —
	// CI's stub dist/ is exactly that, and so is a mis-built image:
	//
	//	GET /one/task.html
	//	-> assetFs.Open("dist/one/task.html") does not exist
	//	-> next(c) matches no route, echo answers 404
	//	-> static() takes its fallback and calls this function
	//	-> the target computed for /one/task.html is /one/task.html
	//	-> 302 Location: /one/task.html
	//	-> the browser asks for it again, and again, forever.
	//
	// A missing page has to read as missing. This is the only request that can
	// trip it: every other target either differs from the cleaned request path
	// or carries a ?task= query, which a cleaned path never has.
	// Both documents, not just the settings one: /one/task.html with no query
	// computes itself as its target too, and on a build where the page was not
	// copied into dist/ that is the same infinite bounce.
	if target == requested || requested == restrictedUITaskPage {
		return echo.ErrNotFound
	}

	// Authentication has to stay reachable, or the lockout is a lockout on
	// everyone — see restrictedUIAuthPaths for why, and for what it costs.
	if restrictedUIAuthPaths[requested] || strings.HasPrefix(requested, restrictedUIAuthPrefix) {
		return serveIndexFile(c, assetFs)
	}

	http.Redirect(c.Response(), c.Request(), braznRestrictedUILocation(target), http.StatusFound)

	return nil
}

// braznRestrictedUITarget is where a locked-out request is sent. A /tasks/{id}
// deep link keeps its numeric id, so a link somebody already holds still opens
// the task it named; everything else lands on the page itself. Redirecting
// rather than 404ing is what preserves those links.
//
// The id must be a bare run of digits rather than merely parseable: it is the
// only caller-supplied text that reaches the Location header, and "+1" would
// arrive at the page as a space.
//
// Pure, and takes the cleaned path rather than the context, so the whole
// decision table is testable without an embedded filesystem.
func braznRestrictedUITarget(cleanedPath string) string {
	id := strings.TrimPrefix(cleanedPath, restrictedUITaskPrefix)
	if id == cleanedPath || id == "" || strings.Trim(id, `0123456789`) != "" {
		return restrictedUIPage
	}

	return restrictedUITaskPage + `?task=` + id
}

// braznRestrictedUILocation turns an app-relative target into a Location the
// browser can follow.
//
// THE BINARY DOES NOT KNOW WHERE IT IS MOUNTED. It is served at the host root,
// so the target and the Location are the same string today. Building the
// Location from configuration rather than assuming that keeps a redirect from
// pointing off the product if it ever moves.
//
// service.publicurl is the one value configured for exactly this. static.go:111
// already reads it the same way, for the API base it writes into index.html, and
// the deployment sets it to the prefixed URL. Empty means "mounted at the root",
// which is what a plain build and CI both are, and the target is already correct
// for that.
func braznRestrictedUILocation(target string) string {
	publicURL := config.ServicePublicURL.GetString()
	if publicURL == "" {
		return target
	}

	return strings.TrimSuffix(publicURL, "/") + target
}
