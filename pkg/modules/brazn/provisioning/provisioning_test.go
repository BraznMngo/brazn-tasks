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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKeyID = "provisioning-test"

// The two signing domains, written out as literals rather than taken from
// SigningDomain and entitlement.SigningDomain.
//
// A test that builds its input from the constant under test can only prove the
// code agrees with itself: a typo would sign, verify and stay green here while
// Percy's producer - which has its own copy of the contract's string - was
// rejected by every message it sent. These literals are the contract, and the
// constants are what is on trial.
const (
	provisioningPrefix = "percy.provisioning.v1\n"
	entitlementPrefix  = "percy.entitlement-projection.v2\n"
)

// createUser is a well-formed create_user payload in canonical JSON: members
// sorted by key, which is what a producer emits and therefore what a signature
// is made over.
const createUser = `{"contract_version":"1","email":"someone@example.com","operation":"create_user"}`

// trustedKey generates a key pair and configures the instance to trust it.
func trustedKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	config.InitDefaultConfig()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	config.BraznEntitlementKeys.Set(testKeyID + ":" + base64.StdEncoding.EncodeToString(public))
	return private
}

// envelopeOver signs a payload under an explicitly named domain and splices the
// payload into an envelope verbatim. The domain is a parameter because the
// tests below need to send a message signed for the OTHER channel.
func envelopeOver(key ed25519.PrivateKey, domain, payload string) []byte {
	signature := ed25519.Sign(key, []byte(domain+payload))
	return []byte(`{"signature":{"algorithm":"ed25519","key_id":"` + testKeyID +
		`","value":"` + base64.RawURLEncoding.EncodeToString(signature) +
		`"},"signed":` + payload + `}`)
}

// TestSigningDomainMatchesTheContract pins the constant against the literal.
func TestSigningDomainMatchesTheContract(t *testing.T) {
	assert.Equal(t, provisioningPrefix, SigningDomain)
	assert.Equal(t, byte(0x0A), SigningDomain[len(SigningDomain)-1],
		"the domain is terminated by the 0x0A the entitlement contract specifies, and this one follows it")
}

func TestVerifyReadsAWellFormedCreateUserRequest(t *testing.T) {
	key := trustedKey(t)

	operation, payload, err := Verify(envelopeOver(key, provisioningPrefix, createUser))
	require.NoError(t, err)
	assert.Equal(t, OperationCreateUser, operation)

	request, err := DecodeCreateUser(payload)
	require.NoError(t, err)
	assert.Equal(t, "someone@example.com", request.Email)
}

// TestTheTwoChannelsDoNotAcceptEachOther is the domain separation, asserted
// where it decides the outcome rather than by comparing two strings.
//
// One key signs for both channels, so the ONLY thing keeping a projection from
// being a candidate provisioning request is the prefix the signature covers.
// Both directions are checked because the failure is not symmetric to read:
// making the two domains equal breaks the first, and dropping the domain from
// either signing input breaks the other.
func TestTheTwoChannelsDoNotAcceptEachOther(t *testing.T) {
	key := trustedKey(t)

	t.Run("a create_user request signed for the entitlement channel is refused", func(t *testing.T) {
		_, _, err := Verify(envelopeOver(key, entitlementPrefix, createUser))
		require.Error(t, err)
		assert.Equal(t, entitlement.ReasonInvalidSignature, entitlement.RefusalReason(err))
	})

	t.Run("a message signed for the provisioning channel is not a projection", func(t *testing.T) {
		_, err := entitlement.Verify(envelopeOver(key, provisioningPrefix, createUser))
		require.Error(t, err)
		assert.Equal(t, entitlement.ReasonInvalidSignature, entitlement.RefusalReason(err))
	})
}

func TestVerifyRefusesAKeyThisInstanceDoesNotTrust(t *testing.T) {
	trustedKey(t)
	_, untrusted, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	_, _, err = Verify(envelopeOver(untrusted, provisioningPrefix, createUser))
	require.Error(t, err)
	assert.Equal(t, entitlement.ReasonInvalidSignature, entitlement.RefusalReason(err))
}

func TestVerifyRefusesAnEmptyTrustStore(t *testing.T) {
	key := trustedKey(t)
	config.BraznEntitlementKeys.Set("")

	_, _, err := Verify(envelopeOver(key, provisioningPrefix, createUser))
	require.Error(t, err)
	assert.Equal(t, entitlement.ReasonUnknownKey, entitlement.RefusalReason(err))
}

func TestDecodeCreateUserRefusesWhatThisBuildCannotActOn(t *testing.T) {
	key := trustedKey(t)

	// The envelope is sound in every case below: what is refused is what it
	// carries, which is a separate decision made after the signature.
	refused := func(t *testing.T, payload string) {
		t.Helper()

		operation, verified, err := Verify(envelopeOver(key, provisioningPrefix, payload))
		require.NoError(t, err)
		require.Equal(t, OperationCreateUser, operation)

		_, err = DecodeCreateUser(verified)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}

	t.Run("an undeclared member, which a later contract carries meaning in", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com",`+
			`"operation":"create_user","organization_id":"org_1"}`)
	})

	t.Run("a contract version this build does not define", func(t *testing.T) {
		refused(t, `{"contract_version":"2","email":"someone@example.com","operation":"create_user"}`)
	})

	t.Run("an address that is not a mailbox", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone","operation":"create_user"}`)
	})

	t.Run("no address at all", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"","operation":"create_user"}`)
	})

	t.Run("an address longer than the column that would store it", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"`+
			strings.Repeat("a", 245)+`@example.com","operation":"create_user"}`)
	})
}

// resolveMailbox is a well-formed resolve_mailbox payload in canonical JSON.
//
// It carries EXACTLY the members a create_personal_inbox payload does apart from
// the operation name, which is the point of the test below and the reason the
// two operations must never share one.
const resolveMailbox = `{"contract_version":"1","operation":"resolve_mailbox",` +
	`"organization_id":"org_3d77e0c15a84","user_id":"42"}`

// TestResolveMailboxIsItsOwnOperation pins the operation name against the
// contract's literal and against its three siblings.
//
// The literal is written out here rather than taken from the constant, for the
// reason the signing domains above are: a typo would route and decode happily on
// this side while Percy's caller - which has its own copy of the contract's
// string - got the flat refusal for every message it ever sent.
//
// The inequality is not padding either. A resolve_mailbox payload and a
// create_personal_inbox payload carry the same two identifiers, so if the two
// names were ever made equal the switch would hand a question about an address
// to the code that PROVISIONS one, and the decode would succeed.
func TestResolveMailboxIsItsOwnOperation(t *testing.T) {
	assert.Equal(t, "resolve_mailbox", OperationResolveMailbox)
	assert.NotEqual(t, OperationCreatePersonalInbox, OperationResolveMailbox)
	assert.NotEqual(t, OperationCreateTeamRoots, OperationResolveMailbox)
	assert.NotEqual(t, OperationCreateUser, OperationResolveMailbox)
}

func TestVerifyReadsAWellFormedResolveMailboxRequest(t *testing.T) {
	key := trustedKey(t)

	operation, payload, err := Verify(envelopeOver(key, provisioningPrefix, resolveMailbox))
	require.NoError(t, err)
	assert.Equal(t, OperationResolveMailbox, operation)

	request, err := DecodeResolveMailbox(payload)
	require.NoError(t, err)
	// The member names are the contract's, so the values are asserted against
	// what the payload above puts under each of them: a decoder that had the two
	// identifiers the wrong way round would still return a populated struct.
	assert.Equal(t, "org_3d77e0c15a84", request.OrganizationID)
	assert.Equal(t, "42", request.UserID)
}

func TestDecodeResolveMailboxRefusesWhatThisBuildCannotActOn(t *testing.T) {
	key := trustedKey(t)

	refused := func(t *testing.T, payload string) {
		t.Helper()

		operation, verified, err := Verify(envelopeOver(key, provisioningPrefix, payload))
		require.NoError(t, err)
		require.Equal(t, OperationResolveMailbox, operation)

		_, err = DecodeResolveMailbox(verified)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}

	// The control. Every case below differs from this payload in one member, so
	// a refusal is attributable to that member and to nothing else - and if the
	// decoder started refusing everything, this is what would say so.
	t.Run("control: the well-formed request is accepted", func(t *testing.T) {
		operation, verified, err := Verify(envelopeOver(key, provisioningPrefix, resolveMailbox))
		require.NoError(t, err)
		require.Equal(t, OperationResolveMailbox, operation)

		_, err = DecodeResolveMailbox(verified)
		require.NoError(t, err)
	})

	// The contract's request schema carries no address, deliberately: a request
	// that named a mailbox would be a confirmation oracle for "is this person
	// here" for anyone holding the signing key, which is the exact fact erasure
	// destroys. decodeExactly is what makes that structural rather than a
	// promise - the member is undeclared, so it is REFUSED rather than ignored.
	t.Run("an address in the request, which this operation answers with", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"dana@acme.example",`+
			`"operation":"resolve_mailbox","organization_id":"org_3d77e0c15a84","user_id":"42"}`)
	})

	t.Run("a team id, which belongs to another operation", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"resolve_mailbox",`+
			`"organization_id":"org_3d77e0c15a84","team_id":"team_1","user_id":"42"}`)
	})

	t.Run("a contract version this build does not define", func(t *testing.T) {
		refused(t, `{"contract_version":"2","operation":"resolve_mailbox",`+
			`"organization_id":"org_3d77e0c15a84","user_id":"42"}`)
	})

	t.Run("no subject at all", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"resolve_mailbox",`+
			`"organization_id":"org_3d77e0c15a84","user_id":""}`)
	})

	t.Run("no organization at all", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"resolve_mailbox",`+
			`"organization_id":"","user_id":"42"}`)
	})

	// A subject longer than the varchar(64) columns this instance stores
	// commercial identifiers in. It is refused for the reason commercialID's
	// bound exists: a value past it is one a store could truncate into a
	// DIFFERENT subject.
	t.Run("a subject longer than the column that would store it", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"resolve_mailbox",`+
			`"organization_id":"org_3d77e0c15a84","user_id":"`+strings.Repeat("9", 65)+`"}`)
	})

	// A JSON number rather than the decimal string the contract fixes. This is
	// the single most likely defect on this seam, because a Go or TypeScript
	// producer marshalling an integer emits it without thinking - and it must
	// fail here rather than be coerced, or the two sides disagree about the type
	// of a primary key.
	t.Run("a subject as a JSON number", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"resolve_mailbox",`+
			`"organization_id":"org_3d77e0c15a84","user_id":42}`)
	})
}

func TestVerifyReportsAnOperationThisBuildDoesNotDefine(t *testing.T) {
	key := trustedKey(t)
	payload := `{"contract_version":"1","operation":"delete_everything"}`

	operation, _, err := Verify(envelopeOver(key, provisioningPrefix, payload))
	require.NoError(t, err, "an unknown operation is a routing decision, not a bad envelope")
	// The value itself, not merely "not create_user": the endpoint switches on
	// this, and an assertion that only ruled out one case would also pass on an
	// empty string, which is what a decode that silently dropped the member
	// would produce.
	assert.Equal(t, "delete_everything", operation)
}

// createUserWithPassword is a well-formed create_user_with_password payload
// (BRA-1335) in canonical JSON: members sorted by key.
const createUserWithPassword = `{"contract_version":"1","email":"someone@example.com",` +
	`"operation":"create_user_with_password","password":"a-strong-password","username":"someone"}`

func TestVerifyReadsAWellFormedCreateUserWithPasswordRequest(t *testing.T) {
	key := trustedKey(t)

	operation, payload, err := Verify(envelopeOver(key, provisioningPrefix, createUserWithPassword))
	require.NoError(t, err)
	assert.Equal(t, OperationCreateUserWithPassword, operation)

	request, err := DecodeCreateUserWithPassword(payload)
	require.NoError(t, err)
	assert.Equal(t, "someone@example.com", request.Email)
	assert.Equal(t, "someone", request.Username)
	assert.Equal(t, "a-strong-password", request.Password)
}

// TestCreateUserWithPasswordIsItsOwnOperation pins the operation name and the
// asymmetric relationship with create_user its own comment describes: dropping
// username and password from this payload leaves exactly create_user's shape,
// so a create_user_with_password payload sent with the WRONG operation name
// must decode as create_user's undeclared-member refusal, while a genuine
// create_user payload decodes cleanly under this operation's own decoder
// (its username and password simply read as empty, which
// DecodeCreateUserWithPassword's own format check then refuses on a different
// ground - see the refusal table below).
func TestCreateUserWithPasswordIsItsOwnOperation(t *testing.T) {
	assert.Equal(t, "create_user_with_password", OperationCreateUserWithPassword)
	assert.NotEqual(t, OperationCreateUser, OperationCreateUserWithPassword)

	key := trustedKey(t)

	t.Run("a create_user_with_password payload does not decode as create_user", func(t *testing.T) {
		operation, verified, err := Verify(envelopeOver(key, provisioningPrefix, createUserWithPassword))
		require.NoError(t, err)
		require.Equal(t, OperationCreateUserWithPassword, operation)

		_, err = DecodeCreateUser(verified)
		require.ErrorIs(t, err, ErrInvalidRequest,
			"username and password are undeclared on CreateUser, so decodeExactly must refuse rather than ignore them")
	})
}

func TestDecodeCreateUserWithPasswordRefusesWhatThisBuildCannotActOn(t *testing.T) {
	key := trustedKey(t)

	refused := func(t *testing.T, payload string) {
		t.Helper()

		operation, verified, err := Verify(envelopeOver(key, provisioningPrefix, payload))
		require.NoError(t, err)
		require.Equal(t, OperationCreateUserWithPassword, operation)

		_, err = DecodeCreateUserWithPassword(verified)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}

	// The control, for the reason every sibling's own control exists: every
	// case below differs from this payload in one member, so a refusal is
	// attributable to that member and to nothing else.
	t.Run("control: the well-formed request is accepted", func(t *testing.T) {
		operation, verified, err := Verify(envelopeOver(key, provisioningPrefix, createUserWithPassword))
		require.NoError(t, err)
		require.Equal(t, OperationCreateUserWithPassword, operation)

		_, err = DecodeCreateUserWithPassword(verified)
		require.NoError(t, err)
	})

	t.Run("a contract version this build does not define", func(t *testing.T) {
		refused(t, `{"contract_version":"2","email":"someone@example.com",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"someone"}`)
	})

	t.Run("an undeclared member, which a later contract carries meaning in", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com","organization_id":"org_1",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"someone"}`)
	})

	t.Run("an address that is not a mailbox", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"someone"}`)
	})

	t.Run("no address at all", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"someone"}`)
	})

	t.Run("an address longer than the column that would store it", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"`+strings.Repeat("a", 245)+`@example.com",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"someone"}`)
	})

	// user.CheckUsernameFormat's own rules, reused rather than reimplemented -
	// this is the assertion that they are actually WIRED UP, not a second copy
	// of what user_create_test.go already proves about the rule itself.
	t.Run("no username at all", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":""}`)
	})

	t.Run("a username with a space in it", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"a name"}`)
	})

	t.Run("a username matching the reserved link-share pattern", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com",`+
			`"operation":"create_user_with_password","password":"a-strong-password","username":"link-share-42"}`)
	})

	t.Run("a username longer than the column that would store it", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com","operation":"create_user_with_password",`+
			`"password":"a-strong-password","username":"`+strings.Repeat("a", 251)+`"}`)
	})

	// minPasswordBytes and maxPasswordBytes, quoted from pkg/user/validator.go's
	// own "bcrypt_password" rule rather than a bound this package invented.
	t.Run("a password shorter than every other password this fork accepts", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com",`+
			`"operation":"create_user_with_password","password":"short","username":"someone"}`)
	})

	t.Run("a password past bcrypt's own 72-byte limit", func(t *testing.T) {
		refused(t, `{"contract_version":"1","email":"someone@example.com",`+
			`"operation":"create_user_with_password","password":"`+strings.Repeat("a", 73)+`","username":"someone"}`)
	})
}
