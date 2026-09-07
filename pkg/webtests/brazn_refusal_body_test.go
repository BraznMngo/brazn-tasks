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
	"encoding/json"
	"net/http"
	"testing"

	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BRA-1539, the task-server half: a write the managed gate refuses for
// entitlement reasons must answer in the error dialect of the surface that
// refused it, with words and a stable machine code.
//
// The gate is middleware, so its refusals never pass through any handler's
// error translation - they land in CreateHTTPErrorHandler on both API
// versions. Before this ticket that rendered v1's {"message"} shape on
// /api/v2 too, and a v2 caller reads RFC 9457's `detail` and `title` plus
// Vikunja's `code`, so it found nothing it knew. That is how a read-only
// trial account's refused write reached ONE wordless on 6 September 2026 and
// was presented to the customer as an outage.
//
// Every assertion below parses the wire bytes, because the constructor that
// produces them is the code under test.

// managedProblemBody is RFC 9457's shape as /api/v2 documents it
// (apiv2.vikunjaErrorModel), written out as an independent expectation rather
// than imported from the code under test.
type managedProblemBody struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   int    `json:"code"`
}

// The stable machine codes the two refusals carry, pinned as literals so a
// renumbering is a test failure and not a silent contract change.
const (
	pinnedCodeWritesRestricted   = 20001
	pinnedCodeManagedUnavailable = 20002
)

// managedUnavailableSentence is errManagedUnavailable's whole wording, which
// BRA-1539 deliberately did not change - it added the shape and code around it.
const managedUnavailableSentence = "This operation is managed by Brazn and is not available for this account."

// TestWriteRestrictedRefusalSpeaksV2OnV2 is the wire ONE's connector reads.
//
// DELETE THE GUARD AND THIS FAILS: revert errWritesRestricted to returning
// echo.NewHTTPError (the pre-BRA-1539 code) and the response body is
// {"message": ...} again - no `detail`, no `code` - so the require.NoError
// unmarshal still passes but every field assertion below fails on its zero
// value. Delete only the words instead and the detail assertions fail on the
// named sentence. Neither deletion can hide in the control at the end, which
// pins that the refusal observed above really was the write restriction's.
func TestWriteRestrictedRefusalSpeaksV2OnV2(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	require.True(t, env.tokenIsEntitled(&testuser1),
		"the subject must still be ENTITLED - the restriction rides a live entitlement")

	rec := env.request(http.MethodPut, "/api/v2/tasks/1",
		`{"title":"a v2 edit the restriction must refuse with words"}`, &testuser1)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	var body managedProblemBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"the refusal must be a JSON body: %s", rec.Body.String())

	assert.Equal(t, "Forbidden", body.Title)
	assert.Equal(t, http.StatusForbidden, body.Status)
	assert.Equal(t, pinnedCodeWritesRestricted, body.Code,
		"the machine code is contract; a caller telling refusals apart keys on it")
	// The three things the words owe the reader: what was refused, that the
	// account can view but not change right now, and one next step.
	assert.Contains(t, body.Detail, "This change was refused")
	assert.Contains(t, body.Detail, writeRestrictionSentence)
	assert.Contains(t, body.Detail, "The subscription page",
		"the refusal must carry a next step, not only a diagnosis")

	// The control: the identical request without the restriction reaches the
	// handler and succeeds, so the refusal above was the restriction's and not
	// some other guard's.
	env.revoke(testuser1.ID)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, fullWriteAccess())
	allowed := env.request(http.MethodPut, "/api/v2/tasks/1",
		`{"title":"the same v2 edit must succeed without the restriction"}`, &testuser1)
	assert.Equal(t, http.StatusOK, allowed.Code,
		"the control must succeed, or the refusal above proves nothing: %s", allowed.Body.String())
}

// TestWriteRestrictedRefusalKeepsV1Shape pins the other dialect: on /api/v1
// the same refusal stays Vikunja's classic {"code", "message"} error, now
// carrying the stable code, and does NOT become problem-shaped - the v1
// frontend and every v1 client read `message`.
func TestWriteRestrictedRefusalKeepsV1Shape(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	rec := env.request(http.MethodPost, "/api/v1/tasks/1",
		`{"id":1,"title":"a v1 edit the restriction must refuse with words"}`, &testuser1)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())

	code, hasCode := body["code"].(float64)
	require.True(t, hasCode, "v1 carries a numeric code: %s", rec.Body.String())
	assert.Equal(t, pinnedCodeWritesRestricted, int(code), rec.Body.String())
	message, isString := body["message"].(string)
	require.True(t, isString, "v1 carries the words under `message`: %s", rec.Body.String())
	assert.Contains(t, message, writeRestrictionSentence)
	assert.Contains(t, message, "The subscription page")
	_, hasDetail := body["detail"]
	assert.False(t, hasDetail, "v1 must keep its own dialect rather than turning problem-shaped")
}

// TestWriteRestrictedLeavesV2ReadsAlone is the reads half on the surface the
// connector uses: the restriction is read-only, not no-access, so the same
// subject whose v2 write refuses above still reads over v2.
func TestWriteRestrictedLeavesV2ReadsAlone(t *testing.T) {
	env := newManagedEnv(t)
	env.grantWriteAccess(testuser1.ID, entitlement.EditionPersonal, settingsOnly())

	rec := env.request(http.MethodGet, "/api/v2/tasks/1", "", &testuser1)
	assert.Equal(t, http.StatusOK, rec.Code,
		"/api/v2/tasks/1 must stay readable under the restriction: %s", rec.Body.String())
}

// TestEntitlementRefusalSpeaksV2OnV2 covers the gate's other refusal - the one
// an entitlement-blocked operation gets - on the same wire. The sentence is
// unchanged by BRA-1539 and is asserted whole; what is new is that a v2 caller
// receives it as `detail` with the stable code beside it, so ONE can name the
// rule that refused instead of reporting an outage.
//
// Every policy refusal shares this one body, so this test cannot tell WHICH
// rule refused - the precondition pins the state (no entitlement at all) and
// the assertion pins the shape and words every such refusal now carries.
func TestEntitlementRefusalSpeaksV2OnV2(t *testing.T) {
	env := newManagedEnv(t)

	require.False(t, env.tokenIsEntitled(&testuser1),
		"this case is about a subject with NO entitlement, so the fixture must not grant one")

	rec := env.request(http.MethodPost, "/api/v2/projects",
		`{"title":"a project creation the missing entitlement must refuse"}`, &testuser1)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	var body managedProblemBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())

	assert.Equal(t, "Forbidden", body.Title)
	assert.Equal(t, http.StatusForbidden, body.Status)
	assert.Equal(t, pinnedCodeManagedUnavailable, body.Code)
	assert.Equal(t, managedUnavailableSentence, body.Detail,
		"the flat wording is deliberate and must survive the reshaping verbatim")
}
