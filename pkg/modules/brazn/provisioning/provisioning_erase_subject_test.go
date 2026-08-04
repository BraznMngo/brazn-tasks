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

package provisioning

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The erase_subject payloads, written out as literals for the reason the file
// beside this one gives about the signing domains: a test that builds its input
// from the constant under test can only prove the code agrees with itself.
//
// What the commercial service emits is these exact four members
// (cloud/service/src/fork.ts, createForkTaskBackendEraser), in canonical JSON
// with members sorted by key.
const (
	eraseSubject = `{"contract_version":"1","operation":"erase_subject",` +
		`"organization_id":"org_test","user_id":"42"}`

	// The same two identifiers under the CREATING operation's name. It is a
	// valid create_personal_inbox request and must never decode as an erasure.
	personalInbox = `{"contract_version":"1","operation":"create_personal_inbox",` +
		`"organization_id":"org_test","user_id":"42"}`
)

// TestOperationEraseSubjectMatchesTheContract pins the constant against the
// literal the producer signs, so a typo here cannot pass every test in this
// package and be refused by the only party that matters.
func TestOperationEraseSubjectMatchesTheContract(t *testing.T) {
	assert.Equal(t, "erase_subject", OperationEraseSubject)
	assert.NotEqual(t, OperationCreatePersonalInbox, OperationEraseSubject,
		"the two carry identical payloads, so one shared name would route a destruction to a creation")
}

func TestDecodeEraseSubjectReadsAWellFormedRequest(t *testing.T) {
	request, err := DecodeEraseSubject([]byte(eraseSubject))
	require.NoError(t, err)

	assert.Equal(t, "org_test", request.OrganizationID)
	// A DECIMAL STRING, never a JSON number. The contract declares it as a
	// string and validates it against ^[1-9][0-9]{0,18}$; a number would pass
	// that pattern by coercion and fail the declared type.
	assert.Equal(t, "42", request.UserID)
}

// TestDecodeEraseSubjectRefusesACreationInDisguise is the one property no test
// above the decoder can reach.
//
// Routing already guarantees the operation member matches the case that
// dispatched, because Verify reads it from the same signed bytes - so through
// the channel these two can never be confused. The failure this guards is an
// editing mistake in the switch: a create_personal_inbox case pointed at this
// decoder, or the reverse. Both payloads are field for field identical, so
// without the comparison in DecodeEraseSubject that mistake would provision an
// Inbox where a deletion was asked for, or delete an account where an Inbox was.
func TestDecodeEraseSubjectRefusesACreationInDisguise(t *testing.T) {
	_, err := DecodeEraseSubject([]byte(personalInbox))
	require.ErrorIs(t, err, ErrInvalidRequest)

	// And the reverse holds by construction: the creating decoder is reached
	// only from its own case, and this asserts the payloads really are
	// interchangeable, which is what makes the comparison necessary rather than
	// decorative. If this ever fails, the two shapes have diverged and the
	// argument above needs rewriting rather than the assertion relaxing.
	asInbox, err := DecodeCreatePersonalInbox([]byte(eraseSubject))
	require.NoError(t, err,
		"the erasure payload decodes cleanly as a create_personal_inbox one, which is the whole risk")
	assert.Equal(t, "42", asInbox.UserID)
}

func TestDecodeEraseSubjectRefusesWhatItCannotAccept(t *testing.T) {
	for _, refused := range []struct {
		what    string
		payload string
	}{
		{
			// decodeExactly refuses a member this build cannot see rather than
			// dropping it silently. On an erasure that matters more than
			// anywhere else on this channel: a team_id would read as "erase
			// them from this team", and carrying it out as a whole-account
			// deletion is not a narrower reading of the request but a wider one.
			"a member this build cannot see",
			`{"contract_version":"1","operation":"erase_subject",` +
				`"organization_id":"org_test","team_id":"team_x","user_id":"42"}`,
		},
		{
			"a contract version this build does not accept",
			`{"contract_version":"2","operation":"erase_subject",` +
				`"organization_id":"org_test","user_id":"42"}`,
		},
		{
			"a subject id that is not an identifier the contract could have minted",
			`{"contract_version":"1","operation":"erase_subject",` +
				`"organization_id":"org_test","user_id":"not a number"}`,
		},
		{
			// Both bounded values reach varchar(64) columns, so an id past the
			// bound is one a store could truncate into a DIFFERENT subject -
			// which on this operation means erasing the wrong person.
			"a subject id past the bound",
			`{"contract_version":"1","operation":"erase_subject",` +
				`"organization_id":"org_test","user_id":"` +
				`11111111111111111111111111111111111111111111111111111111111111111"}`,
		},
		{
			"an organization past the bound",
			`{"contract_version":"1","operation":"erase_subject","organization_id":"` +
				`11111111111111111111111111111111111111111111111111111111111111111` +
				`","user_id":"42"}`,
		},
		{
			"an empty subject",
			`{"contract_version":"1","operation":"erase_subject",` +
				`"organization_id":"org_test","user_id":""}`,
		},
		{
			"content after the payload",
			`{"contract_version":"1","operation":"erase_subject",` +
				`"organization_id":"org_test","user_id":"42"} {}`,
		},
	} {
		t.Run(refused.what, func(t *testing.T) {
			_, err := DecodeEraseSubject([]byte(refused.payload))
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}
}
