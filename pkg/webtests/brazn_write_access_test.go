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

package webtests

import (
	"net/http"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read-only-except-settings state (BRA-1087), observed where it acts: over
// real HTTP, through the real route table, on a projection this test signed.
//
// Every refusal below is paired with the SAME request against a subject whose
// projection carries no write_access, because a refusal only means something if
// the identical request succeeds without the restriction. A refusal asserted on
// its own passes just as well against a broken fixture, a mistyped path, or a
// guard somewhere else entirely.

// writeRestrictionSentence is the distinctive half of errWritesRestricted.
//
// It is matched rather than the status code because the code alone cannot tell
// this refusal from errManagedUnavailable's, and they are different refusals for
// different reasons: one says an account may never do a thing, this one says it
// may not until an invoice is paid. A test that only counted 403s would pass
// while an unrelated guard did the refusing, which is the exact shape that let a
// defect through on this codebase before.
const writeRestrictionSentence = "read-only because its subscription is unpaid"

func settingsOnly() *string {
	value := entitlement.WriteAccessSettingsOnly
	return &value
}

func fullWriteAccess() *string {
	value := entitlement.WriteAccessFull
	return &value
}

// requireWriteRestricted asserts a response IS the restriction's own refusal.
func requireWriteRestricted(t *testing.T, name string, code int, body string) {
	t.Helper()

	assert.Equal(t, http.StatusForbidden, code, "%s must be refused: %s", name, body)
	assert.Contains(t, body, writeRestrictionSentence,
		"%s must be refused BY THE WRITE RESTRICTION and not by some other guard", name)
}

// requireNotWriteRestricted asserts a response is not the restriction's refusal
// and - just as importantly - that the request reached a handler at all.
//
// THE 404 CHECK IS WHAT STOPS THIS BEING A TEST THAT CANNOT FAIL. Without it a
// mistyped path, or a route behind a config flag this harness does not set,
// answers 404, contains no restriction sentence, and reports success for a route
// that was never exercised. Every case using this is one of the things that must
// stay writable, so a silent vacuous pass here is the most expensive kind this
// ticket could ship.
func requireNotWriteRestricted(t *testing.T, name string, code int, body string) {
	t.Helper()

	require.NotEqual(t, http.StatusNotFound, code,
		"%s did not reach a handler, so this case asserts nothing", name)
	assert.NotContains(t, body, writeRestrictionSentence,
		"%s must not be refused by the write restriction (answered %d)", name, code)
}

// ordinaryWriteCases are the writes AC1 names, chosen so that THE ONLY THING
// REFUSING THEM IS THIS TICKET'S GUARD.
//
// Every one is classified `ordinary` with no managed rule, or - for the task
// update - is access-expanding but reaches its rule's permitting branch, because
// `task-move` deliberately lets an edit naming no destination through. So each
// succeeds today for a subject with a live personal entitlement, and each must
// refuse once that entitlement says settings_only.
//
// DELETE refuseRestrictedWrite AND EVERY CASE HERE ANSWERS 2xx INSTEAD OF 403.
// That is reasoned rather than run, because nothing may execute on this host:
// with the call gone, RequireManagedPolicy looks each of these up in
// managedRouteRules, finds no rule for three of them and returns next(c); the
// fourth finds `task-move`, whose preflight returns nil for a body naming no
// destination. Both paths reach the handler, which answers success - which is
// precisely what TestOrdinaryWritesSucceedWithoutTheRestriction observes.
//
// Routes refused by an EXISTING rule are deliberately absent. A personal account
// already cannot create, rename or delete a project, so those refuse identically
// with this guard deleted and would be evidence of nothing.
var ordinaryWriteCases = []managedCase{
	{
		name:   "creating a task",
		method: http.MethodPut,
		path:   "/api/v1/projects/1/tasks",
		body:   `{"title":"a task the restriction must refuse"}`,
		want:   http.StatusCreated,
	},
	{
		name:   "editing a task",
		method: http.MethodPost,
		path:   "/api/v1/tasks/1",
		body:   `{"id":1,"title":"a retitle the restriction must refuse"}`,
		want:   http.StatusOK,
	},
	{
		name:   "creating a label",
		method: http.MethodPut,
		path:   "/api/v1/labels",
		body:   `{"title":"a label the restriction must refuse"}`,
		want:   http.StatusCreated,
	},
	// Last, because it removes the fixture the two task cases above act on.
	{
		name:   "deleting a task",
		method: http.MethodDelete,
		path:   "/api/v1/tasks/1",
		body:   "{}",
		want:   http.StatusOK,
	},
}

// TestOrdinaryWritesSucceedWithoutTheRestriction is the control, and it is not
// decoration: it establishes that every case in ordinaryWriteCases is a request
// this instance really does allow, so the refusals asserted next are caused by
// the restriction rather than by the fixture.
func TestOrdinaryWritesSucceedWithoutTheRestriction(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, nil)

	require.True(t, env.tokenIsEntitled(&testuser1),
		"the control must run against a LIVE entitlement, or it proves nothing about the restriction")

	for _, c := range ordinaryWriteCases {
		rec := env.request(c.method, c.path, c.body, &testuser1)
		assert.Equal(t, c.want, rec.Code, "%s must succeed without the restriction: %s",
			c.name, rec.Body.String())
	}
}

// TestSettingsOnlyRefusesOrdinaryWrites is AC1.
func TestSettingsOnlyRefusesOrdinaryWrites(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	require.True(t, env.tokenIsEntitled(&testuser1),
		"the subject must still be ENTITLED - this rule restricts writing, it does not withdraw access")

	for _, c := range ordinaryWriteCases {
		rec := env.request(c.method, c.path, c.body, &testuser1)
		requireWriteRestricted(t, c.name, rec.Code, rec.Body.String())
	}
}

// TestSettingsOnlyLeavesReadingAlone is the other half of AC1's meaning, and the
// one the word "read-only" would be a lie without: existing work keeps
// functioning and stays readable, and what stops is changing it.
func TestSettingsOnlyLeavesReadingAlone(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	for _, path := range []string{"/api/v1/tasks/1", "/api/v1/projects/1", "/api/v1/labels"} {
		rec := env.request(http.MethodGet, path, "", &testuser1)
		assert.Equal(t, http.StatusOK, rec.Code, "%s must stay readable: %s", path, rec.Body.String())
	}
}

// settingsWriteCases are the things that must stay writable, and this is the
// half a naive "everything refuses" implementation gets wrong. Both omissions
// are serious and neither is cosmetic: without a reachable way to pay the block
// can NEVER BE CURED, and without export a statutory deadline cannot be
// honoured.
//
// EVERY CASE HERE IS ON AN AUTHENTICATED ROUTE GROUP, which is what makes the
// permit markers load-bearing for them. Routes on the unauthenticated groups -
// /user/token/refresh, /login, and the two service-plane doors - carry markers
// too, but a test could not fail on those markers: with no user JWT in context
// there is no restriction claim to read, so they are permitted whether marked or
// not. They are marked defensively, for the day one of them moves behind auth.
// Asserting them here would be a test that cannot fail, so they are not here.
//
// The assertion is "not refused by the restriction" rather than a status code,
// because what these handlers then do is not this ticket's business: the
// password change rejects a wrong current password, the export request rejects a
// missing one. Driving six handlers to a real success would mean building
// credential fixtures to observe a decision already made before they run.
var settingsWriteCases = []managedCase{
	{
		name:   "changing the password",
		method: http.MethodPost,
		path:   "/api/v1/user/password",
		body:   `{"old_password":"12345","new_password":"1234567"}`,
	},
	{
		name:   "changing the email address",
		method: http.MethodPost,
		path:   "/api/v1/user/settings/email",
		body:   `{"new_email":"new@example.com","password":"12345"}`,
	},
	{
		name:   "enrolling two-factor",
		method: http.MethodPost,
		path:   "/api/v1/user/settings/totp/enroll",
		body:   "{}",
	},
	{
		name:   "requesting a data export",
		method: http.MethodPost,
		path:   "/api/v1/user/export/request",
		body:   `{"password":"12345"}`,
	},
	{
		name:   "cancelling a pending account deletion",
		method: http.MethodPost,
		path:   "/api/v1/user/deletion/cancel",
		body:   "{}",
	},
}

// TestSettingsOnlyPermitsTheFour is AC2.
//
// DELETE THE `write` MARKERS from route-classification.json and every case here
// fails: settingsWritableRoutes would be empty, so refuseRestrictedWrite would
// find no permit for any of these mutating paths and answer errWritesRestricted
// for all of them. That is the property being asserted, and it is why the same
// list is also guarded statically by TestWriteMarkersAreSpelledCorrectly - the
// markers are additions to a shared registry file, which is the shape a merge
// drops entries from without ever raising a conflict.
func TestSettingsOnlyPermitsTheFour(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	for _, c := range settingsWriteCases {
		rec := env.request(c.method, c.path, c.body, &testuser1)
		requireNotWriteRestricted(t, c.name, rec.Code, rec.Body.String())
	}
}

// TestSettingsOnlyDoesNotWidenWhatWasAlreadyRefused catches the opposite
// mistake from everything above: a permit marker applied too broadly, which
// would turn an existing refusal into a success and open a hole while every
// other test still passed.
//
// It is deliberately weaker evidence than the AC1 cases and says so: a personal
// account already cannot share a project, so this refuses with this ticket's
// guard deleted too.
func TestSettingsOnlyDoesNotWidenWhatWasAlreadyRefused(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	rec := env.request(http.MethodPut, "/api/v1/projects/1/users", `{"user_id":"user2"}`, &testuser1)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"sharing must stay refused: %s", rec.Body.String())
}

// TestUnrecognisedWriteAccessRefuses is AC3 in its corrected form: the question
// is no longer about an unknown `effective_state` but about an unknown value of
// `write_access` itself.
//
// THE VALUE IS GENUINELY UNKNOWN AND NOT A MOCKED BRANCH. It is signed into a
// real projection, delivered through the real verifier and read back off a real
// token - which is why the middle assertion matters more than the last one.
//
// Had Verify refused this projection for its unrecognised value, the subject
// would hold no entitlement at all, every guarded route would refuse them for
// THAT reason, and a test checking only for a 403 would report success while
// proving nothing about write_access. Asserting the token still carries an
// edition is what separates "refused because the value is unknown" from
// "refused because the whole message was thrown away". The two are
// indistinguishable from the response alone.
func TestUnrecognisedWriteAccessRefuses(t *testing.T) {
	env := newManagedEnv(t)

	// A value no version of the contract defines - not settings_only, not full,
	// and not empty. The empty string is a separate case, asserted in the
	// entitlement package's own tests.
	unknown := "quarantined"
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, &unknown)

	require.True(t, env.tokenIsEntitled(&testuser1),
		"the projection must have been ACCEPTED - a refused one would make the refusal below "+
			"prove nothing about write_access")

	rec := env.request(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"a task an unknown write_access must refuse"}`, &testuser1)
	requireWriteRestricted(t, "a write under an unrecognised write_access", rec.Code, rec.Body.String())
}

// TestExplicitFullWriteAccessPermits is the pair to the test above, and what
// stops the fail-closed reading becoming "refuse whenever the member is
// present". A producer with its feature switch on still sends `full` for every
// customer who is paying.
func TestExplicitFullWriteAccessPermits(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, fullWriteAccess())

	rec := env.request(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"a task an explicit full write_access must allow"}`, &testuser1)
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestCancelledAccountInsideItsPaidPeriodIsWritable is AC5 - the half this
// ticket gets wrong in the OTHER direction.
//
// The tempting implementation keys the block on "has no active entitlement".
// A cancelled customer eventually satisfies that too, and they owe nothing:
// they paid for a period and keep it to the end. Blocking them would stay
// invisible until someone cancelled and then could not finish their work.
//
// The commercial service agrees from its own side - writeAccessFor returns
// "full" for a cancelled account BEFORE it evaluates any money condition - so
// the block is keyed on what that service actually sends and not on a state
// the fork inferred.
func TestCancelledAccountInsideItsPaidPeriodIsWritable(t *testing.T) {
	env := newManagedEnv(t)
	setConfigForTest(t, config.BraznEntitlementGrace, "0s")

	// A cancellation recorded today with a paid period still running: the end
	// date is set, the entitlement is live until then, and no write_access is
	// carried because nothing is owed.
	endsAt := time.Now().UTC().Add(90 * 24 * time.Hour)
	env.grantUntil(testuser1.ID, entitlement.EditionPersonal, false, &endsAt)

	require.True(t, env.tokenIsEntitled(&testuser1),
		"a cancelled customer inside their paid period is still entitled")

	rec := env.request(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"a task a cancelled customer must still be able to create"}`, &testuser1)
	assert.Equal(t, http.StatusCreated, rec.Code,
		"a cancelled customer inside their paid period owes nothing and must stay writable: %s",
		rec.Body.String())
}

// TestNoEntitlementIsNotTreatedAsRestricted is the sharper half of AC5.
//
// Once a paid period ends there is no entitlement claim on the token at all,
// which is the state an implementation keyed on "no active entitlement" would
// block. It must not: ordinary task work continues when entitlement state is
// missing, which is this contract's failure policy and was the product's
// behaviour long before write_access existed.
func TestNoEntitlementIsNotTreatedAsRestricted(t *testing.T) {
	env := newManagedEnv(t)

	require.False(t, env.tokenIsEntitled(&testuser1),
		"this case is about a subject with NO entitlement, so the fixture must not grant one")

	rec := env.request(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"ordinary task work continues without any entitlement"}`, &testuser1)
	assert.Equal(t, http.StatusCreated, rec.Code,
		"a subject with no entitlement does ordinary task work; only guarded routes refuse: %s",
		rec.Body.String())
}

// TestSelfHostedIsUnaffectedByWriteAccess is AC6.
//
// It is honestly a regression guard rather than a guard-deletion test, and that
// is worth saying rather than leaving a reviewer to work out: managed mode being
// off short-circuits RequireManagedPolicy AND makes EntitlementForToken answer
// nil, so no restriction claim is minted in the first place. Deleting either
// mechanism leaves this passing. What it catches is a change that starts reading
// write_access outside managed mode, which would alter the product for every
// self-hosted operator who is not a Brazn customer at all.
func TestSelfHostedIsUnaffectedByWriteAccess(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	// The projection is in place and says settings_only; now turn managed mode
	// off underneath it, which is the self-hosted instance's configuration.
	setConfigForTest(t, config.BraznManagedMode, false)

	rec := env.request(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"a self-hosted instance must not read write_access"}`, &testuser1)
	assert.Equal(t, http.StatusCreated, rec.Code,
		"managed mode off must behave exactly like stock Vikunja: %s", rec.Body.String())
}

// TestPayingClearsTheRestrictionAtTheNextTokenIssue is AC4's mechanism, which is
// what makes "no manual step, no support ticket" true rather than aspirational.
//
// A token minted while the restriction was in force keeps it until it expires,
// and a token minted after the projection changed does not carry it. That bound
// - one token lifetime - is the same one the edition claim and the validity cap
// already accept, and it is why this needs no revocation channel either.
func TestPayingClearsTheRestrictionAtTheNextTokenIssue(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	restricted := env.tokenFor(&testuser1)

	// The customer pays and a new projection arrives saying write access is
	// full again. GetEntitlement holds one row per subject, so replacing it is
	// what a state change looks like from this side.
	env.revoke(testuser1.ID)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, fullWriteAccess())

	rec := env.request(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"paying clears the block at the next token issue"}`, &testuser1)
	assert.Equal(t, http.StatusCreated, rec.Code,
		"a token minted after payment must carry no restriction: %s", rec.Body.String())

	// The token minted before payment still carries what it was minted with,
	// which is the bound rather than a defect.
	stale := env.requestWith(http.MethodPut, "/api/v1/projects/1/tasks",
		`{"title":"the old token still carries the restriction"}`, restricted)
	requireWriteRestricted(t, "a request on the pre-payment token", stale.Code, stale.Body.String())
}

// tokenIsEntitled reports whether the token this instance would mint for a user
// right now carries an edition claim - which is to say whether the projection
// behind it was accepted and is live.
//
// Several assertions above depend on it for a reason that is easy to miss: a
// refused or absent projection produces a token with no claims, and then EVERY
// guarded route refuses for that reason instead. Without checking this, a test
// asserting a refusal cannot tell which guard answered.
func (env *managedEnv) tokenIsEntitled(as *user.User) bool {
	env.t.Helper()

	_, entitled := tokenEdition(env.t, env.tokenFor(as))
	return entitled
}
