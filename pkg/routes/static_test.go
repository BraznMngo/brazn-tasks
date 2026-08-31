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
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"code.vikunja.io/api/frontend"
	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/notifications"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What these tests can assert is constrained by the environment they run in,
// and getting that wrong would produce exactly the test CLAUDE.md §4 warns
// about, so it is stated once here.
//
// Every Go job in .github/workflows/test.yml creates the embedded frontend with
// `mkdir -p frontend/dist` and `touch frontend/dist/index.html` (lines 55, 80,
// 291, 312, 333, 374), and frontend/dist is gitignored. The index that
// frontend/embed.go embeds is therefore an EMPTY FILE, and dist/ contains
// nothing else — no dist/one/, no dist/favicon.ico.
//
// Two consequences:
//
//  1. Asserting that a served response CONTAINS the SPA's `<div id="app">`
//     marker would fail in CI even when the SPA is served correctly. The
//     `Server: Brazn Tasks` header that serveFile sets (static.go:268) is used
//     instead: it is present if and only if serveFile ran, which is precisely
//     the invariant these tests are about, and it behaves identically in CI and
//     on a real build.
//  2. Asserting that a locked-out response does NOT contain that marker cannot
//     fail here on its own, because the marker is absent from every response in
//     this environment. Those assertions are kept — they are what bites on a
//     real build — but they are never the only assertion, and the 302 plus the
//     absent Server header are what actually carry the invariant.
//
// Every HTTP case below was chosen so that it behaves the same way on the stub
// dist/ and on a real build. Where that was impossible — the redirect-loop
// guard, which by definition only fires when the restricted page is missing — the
// function is called directly instead of being reached through the router, and
// that is said where it happens rather than papered over.
const (
	restrictedUISPAMarker         = `<div id="app">`
	restrictedUITestHandlerBody   = `reached the registered handler for `
	restrictedUITestAPIPath       = `/api/v1/info`
	restrictedUITestDavPath       = `/dav`
	restrictedUITestWellKnownPath = `/.well-known/caldav`
	restrictedUITestFeedPath      = `/feeds/notifications.atom`
	restrictedUITestHealthPath    = `/health`
)

// restrictedUITestHandler answers with the path it was reached at, so a subtest
// proves that ITS OWN handler ran rather than merely that something returned
// 200.
func restrictedUITestHandler(c *echo.Context) error {
	return c.String(http.StatusOK, restrictedUITestHandlerBody+c.Request().URL.Path)
}

// newStaticTestEcho wires static() the way setupStaticFrontendFilesHandler
// does, with the non-static routes this application actually registers
// (routes.go:286-305) standing in for their real handlers.
//
// Registering them matters twice. It makes the assertions below real responses
// rather than 404s, and it means the router has routes, so an unmatched path
// exercises the same NotFoundHandler fallback static() relies on in production.
func newStaticTestEcho() *echo.Echo {
	e := echo.New()
	e.Use(static())
	for _, p := range []string{
		restrictedUITestAPIPath,
		restrictedUITestDavPath,
		restrictedUITestWellKnownPath,
		restrictedUITestFeedPath,
		restrictedUITestHealthPath,
	} {
		e.GET(p, restrictedUITestHandler)
	}

	return e
}

func doStaticRequest(t *testing.T, e *echo.Echo, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	return rec
}

// TestBraznRestrictedUIOnlyDefaultsToOff must stay the FIRST test in this file,
// and this file is the only `package routes` test file — route_classification_test.go
// is `package routes_test`, which the generated test main runs afterwards. It
// reads the shipped default, and every test below sets the key explicitly; a
// viper Set is not a default and masks one for the rest of the process, so the
// default can only be observed before any of them have run.
//
// The default is not a placeholder. Every Playwright spec in this repository
// drives the Vue SPA, and an instance that turned this on unintentionally would
// leave every one of its users with no interface at all.
//
// MUTATION, traced: changing BraznRestrictedUIOnly.setDefault(false) to true in
// pkg/config/config.go makes this fail. Traced because it is worth checking
// rather than assuming — InitDefaultConfig is the only thing that touches this
// key at this point in the run, GetBool reads viper directly, and nothing in
// .github/ sets VIKUNJA_BRAZN_RESTRICTEDUIONLY in any job, step or env block.
func TestBraznRestrictedUIOnlyDefaultsToOff(t *testing.T) {
	config.InitDefaultConfig()

	assert.False(t, config.BraznRestrictedUIOnly.GetBool(),
		"brazn.restricteduionly must ship off — on, this instance serves no Vue SPA to anyone")
}

// TestStaticServesTheSPAWhenTheLockoutIsOff is the control, and it is
// mandatory. Every other assertion in this file is about what is NOT served, so
// without this one the whole file would still pass if static() broke entirely
// and stopped serving anything at all. That is §4's "guard the guard".
//
// MUTATION, traced: deleting the `if !config.BraznRestrictedUIOnly.GetBool()`
// early return from braznServeAppShell, so that the lockout applies
// unconditionally, makes this fail. Traced: GET / with the key off opens "dist"
// in the embed FS, which succeeds because dist/ is a directory there, so
// info.IsDir() is true and static() calls braznServeAppShell, which today
// delegates to serveIndexFile, then serveFile, which sets Server: Brazn Tasks at
// 200. Without the early return the same request is answered by http.Redirect:
// 302, a Location of /one/settings.html, and no Server header, so all three
// assertions fail.
func TestStaticServesTheSPAWhenTheLockoutIsOff(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(false)

	rec := doStaticRequest(t, newStaticTestEcho(), "/")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Brazn Tasks", rec.Header().Get("Server"),
		"the index file must still be served when the lockout is off")
	assert.Empty(t, rec.Header().Get("Location"))
}

// TestStaticRestrictedUIRedirectsTheAppShell covers the lockout itself: with the
// key on, every request that would have been answered with the Vue SPA is
// answered with a redirect instead.
//
// MUTATION, traced, and the sentence the previous brief carried was wrong, so
// this one is traced case by case rather than copied. The true claim is:
//
//	Reverting static.go:170 (the not-found fallback) fails THIS test and, of the
//	tests in this file, only this one. Reverting static.go:181 (the directory
//	case) fails this test AND TestStaticRestrictedUIDoesNotServeTheIndexFile.
//
// The two call sites stopped being independent when braznBlocksAppShell joined
// the :181 branch: reverting that line strands the predicate, so /index.html
// satisfies it, takes the branch and is answered by serveIndexFile at 200 with
// Server: Brazn Tasks. An earlier draft of this comment claimed "only this one"
// for both sites; it was wrong, and it is written out per-site now because a
// mutation claim nobody can disprove is decoration (CLAUDE.md section 4).
//
// Traced against the CI filesystem described at the top of this file, with the
// key still true and the call sites reverted:
//
//	GET /                      -> Open("dist") succeeds, IsDir, serveIndexFile
//	GET /user/settings/general -> not found -> next(c) -> echo 404 -> serveIndexFile
//	GET /tasks/123             -> as above -> serveIndexFile
//	GET /one/                  -> as above -> serveIndexFile
//	GET /one/missing           -> as above -> serveIndexFile
//
// All five then answer 200 with Server: Brazn Tasks and no Location, so every
// assertion in every subtest fails. The same revert does NOT fail the control
// (key off, both shapes serve the SPA), the registered-handler test (those never
// reach either call site) or the loop-guard test (which calls
// braznServeAppShell directly). Each of those guards a different mutation, which
// is stated there rather than glossed over.
//
// The last two cases are the ones worth spelling out, because they are the pair
// that reaches the two DIFFERENT call sites depending on the build:
// on the stub dist/ there is no dist/one at all, so /one/ 404s into the
// fallback; on a real build dist/one is a directory and takes the IsDir branch.
// Both end in braznServeAppShell and both must redirect, which is why the case
// is here in the first place — path.Clean strips the trailing slash, so /one/
// arrives as /one, which is not the page and must not be mistaken for it.
func TestStaticRestrictedUIRedirectsTheAppShell(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.BraznRestrictedUIOnly.Set(false) })

	e := newStaticTestEcho()

	cases := []struct {
		name     string
		request  string
		location string
	}{
		{"the root", "/", "/one/settings.html"},
		{"a Vue SPA route", "/user/settings/general", "/one/settings.html"},
		{"a numeric task deep link", "/tasks/123", "/one/task.html?task=123"},
		{"the /one/ directory", "/one/", "/one/settings.html"},
		{"a missing asset under /one/", "/one/missing", "/one/settings.html"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doStaticRequest(t, e, tc.request)

			assert.Equal(t, http.StatusFound, rec.Code)
			assert.Equal(t, tc.location, rec.Header().Get("Location"))
			assert.Empty(t, rec.Header().Get("Server"),
				"a Server header means serveFile ran, so the SPA was delivered")
			assert.NotContains(t, rec.Body.String(), restrictedUISPAMarker)
		})
	}
}

// TestStaticRestrictedUIDoesNotServeTheIndexFile is the regression test for the
// hole that defeated the first two passes of this feature, and it is the most
// valuable test in this file.
//
// Both passes were built on the claim that the Vue SPA is delivered by exactly
// one function, serveIndexFile, so intercepting its two call sites was the whole
// job. dist/index.html is itself a real file in the embed FS and static() serves
// real files verbatim, so GET /index.html was answered with the whole SPA at 200
// without braznServeAppShell ever running — and the SPA's router is client-side,
// so from there every route in the application worked with no further round
// trip. One guessable URL defeated the entire lockout.
//
// Asserted on the Server header rather than on the body, for the reason given at
// the top of this file: CI's dist/index.html is an empty file, so a body
// assertion cannot fail here. The marker assertion is kept because it bites on a
// real build, and it is not the only assertion.
//
// MUTATION, traced: deleting `|| braznBlocksAppShell(name)` from the directory
// branch's condition in static() (static.go:180) makes this fail, and of the
// tests in this file only this one. Traced against the stub dist/ described at
// the top of this file, with the key on: /index.html does not start with /api/,
// so name = path.Join("dist/", path.Clean("//index.html")) = "dist/index.html";
// assetFs.Open succeeds, because touch frontend/dist/index.html created exactly
// that file; info.IsDir() is false, so without the predicate the branch is not
// taken, the request falls through to generateEtag and serveFile
// (static.go:184-189), and the answer is 200 with Server: Brazn Tasks and an
// ETag and no Location. Four of the five assertions below fail.
func TestStaticRestrictedUIDoesNotServeTheIndexFile(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.BraznRestrictedUIOnly.Set(false) })

	rec := doStaticRequest(t, newStaticTestEcho(), "/index.html")

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, restrictedUIPage, rec.Header().Get("Location"))
	assert.Empty(t, rec.Header().Get("Server"),
		"a Server header means serveFile ran, so /index.html delivered the SPA")
	assert.Empty(t, rec.Header().Get("Etag"))
	assert.NotContains(t, rec.Body.String(), restrictedUISPAMarker)
}

// TestStaticServesTheIndexFileUnchangedWhenTheLockoutIsOff is the control for
// the test above: with the key off, /index.html must be answered exactly as
// upstream answers it, by the real-file path.
//
// THE ETAG ASSERTION IS THE WHOLE OF THIS TEST and must not be dropped as
// incidental. Without it no single change to the lockout can make this test
// fail, which would make it decorative in the way CLAUDE.md §4 describes.
//
// MUTATION, traced, in two parts, because tracing showed the obvious sentence to
// be wrong:
//
//  1. Deleting the `if !config.BraznRestrictedUIOnly.GetBool()` guard from
//     braznBlocksAppShell — the ungated inline comparison, which is the change
//     this test exists to forbid — fails the ETag assertion ALONE. Traced: with
//     the key off the predicate then returns true for "dist/index.html", so
//     static() calls braznServeAppShell, which still sees the key off and
//     delegates to serveIndexFile. That answers 200 and sets Server: Brazn Tasks
//     just as serveFile does, so the first three assertions still pass; but
//     serveIndexFile passes an empty etag (static.go:130) and serveFile only sets
//     the header when the etag is non-empty (static.go:270-272), so the ETag
//     header is absent and that assertion fails. On a real build the same
//     mutation also injects the SPA config into a document upstream serves
//     verbatim, which is invisible here because CI's index is empty.
//  2. Removing the key guard from braznServeAppShell as well, so the lockout
//     applies unconditionally, additionally fails the status, Location and Server
//     assertions: the request is then answered by http.Redirect at 302.
//
// The ETag itself is never empty and cannot be: generateEtag delegates to
// etaggenerator.Generate, which formats the length and hex of a SHA-1 sum, and
// SHA-1 of an empty file is still twenty bytes. So this assertion behaves
// identically on the stub dist/ and on a real build.
func TestStaticServesTheIndexFileUnchangedWhenTheLockoutIsOff(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(false)

	rec := doStaticRequest(t, newStaticTestEcho(), "/index.html")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Brazn Tasks", rec.Header().Get("Server"))
	assert.Empty(t, rec.Header().Get("Location"))
	assert.NotEmpty(t, rec.Header().Get("Etag"),
		"an absent ETag means /index.html was diverted through serveIndexFile rather than served as the file it is")
}

// TestStaticRestrictedUILeavesRegisteredHandlersAlone is THE REGRESSION TEST for
// the blocker that the first implementation shipped, and its absence is what let
// that blocker through. The first implementation intercepted before the file
// lookup, so with the key on it 302'd /dav, /.well-known/*, /health and /feeds/*
// at the restricted page: CalDAV clients broken, health probes blinded, feed
// readers fed an HTML redirect. None of those is UI, and blocking the web UI
// must not take them down.
//
// The paths are the ones routes.go actually registers (lines 286-305), plus the
// API. Each handler answers with its own path, so a subtest proves that its own
// handler ran rather than that something returned 200.
//
// MUTATION, traced: restoring the C19b shape, which put a
// `if config.BraznRestrictedUIOnly.GetBool() { return braznRestrictedUI(...) }`
// block in static() above the `assetFs.Open(name)` lookup, makes FOUR of these
// five fail. Traced: with the key on, /dav, /.well-known/caldav,
// /feeds/notifications.atom and /health are none of them under /one/ and none of
// them /favicon.ico, so that shape answers each with a 302 before next(c) is
// ever called. The status is 302 rather than 200, the body is http.Redirect's
// stub HTML rather than the handler's, and Location is set — three failing
// assertions each.
//
// /api/v1/info deliberately does NOT fail that mutation, and saying so is the
// point of including it: static() returns early for /api/ (static.go:140-142)
// above where C19b put the branch. It fails only the stronger mutation of moving
// the lockout above that early return, which is the other way to break the API.
func TestStaticRestrictedUILeavesRegisteredHandlersAlone(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.BraznRestrictedUIOnly.Set(false) })

	e := newStaticTestEcho()

	cases := []struct {
		name    string
		request string
	}{
		{"CalDAV", restrictedUITestDavPath},
		{"CalDAV discovery", restrictedUITestWellKnownPath},
		{"the notifications feed", restrictedUITestFeedPath},
		{"the health probe", restrictedUITestHealthPath},
		{"the API", restrictedUITestAPIPath},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doStaticRequest(t, e, tc.request)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, restrictedUITestHandlerBody+tc.request, rec.Body.String(),
				"the lockout must not stand between a registered route and its handler")
			assert.Empty(t, rec.Header().Get("Location"))
		})
	}
}

// TestStaticRestrictedUIGuardsTheRedirectLoop covers the one hazard that
// intercepting late introduces. With the key on, a request for /one/task.html on
// a build where that file is missing reaches the fallback and would be
// redirected to /one/task.html — itself — forever.
//
// The first subtest calls braznServeAppShell directly, which is deliberate and
// is the only way to make this deterministic. Reaching the guard through the
// router requires /one/task.html to be absent from the embedded dist/; that is
// true in CI and false on a real build, so an end-to-end assertion of 404 would
// be an assertion about the environment rather than about the code. The direct
// call reproduces exactly what both static() call sites do — hand the context
// and the same assetFs to this function — with no dependency on what dist/
// contains, because the guard returns before touching the filesystem at all.
//
// MUTATION, traced: deleting the guard from braznServeAppShell, the
// `if target == requested` branch that returns echo.ErrNotFound, makes the first
// subtest fail. Traced: without it the function falls through to http.Redirect,
// which returns nil, so require.ErrorAs fails on a nil error, and it sets
// Location to /one/task.html, so the Location assertion fails too. The second
// subtest fails with it as well in CI, where the 404 becomes a 302 carrying a
// Location, though not on a real build, which is why the first one exists.
func TestStaticRestrictedUIGuardsTheRedirectLoop(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.BraznRestrictedUIOnly.Set(false) })

	t.Run("the guard answers 404 instead of redirecting the page at itself", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, restrictedUIPage, nil)
		rec := httptest.NewRecorder()

		err := braznServeAppShell(e.NewContext(req, rec), http.FS(frontend.Files))

		// Echo v5's predefined errors are an unexported type, so the status is
		// read through the interface, exactly as static.go:164-167 and
		// error_handler.go:66-71 do.
		var status echo.HTTPStatusCoder
		require.ErrorAs(t, err, &status)
		assert.Equal(t, http.StatusNotFound, status.StatusCode())
		assert.Empty(t, rec.Header().Get("Location"),
			"redirecting the restricted page at itself is an infinite loop in a browser")
	})

	t.Run("and the same holds through the middleware chain", func(t *testing.T) {
		rec := doStaticRequest(t, newStaticTestEcho(), restrictedUIPage)

		// On the stub dist/ this is the guard's 404; on a real build the page
		// is a file and static() serves it at 200 without ever reaching
		// braznServeAppShell. Only the property both share is asserted here.
		//
		// Deliberately no SPA-marker assertion: on a real build the body IS
		// frontend/public/one/task.html, whose own root element carries
		// id="app". It does not match restrictedUISPAMarker today only because
		// that element is a <main> rather than a <div>, which is far too thin a
		// thread to hang a test on.
		assert.NotEqual(t, http.StatusFound, rec.Code)
		assert.Empty(t, rec.Header().Get("Location"),
			"the restricted page must never be redirected at itself")
	})
}

// TestBraznRestrictedUITarget pins the redirect target as a pure decision,
// independent of the embedded filesystem. The HTTP tests above can only reach
// the cases the stub dist/ allows; this one reaches all of them.
//
// The /one/task.html row is not decoration: it is the precondition the
// redirect-loop guard keys on, so it is asserted here rather than left implicit
// in the guard's `target == requested` comparison.
//
// MUTATION, traced: widening the id check to anything a parser accepts, the
// strconv.ParseInt shape used elsewhere in this package for instance, makes the
// "/tasks/-1" and "/tasks/+1" cases fail, and only those two. Traced: ParseInt
// accepts a leading sign, so both would produce a target, and "+1" is the one
// that matters, because the page reads ?task= with URLSearchParams, which
// decodes "+" as a space. ParseInt still rejects "12ab" and "123/subtask", so
// those two rows survive the mutation and are not evidence for this claim.
func TestBraznRestrictedUITarget(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/", "/one/settings.html"},
		{"/user/settings/general", "/one/settings.html"},
		{"/one", "/one/settings.html"},
		{"/one/task.html", "/one/settings.html"},
		{"/tasks/123", "/one/task.html?task=123"},
		{"/tasks/0", "/one/task.html?task=0"},
		{"/tasks", "/one/settings.html"},
		{"/tasks/", "/one/settings.html"},
		{"/tasks/abc", "/one/settings.html"},
		{"/tasks/12ab", "/one/settings.html"},
		{"/tasks/-1", "/one/settings.html"},
		{"/tasks/+1", "/one/settings.html"},
		{"/tasks/123/subtask", "/one/settings.html"},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, braznRestrictedUITarget(tc.path))
		})
	}
}

// TestBraznBlocksAppShell pins the predicate that closes the /index.html hole,
// and it is where THE /one/ EXEMPTION is proved.
//
// The exemption cannot be proved over HTTP in CI, for the same reason the
// redirect-loop guard cannot: the stub dist/ has no one/ at all, so a request
// for /one/task.html there never reaches a real file and an end-to-end assertion
// would be an assertion about the environment rather than about the code. The
// values below are exactly what static.go:150 computes into `name`, so this
// table is the call site with the filesystem taken out of it.
//
// The second pass over the same table with the key off is not padding. It is the
// only place the predicate's key-gating is proved directly: braznServeAppShell
// checks the key again and delegates to serveIndexFile, so an ungated predicate
// still answers 200 over HTTP and is invisible to every status assertion in this
// file.
//
// MUTATION, MEASURED rather than reasoned — every count below was produced by
// making the change and reading which subtests turned red. The notes that stood
// here were rewritten during BRA-1475's re-review because all three had gone
// stale against the code they describe, which is a reminder that a traced
// mutation note is only true of the revision it was traced on.
//
// The function now has two clauses that can block, not three, and this is what
// each one costs:
//
//   - Deleting the `if !config.BraznRestrictedUIOnly.GetBool()` guard fails
//     FOUR rows of the second pass — every row whose want is true in the first:
//     the root index, the service worker, and the two stray .html rows. The note
//     here used to say three and omitted the service worker, which was added to
//     the table after the note was written.
//   - The last clause is a suffix test AND a lookup in the one list, and the two
//     halves fail different rows, so both are covered here. Making it block
//     nothing fails three rows of the first pass — the root index and the two
//     stray .html — because those are the documents it is the only thing
//     blocking. Reducing it to a BARE suffix test that blocks every .html fails
//     exactly one row, dist/one/task.html, because that is a page this product
//     ships. On a real build that second mutation is fatal rather than cosmetic:
//     the restricted page would be diverted into braznServeAppShell, the loop
//     guard would answer 404, and the instance would serve no interface at all.
//   - There is no longer a `name == path.Join(rootPath, indexFile)` clause. It
//     was removed in BRA-1475 as dead, and the note claiming it failed no row
//     went with it. That claim was true and it was the reason to delete it: two
//     conditions where one decides means neither can be shown to matter, and the
//     redundant one reads as the live guard to whoever comes next. Removing it
//     changed no behaviour and made the HTTP-level test at
//     TestBRA1475TheOldApplicationIsNotServedAtAnyAddress able to detect a
//     single deletion, which while both clauses stood it could not.
func TestBraznBlocksAppShell(t *testing.T) {
	config.InitDefaultConfig()

	cases := []struct {
		name string
		file string
		want bool
	}{
		{"the root index, which is the hole this closes", `dist/index.html`, true},
		{"the restricted page itself", `dist/one/task.html`, false},
		{"a module of the restricted page", `dist/one/app.js`, false},
		{"a catalogue of the restricted page", `dist/one/i18n/en.json`, false},
		{"a logo of the restricted page", `dist/one/logo-light.v1.png`, false},
		{"the favicon", `dist/favicon.ico`, false},
		{"robots.txt", `dist/robots.txt`, false},
		{"a hashed SPA chunk, inert on its own", `dist/assets/index-a1b2c3d4.js`, false},
		{"the service worker", `dist/sw.js`, true},
		{"a stray .html at the root of dist", `dist/stats.html`, true},
		{"a stray .html in a subdirectory of dist", `dist/legal/imprint.html`, true},
		{"the /one directory itself, which the IsDir branch already has", `dist/one`, false},
	}

	t.Run("with the lockout on", func(t *testing.T) {
		config.BraznRestrictedUIOnly.Set(true)
		t.Cleanup(func() { config.BraznRestrictedUIOnly.Set(false) })

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assert.Equal(t, tc.want, braznBlocksAppShell(tc.file))
			})
		}
	})

	t.Run("with the lockout off, not one of them is blocked", func(t *testing.T) {
		// Set(false) leaves a viper OVERRIDE of false, which is not the same
		// thing as the shipped default and would mask it for any test appended
		// after this one. Restoring the defaults is what clears the override -
		// TestBraznRestrictedUIOnlyDefaultsToOff is only trustworthy today
		// because it runs before anything sets one.
		t.Cleanup(func() { config.InitDefaultConfig() })
		config.BraznRestrictedUIOnly.Set(false)

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				assert.False(t, braznBlocksAppShell(tc.file),
					"with the key off this fork must be byte-identical to upstream")
			})
		}
	})
}

// TestStaticRestrictedUIKeepsAuthenticationReachable is the test that stops the
// lockout locking out everyone, and it survives BRA-1475 with one assertion
// removed and the other two kept.
//
// WHAT CHANGED, AND WHY THIS WAS NOT SIMPLY DELETED. When this was written the
// sign-in form was part of the Vue application — there was no separate login
// document — so "authentication is reachable" and "the Vue shell is served"
// were the same sentence, and the test asserted the second to prove the first.
// BRA-1475 gives every one of these addresses a document of ours, and its
// criterion 17 forbids serving the old application at any address at all. So
// the Server-header assertion is now asserting the defect and is gone.
//
// The other two assertions were never about the Vue application and still
// protect the thing this test was named for. A redirect here is the sign-in
// loop: the page finds no session, hands off to /login, /login is redirected
// back, and the browser gives up with ERR_TOO_MANY_REDIRECTS with no way into
// the instance at all. That is asserted below and it holds in both
// environments.
//
// What replaces the deleted assertion is a statement of which of OUR documents
// answers each address, which is the reachability the test was really about.
// It is asserted as a decision rather than as a 200 because CI's stub dist/
// carries no dist/one/, so the response there is an honest 404 — see
// TestBRA1475AMissingDocumentIs404AndNeverALoop, which asserts exactly that.
//
// MUTATION, traced: deleting the restrictedUIRewrites lookup from
// braznRestrictedUIDocument makes every subtest fail. With it gone /login is
// not a mailed token and not under /auth/, so the function returns "", the
// request falls through to http.Redirect and is answered 302 with a Location —
// failing the redirect assertion — and the document assertion gets "" instead
// of a page.
func TestStaticRestrictedUIKeepsAuthenticationReachable(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.InitDefaultConfig() })

	e := newStaticTestEcho()

	// Written as literals rather than as the constants the server uses, so a
	// rename cannot make this agree with itself.
	for path, wantDocument := range map[string]string{
		"/login":              `/one/signin.html`,
		"/register":           `/one/signin.html`,
		"/password-reset":     `/one/password.html`,
		"/get-password-reset": `/one/password.html`,
		"/auth/openid/google": `/one/signin.html`,
	} {
		t.Run(path, func(t *testing.T) {
			assert.Equal(t, wantDocument, braznRestrictedUIDocument(path, url.Values{}),
				"authentication must stay reachable, and by a page of ours rather than the old application")

			rec := doStaticRequest(t, e, path)

			assert.Empty(t, rec.Header().Get("Location"),
				"a redirect here is the sign-in loop this test exists to prevent")
			assert.NotContains(t, rec.Body.String(), restrictedUISPAMarker,
				"criterion 17: no address may serve the old application")
		})
	}
}

// TestStaticRestrictedUIEvictsTheServiceWorker covers the third of the three
// ways the Vue application can survive the lockout, and the one that does not
// involve this server at all.
//
// frontend/src/sw.ts precaches the built assets, answers HTML with
// StaleWhileRevalidate and calls clientsClaim(), so a browser that visited the
// application before the key was turned on serves the shell FROM ITS OWN CACHE
// on a controlled navigation. It never asks this server, so intercepting server
// reads of dist/index.html cannot reach it. A service worker is replaced by
// bytes, not by configuration — so /sw.js has to answer with a DIFFERENT script,
// one that deletes the caches and unregisters itself.
//
// Cache-Control: no-store is asserted because without it the browser's own
// update check can be satisfied from cache, and the replacement is never seen.
//
// MUTATION, traced: deleting the restrictedUIServiceWorkerFile branch from
// braznBlocksAppShell makes this fail. Traced: dist/sw.js does not exist in CI's
// stub dist/, so without the branch the request misses the embed FS, falls to
// next(c), matches no route, 404s, reaches braznServeAppShell through the
// fallback — and the /sw.js check there still catches it. So the branch that
// actually carries this on a REAL build is the one in braznBlocksAppShell, and
// on the stub it is the path check in braznServeAppShell. Both are asserted by
// this test because both must hold; the assertion cannot tell which fired, and
// that is stated rather than hidden.
func TestStaticRestrictedUIEvictsTheServiceWorker(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.InitDefaultConfig() })

	rec := doStaticRequest(t, newStaticTestEcho(), restrictedUIServiceWorkerPath)

	require.Equal(t, http.StatusOK, rec.Code, "the replacement worker must be served, not redirected")
	assert.Empty(t, rec.Header().Get("Location"),
		"a redirect leaves the installed worker in place, which is the whole defect")
	assert.Contains(t, rec.Header().Get("Content-Type"), "javascript",
		"a browser will not install a worker that is not script-typed")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"a cacheable worker lets the browser satisfy its update check from cache")

	body := rec.Body.String()
	assert.Contains(t, body, "registration.unregister()", "the worker must remove itself")
	assert.Contains(t, body, "caches.delete", "the worker must drop the cached application")
	assert.Contains(t, body, "skipWaiting", "without it the eviction waits for every tab to close")
}

// TestStaticServesTheRealServiceWorkerWhenTheLockoutIsOff is the control for the
// test above. Without it that one would still pass if the eviction script were
// served unconditionally, which would strip offline support from every instance
// that never turned the lockout on.
//
// On CI's stub dist/ there is no dist/sw.js, so the request 404s through the
// fallback rather than being served — what matters, and what this asserts, is
// that it is NOT the eviction script.
func TestStaticServesTheRealServiceWorkerWhenTheLockoutIsOff(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(false)
	t.Cleanup(func() { config.InitDefaultConfig() })

	rec := doStaticRequest(t, newStaticTestEcho(), restrictedUIServiceWorkerPath)

	assert.NotContains(t, rec.Body.String(), "registration.unregister()",
		"the eviction script must never be served while the lockout is off")
}

// TestStaticRestrictedUIDeliversConfirmationTokens is the launch blocker this
// file did not previously catch, and the reason a path allowlist is not enough.
//
// The backend mails confirmation links to the SITE ROOT with the token in a
// query — `ServicePublicURL + "?userEmailConfirm=" + token`
// (pkg/user/notifications.go:52, and :206 for account deletion). "/" is a
// directory, so it reaches braznServeAppShell and is redirected; http.Redirect
// carries only the target string, so THE TOKEN IS DISCARDED. The user can then
// never confirm, and pkg/user/user.go:413-415 refuses every later sign-in with
// ErrEmailNotConfirmed. Changing an email address from the shipped settings page
// is enough to trigger it (frontend/public/one/view-settings.js:544).
//
// THE ASSERTION IS THAT THE REQUEST IS NOT REDIRECTED, NOT THAT THE PATH IS
// ALLOWLISTED. A path-only assertion passes while the token is still destroyed,
// which is exactly the shape of test that would have shipped this bug.
//
// WHAT BRA-1475 CHANGED HERE, in two different directions.
//
// The four token and consent cases keep their point and lose one assertion. The
// original proved "not redirected" by proving the Vue shell was served, because
// the shell was the only thing there was to serve. Criterion 17 forbids serving
// it at all, so that assertion now asserts the defect and is replaced by its
// honest form: no Location, no 3xx, and the document the lockout chose is the
// one that owns the token. On CI's stub dist/ the response is a 404, which is
// correct — the page was not built — and is asserted in
// TestBRA1475AMissingDocumentIs404AndNeverALoop rather than papered over here.
//
// The /share/ case is REVERSED rather than adjusted. It asserted that a
// signed-out share recipient reaches the application; BRA-1475 rules link
// sharing shut for signed-out visitors, because that recipient authenticated at
// /share/{hash}/auth, which is a route of the Vue application, so keeping the
// prefix open kept the whole application open. Its replacement is
// TestBRA1475LinkSharingIsClosedToASignedOutVisitor, which asserts the
// redirect this test used to forbid.
//
// MUTATION, traced: deleting the restrictedUIMailedTokens loop from
// braznRestrictedUIDocument makes the three token subtests fail. Traced: "/" is
// not in restrictedUIRewrites and is not under /auth/, so without that loop the
// function returns "" and the request falls through to http.Redirect — a 302
// with a Location, failing both assertions. The /confirm and /oauth/authorize
// subtests fail instead when their restrictedUIRewrites entries are removed,
// which is a different mutation and is why they are separate cases.
func TestStaticRestrictedUIDeliversConfirmationTokens(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.InitDefaultConfig() })

	e := newStaticTestEcho()

	// The document each address must be answered by, written as a literal.
	for target, wantDocument := range map[string]string{
		"/?userEmailConfirm=sometoken":        `/one/confirmed.html`,
		"/?accountDeletionConfirm=sometoken":  `/one/confirmed.html`,
		"/confirm?userEmailConfirm=sometoken": `/one/confirmed.html`,
		"/oauth/authorize?client_id=x":        `/one/signin.html`,
	} {
		t.Run(target, func(t *testing.T) {
			requested, rawQuery, _ := strings.Cut(target, "?")
			query, err := url.ParseQuery(rawQuery)
			require.NoError(t, err)

			assert.Equal(t, wantDocument, braznRestrictedUIDocument(requested, query),
				"the document that owns this token is the one that must answer here")

			rec := doStaticRequest(t, e, target)

			assert.Empty(t, rec.Header().Get("Location"),
				"a redirect drops the query, and the query IS the token")
			assert.NotEqual(t, http.StatusFound, rec.Code,
				"a redirect drops the query, and the query IS the token")
			assert.NotContains(t, rec.Body.String(), restrictedUISPAMarker,
				"criterion 17: the old application may not be served, here or anywhere")
		})
	}
}

// TestStaticRestrictedUIStillRedirectsThePlainRoot guards the guard above. The
// root must keep redirecting: it is the main way in, and the whole point of the
// lockout. Without this, widening the confirmation exemption to every root
// request would pass every assertion in the test above while quietly serving the
// Vue application to everyone who types the hostname.
func TestStaticRestrictedUIStillRedirectsThePlainRoot(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.InitDefaultConfig() })

	rec := doStaticRequest(t, newStaticTestEcho(), "/?utm_source=newsletter")

	assert.Equal(t, http.StatusFound, rec.Code,
		"an ordinary root request, query or not, must still be redirected")
	assert.Equal(t, restrictedUIPage, rec.Header().Get("Location"))
}

// BRA-1475 acceptance tests, written by the reviewing agent from the ticket
// text before the implementation was read, and kept in their own file so that
// what the ticket asked for stays separable from what the change happened to do.
//
// EVERY EXPECTATION BELOW IS WRITTEN OUT AS A LITERAL rather than taken from a
// constant in pkg/routes. That is deliberate and it is docs/Testing-Rules.md's
// first shape of a test that passes for the wrong reason: comparing against a
// value the code under test produced makes the test agree with itself whatever
// the code does. Renaming restrictedUIPasswordPage would leave every assertion
// here still checking the address a customer's mail actually points at.
//
// The addresses and the documents come from the ticket's "The pages a
// signed-out person sees are ours" section and from the orchestrator's
// Decision 1 table, not from static_brazn.go.
const (
	bra1475SignInDocument    = `/one/signin.html`
	bra1475PasswordDocument  = `/one/password.html`
	bra1475ConfirmedDocument = `/one/confirmed.html`
	bra1475SettingsDocument  = `/one/settings.html`
	bra1475TaskDocument      = `/one/task.html`

	// The query name the server has already mailed to customers. Written from
	// the ticket's evidence block (`tasks.brazn.one/?userPasswordReset=…`),
	// which is what makes this test able to fail if the name is ever changed.
	bra1475MailedResetQuery   = `userPasswordReset`
	bra1475MailedConfirmQuery = `userEmailConfirm`
)

// bra1475AllDocuments is every page the ticket says a person can be given.
// Written here as a list of literals so that a document quietly dropped from
// the server's own const block is a failure rather than a smaller loop.
var bra1475AllDocuments = []string{
	bra1475SettingsDocument,
	bra1475TaskDocument,
	bra1475SignInDocument,
	`/one/join.html`,
	bra1475PasswordDocument,
	bra1475ConfirmedDocument,
	`/one/error.html`,
}

// bra1475LockoutOn turns the key on for one test and puts it back afterwards.
func bra1475LockoutOn(t *testing.T) {
	t.Helper()
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.InitDefaultConfig() })
}

// bra1475DocumentIsBuilt reports whether this build actually carries the
// document, because the answer differs between CI and a real image and the
// tests have to say which they are asserting.
//
// Every Go job in .github/workflows/test.yml builds the embedded frontend with
// nothing but `touch frontend/dist/index.html`, so on CI there is no dist/one/
// at all. A test that demanded 200 here would be asserting the stub rather than
// the product. The parts that hold in BOTH environments — that the request is
// never redirected, and that the token therefore survives — are asserted
// unconditionally; the 200 is asserted only where the page exists to serve.
func bra1475DocumentIsBuilt(t *testing.T, document string) bool {
	t.Helper()

	file, err := http.FS(frontend.Files).Open(path.Join(rootPath, document))
	if err != nil {
		return false
	}
	_ = file.Close()

	return true
}

// TestBRA1475MailedResetTokenIsAnsweredByThePasswordDocument is criterion 1's
// first half: a link already sitting in a customer's inbox has to keep working.
//
// That link is the site root with the token in the query. The decision is
// tested as a decision — the function is pure — because this is the exact point
// at which the live fault occurred: the root was recognised as "an ordinary
// request" and redirected, and a redirect carries only its destination, so the
// token was gone before any page could read it.
func TestBRA1475MailedResetTokenIsAnsweredByThePasswordDocument(t *testing.T) {
	query := url.Values{bra1475MailedResetQuery: []string{"a-real-looking-token"}}

	// Whatever path it arrives on. The mailed link is the root, but a customer
	// who has bookmarked anything at all must not lose the token either.
	for _, arrivedAt := range []string{"/", "/tasks/5", "/login", "/anything-at-all"} {
		t.Run(arrivedAt, func(t *testing.T) {
			assert.Equal(t, bra1475PasswordDocument, braznRestrictedUIDocument(arrivedAt, query),
				"a mailed reset token must be answered by the page that sets a password")
		})
	}

	// The control. Without this the test above would pass just as well if every
	// request in the product were answered with the password page.
	assert.Empty(t, braznRestrictedUIDocument("/tasks/5", url.Values{}),
		"an ordinary request carries no token and must not be diverted to the password page")
}

// TestBRA1475MailedResetLinkIsNeverRedirected is the same criterion at the HTTP
// layer, and it is the assertion that would have caught the live fault.
//
// A 302 is the defect, not a detail of it: http.Redirect writes only the target
// string, so the token in the query never reaches the browser's next request
// and the customer's link is spent for nothing. The assertion is therefore on
// the ABSENCE of a Location header and of a 3xx code, which is true on CI's
// stub dist/ and on a real image alike.
func TestBRA1475MailedResetLinkIsNeverRedirected(t *testing.T) {
	bra1475LockoutOn(t)

	e := newStaticTestEcho()

	for _, target := range []string{
		"/?" + bra1475MailedResetQuery + "=sometoken",
		"/?" + bra1475MailedConfirmQuery + "=sometoken",
		"/password-reset",
		"/get-password-reset",
		"/confirm?" + bra1475MailedConfirmQuery + "=sometoken",
	} {
		t.Run(target, func(t *testing.T) {
			rec := doStaticRequest(t, e, target)

			assert.Empty(t, rec.Header().Get("Location"),
				"a redirect carries only its destination, so the token would be discarded")
			assert.NotContains(t, []int{http.StatusFound, http.StatusMovedPermanently, http.StatusSeeOther,
				http.StatusTemporaryRedirect, http.StatusPermanentRedirect}, rec.Code,
				"any redirect at all loses the query, and the query is the token")
		})
	}
}

// TestBRA1475ResetLinkReachesARealPageOnABuiltImage is the other half of
// criterion 1, and it is SKIPPED ON CI ON PURPOSE rather than weakened.
//
// The criterion says the customer reaches a page that sets their password.
// Routing them there is this branch's work and is asserted above; the page
// existing is another branch's work. Where the page has been built, this
// asserts the whole outcome; where it has not, it says so rather than passing
// quietly, because "the document was never written" is precisely the state the
// fork-guards step fails the build for.
func TestBRA1475ResetLinkReachesARealPageOnABuiltImage(t *testing.T) {
	bra1475LockoutOn(t)

	if !bra1475DocumentIsBuilt(t, bra1475PasswordDocument) {
		t.Skip("this build carries no " + bra1475PasswordDocument +
			", so criterion 1 cannot be observed here; the fork-guards step is what fails on that")
	}

	rec := doStaticRequest(t, newStaticTestEcho(), "/?"+bra1475MailedResetQuery+"=sometoken")

	assert.Equal(t, http.StatusOK, rec.Code, "the customer must be given the page, not an error")
	assert.Equal(t, "Brazn Tasks", rec.Header().Get("Server"),
		"the Server header is set by serveFile and is what proves a document was actually delivered")
}

// TestBRA1475EveryFormerlyExemptAddressIsAnsweredByOurOwnDocument is criterion
// 17 stated positively, and it is the half that stops the lockout locking
// everybody out.
//
// The six addresses are the exemption list the ticket says must end up empty,
// read from its own evidence block. Each is now a page of ours. Testing the
// decision rather than the response is what makes this hold on CI's stub dist/,
// where none of the documents exist.
func TestBRA1475EveryFormerlyExemptAddressIsAnsweredByOurOwnDocument(t *testing.T) {
	// Written from the ticket and from Decision 1, not from restrictedUIRewrites.
	for address, want := range map[string]string{
		"/login":              bra1475SignInDocument,
		"/register":           bra1475SignInDocument,
		"/oauth/authorize":    bra1475SignInDocument,
		"/password-reset":     bra1475PasswordDocument,
		"/get-password-reset": bra1475PasswordDocument,
		"/confirm":            bra1475ConfirmedDocument,
		"/auth/openid/google": bra1475SignInDocument,
	} {
		t.Run(address, func(t *testing.T) {
			assert.Equal(t, want, braznRestrictedUIDocument(address, url.Values{}),
				"this address used to reach the old application and must now be a page of ours")
		})
	}
}

// TestBRA1475TheOldApplicationIsNotServedAtAnyAddress is criterion 17's real
// assertion, and the one that bites in this environment.
//
// dist/index.html EXISTS on CI's stub, and it is the document that loads the
// SPA's entry module. Before this work, asking for it by name defeated the
// whole lockout: static() serves real files verbatim, so /index.html answered
// 200 with the application. The Server header is what proves a file was
// actually delivered, so its absence is what proves it was not.
//
// The escaped and dot-segment forms are here because path.Clean runs after
// url.PathUnescape in static(), so each of these resolves to dist/index.html
// and each is a way somebody would try.
func TestBRA1475TheOldApplicationIsNotServedAtAnyAddress(t *testing.T) {
	bra1475LockoutOn(t)

	e := newStaticTestEcho()

	for _, target := range []string{
		"/index.html",
		"/./index.html",
		"/one/../index.html",
		"//index.html",
		"/%69ndex.html",
	} {
		t.Run(target, func(t *testing.T) {
			rec := doStaticRequest(t, e, target)

			assert.Empty(t, rec.Header().Get("Server"),
				"the Server header means serveFile ran, which means the Vue application was handed out")
			assert.NotEqual(t, http.StatusOK, rec.Code,
				"one guessable address must not defeat the lockout")
			assert.NotContains(t, rec.Body.String(), restrictedUISPAMarker,
				"this cannot fail on CI's empty stub index, and it is what bites on a real image")
		})
	}
}

// TestBRA1475LinkSharingIsClosedToASignedOutVisitor is the part of criterion 17
// that REVERSES a previously asserted behaviour, so it is written as its own
// test rather than folded into another.
//
// A share recipient used to authenticate at /share/{hash}/auth, which is a
// route of the Vue application, so keeping the prefix open kept the whole
// application open. The ticket rules it shut like any other address.
func TestBRA1475LinkSharingIsClosedToASignedOutVisitor(t *testing.T) {
	bra1475LockoutOn(t)

	for _, target := range []string{"/share/abc123/auth", "/share/abc123"} {
		t.Run(target, func(t *testing.T) {
			rec := doStaticRequest(t, newStaticTestEcho(), target)

			assert.Equal(t, http.StatusFound, rec.Code,
				"a signed-out visitor gets no share page, they get sent to the product")
			assert.Equal(t, bra1475SettingsDocument, rec.Header().Get("Location"))
			assert.Empty(t, rec.Header().Get("Server"),
				"nothing of the old application may be served here")
		})
	}
}

// TestBRA1475NothingOutsideOurOwnPagesCanBeReached is the exemption list being
// empty, expressed as something a test can actually observe.
//
// "The list is empty" is a statement about a variable, and a variable can be
// added to tomorrow. What the criterion is really about is that no answer the
// lockout gives is ever the old application — so every document it chooses and
// every target it redirects to is asserted to be one of ours, over a corpus of
// addresses drawn from the ticket rather than from the code.
func TestBRA1475NothingOutsideOurOwnPagesCanBeReached(t *testing.T) {
	corpus := []string{
		"/", "/login", "/register", "/password-reset", "/get-password-reset",
		"/confirm", "/oauth/authorize", "/auth/openid/google", "/share/abc/auth",
		"/tasks/5", "/tasks/notanumber", "/projects/1", "/user/settings/general",
		"/index.html", "/anything", "/one", "/one/",
	}

	known := map[string]bool{}
	for _, document := range bra1475AllDocuments {
		known[document] = true
	}

	for _, address := range corpus {
		t.Run(address, func(t *testing.T) {
			if document := braznRestrictedUIDocument(address, url.Values{}); document != "" {
				assert.True(t, known[document],
					"the lockout chose %q, which is not one of the pages this product ships", document)
				return
			}

			target := braznRestrictedUITarget(address)
			base := target
			if i := strings.IndexByte(base, '?'); i >= 0 {
				base = base[:i]
			}
			assert.True(t, known[base],
				"a locked-out request was sent to %q, which is not one of the pages this product ships", target)
		})
	}
}

// TestBRA1475OnlyPagesOnTheOneListAreServed is criterion 18's server-side half:
// the list decides what is reachable, so it is load-bearing rather than a
// description of something decided elsewhere.
//
// The build-failing half is the "Every ONE document is on the one list" step in
// .github/workflows/fork-guards.yml, which a Go test cannot run.
func TestBRA1475OnlyPagesOnTheOneListAreServed(t *testing.T) {
	bra1475LockoutOn(t)

	t.Run("a page nobody decided to serve is blocked", func(t *testing.T) {
		for _, name := range []string{
			"dist/one/rogue.html",
			"dist/one/admin.html",
			"dist/one/nested/whatever.html",
		} {
			assert.True(t, braznBlocksAppShell(name),
				"%s would be embedded in the binary and served to anyone who asked for it by name", name)
		}
	})

	t.Run("every page the product ships is served", func(t *testing.T) {
		for _, document := range bra1475AllDocuments {
			assert.False(t, braznBlocksAppShell("dist"+document),
				"%s is a page this product ships and must not be blocked", document)
		}
	})

	t.Run("the old application's own index is blocked", func(t *testing.T) {
		assert.True(t, braznBlocksAppShell("dist/index.html"))
	})

	// The control that keeps this fork byte-identical to upstream with the key
	// off. Without it, blocking would be a deviation every self-hosted instance
	// paid for.
	t.Run("nothing is blocked while the lockout is off", func(t *testing.T) {
		config.BraznRestrictedUIOnly.Set(false)
		assert.False(t, braznBlocksAppShell("dist/index.html"))
		assert.False(t, braznBlocksAppShell("dist/one/rogue.html"))
		assert.False(t, braznBlocksAppShell("dist/sw.js"))
	})
}

// TestBRA1475AMissingDocumentIs404AndNeverALoop is the failure mode the ticket
// and this file's own comments both single out, and it is the one worth proving
// rather than reasoning about.
//
// If a document the lockout answers with was not copied into dist/, every way
// of handling that except a 404 ends in a redirect — and a redirect to a
// document that is also missing is an infinite bounce. A customer sees the
// browser give up with ERR_TOO_MANY_REDIRECTS and no page at all, which is
// worse than the fault this ticket exists to fix.
//
// CI's stub dist/ has no dist/one/ whatsoever, so this environment IS the
// broken build, and the assertion is exact rather than conditional.
func TestBRA1475AMissingDocumentIs404AndNeverALoop(t *testing.T) {
	bra1475LockoutOn(t)

	if bra1475DocumentIsBuilt(t, bra1475SettingsDocument) {
		t.Skip("this build carries the documents, so the missing-document case cannot be exercised here")
	}

	e := newStaticTestEcho()

	t.Run("asking for a document that was never built", func(t *testing.T) {
		for _, document := range bra1475AllDocuments {
			rec := doStaticRequest(t, e, document)

			assert.Equal(t, http.StatusNotFound, rec.Code,
				"%s was not built, and a missing page has to read as missing", document)
			assert.Empty(t, rec.Header().Get("Location"),
				"%s redirected instead of 404ing, which is the infinite bounce", document)
		}
	})

	t.Run("an address the lockout answers with a document that was never built", func(t *testing.T) {
		for _, address := range []string{"/login", "/password-reset", "/confirm", "/oauth/authorize", "/auth/openid/google"} {
			rec := doStaticRequest(t, e, address)

			assert.Equal(t, http.StatusNotFound, rec.Code,
				"%s must report the build failure rather than bouncing", address)
			assert.Empty(t, rec.Header().Get("Location"))
		}
	})

	// Following the chain rather than asserting one hop, because a loop of two
	// is still a loop and a single-hop assertion cannot see it.
	t.Run("every redirect chain terminates", func(t *testing.T) {
		for _, start := range []string{"/", "/tasks/5", "/projects/1", "/share/abc/auth", "/index.html"} {
			target := start
			hops := 0
			for {
				rec := doStaticRequest(t, e, target)
				location := rec.Header().Get("Location")
				if location == "" {
					break
				}
				hops++
				require.LessOrEqual(t, hops, 3,
					"starting at %s the browser is still being redirected after %d hops, which is the bounce", start, hops)
				target = location
			}
		}
	})
}

// TestBRA1475TheServiceWorkerIsStillEvicted guards a claim criterion 17 depends
// on and that nothing else in this file covers: a browser that visited the old
// application before the key was turned on serves it FROM ITS OWN CACHE and
// never asks this server at all, so blocking dist/index.html is not by itself
// "the old application is unreachable".
func TestBRA1475TheServiceWorkerIsStillEvicted(t *testing.T) {
	bra1475LockoutOn(t)

	rec := doStaticRequest(t, newStaticTestEcho(), "/sw.js")

	require.Equal(t, http.StatusOK, rec.Code, "a redirect leaves the installed worker in place")
	assert.Contains(t, rec.Body.String(), "caches.delete",
		"without this the cached copy of the old application survives the lockout")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"a cacheable worker lets the browser satisfy its update check from its own cache")
}

// TestBRA1475TheMailedResetLinkLandsOnAPageThisServerServes is the join between
// criterion 2 and criterion 1, and it exists because nothing else in either
// repository makes it.
//
// pkg/user/notifications.go writes the address of the password page out as its
// own string literal — it cannot import the constant, because pkg/routes
// imports pkg/user and the cycle would be the other way round — so there are
// now two independent spellings of one address. The fork-guards step checks the
// const block against the documents that exist; it never reads
// notifications.go. Rename the page in both of those and the build stays green
// while every password-reset mail this product sends lands on a 404.
//
// This test is in pkg/routes because it is the only package that can see both
// halves. It takes the address a customer will actually click, and asks the
// lockout what it would do with it.
func TestBRA1475TheMailedResetLinkLandsOnAPageThisServerServes(t *testing.T) {
	bra1475LockoutOn(t)

	const publicURL = "https://tasks.example.test/"
	config.ServicePublicURL.Set(publicURL)
	t.Cleanup(func() { config.ServicePublicURL.Set("") })

	n := &user.ResetPasswordNotification{
		User:  &user.User{Username: "somebody"},
		Token: &user.Token{ClearTextToken: "a-real-looking-token"},
	}
	opts, err := notifications.RenderMail(n.ToMail("en"), "en")
	require.NoError(t, err)

	// Pull the customer's actual link out of the rendered mail rather than
	// rebuilding it, so this asserts what was sent and not what we think was.
	start := strings.Index(opts.HTMLMessage, publicURL)
	require.GreaterOrEqual(t, start, 0, "no link to this instance appears in the reset mail at all")
	rest := opts.HTMLMessage[start:]
	end := strings.IndexAny(rest, `"'<> `)
	// Positive rather than Greater(end, 0): identical meaning, and it is what
	// this repository's linter requires. Both reject the two ways this can go
	// wrong — IndexAny answers -1 when the link is never terminated, and 0
	// would mean the link is empty.
	require.Positive(t, end)
	link := rest[:end]

	parsed, err := url.Parse(strings.ReplaceAll(link, "&amp;", "&"))
	require.NoError(t, err)

	document := braznRestrictedUIDocument(parsed.Path, parsed.Query())
	if document == "" {
		// The link points at a real file rather than at an address the lockout
		// rewrites, which is equally fine — so long as that file is a page this
		// product has decided to serve.
		document = parsed.Path
	}

	known := map[string]bool{}
	for _, d := range bra1475AllDocuments {
		known[d] = true
	}
	assert.True(t, known[document],
		"the mail sends customers to %q, which this server does not serve; every reset link would 404", document)

	assert.False(t, braznBlocksAppShell("dist"+document),
		"the lockout blocks %q, so the customer's link is refused by the server that sent it", document)
}
