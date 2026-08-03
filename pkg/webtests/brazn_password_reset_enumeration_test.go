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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPasswordResetRequestDoesNotEnumerateAddresses is BRA-1072 AC3/AC7 and
// Percy-Account-Path.md §6 open item D: the reply to a password-reset request
// is the same whether or not the address has an account.
//
// IT ASSERTS THE BODY AND NOT ONLY THE STATUS. Four addresses all answering 200
// with four different sentences would pass a status-only check and still be an
// oracle, which is the whole failure this exists to prevent.
//
// BOTH ADDRESSES ARE CONSTRUCTED RATHER THAN ASSUMED. The registered one is
// registered by this test through the public endpoint, so "it has an account"
// is something the test made true; the others are addresses nothing in the
// fixtures or in this test ever creates.
//
// WHAT BREAKS IF THE GUARD IS DELETED, reasoned through because it cannot be
// run on this host. Removing the `if user.IsErrUserDoesNotExist(err)` branch
// from shared.RequestPasswordResetToken makes the unregistered addresses answer
// 404 with an error body while the registered one still answers 200 with
// {"message":"Token was sent."}. Every equality assertion below then fails, on
// both API versions. The final subtest is the other half of the check: it fails
// if the branch is widened into "swallow everything", because an address that
// is not an address must still be refused.
func TestPasswordResetRequestDoesNotEnumerateAddresses(t *testing.T) {
	e, err := setupTestEnv()
	require.NoError(t, err)

	const registered = `resetenum-registered@example.com`
	unregistered := []string{
		`resetenum-nobody-one@example.com`,
		`resetenum-nobody-two@example.com`,
		`resetenum-nobody-three@example.com`,
	}

	// Make the registered address genuinely registered.
	created := humaRequest(t, e, http.MethodPost, "/api/v2/register",
		`{"username":"resetenumuser","password":"12345678","email":"`+registered+`"}`, "", "")
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	body := func(email string) string {
		return `{"email":"` + email + `"}`
	}

	t.Run("v1 answers every address identically", func(t *testing.T) {
		known := publicRequest(e, http.MethodPost, "/api/v1/user/password/token", body(registered))

		// Pinned against the status this endpoint is documented to answer, not
		// against whatever it happened to return: without this the whole set
		// could be uniformly 404 and every equality below would still hold.
		require.Equal(t, http.StatusOK, known.Code, known.Body.String())

		for _, email := range unregistered {
			stranger := publicRequest(e, http.MethodPost, "/api/v1/user/password/token", body(email))
			assert.Equal(t, known.Code, stranger.Code, "status differs for "+email)
			assert.Equal(t, known.Body.String(), stranger.Body.String(), "body differs for "+email)
		}
	})

	t.Run("v2 answers every address identically", func(t *testing.T) {
		known := humaRequest(t, e, http.MethodPost, "/api/v2/user/password/token", body(registered), "", "")

		require.Equal(t, http.StatusOK, known.Code, known.Body.String())

		for _, email := range unregistered {
			stranger := humaRequest(t, e, http.MethodPost, "/api/v2/user/password/token", body(email), "", "")
			assert.Equal(t, known.Code, stranger.Code, "status differs for "+email)
			assert.Equal(t, known.Body.String(), stranger.Body.String(), "body differs for "+email)
		}
	})

	t.Run("only the does-not-exist refusal is swallowed", func(t *testing.T) {
		// A malformed address and a missing one are not statements about
		// whether an account exists, so they must still be refused. If the
		// guard is ever widened to "return nil on any error", these fail.
		malformed := publicRequest(e, http.MethodPost, "/api/v1/user/password/token", body("not-an-address"))
		assert.Equal(t, http.StatusBadRequest, malformed.Code, malformed.Body.String())

		missing := publicRequest(e, http.MethodPost, "/api/v1/user/password/token", `{}`)
		assert.NotEqual(t, http.StatusOK, missing.Code, missing.Body.String())
	})
}
