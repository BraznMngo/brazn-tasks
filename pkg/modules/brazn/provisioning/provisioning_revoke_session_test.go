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

// The revoke_session payload, written out as a literal for the reason every
// other fixture on this channel is: a test that builds its input from the
// constant under test can only prove the code agrees with itself. What the
// commercial service would emit is these exact four members
// (cloud/service/src/fork.ts), in canonical JSON with members sorted by key.
const revokeSession = `{"contract_version":"1","operation":"revoke_session",` +
	`"session_id":"550e8400-e29b-41d4-a716-446655440000","user_id":"42"}`

func TestOperationRevokeSessionMatchesTheContract(t *testing.T) {
	assert.Equal(t, "revoke_session", OperationRevokeSession)
}

func TestDecodeRevokeSessionReadsAWellFormedRequest(t *testing.T) {
	request, err := DecodeRevokeSession([]byte(revokeSession))
	require.NoError(t, err)

	// A DECIMAL STRING, never a JSON number - the same assertion every other
	// decoder test on this channel makes about its own subject field, for the
	// same reason: the contract declares a string and a number would pass a
	// pattern check by coercion and fail the declared type.
	assert.Equal(t, "42", request.UserID)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", request.SessionID)
}

func TestDecodeRevokeSessionRefusesWhatItCannotAccept(t *testing.T) {
	for _, refused := range []struct {
		what    string
		payload string
	}{
		{
			"a member this build cannot see",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"42","extra":"x"}`,
		},
		{
			"a contract version this build does not accept",
			`{"contract_version":"2","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"42"}`,
		},
		{
			// Routing already guarantees the operation member matches by the
			// time Verify's switch reaches this decoder - this is the same
			// belt-and-suspenders check DecodeEraseSubject and DecodeResolveUser
			// make against an editing mistake in the switch itself.
			"the wrong operation name",
			`{"contract_version":"1","operation":"resolve_user","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"42"}`,
		},
		{
			"a user id that is not an identifier the contract could have minted",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"not a number"}`,
		},
		{
			"an empty user id",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":""}`,
		},
		{
			"an empty session id",
			`{"contract_version":"1","operation":"revoke_session",` +
				`"session_id":"","user_id":"42"}`,
		},
		{
			// commercialID's class is letters, digits, underscore and hyphen -
			// a session id carrying anything else, such as a shell metacharacter
			// or whitespace, is refused here rather than reaching a query with
			// it.
			"a session id outside the identifier class",
			`{"contract_version":"1","operation":"revoke_session",` +
				`"session_id":"550e8400 e29b 41d4","user_id":"42"}`,
		},
		{
			"a user id past the bound",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"` +
				`11111111111111111111111111111111111111111111111111111111111111111"}`,
		},
		{
			"content after the payload",
			`{"contract_version":"1","operation":"revoke_session","session_id":"` +
				`550e8400-e29b-41d4-a716-446655440000","user_id":"42"} {}`,
		},
	} {
		t.Run(refused.what, func(t *testing.T) {
			_, err := DecodeRevokeSession([]byte(refused.payload))
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}
}
