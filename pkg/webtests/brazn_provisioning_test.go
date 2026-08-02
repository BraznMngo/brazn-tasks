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
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/builder"
)

const provisioningPath = "/api/v1/brazn/provisioning"

// provisioningContractPrefix is written out here rather than taken from
// provisioning.SigningDomain, for the reason the entitlement ingest tests give
// above their own copy: what these tests sign has to be the contract's input
// and not agreement with our constant. The provisioning package pins the two
// against each other; this file assumes neither.
const provisioningContractPrefix = "percy.provisioning.v1\n"

// createUserPayload is the signed half of a create_user request in canonical
// JSON - members sorted by key, which is what a producer emits and therefore
// what the signature is made over.
func createUserPayload(mailbox string) string {
	return `{"contract_version":"1","email":"` + mailbox + `","operation":"create_user"}`
}

// provision posts one correctly signed provisioning request, the way the
// commercial service would: no session, no bearer token, nothing but the
// signed message.
func (env *managedEnv) provision(payload string) *httptest.ResponseRecorder {
	env.t.Helper()

	return env.postProvisioning(env.signedFor(provisioningContractPrefix, payload))
}

// signedFor builds an envelope over an explicitly named domain, because two of
// the tests below need one signed for the OTHER channel.
func (env *managedEnv) signedFor(domain, payload string) string {
	env.t.Helper()

	signature := ed25519.Sign(env.signingKey, []byte(domain+payload))
	return `{"signature":{"algorithm":"ed25519","key_id":"` + managedTestKeyID +
		`","value":"` + base64.RawURLEncoding.EncodeToString(signature) +
		`"},"signed":` + payload + `}`
}

func (env *managedEnv) postProvisioning(envelope string) *httptest.ResponseRecorder {
	env.t.Helper()

	req := httptest.NewRequest(http.MethodPost, provisioningPath, strings.NewReader(envelope))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec
}

// provisionedUser is the reply the consumer is written against
// (cloud/service/src/identity.ts, TaskUser). It is redeclared here rather than
// imported so the test asserts the WIRE shape - a handler that started
// answering with a JSON number for id would still satisfy a Go struct that
// declared one.
type provisionedUser struct {
	ID            string `json:"id"`
	Created       bool   `json:"created"`
	EmailVerified bool   `json:"email_verified"`
}

func provisioned(t *testing.T, rec *httptest.ResponseRecorder) provisionedUser {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := provisionedUser{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	// The contract validates this against ^[1-9][0-9]{0,18}$ and throws on
	// anything else, so an id that is not a decimal string is a broken reply
	// however plausible it looks.
	assert.Regexp(t, `^[1-9][0-9]{0,18}$`, out.ID)
	return out
}

func TestBraznProvisioningCreatesAWholeAccount(t *testing.T) {
	env := newManagedEnv(t)
	mailbox := "provisioned-one@example.com"

	result := provisioned(t, env.provision(createUserPayload(mailbox)))
	assert.True(t, result.Created)

	id, err := strconv.ParseInt(result.ID, 10, 64)
	require.NoError(t, err)

	db.AssertExists(t, "users", map[string]interface{}{"id": id, "email": mailbox}, false)
	// RegisterUser rather than user.CreateUser, and this is the difference:
	// the Inbox and the default saved filter. auth.CreateUserWithRandomUsername,
	// which the OpenID path uses, would have produced the first and not the
	// second.
	db.AssertExists(t, "projects", map[string]interface{}{"owner_id": id, "title": "Inbox"}, false)
	db.AssertExists(t, "saved_filters", map[string]interface{}{"owner_id": id}, false)
}

// TestBraznProvisioningIsIdempotentOnTheMailbox is the create-or-resolve
// contract, exercised on the path a concurrent loser takes.
//
// The second call is not a shortcut past the race: CreateOrResolveUserForMailbox
// claims the mailbox by INSERT before it looks at anything, so a mailbox that
// already has a claim reaches the conflict branch here exactly as it would if
// the two calls overlapped. Deleting that branch does not make this test pass
// differently - it makes the second call fail outright.
//
// The count is the other half. "Same id" alone would still hold if a second
// user had been created and the first happened to be returned; asserting that
// the instance holds ONE user for the mailbox is what rules that out.
func TestBraznProvisioningIsIdempotentOnTheMailbox(t *testing.T) {
	env := newManagedEnv(t)
	mailbox := "provisioned-twice@example.com"

	first := provisioned(t, env.provision(createUserPayload(mailbox)))
	require.True(t, first.Created)

	second := provisioned(t, env.provision(createUserPayload(mailbox)))
	assert.False(t, second.Created, "the second call resolved rather than created")
	assert.Equal(t, first.ID, second.ID, "one mailbox never gets a second id")

	db.AssertCount(t, "users", builder.Eq{"email": mailbox}, 1)
	db.AssertCount(t, "brazn_provisioned_users", builder.Eq{"email": mailbox}, 1)
}

// TestTheMailboxClaimCannotBeHeldTwice asserts the atomicity guarantee where
// the database decides it, rather than where a test happens to construct it.
//
// This is deliberately not written as two goroutines. The test database is
// SQLite, which serialises writers, so two concurrent calls would very likely
// run one after the other - and then they would produce one user and one id
// with the unique index REMOVED, because the second would simply adopt the
// first's user. That test would pass whether the guarantee existed or not.
// What actually makes concurrent callers safe is this index refusing the second
// claim, so this is what is asserted.
func TestTheMailboxClaimCannotBeHeldTwice(t *testing.T) {
	newManagedEnv(t)

	s := db.NewSession()
	defer s.Close()

	_, err := s.Insert(&models.ProvisionedUser{Email: "contested@example.com", UserID: 1})
	require.NoError(t, err)

	_, err = s.Insert(&models.ProvisionedUser{Email: "contested@example.com", UserID: 2})
	require.Error(t, err, "the mailbox is the unique key, and a second claim on it must not be stored")
	assert.True(t, db.IsUniqueConstraintError(err, "brazn_provisioned_users"),
		"the refusal must come from the unique index, not from something else: %v", err)
}

// TestBraznProvisioningAdoptsAnAccountThisInstanceAlreadyHas covers the
// accounts already on the development instance, which Google sign-in created
// before any of this existed. Creating a second user for one of them would
// strand everything they have.
func TestBraznProvisioningAdoptsAnAccountThisInstanceAlreadyHas(t *testing.T) {
	env := newManagedEnv(t)

	result := provisioned(t, env.provision(createUserPayload("user1@example.com")))
	assert.False(t, result.Created, "an account that was already here was not created by this call")
	assert.Equal(t, "1", result.ID)
	assert.True(t, result.EmailVerified)

	db.AssertCount(t, "users", builder.Eq{"email": "user1@example.com"}, 1)
}

// TestBraznProvisioningReportsAnUnconfirmedMailbox is the other half of the
// assertion above: email_verified follows the stored account status rather than
// being a constant that happens to read true.
func TestBraznProvisioningReportsAnUnconfirmedMailbox(t *testing.T) {
	env := newManagedEnv(t)

	func() {
		s := db.NewSession()
		defer s.Close()

		_, err := s.Exec("UPDATE users SET status = ? WHERE id = ?",
			int(user.StatusEmailConfirmationRequired), 2)
		require.NoError(t, err)
		require.NoError(t, s.Commit())
	}()

	result := provisioned(t, env.provision(createUserPayload("user2@example.com")))
	assert.False(t, result.Created)
	assert.Equal(t, "2", result.ID)
	assert.False(t, result.EmailVerified)
}

// TestBraznProvisioningRefusesEverythingItCannotAuthenticate checks that the
// door is shut, and checks it by the only thing that matters: nothing was
// created. A 400 on its own would also be produced by a router that never
// reached the handler.
func TestBraznProvisioningRefusesEverythingItCannotAuthenticate(t *testing.T) {
	mailbox := "never-provisioned@example.com"

	refused := func(t *testing.T, envelope func(env *managedEnv) string) {
		t.Helper()

		env := newManagedEnv(t)
		rec := env.postProvisioning(envelope(env))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		db.AssertMissing(t, "users", map[string]interface{}{"email": mailbox})
		db.AssertMissing(t, "brazn_provisioned_users", map[string]interface{}{"email": mailbox})
	}

	t.Run("an unsigned envelope", func(t *testing.T) {
		refused(t, func(_ *managedEnv) string {
			return `{"signed":` + createUserPayload(mailbox) + `}`
		})
	})

	t.Run("a request signed for the entitlement channel", func(t *testing.T) {
		// The same key and the same key id: only the domain the signature
		// covers differs, which is the one thing keeping the two channels
		// apart.
		refused(t, func(env *managedEnv) string {
			return env.signedFor(projectionContractPrefix, createUserPayload(mailbox))
		})
	})

	t.Run("an operation this build does not define", func(t *testing.T) {
		refused(t, func(env *managedEnv) string {
			return env.signedFor(provisioningContractPrefix,
				`{"contract_version":"1","operation":"delete_everything"}`)
		})
	})

	t.Run("a create_user carrying a member this build cannot act on", func(t *testing.T) {
		refused(t, func(env *managedEnv) string {
			return env.signedFor(provisioningContractPrefix,
				`{"contract_version":"1","email":"`+mailbox+
					`","operation":"create_user","organization_id":"org_1"}`)
		})
	})
}
