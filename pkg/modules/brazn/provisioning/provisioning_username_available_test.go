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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The username_available payload, written out as a literal rather than built
// from the constant under test, for the reason the files beside this one give.
const usernameAvailable = `{"contract_version":"1","operation":"username_available",` +
	`"username":"grace"}`

func TestOperationUsernameAvailableMatchesTheContract(t *testing.T) {
	assert.Equal(t, "username_available", OperationUsernameAvailable)
}

func TestDecodeUsernameAvailableReadsAWellFormedRequest(t *testing.T) {
	request, err := DecodeUsernameAvailable([]byte(usernameAvailable))
	require.NoError(t, err)
	// UNTRANSFORMED: nothing here lowercases or trims, because users.username is
	// compared as stored and a normalised value would answer for a different
	// name than the one the registration would go on to refuse.
	assert.Equal(t, "grace", request.Username)
}

// TestDecodeUsernameAvailableKeepsTheNameExactlyAsTyped is that property with
// teeth: the values a normaliser would most likely mangle.
func TestDecodeUsernameAvailableKeepsTheNameExactlyAsTyped(t *testing.T) {
	for _, name := range []string{"Grace", "GRACE", "grace-hopper", "grace_1", "ünïcode"} {
		payload := `{"contract_version":"1","operation":"username_available",` +
			`"username":"` + name + `"}`
		request, err := DecodeUsernameAvailable([]byte(payload))
		require.NoError(t, err, name)
		assert.Equal(t, name, request.Username, "the name must arrive exactly as sent")
	}
}

// TestDecodeUsernameAvailableRefusesAnotherOperationInDisguise — the same
// editing-mistake guard every decoder on this channel that checks its operation
// member makes.
//
// DELETE-THE-GUARD: remove `if request.Operation != OperationUsernameAvailable`
// from DecodeUsernameAvailable. RUN: this test failed with
// `error is not ErrInvalidRequest`, while
// TestDecodeUsernameAvailableReadsAWellFormedRequest stayed green. Guard
// restored.
func TestDecodeUsernameAvailableRefusesAnotherOperationInDisguise(t *testing.T) {
	_, err := DecodeUsernameAvailable(
		[]byte(`{"contract_version":"1","operation":"create_user","username":"grace"}`))
	require.ErrorIs(t, err, ErrInvalidRequest)
}

// TestDecodeUsernameAvailableCarriesNoSubject is §5.1's shape, checked where it
// is decided. A member naming a person would make this a question ABOUT
// somebody rather than about a string, and decodeExactly refuses one rather
// than dropping it silently — which matters here because a sender that believed
// in the member would think it had scoped a question this build answered
// globally.
func TestDecodeUsernameAvailableCarriesNoSubject(t *testing.T) {
	for _, what := range []string{
		`{"contract_version":"1","operation":"username_available","username":"grace","user_id":"42"}`,
		`{"contract_version":"1","operation":"username_available","username":"grace","organization_id":"org_x"}`,
		`{"contract_version":"1","operation":"username_available","username":"grace","email":"a@b.c"}`,
	} {
		_, err := DecodeUsernameAvailable([]byte(what))
		require.ErrorIs(t, err, ErrInvalidRequest, what)
	}
}

func TestDecodeUsernameAvailableRefusesWhatItCannotAccept(t *testing.T) {
	for _, refused := range []struct {
		what    string
		payload string
	}{
		{
			"a contract version this build does not accept",
			`{"contract_version":"2","operation":"username_available","username":"grace"}`,
		},
		{
			"an empty name, which is a question about nothing",
			`{"contract_version":"1","operation":"username_available","username":""}`,
		},
		{
			// The column is varchar(250). A longer value would otherwise be
			// truncated into a question about a DIFFERENT name than the one
			// asked, and the answer would be about somebody else's.
			"a name past the column width",
			`{"contract_version":"1","operation":"username_available","username":"` +
				strings.Repeat("a", 251) + `"}`,
		},
		{
			"no name at all",
			`{"contract_version":"1","operation":"username_available"}`,
		},
		{
			"content after the payload",
			`{"contract_version":"1","operation":"username_available","username":"grace"} {}`,
		},
	} {
		t.Run(refused.what, func(t *testing.T) {
			_, err := DecodeUsernameAvailable([]byte(refused.payload))
			require.ErrorIs(t, err, ErrInvalidRequest)
		})
	}
}

// TestUsernameAvailableAtTheColumnWidthIsAccepted is the other side of the
// bound: 250 is the width, so 250 must pass. A test that only checked the
// refusal would stay green against an off-by-one that refused legitimate names.
func TestUsernameAvailableAtTheColumnWidthIsAccepted(t *testing.T) {
	name := strings.Repeat("a", 250)
	request, err := DecodeUsernameAvailable(
		[]byte(`{"contract_version":"1","operation":"username_available","username":"` + name + `"}`))
	require.NoError(t, err)
	assert.Len(t, request.Username, 250)
}
