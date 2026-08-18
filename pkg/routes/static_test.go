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
	"testing"

	"code.vikunja.io/api/frontend"
	"code.vikunja.io/api/pkg/config"

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
// MUTATION, traced, one per clause:
//
//   - Deleting the `if !config.BraznRestrictedUIOnly.GetBool()` guard fails
//     every row of the second pass whose want is true in the first — that is
//     the root index and the two stray .html rows, three subtests.
//   - Deleting the `name == path.Join(rootPath, indexFile)` clause fails NO row
//     here, and saying so is the point of tracing it rather than asserting it:
//     "dist/index.html" also ends in .html and is not under dist/one/, so the
//     suffix clause catches it anyway. The clause is kept regardless, because it
//     is the one the hole is actually about, and leaning on a suffix test for it
//     would make a later narrowing of that test reopen the hole silently. This
//     test therefore does not guard it; TestStaticRestrictedUIDoesNotServeThe-
//     IndexFile guards the behaviour, which is what matters.
//   - Deleting the `strings.HasPrefix(name, exemptDir)` exemption fails the
//     dist/one/task.html row, and only it. On a real build that mutation is
//     fatal rather than cosmetic: the restricted page would be diverted into
//     braznServeAppShell, braznRestrictedUITarget would compute /one/task.html
//     as its own target, and the loop guard would answer 404 — a locked-down
//     instance serving no interface at all.
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
		{"the service worker", `dist/sw.js`, false},
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
// lockout locking out everyone, and it is the reason the exemption exists.
//
// The sign-in form is part of the Vue application — there is no separate login
// document. Without the exemption, a signed-out visitor is redirected to the
// restricted page, the page finds no session and hands off to /login exactly as
// the SPA does, /login matches no file and no route, and the fallback redirects
// it straight back. The browser gives up with ERR_TOO_MANY_REDIRECTS and the
// instance has no way in at all.
//
// The assertion is on the Server header rather than the body for the reason
// stated at the top of this file: CI's dist/index.html is an empty file, so no
// body assertion can distinguish the shell from anything else here. serveFile
// sets Server: Brazn Tasks (static.go:268) and is reached only when a document
// is actually served, which is exactly the distinction under test.
//
// MUTATION, traced: deleting the restrictedUIAuthPaths branch from
// braznServeAppShell makes every subtest fail. Traced rather than assumed — with
// the branch gone, /login reaches braznRestrictedUITarget, which returns the
// general page because /login carries no /tasks/ prefix; that differs from the
// request path so the loop guard does not fire; so the request is answered 302
// with a Location and no Server header, failing all three assertions.
func TestStaticRestrictedUIKeepsAuthenticationReachable(t *testing.T) {
	config.InitDefaultConfig()
	config.BraznRestrictedUIOnly.Set(true)
	t.Cleanup(func() { config.InitDefaultConfig() })

	e := newStaticTestEcho()

	for _, path := range []string{
		"/login",
		"/register",
		"/password-reset",
		"/get-password-reset",
		"/auth/openid/google",
	} {
		t.Run(path, func(t *testing.T) {
			rec := doStaticRequest(t, e, path)

			assert.Equal(t, http.StatusOK, rec.Code,
				"authentication must stay reachable or nobody can sign in")
			assert.Equal(t, "Brazn Tasks", rec.Header().Get("Server"),
				"the app shell must actually be served, not merely not-redirected")
			assert.Empty(t, rec.Header().Get("Location"),
				"a redirect here is the sign-in loop this test exists to prevent")
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
