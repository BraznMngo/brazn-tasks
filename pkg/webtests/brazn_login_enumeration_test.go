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
	"net/http/httptest"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoginDoesNotEnumerateAccounts is BRA-1101 and Percy-Account-Path.md §4:
// somebody who has not proved the password learns nothing about the address they
// typed. Four accounts in four different states must be refused with the same
// status and the same bytes; only after the password verifies may the answer
// name the reason.
//
// IT ASSERTS THE BODY AND NOT ONLY THE STATUS. Before BRA-1101 an unconfirmed
// account answered 412/1012 and a Google account 412/1021, both without any
// password being checked, so a stranger with a list of addresses could sort it
// into customers and non-customers. Four different sentences behind one status
// would be the same oracle, so equality of the rendered body is the assertion.
//
// EVERY ACCOUNT IS IN A STATE THE TEST PUT IT IN OR THE FIXTURES STATE PLAINLY:
// user1 is confirmed and local, user5 carries status 1 with a real bcrypt hash
// (which is exactly what an unconfirmed account looks like in production), the
// Google account is built below by production's own constructor, and the fourth
// address is one nothing ever creates.
//
// WHAT BREAKS IF EACH GUARD IS REMOVED, reasoned through rather than run,
// because this host is not an execution environment for this repository:
//
//   - Move `if user.Status == StatusEmailConfirmationRequired` back above
//     CheckUserPassword — i.e. undo the fix — and the unconfirmed row of the
//     first subtest answers 412 with code 1012 against a baseline of 403 with
//     code 1011. Both equality assertions fail, on both API versions.
//   - Move `if user.Issuer != IssuerLocal` back above it and the Google row
//     answers 412 with code 1021. Same two failures.
//   - Delete either guard outright instead of moving it and the second subtest
//     fails, because the right password then signs the unconfirmed account in
//     (200) instead of answering 1012. That subtest is the reason the first one
//     cannot be satisfied by a server that has simply lost the ability to say
//     anything but 1011.
//   - Drop bcrypt.ErrHashTooShort from CheckUserPassword's mismatch branch,
//     keeping the reorder, and the Google row answers 500: an account with no
//     stored hash makes bcrypt answer ErrHashTooShort, which is not
//     ErrMismatchedHashAndPassword, and the v1 error handler renders an
//     unrecognised error as Internal Server Error. That is a louder oracle than
//     the one the reorder closes, which is why the account below is built with
//     no password rather than taken from users.yml.
func TestLoginDoesNotEnumerateAccounts(t *testing.T) {
	e, err := setupTestEnv()
	require.NoError(t, err)

	const (
		confirmedAddress   = `user1@example.com`
		unconfirmedAddress = `user5@example.com`
		googleAddress      = `loginenum-google@example.com`
		strangerAddress    = `loginenum-nobody@example.com`

		correctPassword = `12345678`
		wrongPassword   = `wrong`
	)

	// The Google account is built by the same constructor the OIDC sign-up path
	// uses, not written into users.yml, and the difference is the whole point:
	// CreateUser hashes a password only when the issuer is local, and the OIDC
	// path supplies none, so this row lands with an empty password column just
	// as a real Google account does. The two non-local rows already in the
	// fixtures (user14, user19) carry a real bcrypt hash for "12345678", which
	// no OIDC account ever has — a fixture that gentle would hide the 500
	// described above and report this test as passing.
	func() {
		s := db.NewSession()
		defer s.Close()

		created, err := user.CreateUser(s, &user.User{
			Username: "loginenum-google",
			Email:    googleAddress,
			Issuer:   "https://accounts.google.com",
			Subject:  "loginenum-google-subject",
		})
		require.NoError(t, err)

		// Read the row back the same way the login path does — a bare xorm Get,
		// not one of pkg/user's helpers. Those blank fields on the way out
		// (getUserByUsernameOrEmail blanks Email), so a helper is the wrong
		// witness for "this account has no password": it could answer empty for
		// a row that is not. This assertion is load-bearing rather than
		// decorative. If this account had a usable hash, a wrong password would
		// answer 1011 through the ordinary mismatch branch and the row below
		// would pass whether or not CheckUserPassword handles a missing hash.
		stored := &user.User{}
		found, err := s.Where("id = ?", created.ID).Get(stored)
		require.NoError(t, err)
		require.True(t, found, "the account this test just created must be readable")
		require.Empty(t, stored.Password,
			"a non-local account must reach the login path with no password hash, or this test is gentler than production")

		require.NoError(t, s.Commit())
	}()

	login := func(path, usernameOrEmail, password string) *httptest.ResponseRecorder {
		return humaRequest(t, e, http.MethodPost, path,
			`{"username":"`+usernameOrEmail+`","password":"`+password+`"}`, "", "")
	}

	for _, api := range []struct {
		version string
		path    string
	}{
		{"v1", "/api/v1/login"},
		{"v2", "/api/v2/login"},
	} {
		t.Run(api.version+" refuses every account with the same answer", func(t *testing.T) {
			// Pinned against the contract rather than against itself: a wrong
			// password on an ordinary account is documented to answer 403 with
			// code 1011. Without this the whole set could be uniformly 500 and
			// every equality below would still hold.
			baseline := login(api.path, confirmedAddress, wrongPassword)
			require.Equal(t, http.StatusForbidden, baseline.Code, baseline.Body.String())
			require.Equal(t, user.ErrCodeWrongUsernameOrPassword, problemCode(t, baseline), baseline.Body.String())

			for _, refused := range []struct {
				what            string
				usernameOrEmail string
			}{
				{"an unconfirmed account", unconfirmedAddress},
				{"an account that signs in with Google", googleAddress},
				{"an address with no account at all", strangerAddress},
			} {
				rec := login(api.path, refused.usernameOrEmail, wrongPassword)
				assert.Equal(t, baseline.Code, rec.Code,
					"status differs for "+refused.what+": "+rec.Body.String())
				assert.Equal(t, baseline.Body.String(), rec.Body.String(),
					"body differs for "+refused.what)
			}
		})

		t.Run(api.version+" names the reason once the password verified", func(t *testing.T) {
			// The other half, and the one that stops the first from being
			// satisfiable by refusing everything the same way. This is the one
			// place the product may admit an account exists, because by now the
			// caller has proved they already knew the password.
			rec := login(api.path, unconfirmedAddress, correctPassword)
			assert.Equal(t, http.StatusPreconditionFailed, rec.Code, rec.Body.String())
			assert.Equal(t, user.ErrCodeEmailNotConfirmed, problemCode(t, rec), rec.Body.String())
		})
	}
}
