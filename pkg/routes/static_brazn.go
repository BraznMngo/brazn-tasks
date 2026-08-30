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
	"net/url"
	"os"
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
	// THE ONE LIST OF DOCUMENTS THIS PRODUCT SERVES (BRA-1475 criterion 18).
	// Every page a person can be given lives in this block and nowhere else, and
	// two things read it. braznBlocksAppShell serves an .html under dist/one/
	// only when it is named here, so a page dropped into frontend/public/one/
	// and forgotten is not quietly reachable. And the "Every ONE document is on
	// the one list" step of .github/workflows/fork-guards.yml fails the build
	// both ways: when a document exists in frontend/public/one/ and is missing
	// from this block, and when a document named here was never written.
	//
	// That step matches these literals by shape, so keep one document per line
	// and never write a /one/*.html path in backticks anywhere else in this
	// file — a mention in a comment would read as an eighth document.
	//
	// Settings is the general entry point and its own document; the task detail
	// is a deep link and its own. A settings screen answering to a URL called
	// task.html is a URL nobody can hand to anyone.
	restrictedUIPage          = `/one/settings.html`
	restrictedUITaskPage      = `/one/task.html`
	restrictedUISignInPage    = `/one/signin.html`
	restrictedUIJoinPage      = `/one/join.html`
	restrictedUIPasswordPage  = `/one/password.html`
	restrictedUIConfirmedPage = `/one/confirmed.html`
	restrictedUIErrorPage     = `/one/error.html`

	restrictedUITaskPrefix = `/tasks/`
	restrictedUIHTMLSuffix = `.html`
	restrictedUIDir        = `/one/`

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

// restrictedUIDocuments is the set form of the block above, and it is what
// makes that block load-bearing rather than descriptive. Membership is by the
// document's own address under /one/, which is what braznBlocksAppShell holds
// after stripping dist/ from an embedded file name.
var restrictedUIDocuments = map[string]bool{
	restrictedUIPage:          true,
	restrictedUITaskPage:      true,
	restrictedUISignInPage:    true,
	restrictedUIJoinPage:      true,
	restrictedUIPasswordPage:  true,
	restrictedUIConfirmedPage: true,
	restrictedUIErrorPage:     true,
}

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

// THE EXEMPTION LIST IS GONE, and its absence is the point (BRA-1475
// criterion 17). What stood here was restrictedUIAuthPaths, six vue-router
// paths that kept reaching the Vue application while the lockout was on,
// because the sign-in form was part of that application and there was no
// separate document to send anybody to. Its own comment admitted what it cost:
// serving the shell at /login serves the whole application, router included,
// so every one of the twelve old settings pages was one typed path away.
//
// The list is empty because it has nothing left to do. Every address it named
// is now answered by a document of ours, named in the block at the top of this
// file, so nothing has to keep serving the old application to keep anybody
// able to sign in. serveIndexFile is not reached at all while the key is on.
//
// restrictedUIRewrites is that replacement: the address a person arrives at,
// and the document that answers there.
//
// THESE ARE SERVED IN PLACE AND NEVER REDIRECTED TO, which is the whole reason
// fault 1 of BRA-1475 cannot come back. A redirect carries only its
// destination, so a token in the query is discarded on the way — that is how a
// mailed password-reset link came to land on a settings page with the token
// gone. Serving the document at the address the person actually asked for
// leaves the request untouched: the query is still there for the page to read,
// and so is the path, which is where /auth/openid/{provider} keeps the name of
// the provider that just answered.
//
// It also keeps the desktop application's five parameters to one line in the
// access log. Redirecting /oauth/authorize would copy them into a second
// request, and the ticket is explicit that those parameters are not to be
// spread around.
//
// /register has no page of its own: an account is created on the public
// website, not here. It answers with the sign-in document because that
// document carries the link to where accounts are made, which is a better
// answer for a bookmark than the settings page a signed-out person would
// otherwise be bounced through.
var restrictedUIRewrites = map[string]string{
	"/login":    restrictedUISignInPage,
	"/register": restrictedUISignInPage,
	// The native client's OAuth consent screen. Note the /auth/ prefix used
	// below does NOT cover "/oauth/" — they are different routes
	// (frontend/src/router/index.ts:502).
	"/oauth/authorize": restrictedUISignInPage,
	// One document with two states: asking for a reset, and setting the new
	// password. Which state it shows is decided by whether a token arrived, so
	// the two addresses do not have to be told apart here.
	"/password-reset":     restrictedUIPasswordPage,
	"/get-password-reset": restrictedUIPasswordPage,
	// Where the email-confirmation link used to land after the SPA's router
	// guard moved it (frontend/src/router/index.ts:96, :629-631).
	"/confirm": restrictedUIConfirmedPage,
}

// restrictedUIMailedTokens are the tokens the server mails to a person AT THE
// SITE ROOT, and they are the reason a table of paths is not enough on its own.
//
// pkg/user/notifications.go builds those links as
// `ServicePublicURL + "?userEmailConfirm=" + token` — the ROOT with a query,
// not a path anybody could name in a table. The root must keep redirecting,
// because it is the main way in; a root request carrying one of these is
// answered with the document that owns the token instead.
//
// Ordered rather than a map, so that a request carrying two of these names is
// answered the same way every time.
//
// The password-reset name is what rescues the links already sitting in
// inboxes: pkg/user/notifications.go now points new mail straight at the
// password document, but a link mailed before that change still arrives here.
//
// accountDeletionConfirm IS NOT IN BRA-1475's TABLE OF DOCUMENTS, and this is
// the honest placement rather than the specified one. It cannot keep reaching
// the Vue application, because that is the hole criterion 17 closes, and it
// must not be redirected, because that would discard the token. The confirmed
// document is the result screen a mailed token ends on, so it goes there and
// the page has to be able to say what happened. On this deployment the link is
// not produced at all today: both POST /api/v{1,2}/user/deletion/request and
// .../confirm are classified service-managed in
// pkg/routes/route-classification.json, so a browser cannot ask for one and
// the mail that carries this token is never sent.
var restrictedUIMailedTokens = []struct {
	query    string
	document string
}{
	{"userPasswordReset", restrictedUIPasswordPage},
	{"userEmailConfirm", restrictedUIConfirmedPage},
	{"accountDeletionConfirm", restrictedUIConfirmedPage},
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
// The last clause blocks every .html under dist/ that this product has not
// decided to serve. Vite has a single HTML entry, frontend/index.html, and
// copies frontend/public/ in verbatim, so the documents in that directory are
// the only other .html the build can emit — and an .html dropped in there later
// stays blocked until somebody names it, rather than becoming reachable the
// moment it is committed.
//
// THE EXEMPTION IS LOAD-BEARING, not a nicety, and it is BY DOCUMENT NAME
// rather than by directory (BRA-1475 criterion 18). dist/one/settings.html is
// the page this whole feature exists to serve; without an exemption it would be
// diverted into braznServeAppShell and answered 404, which is a locked-down
// instance with no interface at all. It used to be the whole of dist/one/,
// which meant anything committed under that directory was served to anybody who
// asked. Naming each document is what gives criterion 18 its single list, and
// it is the list the fork-guards step checks the built page set against.
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

	// static.go builds `name` as path.Join(rootPath, …), so "dist/one/x.html"
	// here is the document "/one/x.html" there. Anything outside dist/ cannot
	// match, which is what keeps a stray .html elsewhere in dist/ blocked.
	return !restrictedUIDocuments[`/`+strings.TrimPrefix(name, rootPath)]
}

// braznServeAppShell stands in for serveIndexFile at its two call sites in
// static(): the not-found fallback and the directory case. Together with
// braznBlocksAppShell on the second of those conditions, those are every way the
// Vue SPA can leave this server — serveIndexFile is the function that injects
// the SPA config into <div id="app"></div>, the raw dist/index.html is the only
// other document that loads the SPA's entry module, and the hashed asset chunks
// are inert on their own because nothing remains to load them.
//
// WITH THE KEY ON, THE VUE APPLICATION IS NEVER SERVED, BY ANY ADDRESS, TO
// ANYBODY (BRA-1475 criterion 17). serveIndexFile is called from exactly one
// place below and only when the key is off. That is a stronger statement than
// "the exemption list is empty", and it is the one worth checking: an empty
// list can be added to, whereas a function nothing calls cannot serve anybody.
//
// LINK SHARING IS CLOSED TO A SIGNED-OUT VISITOR, deliberately, and this is
// where the /share/ prefix used to be exempt. A share recipient authenticated
// at /share/{hash}/auth, which is a route of the Vue application, so keeping it
// open kept the whole application open. BRA-1475 rules the prefix shut like any
// other, so it is now an ordinary locked-out request.
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
// braznBlocksAppShell exempts it by name, so static() serves it by the ordinary
// path with the same ETag and cache headers every other asset gets, and it
// never reaches this function at all.
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

	// THE REDIRECT-LOOP GUARD. It is not paranoia; write the loop out before
	// touching it. On any build where a document was not copied into dist/ —
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
	// A missing page has to read as missing. The rule is structural rather than
	// a list of the documents that happen to exist today: reaching this
	// function at all means the file was not there, so ANY .html under /one/
	// that gets here is a document the build failed to produce, and every one
	// of them would otherwise bounce — either at itself, or off the settings
	// page which on that build is missing too. Nothing else under /one/ is
	// affected, because /one/missing and /one/ carry no .html suffix and are
	// ordinary locked-out requests.
	//
	// This replaces the `target == requested` comparison that stood here, and
	// subsumes it: every target braznRestrictedUITarget can produce is a
	// document under /one/, so a request that equals its own target is one this
	// test has already caught. Keeping both would leave a second condition
	// nothing can reach, which reads like a live guard to whoever comes next.
	if braznIsRestrictedUIDocument(requested) {
		return echo.ErrNotFound
	}

	// A document of ours answering at this address is SERVED HERE, at the
	// address asked for, rather than redirected to. See restrictedUIRewrites
	// for why that is not a style choice: a redirect would discard the query,
	// and the query is the token.
	if document := braznRestrictedUIDocument(requested, c.Request().URL.Query()); document != "" {
		return braznServeDocument(c, assetFs, document)
	}

	target := braznRestrictedUITarget(requested)

	http.Redirect(c.Response(), c.Request(), braznRestrictedUILocation(target), http.StatusFound)

	return nil
}

// braznServeDocument serves one of our own documents at whatever address the
// request arrived at, with the ETag and cache headers static() gives every
// other file, and answers 404 when the build did not produce it.
//
// THE 404 IS THE POINT. Every other way of handling a missing document ends in
// a redirect, and a redirect to a document that is also missing is the infinite
// bounce the guard above exists to prevent. A build that failed to copy the
// page in has to read as a build that failed.
func braznServeDocument(c *echo.Context, assetFs http.FileSystem, document string) error {
	name := path.Join(rootPath, document)

	file, err := assetFs.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return echo.ErrNotFound
		}

		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	etag, err := generateEtag(file, name)
	if err != nil {
		return err
	}

	return serveFile(c, file, info, etag)
}

// braznIsRestrictedUIDocument reports whether a cleaned request path names one
// of our documents. It is a shape test rather than a lookup in
// restrictedUIDocuments on purpose: the caller is asking "was this request for
// a page that should have been a file", and a page added to the build but not
// yet to the list must answer yes to that, or it bounces instead of 404ing.
func braznIsRestrictedUIDocument(cleanedPath string) bool {
	return strings.HasPrefix(cleanedPath, restrictedUIDir) &&
		strings.HasSuffix(cleanedPath, restrictedUIHTMLSuffix)
}

// braznRestrictedUIDocument answers which document must be served at this
// address, or "" when the request is an ordinary one to be redirected.
//
// A MAILED TOKEN IS ANSWERED FIRST, whatever path it arrives on. The token is
// the thing that cannot be replaced — it is spent, or it expires, and the
// person cannot get another one without asking again — so the document that
// knows what to do with it wins over the document the path would have chosen.
// In practice the two never disagree: the reset link is the site root and the
// reset page is /password-reset, and both lead to the same document.
//
// Pure, and takes the cleaned path and the parsed query rather than the
// context, so the whole decision table can be exercised without an embedded
// filesystem.
func braznRestrictedUIDocument(cleanedPath string, query url.Values) string {
	for _, mailed := range restrictedUIMailedTokens {
		if query.Has(mailed.query) {
			return mailed.document
		}
	}

	if document, ok := restrictedUIRewrites[cleanedPath]; ok {
		return document
	}

	// The OIDC round trip returns to /auth/openid/{provider}, and the provider
	// is in the path rather than the query, which is the other reason these
	// documents are served in place rather than redirected to.
	if strings.HasPrefix(cleanedPath, restrictedUIAuthPrefix) {
		return restrictedUISignInPage
	}

	return ""
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
