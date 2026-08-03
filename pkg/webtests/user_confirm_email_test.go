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

	"code.vikunja.io/api/pkg/db"
	apiv1 "code.vikunja.io/api/pkg/routes/api/v1"
	"code.vikunja.io/api/pkg/user"
	"code.vikunja.io/api/pkg/utils"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedConfirmLink writes a real confirmation link for a user, created now, and
// returns the clear text that would have gone out in the mail.
//
// The fixtures cannot supply one. Their tokens are dated 2021, and a link that
// old is now past the 24-hour lifetime the product states on screen - which is
// exactly what the expired case below uses them for.
func seedConfirmLink(t *testing.T, userID int64) string {
	clearText, err := utils.CryptoRandomString(64)
	require.NoError(t, err)

	s := db.NewSession()
	defer s.Close()

	// Created is filled in by xorm's `created` tag, so this row is genuinely
	// as old as the moment the test wrote it.
	_, err = s.Insert(&user.Token{
		UserID: userID,
		Kind:   user.TokenEmailConfirm,
		Token:  utils.Sha256Hex(clearText),
	})
	require.NoError(t, err)
	require.NoError(t, s.Commit())

	return clearText
}

func confirmRequest(e *echo.Echo, token string) (string, error) {
	c, rec := createRequest(e, http.MethodPost, `{"token": "`+token+`"}`, nil, nil)
	if err := apiv1.UserConfirmEmail(c); err != nil {
		return "", err
	}
	return rec.Body.String(), nil
}

func TestUserConfirmEmail(t *testing.T) {
	t.Run("a fresh link confirms", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		// User 4 carries status 1 in the fixtures: waiting on confirmation.
		link := seedConfirmLink(t, 4)

		body, err := confirmRequest(e, link)
		require.NoError(t, err)
		assert.Contains(t, body, `"already_confirmed":false`)
	})

	// The ruling: a second click is a success, not a failure. Both requests go
	// through the handler against the same instance, so the second one is a
	// genuinely used link and not a fixture pretending to be one.
	t.Run("a link that was already used confirms again", func(t *testing.T) {
		e, err := setupTestEnv()
		require.NoError(t, err)

		link := seedConfirmLink(t, 4)

		first, err := confirmRequest(e, link)
		require.NoError(t, err)
		require.Contains(t, first, `"already_confirmed":false`)

		second, err := confirmRequest(e, link)
		require.NoError(t, err, "the second click must not be an error")
		assert.Contains(t, second, `"already_confirmed":true`)
	})

	// The fixture token is dated 2021-07-12, which is a genuinely expired link
	// rather than one flagged as expired.
	t.Run("a link past the lifetime is expired, not invalid", func(t *testing.T) {
		_, err := newTestRequest(t, http.MethodPost, apiv1.UserConfirmEmail, `{"token": "tiepiQueed8ahc7zeeFe1eveiy4Ein8osooxegiephauph2Ael"}`, nil, nil)
		require.Error(t, err)
		assert.Equal(t, http.StatusPreconditionFailed, getHTTPErrorCode(err))
		assertHandlerErrorCode(t, err, user.ErrCodeExpiredEmailConfirmToken)
	})

	t.Run("Empty payload", func(t *testing.T) {
		_, err := newTestRequest(t, http.MethodPost, apiv1.UserConfirmEmail, `{}`, nil, nil)
		require.Error(t, err)
		assert.Equal(t, http.StatusPreconditionFailed, getHTTPErrorCode(err))
		assertHandlerErrorCode(t, err, user.ErrCodeInvalidEmailConfirmToken)
	})
	t.Run("Empty token", func(t *testing.T) {
		_, err := newTestRequest(t, http.MethodPost, apiv1.UserConfirmEmail, `{"token": ""}`, nil, nil)
		require.Error(t, err)
		assertHandlerErrorCode(t, err, user.ErrCodeInvalidEmailConfirmToken)
	})
	t.Run("Invalid token", func(t *testing.T) {
		_, err := newTestRequest(t, http.MethodPost, apiv1.UserConfirmEmail, `{"token": "invalidToken"}`, nil, nil)
		require.Error(t, err)
		assertHandlerErrorCode(t, err, user.ErrCodeInvalidEmailConfirmToken)
	})
}

func TestUserResendEmailConfirmation(t *testing.T) {
	// AC7. Three addresses that are three different facts about this instance:
	// one waiting on confirmation, one confirmed years ago, and one belonging
	// to nobody. If any of them can be told apart from the outside, this
	// endpoint is an account-existence oracle and the reset screen is one too.
	//
	// A string that is not an address is a separate case below: it is refused,
	// and that discloses nothing, because the caller could tell without asking.
	addresses := []struct {
		name    string
		address string
	}{
		{"waiting on confirmation", "user4@example.com"},
		{"long since confirmed", "user1@example.com"},
		{"belongs to nobody", "nobody@example.com"},
	}

	var status int
	var body string

	for i, a := range addresses {
		t.Run(a.name, func(t *testing.T) {
			rec, err := newTestRequest(t, http.MethodPost, apiv1.UserResendEmailConfirmation, `{"email": "`+a.address+`"}`, nil, nil)
			require.NoError(t, err)

			if i == 0 {
				status = rec.Code
				body = rec.Body.String()
				require.Equal(t, http.StatusOK, status)
				return
			}

			assert.Equal(t, status, rec.Code, "this address answers with a different status from the first one")
			assert.Equal(t, body, rec.Body.String(), "this address answers with a different body from the first one")
		})
	}

	// v1 and v2 must refuse it the same way. v2 validates from the struct tag
	// whether the handler asks for it or not, so without the matching check on
	// v1 one endpoint would answer two different ways depending on which API
	// version reached it - which is how this was found.
	t.Run("not an address at all", func(t *testing.T) {
		_, err := newTestRequest(t, http.MethodPost, apiv1.UserResendEmailConfirmation, `{"email": "not-even-an-address"}`, nil, nil)
		require.Error(t, err)
		assert.Equal(t, http.StatusBadRequest, getHTTPErrorCode(err))
	})
}
