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
	restrictedUIPage       = `/one/task.html`
	restrictedUITaskPrefix = `/tasks/`
	restrictedUIHTMLSuffix = `.html`

	// The commercial service's surface: /v1 with no /api segment, same host,
	// different codebase. static()'s early return covers "/api/" only, so this
	// prefix has to be named here or the lockout swallows it.
	restrictedUICommercialPrefix = `/v1/`
)

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
	if target == requested {
		return echo.ErrNotFound
	}

	http.Redirect(c.Response(), c.Request(), target, http.StatusFound)

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

	return restrictedUIPage + `?task=` + id
}
