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

package signup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"code.vikunja.io/api/pkg/config"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The vendored conformance set. See testdata/contract/README.md for what it is
// and why a copy of it lives here. Constants rather than paths built at call
// time, which is the form gosec resolves and the form already used for the
// golden entitlement set.
const (
	contractRequestPath  = "testdata/contract/signup-token-redemption-request.valid.conformance.json"
	contractResponsePath = "testdata/contract/signup-token-redemption-response.valid.conformance.json"
	contractErrorPath    = "testdata/contract/signup-token-redemption-error.valid.conformance.json"
)

// readContract reads one vendored fixture as raw bytes.
func readContract(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

// jsonKind names the JSON type of a value, which is what the request fixture is
// really pinning. `42` and `"42"` are the same value to a careless reader and
// different messages to the receiver.
func jsonKind(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	var decoded interface{}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	switch decoded.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case []interface{}:
		return "array"
	default:
		return "object"
	}
}

// capturedRedemption is what a stub server saw.
type capturedRedemption struct {
	calls         int
	body          []byte
	authorization string
	contentType   string
	path          string
	query         string
}

// stub points this package's configuration at a server that answers a fixed
// status and body, and records what it was sent.
func stub(t *testing.T, status int, answer []byte) *capturedRedemption {
	t.Helper()

	seen := &capturedRedemption{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		seen.calls++
		seen.body = body
		seen.authorization = r.Header.Get(echo.HeaderAuthorization)
		seen.contentType = r.Header.Get(echo.HeaderContentType)
		seen.path = r.URL.Path
		seen.query = r.URL.RawQuery

		w.WriteHeader(status)
		_, err = w.Write(answer)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	configure(t, server.URL+"/v1/signup-tokens/redemptions", "the-service-credential")
	return seen
}

// configure sets the two keys Redeem reads and puts them back afterwards.
func configure(t *testing.T, endpoint, credential string) {
	t.Helper()

	config.InitDefaultConfig()
	previousURL := config.BraznSignupRedemptionURL.Get()
	previousToken := config.BraznServiceToken.Get()
	t.Cleanup(func() {
		config.BraznSignupRedemptionURL.Set(previousURL)
		config.BraznServiceToken.Set(previousToken)
	})
	config.BraznSignupRedemptionURL.Set(endpoint)
	config.BraznServiceToken.Set(credential)
}

// tokenFromContract reads the token out of the vendored request fixture rather
// than restating it, so a contract that changed the token's length is a failure
// here and not a test that quietly kept testing the old one.
func tokenFromContract(t *testing.T) string {
	t.Helper()

	var fixture struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(readContract(t, contractRequestPath), &fixture))
	require.NotEmpty(t, fixture.Token)
	return fixture.Token
}

// TestTheRequestThisBuildSendsMatchesTheContract is the assertion the vendored
// set exists for.
//
// IT COMPARES AGAINST BYTES THIS REPOSITORY DID NOT PRODUCE. A test that built
// its expectation from redemptionRequest would agree with itself whatever that
// struct said, which is exactly how a signed-message rule was got wrong twice
// in the entitlement package with every test green on both sides.
func TestTheRequestThisBuildSendsMatchesTheContract(t *testing.T) {
	seen := stub(t, http.StatusOK, readContract(t, contractResponsePath))

	require.NoError(t, Redeem(context.Background(), tokenFromContract(t), 42, "dana@acme.example"))
	require.Equal(t, 1, seen.calls)

	var expected map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(readContract(t, contractRequestPath), &expected))
	var sent map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(seen.body, &sent))

	// Exactly the members the contract declares. additionalProperties is false,
	// so a fourth is a malformed request rather than an ignored one - and a
	// missing one cannot be defaulted by the receiver.
	require.Len(t, sent, len(expected))
	for member := range expected {
		require.Contains(t, sent, member, "the contract declares %q and this build did not send it", member)
		assert.Equalf(t, jsonKind(t, expected[member]), jsonKind(t, sent[member]),
			"%q must be sent as the JSON type the contract declares", member)
	}

	// The one that is not interchangeable with its own value. A Go int64 field
	// would have produced 42 here and been wrong in a way nothing downstream
	// reports: the projection for a subject spelled differently is answered 204
	// and stored nowhere.
	assert.Equal(t, `"42"`, string(sent["user_id"]))
}

// TestTheTokenNeverTravelsInTheURL pins the other half of the handoff rule. A
// URL reaches access logs; the body does not.
func TestTheTokenNeverTravelsInTheURL(t *testing.T) {
	seen := stub(t, http.StatusOK, readContract(t, contractResponsePath))
	token := tokenFromContract(t)

	require.NoError(t, Redeem(context.Background(), token, 7, "dana@acme.example"))

	assert.Empty(t, seen.query)
	assert.NotContains(t, seen.path, token)
	assert.Equal(t, "Bearer the-service-credential", seen.authorization,
		"the service credential authenticates this call; the signup token never does")
	assert.Equal(t, echo.MIMEApplicationJSON, seen.contentType)
}

// TestRedeemCommitsOnlyOnRedeemed drives the two vendored answers back through
// the production path.
func TestRedeemCommitsOnlyOnRedeemed(t *testing.T) {
	t.Run("the response fixture is accepted", func(t *testing.T) {
		stub(t, http.StatusOK, readContract(t, contractResponsePath))
		require.NoError(t, Redeem(context.Background(), tokenFromContract(t), 1, "dana@acme.example"))
	})

	t.Run("the error fixture is a refusal", func(t *testing.T) {
		stub(t, http.StatusForbidden, readContract(t, contractErrorPath))
		err := Redeem(context.Background(), tokenFromContract(t), 1, "dana@acme.example")
		require.ErrorIs(t, err, ErrTokenUnusable)
	})

	t.Run("user_already_registered is its own outcome", func(t *testing.T) {
		stub(t, http.StatusForbidden, []byte(`{"error":"user_already_registered"}`))
		err := Redeem(context.Background(), tokenFromContract(t), 1, "dana@acme.example")
		require.ErrorIs(t, err, ErrUserAlreadyRegistered)
	})

	t.Run("malformed_request is a defect here, not a bad token", func(t *testing.T) {
		stub(t, http.StatusBadRequest, []byte(`{"error":"malformed_request"}`))
		err := Redeem(context.Background(), tokenFromContract(t), 1, "dana@acme.example")
		require.ErrorIs(t, err, ErrUnavailable)
	})

	// A 200 carrying a result this build does not know must NOT be committed
	// on. The enum is closed with one member precisely so a later outcome can
	// be added, and a build that treated an unknown one as success would act on
	// an answer it had not understood.
	t.Run("a result this build does not know is not a success", func(t *testing.T) {
		stub(t, http.StatusOK, []byte(`{"result":"held_for_review"}`))
		err := Redeem(context.Background(), tokenFromContract(t), 1, "dana@acme.example")
		require.ErrorIs(t, err, ErrUnavailable)
	})

	// 401 is a credential this instance got wrong. It must never be reported as
	// the customer's token being bad, because the customer cannot act on it and
	// the operator has to.
	t.Run("an unauthenticated call is not a bad token", func(t *testing.T) {
		stub(t, http.StatusUnauthorized, nil)
		err := Redeem(context.Background(), tokenFromContract(t), 1, "dana@acme.example")
		require.ErrorIs(t, err, ErrUnavailable)
	})
}

// TestRedeemRefusesBeforeReachingTheNetwork covers the cases that must cost no
// round trip, including the one that matters most: an instance in managed mode
// that has not been configured refuses every registration rather than allowing
// them.
func TestRedeemRefusesBeforeReachingTheNetwork(t *testing.T) {
	token := tokenFromContract(t)

	t.Run("a token that is not the contract's shape", func(t *testing.T) {
		seen := stub(t, http.StatusOK, readContract(t, contractResponsePath))

		for _, wrong := range []string{
			"",
			token[:len(token)-1],                    // one short
			token + "x",                             // one long
			token[:len(token)-1] + "=",              // padded
			token[:len(token)-1] + "+",              // standard base64, not base64url
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", // hex
		} {
			require.ErrorIs(t, Redeem(context.Background(), wrong, 1, "dana@acme.example"), ErrTokenUnusable)
		}
		assert.Equal(t, 0, seen.calls)
	})

	t.Run("an unconfigured instance fails closed", func(t *testing.T) {
		configure(t, "", "")
		require.ErrorIs(t, Redeem(context.Background(), token, 1, "dana@acme.example"), ErrUnavailable)
	})

	t.Run("a credential this instance does not have fails closed", func(t *testing.T) {
		configure(t, "https://example.invalid/v1/signup-tokens/redemptions", "")
		require.ErrorIs(t, Redeem(context.Background(), token, 1, "dana@acme.example"), ErrUnavailable)
	})

	t.Run("a user id or address this build did not supply", func(t *testing.T) {
		seen := stub(t, http.StatusOK, readContract(t, contractResponsePath))

		require.ErrorIs(t, Redeem(context.Background(), token, 0, "dana@acme.example"), ErrUnavailable)
		require.ErrorIs(t, Redeem(context.Background(), token, 1, ""), ErrUnavailable)
		assert.Equal(t, 0, seen.calls)
	})
}

// TestHTTPRefusalTellsThemNothingAboutTheToken is AC6 at the boundary the
// customer actually sees.
func TestHTTPRefusalTellsThemNothingAboutTheToken(t *testing.T) {
	unusable := HTTPRefusal(ErrTokenUnusable)
	registered := HTTPRefusal(ErrUserAlreadyRegistered)

	var first, second *echo.HTTPError
	require.ErrorAs(t, unusable, &first)
	require.ErrorAs(t, registered, &second)

	// The two must be indistinguishable: one is a dead link and the other is a
	// defect on this side, and telling them apart would say which tokens are
	// genuine.
	assert.Equal(t, http.StatusForbidden, first.Code)
	assert.Equal(t, first.Code, second.Code)
	assert.Equal(t, first.Message, second.Message)

	// Unavailable is the one that must differ, because it is the difference
	// between "ask for a new link" and "try again", and it says nothing about
	// any token.
	var unavailable *echo.HTTPError
	require.ErrorAs(t, HTTPRefusal(ErrUnavailable), &unavailable)
	assert.Equal(t, http.StatusServiceUnavailable, unavailable.Code)
}
