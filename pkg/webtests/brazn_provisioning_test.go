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
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestCreateOrResolveUserForMailboxRecoversFromALostProvisioning constructs
// the exact state ErrMailboxProvisioningLost answers (BRA-1207) directly on
// the table, for the same reason TestTheMailboxClaimCannotBeHeldTwice does:
// two real goroutines racing an INSERT on SQLite very likely just run one
// after the other, which would prove nothing about the retry. A claim row
// whose winner crashed before recording its UserID is a state this call
// genuinely produces (see resolveProvisionedMailbox's own comment), and
// constructing it directly is the only reliable way to put a test on the
// other side of it.
//
// THE CHEAP CHECK: reduce maxMailboxProvisioningAttempts to 1 (no retry) and
// this goes red — the first and only read finds UserID 0 and returns
// ErrMailboxProvisioningLost before the goroutine below has fixed it.
func TestCreateOrResolveUserForMailboxRecoversFromALostProvisioning(t *testing.T) {
	env := newManagedEnv(t)

	// A real user to be "the winner" - created through the ordinary path so
	// resolveProvisionedMailbox's final userByID has a real row to find.
	winner := provisioned(t, env.provision(createUserPayload("winner@example.com")))
	winnerID, err := strconv.ParseInt(winner.ID, 10, 64)
	require.NoError(t, err)

	s := db.NewSession()
	claim := &models.ProvisionedUser{Email: "raced@example.com", UserID: 0}
	_, err = s.Insert(claim)
	require.NoError(t, err)
	require.NoError(t, s.Commit())
	s.Close()

	// The "other provisioning call" finishing shortly after this one starts -
	// late enough that the first two attempts still see UserID 0 and only the
	// third succeeds, proving the retry ran rather than merely not mattering.
	go func() {
		time.Sleep(70 * time.Millisecond)
		fix := db.NewSession()
		defer fix.Close()
		_, updateErr := fix.Where("email = ?", "raced@example.com").
			Cols("user_id").Update(&models.ProvisionedUser{UserID: winnerID})
		require.NoError(t, updateErr)
		require.NoError(t, fix.Commit())
	}()

	resolved, created, err := models.CreateOrResolveUserForMailbox(context.Background(), "raced@example.com")
	require.NoError(t, err, "the retry must recover once the other call's commit lands")
	assert.False(t, created, "the mailbox already had a winner; this call resolved rather than created")
	assert.Equal(t, winnerID, resolved.ID)

	db.AssertCount(t, "brazn_provisioned_users", builder.Eq{"email": "raced@example.com"}, 1)
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

// TestBraznProvisioningRefusesToGiveOneAccountToTwoMailboxes closes an
// account-takeover path that adoption opens on its own.
//
// openid.getOrCreateUser rewrites users.email IN PLACE when the provider
// reports a new address for a subject it already knows, and that write goes
// through user.UpdateUser rather than user.UpdateEmail - so it skips the
// uniqueness check UpdateEmail makes. An account whose address has moved that
// way onto a mailbox the commercial service later sells to somebody else would
// be adopted a second time, and with the provider's email fallback on (which
// BRA-1021 must enable for provisioned accounts to sign in at all) the second
// customer signs in to the first customer's account.
//
// WHY THIS CANNOT PASS FOR THE WRONG REASON. The first call in this test is the
// same endpoint, the same key and the same environment answering 200, so the
// 400 that follows is not a router, a signature or a harness refusing - only
// the state in between differs. Deleting refuseASecondMailboxForOneUser makes
// the second call answer 200 with id "1", which is the assertion below.
func TestBraznProvisioningRefusesToGiveOneAccountToTwoMailboxes(t *testing.T) {
	env := newManagedEnv(t)

	first := provisioned(t, env.provision(createUserPayload("user1@example.com")))
	require.False(t, first.Created, "user 1 was already here; this call adopted them")
	require.Equal(t, "1", first.ID)

	// The rewrite described above, performed the way the OpenID callback
	// performs it: straight onto the column.
	func() {
		s := db.NewSession()
		defer s.Close()

		_, err := s.Exec("UPDATE users SET email = ? WHERE id = ?", "alice-moved@example.com", 1)
		require.NoError(t, err)
		require.NoError(t, s.Commit())
	}()

	// A DIFFERENT customer buys the address user 1 now happens to carry.
	rec := env.provision(createUserPayload("alice-moved@example.com"))
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// Nothing bound, and nothing left behind: the refusal rolls the claim back
	// with the rest of its transaction, so a later settlement starts from a
	// clean table rather than from a half-written one.
	db.AssertMissing(t, "brazn_provisioned_users",
		map[string]interface{}{"email": "alice-moved@example.com"})
	db.AssertCount(t, "brazn_provisioned_users", builder.Eq{"user_id": 1}, 1)
}

// There is still no test HERE for a newly created account reported as
// unconfirmed - the created:true, email_verified:false combination - but the
// reason has changed, and the old reason is worth not re-deriving.
//
// It used to be unreachable. RegisterUser undid CreateUser's confirmation
// status before returning, because CreateNewProjectForUser passed the pre-write
// struct to user.UpdateUser, whose column list includes "status". A created
// account was therefore always Active whatever the mailer was doing.
//
// BRA-1047 fixed that at the source: CreateNewProjectForUser writes
// default_project_id alone, so the confirmation status survives registration
// and the re-read in provisionUserForClaim is no longer unexercised. The
// combination is reachable now.
//
// What covers it is TestRegisterUserKeepsTheConfirmationStatus in pkg/models,
// which asserts the STORED status after RegisterUser returns with the mailer
// enabled. That is the defect's own boundary. Reaching it through this endpoint
// as well would need managed mode running with a mailer configured, and would
// re-assert through two more layers what is already pinned one call from the
// bug.

// TestBraznProvisioningReportsAnUnconfirmedMailbox pairs with
// TestBraznProvisioningAdoptsAnAccountThisInstanceAlreadyHas, which asserts
// email_verified TRUE for an active account. Two adopted accounts differing
// only in stored status, answered oppositely: the field follows the row rather
// than being a constant that happens to read one way.
//
// The pair covers the resolve side only. Nothing covers it for a freshly
// created account, for the reason set out above.
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
