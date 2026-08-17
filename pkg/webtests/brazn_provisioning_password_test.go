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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"xorm.io/builder"
)

// createUserWithPasswordPayload is the signed half of a
// create_user_with_password request (BRA-1335), canonical JSON with members
// sorted by key - what a producer emits and therefore what the signature is
// made over.
func createUserWithPasswordPayload(email, username, password string) string {
	return `{"contract_version":"1","email":"` + email +
		`","operation":"create_user_with_password","password":"` + password +
		`","username":"` + username + `"}`
}

// passwordAccount is the reply the phase-2 Cloud caller is written against: an
// id and nothing else. Redeclared here rather than imported so the test
// asserts the WIRE shape.
type passwordAccount struct {
	ID string `json:"id"`
}

func provisionedPasswordAccount(t *testing.T, rec *httptest.ResponseRecorder) passwordAccount {
	t.Helper()

	require.Equal(t, 200, rec.Code, rec.Body.String())
	out := passwordAccount{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Regexp(t, `^[1-9][0-9]{0,18}$`, out.ID)
	return out
}

// TestBraznProvisioningWithPasswordCreatesAnAccount is the ordinary path: a
// brand-new account, its Inbox and its default saved filters, exactly as
// create_user's own creation path produces (RegisterUser is the shared
// machinery), plus the one thing create_user never carries - a password this
// account can actually sign in with.
func TestBraznProvisioningWithPasswordCreatesAnAccount(t *testing.T) {
	env := newManagedEnv(t)

	result := provisionedPasswordAccount(t, env.provision(
		createUserWithPasswordPayload("checkout-one@example.com", "checkout-one", "a-strong-password")))

	id, err := strconv.ParseInt(result.ID, 10, 64)
	require.NoError(t, err)

	db.AssertExists(t, "users", map[string]interface{}{
		"id":       id,
		"email":    "checkout-one@example.com",
		"username": "checkout-one",
	}, false)
	db.AssertExists(t, "projects", map[string]interface{}{"owner_id": id, "title": "Inbox"}, false)
	db.AssertExists(t, "saved_filters", map[string]interface{}{"owner_id": id}, false)
	db.AssertExists(t, "brazn_provisioned_users",
		map[string]interface{}{"email": "checkout-one@example.com", "user_id": id}, false)

	// The password is USABLE and never stored as the plaintext this test sent:
	// the stored hash must verify the plaintext (proving RegisterUser hashed
	// what it was given) and must not equal it (proving it is a hash at all).
	s := db.NewSession()
	defer s.Close()
	stored, err := user.GetUserByID(s, id)
	require.NoError(t, err)
	assert.NotEqual(t, "a-strong-password", stored.Password)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("a-strong-password")))
}

// TestBraznProvisioningWithPasswordRefusesAnExistingEmail is the collision
// case the ticket calls out by name: unlike create_user's OAuth-adoption
// semantics, a mailbox this instance already has an account for is REFUSED
// and never adopted - see models.ErrPasswordAccountEmailOrUsernameTaken for
// why silently attaching a chosen password to somebody else's account would
// be worse than a flat refusal.
//
// user1@example.com is the fixture account create_user's own adoption test
// (TestBraznProvisioningAdoptsAnAccountThisInstanceAlreadyHas) resolves to
// user id 1; this asserts the opposite operation refuses the very same
// mailbox instead.
//
// THE CHEAP CHECK: change CreateProvisionedUserWithPassword's collision branch
// to call userForMailbox and adopt like registerUserForMailbox does, and this
// goes red - the request would answer 200 with id "1" instead of a refusal,
// and user 1's password would have been silently overwritten.
//
// THE OTHER CHEAP CHECK: remove the IsErrUserEmailExists/IsErrUsernameExists
// collapse from CreateProvisionedUserWithPassword and the status stays 400 -
// Echo's own error handler already maps user.ErrUserEmailExists to 400 - but
// the body assertion below goes red, because the raw error text ("User with
// that email already exists ... email: user1@example.com") reaches the wire
// instead of the channel's one flat refusal. THAT check is the one this
// operation actually adds: every other refusal on this seam is oracle-safe by
// design (BraznProvision's own comment on why the reply is flat), and a
// collision here is the one new place that guarantee could leak.
func TestBraznProvisioningWithPasswordRefusesAnExistingEmail(t *testing.T) {
	env := newManagedEnv(t)

	rec := env.provision(createUserWithPasswordPayload("user1@example.com", "a-brand-new-username", "a-strong-password"))
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "This is not a provisioning request this instance accepts.")
	assert.NotContains(t, rec.Body.String(), "user1@example.com",
		"the refusal must not echo the mailbox it refused, matching every other refusal on this channel")
	assert.NotContains(t, rec.Body.String(), "already exists")

	// Nothing claimed, nothing created, and user 1's own row untouched.
	db.AssertMissing(t, "users", map[string]interface{}{"username": "a-brand-new-username"})
	db.AssertCount(t, "brazn_provisioned_users", builder.Eq{"email": "user1@example.com"}, 0)

	s := db.NewSession()
	defer s.Close()
	untouched, err := user.GetUserByID(s, 1)
	require.NoError(t, err)
	assert.NotEqual(t, "a-strong-password", untouched.Password,
		"user 1's password must survive a refused request naming their mailbox")
}

// TestBraznProvisioningWithPasswordRefusesAnExistingUsername is the collision
// case on the OTHER identifier: create_user never has this problem because it
// generates a random, unguessable username itself (registerUserForMailbox),
// but this operation's username is chosen by somebody at checkout and can
// collide with an account that has nothing to do with the mailbox they typed.
//
// "user1" is the fixture username fixture user 1 already holds. The mailbox
// here is brand new, so the claim on it succeeds; the refusal has to come from
// RegisterUser's own username check, and this is the assertion that isolates
// that.
func TestBraznProvisioningWithPasswordRefusesAnExistingUsername(t *testing.T) {
	env := newManagedEnv(t)

	rec := env.provision(createUserWithPasswordPayload("nobody-yet@example.com", "user1", "a-strong-password"))
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "This is not a provisioning request this instance accepts.")
	assert.NotContains(t, rec.Body.String(), "already exists")

	// The mailbox claim this call took must have been rolled back along with
	// the rest of the transaction - otherwise a LATER, correctly-named request
	// for this same mailbox would itself be refused as a collision with a
	// half-finished attempt nobody can ever retry successfully.
	db.AssertMissing(t, "brazn_provisioned_users", map[string]interface{}{"email": "nobody-yet@example.com"})
	db.AssertMissing(t, "users", map[string]interface{}{"email": "nobody-yet@example.com"})
}

// TestBraznProvisioningWithPasswordCollisionResolvesToExactlyOneWinner is the
// concurrency requirement: two requests naming the same mailbox must never
// both succeed, however close together they arrive.
//
// It reuses the SAME mechanism create_user's own concurrency tests already
// prove is atomic (brazn_provisioned_users.email's unique index,
// TestTheMailboxClaimCannotBeHeldTwice) rather than racing two real
// goroutines against SQLite, for the identical reason that test gives: two
// genuinely concurrent callers on the test database would very likely just run
// one after the other, which would prove nothing about the guarantee. What
// this adds beyond that existing proof is specific to THIS operation: the
// loser must be REFUSED rather than resolved to the winner, which is the one
// place this operation's collision handling differs from create_user's.
//
// THE CHEAP CHECK: change the claim-conflict branch in
// CreateProvisionedUserWithPassword to retry-and-resolve the way
// CreateOrResolveUserForMailbox does, and the second call here starts
// answering 200 with the first call's id instead of refusing.
func TestBraznProvisioningWithPasswordCollisionResolvesToExactlyOneWinner(t *testing.T) {
	env := newManagedEnv(t)

	first := provisionedPasswordAccount(t, env.provision(
		createUserWithPasswordPayload("racing@example.com", "racing-winner", "a-strong-password")))

	second := env.provision(
		createUserWithPasswordPayload("racing@example.com", "racing-loser", "a-different-password"))
	assert.Equal(t, 400, second.Code, second.Body.String())

	db.AssertCount(t, "users", builder.Eq{"email": "racing@example.com"}, 1)
	db.AssertCount(t, "brazn_provisioned_users", builder.Eq{"email": "racing@example.com"}, 1)
	db.AssertMissing(t, "users", map[string]interface{}{"username": "racing-loser"})

	winnerID, err := strconv.ParseInt(first.ID, 10, 64)
	require.NoError(t, err)
	db.AssertExists(t, "users", map[string]interface{}{"id": winnerID, "username": "racing-winner"}, false)
}

// TestBraznProvisioningWithPasswordNeverLogsThePassword is the minimum
// coverage the ticket names by name: the plaintext password must not appear
// in anything a test can observe being logged, on EITHER the success path or
// the refusal path - a refusal is exactly where a less careful implementation
// would be tempted to log the request it turned down.
//
// It redirects this build's real logger to a file (the same mechanism
// pkg/log/logging_test.go tests directly) rather than asserting on a mock, so
// what is checked is what an operator's actual log file would contain. DEBUG
// level is required because both the success line (provisionUserWithPassword)
// and the refusal line (refuseProvisioning) log at or below that level.
//
// NEITHER SUBTEST WOULD PASS VACUOUSLY: each asserts an expected marker IS in
// the log before asserting the password is NOT, so a logger that was silently
// misconfigured - or a request that never reached the code path it claims to
// exercise - fails on the marker rather than passing by writing nothing at
// all.
func TestBraznProvisioningWithPasswordNeverLogsThePassword(t *testing.T) {
	env := newManagedEnv(t)

	tempDir := t.TempDir()
	log.ConfigureStandardLogger(true, "file", tempDir, "DEBUG", "text")
	t.Cleanup(log.InitLogger)

	const plaintext = "a-password-nobody-should-see-logged"

	created := provisionedPasswordAccount(t, env.provision(
		createUserWithPasswordPayload("logged-nowhere@example.com", "logged-nowhere", plaintext)))
	require.NotEmpty(t, created.ID)

	refused := env.provision(
		createUserWithPasswordPayload("user1@example.com", "logged-nowhere-else", plaintext))
	require.Equal(t, 400, refused.Code)

	logged, err := os.ReadFile(filepath.Join(tempDir, "standard.log"))
	require.NoError(t, err)

	assert.Contains(t, string(logged), "Provisioned a Brazn Tasks password account",
		"the success line must actually have been written, or the absence check below proves nothing")
	assert.Contains(t, string(logged), "Refused a provisioning request",
		"the refusal line must actually have been written, or the absence check below proves nothing")
	assert.NotContains(t, string(logged), plaintext)
}

// TestBraznProvisioningWithPasswordAccountIsImmediatelyActive pins BRA-1335's
// whole point against the one config state that silently defeats it: with
// mail configured, RegisterUser's ordinary path (used by /register) leaves a
// brand-new account at StatusEmailConfirmationRequired and mails a token
// nobody asked this account to need - Percy Cloud already proved the mailbox
// works by reaching this signed call at all, so re-gating on a second proof
// of the same fact would mean the customer lands on the login screen unable
// to log in, which is the opposite of "the account is ready the moment they
// open the task app."
//
// THE CHEAP CHECK: swap CreateProvisionedUserWithPassword's
// RegisterUserConfirmLater + forced-active back to a plain RegisterUser call,
// and this goes red - the account is created but stored at
// StatusEmailConfirmationRequired, and the login below is refused.
func TestBraznProvisioningWithPasswordAccountIsImmediatelyActive(t *testing.T) {
	config.MailerEnabled.Set(true)
	defer config.MailerEnabled.Set(false)

	env := newManagedEnv(t)

	result := provisionedPasswordAccount(t, env.provision(
		createUserWithPasswordPayload("ready-on-arrival@example.com", "ready-on-arrival", "a-strong-password")))

	id, err := strconv.ParseInt(result.ID, 10, 64)
	require.NoError(t, err)

	db.AssertExists(t, "users", map[string]interface{}{
		"id":     id,
		"status": int(user.StatusActive),
	}, false)

	// The real proof: the account this call just created can actually log in,
	// with no confirmation step in the way - not just that the status column
	// reads correctly in isolation.
	loginResp := humaRequest(t, env.e, http.MethodPost, "/api/v1/login",
		`{"username":"ready-on-arrival","password":"a-strong-password"}`, "", "")
	require.Equal(t, 200, loginResp.Code, loginResp.Body.String())
}
