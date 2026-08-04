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

// Package signup redeems the signup token Percy Cloud issues to somebody who is
// entitled to a Brazn Tasks account, and reports the user this instance created
// for them.
//
// ONE CALL DOES BOTH HALVES, and that is the design rather than an economy.
// Consuming the token and reporting the users.id it is bound to are the same
// operation, so a managed user with no entitlement is impossible by
// construction and not by a check somebody can forget: the user cannot exist
// without a consumed token, and the token cannot be consumed without a user id
// to bind it to. The contract deliberately offers no check-only operation - a
// check that does not consume opens a window in which two registrations both
// pass it.
//
// HOW A CALLER USES IT. Begin a transaction, create the user, call Redeem with
// the id it obtained, and COMMIT ONLY when Redeem returns nil. Every refusal is
// a rollback, which is what makes "no token, no user" structural.
//
// VALIDATION IS ONLINE. Nothing here verifies a signature and nothing here
// knows what a token means. Expiry, revocation, the entitlement behind it and
// the address an invitation was bound to are all Percy Cloud's answer; this
// package's whole job is to ask and to refuse safely when it cannot.
//
// The wire contract is cloud/contracts/v1/signup/ in the Percy repository
// (BRA-1080). Its three conformance fixtures are vendored under
// testdata/contract/; see the README beside them.
package signup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/log"

	"github.com/labstack/echo/v5"
)

// The vocabulary of the wire contract, quoted from
// cloud/contracts/v1/signup/*.schema.json by literal.
//
// THEY ARE LITERALS HERE ON PURPOSE. An interop value that both sides import
// from one definition is checked by neither, and a typo would sign, verify and
// stay green on this side while the other rejected everything. These are pinned
// against the contract text so the first real redemption is not the first test.
const (
	// tokenShapePattern is the request schema's `token` pattern, character for
	// character: exactly 43 unpadded base64url characters, which is 256 bits
	// and no other length. Padding, hex, and a longer or shorter value all fail
	// here rather than at a lookup, so one token has exactly one spelling.
	//
	// gosec reads any constant whose NAME contains "token" and whose value has
	// enough entropy as a hardcoded credential (G101). This one is a regular
	// expression describing the SHAPE of a token and contains no token; the
	// name is kept because "the pattern a token matches" is what it is.
	tokenShapePattern = `^[A-Za-z0-9_-]{43}$` //nolint:gosec // G101: a pattern, not a credential.

	// resultRedeemed is the only answer that may be committed on.
	resultRedeemed = "redeemed"

	codeTokenUnusable         = "token_unusable"
	codeUserAlreadyRegistered = "user_already_registered"
	codeMalformedRequest      = "malformed_request"
)

var tokenShape = regexp.MustCompile(tokenShapePattern)

// maxAnswerBytes bounds what is read back. The three possible bodies are a
// single member each; anything past this is not one of them, and reading it
// unbounded would let whatever is on the other end of a misconfigured URL
// decide how much memory a registration costs.
const maxAnswerBytes = 4096

// The three outcomes a caller has to tell apart. They are deliberately fewer
// than the codes on the wire, because the wire's own vocabulary is deliberately
// poor: unknown, expired, consumed, revoked, closed entitlement and an address
// that does not match the invitation are ONE code, so that this endpoint is not
// an oracle for which tokens exist or which address an invitation was sent to.
var (
	// ErrTokenUnusable means the token cannot be redeemed, and says nothing
	// whatsoever about why. Callers must not try to be more specific: there is
	// no information here to be more specific with.
	ErrTokenUnusable = errors.New("the signup token cannot be redeemed")

	// ErrUserAlreadyRegistered means the token was usable and the user id this
	// build reported is already bound to another redemption. It is a defect on
	// THIS side rather than anything to show a customer - an existing user must
	// never acquire a second entitlement this way - and it is the one refusal
	// that should alarm.
	ErrUserAlreadyRegistered = errors.New("the reported user is already bound to another redemption")

	// ErrUnavailable means no answer was obtained: not configured, unreachable,
	// or an answer this build cannot read. It is NOT a statement about the
	// token, and the customer is told to try again rather than that their link
	// is dead.
	ErrUnavailable = errors.New("the signup redemption endpoint could not be reached")
)

// redemptionClient is a plain client, and deliberately not utils.NewSSRFSafeHTTPClient.
// That guard exists for URLs a USER supplies - a webhook target, an avatar - and
// it blocks private ranges, which is exactly where the commercial service sits
// on the deployed compose network. This URL comes from the instance's own
// configuration, so an operator who can set it can already reach anything the
// process can.
var redemptionClient = &http.Client{Timeout: 15 * time.Second}

// redemptionRequest is signup-token-redemption-request.schema.json.
//
// user_id IS A STRING AND THAT IS THE POINT OF THE TYPE. A Go int64 field
// marshals to 42 rather than "42" and would reach the entitlement projection as
// a value that side spells differently - a signup that looks entirely
// successful and a customer with no entitlement, because a projection for a
// subject that does not exist is answered 204 and stored nowhere.
type redemptionRequest struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// redemptionAnswer is signup-token-redemption-response.schema.json.
type redemptionAnswer struct {
	Result string `json:"result"`
}

// redemptionRefusal is signup-token-redemption-error.schema.json. It has no
// message member, and there is nowhere for one to be added: the vocabulary
// cannot express the distinction it must not disclose.
type redemptionRefusal struct {
	Error string `json:"error"`
}

// CanBeRedeemed reports whether a value is even shaped like a signup token.
//
// It exists so a caller can refuse an absent or mistyped one before creating
// anything, and it is the SAME predicate Redeem applies rather than a second
// opinion about what a token looks like. It is an optimisation and not the
// gate: only Redeem can tell a real token from a plausible one.
func CanBeRedeemed(token string) bool {
	return tokenShape.MatchString(token)
}

// Redeem consumes the token and binds it to the user this instance has just
// created. It returns nil only when Percy Cloud answered `redeemed`, which is
// the only condition under which the caller may commit.
//
// email is the address the user was actually registered with - what they typed,
// or what the identity provider asserted. It is sent unconditionally on both
// the password and the Google path, and this build never learns whether the
// token carries an address binding at all: an optional field would let whoever
// holds a forwarded invitation strip the comparison by omitting it.
//
// NOTHING HERE LOGS THE TOKEN. Percy Cloud stores only its SHA-256 hash, and a
// value in a log line is a value in a backup.
func Redeem(ctx context.Context, token string, userID int64, email string) error {
	if !CanBeRedeemed(token) {
		// A token that cannot be spelled correctly cannot be redeemed, and
		// refusing here rather than on the wire means an absent or mistyped
		// token costs no round trip and burns nothing.
		return ErrTokenUnusable
	}

	if userID <= 0 || email == "" {
		// Both are required by the contract and both come from this build, so
		// either being absent is a defect here rather than anything about the
		// token. Refusing locally keeps a body the contract calls malformed off
		// the wire, where it would consume nothing but would be indistinguishable
		// in a log from a customer's bad link.
		log.Errorf("[signup] refusing to redeem for user id %d with an email of length %d", userID, len(email))
		return ErrUnavailable
	}

	endpoint := config.BraznSignupRedemptionURL.GetString()
	credential := config.BraznServiceToken.GetString()
	if endpoint == "" || credential == "" {
		// Fail closed. An instance in managed mode that cannot ask whether a
		// token is good must refuse every registration, not allow them.
		log.Error("[signup] brazn.signupredemptionurl or brazn.servicetoken is unset, so no registration can be redeemed")
		return ErrUnavailable
	}

	payload, err := json.Marshal(redemptionRequest{
		Token:  token,
		UserID: strconv.FormatInt(userID, 10),
		Email:  email,
	})
	if err != nil {
		log.Errorf("[signup] could not encode the redemption request: %s", err)
		return ErrUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		log.Errorf("[signup] could not build the redemption request: %s", err)
		return ErrUnavailable
	}
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	// The service credential, not the signup token: this is a server-to-server
	// call and the token is not its authenticator. An unauthenticated call is
	// answered 401 before the token is read, so a signup token cannot be burned
	// by anyone who merely holds one.
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+credential)

	resp, err := redemptionClient.Do(req)
	if err != nil {
		log.Errorf("[signup] the redemption call did not complete: %s", err)
		return ErrUnavailable
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		log.Errorf("[signup] the redemption answer could not be read: %s", err)
		return ErrUnavailable
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return readAnswer(body)
	case http.StatusBadRequest, http.StatusForbidden:
		return readRefusal(body)
	default:
		// 401 lands here too, and deliberately: a credential this instance got
		// wrong is not the customer's token being bad, and must never be
		// reported as one.
		log.Errorf("[signup] the redemption endpoint answered %d", resp.StatusCode)
		return ErrUnavailable
	}
}

// readAnswer decides whether a 200 may be committed on.
//
// ANYTHING OTHER THAN `redeemed` IS A REFUSAL TO COMMIT. The enum is closed
// with one member so a later outcome can be added additively, which means a
// build that meets one has not understood the answer and must not act on it.
// That direction is the safe one: the token may well have been consumed, and a
// retry replays the stored response.
func readAnswer(body []byte) error {
	var answer redemptionAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		log.Errorf("[signup] a 200 answer that is not the response schema: %s", err)
		return ErrUnavailable
	}
	if answer.Result != resultRedeemed {
		log.Errorf("[signup] a 200 answer this build does not understand: result %q", answer.Result)
		return ErrUnavailable
	}
	return nil
}

// readRefusal maps the contract's three codes onto the two a caller can act on.
func readRefusal(body []byte) error {
	var refusal redemptionRefusal
	if err := json.Unmarshal(body, &refusal); err != nil {
		log.Errorf("[signup] a refusal that is not the error schema: %s", err)
		return ErrUnavailable
	}

	switch refusal.Error {
	case codeTokenUnusable:
		return ErrTokenUnusable
	case codeUserAlreadyRegistered:
		log.Error("[signup] the user id this instance reported is already bound to another redemption - " +
			"a registration reached this call with a user that already exists")
		return ErrUserAlreadyRegistered
	case codeMalformedRequest:
		log.Error("[signup] the redemption body was refused as malformed, which is a defect in this build " +
			"rather than anything about the token")
		return ErrUnavailable
	default:
		log.Errorf("[signup] a refusal code this build does not know: %q", refusal.Error)
		return ErrUnavailable
	}
}

// HTTPRefusal is what the person in front of the browser is told.
//
// TWO OUTCOMES, NOT THREE. ErrTokenUnusable and ErrUserAlreadyRegistered are
// one answer to a customer - the second is a defect on this side, and telling
// them apart on the wire would say which tokens are genuine. ErrUnavailable is
// the one that must be distinguishable, because it is the difference between
// "this link is dead, ask for a new one" and "try again in a moment", and it
// discloses nothing about any token.
func HTTPRefusal(err error) error {
	if errors.Is(err, ErrUnavailable) {
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"Registration is temporarily unavailable. Please try again in a moment.")
	}
	return echo.NewHTTPError(http.StatusForbidden,
		"This signup link cannot be used. Ask for a new one, or sign in if you already have an account.")
}
