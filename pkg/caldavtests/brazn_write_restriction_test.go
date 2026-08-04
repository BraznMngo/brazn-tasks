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

package caldavtests

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1110. These tests exist because /dav had no managed-mode enforcement on
// it at all, and the restriction that read-only-except-settings depends on
// could not have answered for a CalDAV request even if it had.
//
// EVERY REQUEST HERE GOES THROUGH e.ServeHTTP WITH AN Authorization: Basic
// HEADER CARRYING AN ORDINARY PASSWORD, and both halves of that are the test.
// The existing suite in pkg/webtests/caldav_test.go calls the handlers
// directly, which skips the router and therefore every piece of middleware -
// so it would have reported success on a /dav group with nothing attached to
// it. And the password is a password rather than a CalDAV token because
// caldav.BasicAuth falls through to user.CheckUserCredentials, which is what
// makes the bypass need no attacker-specific setup: the restricted customer's
// own login is enough.
//
// The two failures this closes are independent, and each of these tests fails
// if either one is reopened:
//
//   - remove RequireManagedPolicy() from registerCalDavRoutes and the gate
//     never runs, so the write succeeds;
//   - narrow writeRestrictedSubject back to auth2.WriteRestrictedFromToken and
//     the gate runs but reads a JWT that a CalDAV request does not have, so it
//     returns its permitting answer and the write succeeds.
//
// Fixing only one produces a green build and a live bypass, which is why the
// negative control below is not decoration: without it a test asserting 403
// would pass against a CalDAV surface that was refusing for some entirely
// different reason.

// caldavRestrictionKeyID names the signing key these tests own.
const caldavRestrictionKeyID = "brazn-caldav-test-key"

// caldavRestrictionOrganization is the organization the projections below
// belong to. It is half of the contract's subject key, so it is named rather
// than defaulted.
const caldavRestrictionOrganization = "org_caldav_test"

// caldavRestrictionProject is the fixture project user15 owns, the one the rest
// of this package's CRUD tests write to.
const caldavRestrictionProject = "36"

// writeRestrictionSentence is the distinctive half of the refusal
// routes.errWritesRestricted returns.
//
// It is matched instead of the status code alone because a 403 says almost
// nothing here: managed mode has a second refusal with the same code and a
// different meaning, CalDAV handlers answer 403 for permission failures of
// their own, and a test that counted 403s would pass while something else
// entirely did the refusing. That is the failure shape this repository has
// shipped before.
const writeRestrictionSentence = "read-only because its subscription is unpaid"

// restrictedEnv is an instance in managed mode holding a projection this test
// wrote, reachable exactly the way a CalDAV client reaches the real one.
type restrictedEnv struct {
	t          *testing.T
	e          *echo.Echo
	signingKey ed25519.PrivateKey
}

// newRestrictedEnv brings up the instance and installs a signing key it owns.
//
// The ORDER is load-bearing. setupTestEnv calls config.InitDefaultConfig, which
// discards anything set before it, so managed mode has to be switched on after
// that call and not before.
func newRestrictedEnv(t *testing.T) *restrictedEnv {
	t.Helper()

	e := setupTestEnv(t)

	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	setConfigForCaldavTest(t, config.BraznManagedMode, true)
	setConfigForCaldavTest(t, config.BraznEntitlementKeys,
		caldavRestrictionKeyID+":"+base64.StdEncoding.EncodeToString(public))

	clearEntitlementProjections(t)

	return &restrictedEnv{t: t, e: e, signingKey: private}
}

// setConfigForCaldavTest sets a config key and puts it back afterwards. Viper
// overrides survive config.InitDefaultConfig, so a key left set here would
// silently put every later test in this package into managed mode.
func setConfigForCaldavTest(t *testing.T, key config.Key, value interface{}) {
	t.Helper()

	previous := key.Get()
	t.Cleanup(func() { key.Set(previous) })
	key.Set(value)
}

// clearEntitlementProjections empties the projection table before and after the
// test. It is deliberately not in the fixture set - nothing in stock Vikunja
// touches it - so without this a row written by one test would decide the
// result of the next, and the order they happened to run in would decide both.
func clearEntitlementProjections(t *testing.T) {
	t.Helper()

	truncate := func() {
		s := db.NewSession()
		defer s.Close()

		_, err := s.Exec("DELETE FROM brazn_entitlement_projections")
		require.NoError(t, err)
		require.NoError(t, s.Commit())
	}

	truncate()
	t.Cleanup(truncate)
}

// grant writes a correctly signed projection for testuser15, who owns the
// fixture project every CalDAV test in this package writes to. One subject,
// because the question here is what a surface does with a restriction and not
// who holds one - which subjects carry which entitlement is settled by
// pkg/webtests/brazn_write_access_test.go over the API.
//
// `writeAccess` is a pointer and a raw string rather than one of the contract's
// two constants, so a test can express both the ordinary case - a projection
// carrying no member at all, which is what a producer that has not started
// emitting it sends - and a value this build does not define.
func (env *restrictedEnv) grant(writeAccess *string) {
	env.t.Helper()

	signed, err := json.Marshal(entitlement.Signed{
		ContractVersion: entitlement.ContractVersion,
		Subject: entitlement.Subject{
			OrganizationID: caldavRestrictionOrganization,
			UserID:         strconv.FormatInt(testuser15.ID, 10),
		},
		Revision: 1,
		IssuedAt: time.Now().UTC(),
		State: entitlement.State{
			Edition:        entitlement.EditionPersonal,
			SeatStatus:     "active",
			EffectiveState: "active",
			WriteAccess:    writeAccess,
			// Comfortably in the past, so nothing below is accidentally about a
			// window that has not opened yet.
			ValidFrom: time.Now().UTC().Add(-24 * time.Hour),
		},
	})
	require.NoError(env.t, err)

	envelope, err := json.Marshal(map[string]interface{}{
		"signed": json.RawMessage(signed),
		"signature": map[string]string{
			"key_id":    caldavRestrictionKeyID,
			"algorithm": "ed25519",
			// entitlement.SigningInput is the one definition of the bytes the
			// contract signs over, so a signer here and the verifier the server
			// runs cannot drift apart. base64url without padding is the
			// encoding the contract fixes for signature.value.
			"value": base64.RawURLEncoding.EncodeToString(
				ed25519.Sign(env.signingKey, entitlement.SigningInput(signed))),
		},
	})
	require.NoError(env.t, err)

	s := db.NewSession()
	defer s.Close()

	_, err = s.Insert(&models.EntitlementProjection{
		UserID:           testuser15.ID,
		OrganizationID:   caldavRestrictionOrganization,
		Revision:         1,
		RevisionReceived: time.Now(),
		Envelope:         string(envelope),
	})
	require.NoError(env.t, err)
	require.NoError(env.t, s.Commit())
}

func settingsOnly() *string {
	value := entitlement.WriteAccessSettingsOnly
	return &value
}

// requireCaldavWriteRestricted asserts a response IS the write restriction's
// own refusal, and not merely some refusal.
func requireCaldavWriteRestricted(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()

	body := rec.Body.String()
	assert.Equal(t, http.StatusForbidden, rec.Code, "%s must be refused: %s", what, body)
	assert.Contains(t, body, writeRestrictionSentence,
		"%s must be refused BY THE WRITE RESTRICTION and not by another guard", what)
}

// TestCaldavRefusesWritesForARestrictedSubject is the ticket's own scenario:
// the customer whose browser has already stopped letting them edit points a
// CalDAV client at the same server with the same username and password.
func TestCaldavRefusesWritesForARestrictedSubject(t *testing.T) {
	t.Run("PUT creating a task is refused", func(t *testing.T) {
		env := newRestrictedEnv(t)
		env.grant(settingsOnly())

		vtodo := NewVTodo("bra-1110-create-uid", "Task a restricted account must not create").Build()
		rec := caldavPUT(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/bra-1110-create-uid.ics", vtodo)

		requireCaldavWriteRestricted(t, "a CalDAV PUT creating a task", rec)
	})

	t.Run("PUT creating a task really would have succeeded", func(t *testing.T) {
		// The negative control, and the reason the assertion above means
		// anything. Same instance, same credentials, same request, and the only
		// difference is the projection - so a 201 here proves the 403 above came
		// from the restriction rather than from a path that never worked, a
		// permission the fixture lacks, or a CalDAV handler refusing on its own
		// terms.
		env := newRestrictedEnv(t)
		env.grant(nil)

		vtodo := NewVTodo("bra-1110-control-uid", "Task an unrestricted account may create").Build()
		rec := caldavPUT(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/bra-1110-control-uid.ics", vtodo)

		assert.Equal(t, http.StatusCreated, rec.Code,
			"an unrestricted subject must still be able to write over CalDAV: %s", rec.Body.String())
	})

	t.Run("DELETE is refused and the task survives it", func(t *testing.T) {
		// The status code is only half of this. A refusal that answered 403
		// after deleting the row would satisfy the first assertion and be
		// worthless, so the second one asks the question the customer actually
		// has: is my work still there.
		env := newRestrictedEnv(t)
		env.grant(settingsOnly())

		rec := caldavDELETE(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/uid-caldav-test.ics")
		requireCaldavWriteRestricted(t, "a CalDAV DELETE", rec)

		rec = caldavGET(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/uid-caldav-test.ics")
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "Title Caldav Test",
			"a refused delete must leave the task readable")
	})
}

// TestCaldavStillReadsForARestrictedSubject is the other half of the ruling.
// Case 11 is read-only-except-settings, not no-access: a restricted customer
// must still see their tasks in whatever client they already connected, and
// only writing stops. A fix that refused the whole CalDAV surface would pass
// every test above and be wrong.
func TestCaldavStillReadsForARestrictedSubject(t *testing.T) {
	env := newRestrictedEnv(t)
	env.grant(settingsOnly())

	t.Run("GET returns the task", func(t *testing.T) {
		rec := caldavGET(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/uid-caldav-test.ics")

		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "Title Caldav Test")
	})

	t.Run("PROPFIND on the project collection succeeds", func(t *testing.T) {
		rec := caldavPROPFIND(t, env.e, "/dav/projects/"+caldavRestrictionProject, "1",
			PropfindCalendarCollectionProperties)

		assert.Equal(t, http.StatusMultiStatus, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), writeRestrictionSentence)
	})

	t.Run("PROPFIND discovering the principal succeeds", func(t *testing.T) {
		rec := caldavPROPFIND(t, env.e, "/dav/principals/user15/", "0",
			PropfindCalendarHomeSet)

		assert.Equal(t, http.StatusMultiStatus, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), writeRestrictionSentence)
	})

	t.Run("REPORT listing the collection succeeds", func(t *testing.T) {
		// REPORT is how a synchronising client actually reads, so a fix that
		// left it refused would break every connected calendar while every
		// PROPFIND assertion above still passed.
		rec := caldavREPORT(t, env.e, "/dav/projects/"+caldavRestrictionProject,
			ReportCalendarQuery)

		assert.Equal(t, http.StatusMultiStatus, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), writeRestrictionSentence)
	})

	t.Run("the whole surface is not simply closed", func(t *testing.T) {
		// Guards against the fix that refuses everything: a client that cannot
		// reach the entry point never gets as far as reading anything.
		rec := caldavPROPFIND(t, env.e, "/dav/", "0", PropfindCurrentUserPrincipal)

		assert.Equal(t, http.StatusMultiStatus, rec.Code, rec.Body.String())
	})
}

// TestWellKnownCaldavIsGatedToo covers the second entry point. /.well-known is
// its own echo group with its own basic auth, so attaching the gate to /dav
// alone would have left a discovery URL every mainstream client already knows
// how to follow.
func TestWellKnownCaldavIsGatedToo(t *testing.T) {
	t.Run("a write is refused", func(t *testing.T) {
		env := newRestrictedEnv(t)
		env.grant(settingsOnly())

		rec := caldavPUT(t, env.e, "/.well-known/caldav", "")

		requireCaldavWriteRestricted(t, "a write to /.well-known/caldav", rec)
	})

	t.Run("discovery still works", func(t *testing.T) {
		env := newRestrictedEnv(t)
		env.grant(settingsOnly())

		rec := caldavPROPFIND(t, env.e, "/.well-known/caldav", "0",
			PropfindCurrentUserPrincipal)

		// 301, 302 or 207, the same three RFC 6764 §5 allows and the same three
		// discovery_test.go accepts. Pinned to that set rather than to "not
		// 403", because a 404 would satisfy "not 403" and would mean a client
		// could no longer find the service at all.
		assert.True(t,
			rec.Code == http.StatusMovedPermanently ||
				rec.Code == http.StatusFound ||
				rec.Code == http.StatusMultiStatus,
			"discovery must still answer 301, 302 or 207, got %d: %s", rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), writeRestrictionSentence)
	})
}

// TestCaldavIsUnaffectedWithoutARestriction pins the two states that must look
// exactly like stock Vikunja, because a restriction that leaked into either
// would break every self-hosted instance of this fork and every paying
// customer at once.
func TestCaldavIsUnaffectedWithoutARestriction(t *testing.T) {
	t.Run("managed mode off", func(t *testing.T) {
		env := newRestrictedEnv(t)
		// The projection says settings_only and is deliberately left in place:
		// the switch alone must decide, so this fails if the gate ever reads the
		// database before checking whether it is running at all.
		env.grant(settingsOnly())
		setConfigForCaldavTest(t, config.BraznManagedMode, false)

		vtodo := NewVTodo("bra-1110-selfhosted-uid", "Self-hosted writes are untouched").Build()
		rec := caldavPUT(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/bra-1110-selfhosted-uid.ics", vtodo)

		assert.Equal(t, http.StatusCreated, rec.Code,
			"managed mode off must behave exactly like stock Vikunja: %s", rec.Body.String())
	})

	t.Run("managed mode on with no projection at all", func(t *testing.T) {
		// No entitlement is not a restriction. A subject the commercial service
		// has never described owes nothing, and write-blocking them would catch
		// every self-hosted and every not-yet-provisioned account.
		env := newRestrictedEnv(t)

		vtodo := NewVTodo("bra-1110-unprojected-uid", "No projection is not a restriction").Build()
		rec := caldavPUT(t, env.e,
			"/dav/projects/"+caldavRestrictionProject+"/bra-1110-unprojected-uid.ics", vtodo)

		assert.Equal(t, http.StatusCreated, rec.Code,
			"a subject with no projection must not be write-blocked: %s", rec.Body.String())
	})
}
