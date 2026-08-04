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

package entitlement

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The contract's optional `state.write_access` (BRA-1023 produces it, BRA-1087
// honours it), tested at the level where the fail-closed decision is actually
// made.

// contractWriteAccessValues are the two the v2 schema's enum declares, written
// out here rather than taken from the constants they pin. A test that compared
// the constants against themselves would agree with itself whatever they said,
// which is the failure mode this file exists downstream of.
const (
	contractWriteAccessFull         = "full"
	contractWriteAccessSettingsOnly = "settings_only"
)

func TestWriteAccessConstantsMatchTheContract(t *testing.T) {
	assert.Equal(t, contractWriteAccessFull, WriteAccessFull)
	assert.Equal(t, contractWriteAccessSettingsOnly, WriteAccessSettingsOnly)
}

func writeAccessPtr(value string) *string {
	return &value
}

// TestWriteRestrictedFailsClosedOnAnythingItDoesNotKnow is the ticket's central
// question, and the assertion that decides whether this build is correct.
//
// The two ends are easy. The middle rows are the ones that matter: a value from
// a contract version this build has never seen must RESTRICT, because reading
// it as `full` would hand full write access to an account a newer producer had
// just restricted - silently, and on exactly the accounts a future restriction
// would exist to catch.
//
// The empty string is here as its own row because it is the case a value type
// could not have expressed. With a plain string field, "the producer sent
// nothing" and "the producer sent an empty string" would both arrive as "" and
// only one of them is a projection a conforming producer emits.
func TestWriteRestrictedFailsClosedOnAnythingItDoesNotKnow(t *testing.T) {
	cases := []struct {
		name       string
		value      *string
		restricted bool
		why        string
	}{
		{
			name:  "absent",
			value: nil,
			why:   "absence means full - it is what every projection minted before the member existed carries",
		},
		{
			name:  "an explicit full",
			value: writeAccessPtr(contractWriteAccessFull),
			why:   "the value the contract defines for an ordinary subject",
		},
		{
			name:       "settings_only",
			value:      writeAccessPtr(contractWriteAccessSettingsOnly),
			restricted: true,
			why:        "the state this whole mechanism exists for",
		},
		{
			name:       "a value from a newer contract",
			value:      writeAccessPtr("read_only_hard"),
			restricted: true,
			why:        "reading an unknown value as full would silently unblock an account a newer producer restricted",
		},
		{
			name:       "an empty string",
			value:      writeAccessPtr(""),
			restricted: true,
			why:        "a conforming producer never sends this, so it is a value this build cannot follow",
		},
		{
			name:       "a value differing only in case",
			value:      writeAccessPtr("Full"),
			restricted: true,
			why:        "the enum is exact; a near-miss is not a permit",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			signed := &Signed{State: State{WriteAccess: c.value}}
			assert.Equal(t, c.restricted, signed.WriteRestricted(), c.why)
		})
	}
}

// TestAbsentWriteAccessDoesNotChangeTheSignedBytes is what makes this member
// additive rather than a breaking change, and it is asserted rather than assumed
// because getting it wrong would break every projection already signed.
//
// A signature covers the signed member as received, byte for byte, and this
// instance holds a FROZEN GOLDEN CORPUS minted before the member existed. If the
// struct emitted `"write_access":null` or `"write_access":""` for a subject that
// carries none, every one of those envelopes would still verify - they are not
// re-marshalled - but anything this side produced or compared against them
// would no longer agree, and the contract's own claim that no existing
// projection's bytes move would be false.
func TestAbsentWriteAccessDoesNotChangeTheSignedBytes(t *testing.T) {
	raw, err := json.Marshal(State{
		Edition:        EditionPersonal,
		SeatStatus:     "active",
		EffectiveState: "active",
	})
	require.NoError(t, err)

	assert.NotContains(t, string(raw), "write_access",
		"a subject carrying no write_access must serialise without the member at all")

	// And the pair, so the assertion above cannot pass by the member simply
	// never working.
	withValue, err := json.Marshal(State{
		Edition:        EditionPersonal,
		SeatStatus:     "active",
		EffectiveState: "active",
		WriteAccess:    writeAccessPtr(contractWriteAccessSettingsOnly),
	})
	require.NoError(t, err)
	assert.Contains(t, string(withValue), `"write_access":"settings_only"`)
}

// signedPayloadWithWriteAccess builds a signed half carrying a raw write_access
// value, spliced in as TEXT rather than through the struct.
//
// That is deliberate and it is what makes the Verify tests below hostile enough:
// marshalling a Go struct can only ever produce a member this build already
// declares, so it could not express the case that matters - bytes arriving from
// a producer whose contract this build does not share.
func signedPayloadWithWriteAccess(t *testing.T, rawValue string) []byte {
	t.Helper()

	return []byte(fmt.Sprintf(`{`+
		`"contract_version":%q,`+
		`"subject":{"organization_id":"org_9f2c41ab7d30","user_id":"usr_5b1e8c04a927"},`+
		`"revision":1,`+
		`"issued_at":%q,`+
		`"state":{"edition":"personal-cloud","seat_status":"active",`+
		`"organization_admin":false,"effective_state":"active",`+
		`"write_access":%s,`+
		`"valid_from":"2026-01-01T00:00:00Z","valid_to":null}}`,
		ContractVersion, time.Now().UTC().Format(time.RFC3339), rawValue))
}

// TestVerifyAcceptsADeclaredWriteAccess is the assertion the whole two-repo
// sequencing rests on.
//
// Before this build, `state.write_access` was an UNDECLARED member, and Verify
// refuses those: hasUndeclaredField runs a strict decode at every object level,
// so a projection carrying it was rejected whole. That is why the producer keeps
// the member behind a feature switch until a build declaring it has shipped, and
// this is the test that says this build is that one.
func TestVerifyAcceptsADeclaredWriteAccess(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayloadWithWriteAccess(t, `"settings_only"`)

	got, err := Verify(envelopeWith(t, testKeyID, signed, contractSignature(private, signed)))
	require.NoError(t, err, "write_access is a declared member of this contract version")

	require.NotNil(t, got.State.WriteAccess)
	assert.Equal(t, contractWriteAccessSettingsOnly, *got.State.WriteAccess)
	assert.True(t, got.WriteRestricted())
	assert.True(t, got.Active(),
		"the subject stays ENTITLED - write_access is orthogonal to effective_state")
}

// TestVerifyAcceptsAnUnrecognisedWriteAccessValue pins the choice that the
// refusal happens at ENFORCEMENT and not here, which is the opposite of what
// Verify does for an unknown FIELD and is deliberate.
//
// Refusing the projection would leave the LAST GOOD one in force, and the last
// good one is by definition the less restricted one - so rejecting here would be
// fail-open in effect while looking like fail-closed. The message is
// structurally valid; only its meaning is unfollowable, so it is accepted and
// then read as strictly as possible.
func TestVerifyAcceptsAnUnrecognisedWriteAccessValue(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayloadWithWriteAccess(t, `"quarantined"`)

	got, err := Verify(envelopeWith(t, testKeyID, signed, contractSignature(private, signed)))
	require.NoError(t, err,
		"an unknown VALUE of a declared member is structurally valid and must not be refused here")
	assert.True(t, got.WriteRestricted(),
		"it must instead be refused at enforcement, as the strictest reading of a value we cannot follow")
}

// TestVerifyStillRefusesAnUndeclaredFieldInsideState is the guard on the guard.
//
// Declaring one new member inside `state` must not have loosened the strict
// decode that refuses every other one. The contract has a conformance case for
// exactly this - invalid.billing-detail-in-state - and it is billing detail
// this instance has no business holding arriving inside a message it would
// otherwise accept.
func TestVerifyStillRefusesAnUndeclaredFieldInsideState(t *testing.T) {
	private := trustedKey(t)
	signed := []byte(fmt.Sprintf(`{`+
		`"contract_version":%q,`+
		`"subject":{"organization_id":"org_9f2c41ab7d30","user_id":"usr_5b1e8c04a927"},`+
		`"revision":1,`+
		`"issued_at":%q,`+
		`"state":{"edition":"personal-cloud","seat_status":"active",`+
		`"organization_admin":false,"effective_state":"active",`+
		`"write_access":"settings_only","invoice_id":"in_123",`+
		`"valid_from":"2026-01-01T00:00:00Z","valid_to":null}}`,
		ContractVersion, time.Now().UTC().Format(time.RFC3339)))

	_, err := Verify(envelopeWith(t, testKeyID, signed, contractSignature(private, signed)))
	require.Error(t, err)
	assert.Equal(t, ReasonUndeclaredField, RefusalReason(err),
		"the refusal must be for the undeclared member, not for something incidental")
}

// TestForTokenCarriesTheWriteRestriction is the seam between the projection and
// the session token, which is the only thing enforcement reads afterwards.
func TestForTokenCarriesTheWriteRestriction(t *testing.T) {
	now := time.Now().UTC()
	live := State{
		Edition:        EditionPersonal,
		SeatStatus:     "active",
		EffectiveState: "active",
		ValidFrom:      now.Add(-time.Hour),
	}

	t.Run("a restricted subject", func(t *testing.T) {
		state := live
		state.WriteAccess = writeAccessPtr(contractWriteAccessSettingsOnly)

		got := (&Signed{State: state}).ForToken(now, 0)
		require.NotNil(t, got)
		assert.True(t, got.WriteRestricted)
		assert.Equal(t, EditionPersonal, got.Edition,
			"a restricted subject is still entitled and still carries its edition")
	})

	t.Run("an ordinary subject", func(t *testing.T) {
		got := (&Signed{State: live}).ForToken(now, 0)
		require.NotNil(t, got)
		assert.False(t, got.WriteRestricted)
	})

	// The AC5 trap at the level it originates. A cancelled subject past their
	// paid period is not active, so nothing is minted for them at all - and
	// nothing must be, because they owe nothing and a restriction keyed on
	// "not active" would eventually catch every cancelled customer there is.
	t.Run("a subject whose entitlement is not in force", func(t *testing.T) {
		state := live
		state.EffectiveState = "closed"
		state.WriteAccess = writeAccessPtr(contractWriteAccessSettingsOnly)

		assert.Nil(t, (&Signed{State: state}).ForToken(now, 0),
			"no entitlement means no claim of any kind, restriction included")
	})
}
