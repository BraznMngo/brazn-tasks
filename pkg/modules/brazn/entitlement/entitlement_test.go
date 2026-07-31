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
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"code.vikunja.io/api/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKeyID = "entproj-test"

// trustedKey generates a key pair and configures the instance to trust it.
func trustedKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	config.InitDefaultConfig()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	config.BraznEntitlementKeys.Set(testKeyID + ":" + base64.StdEncoding.EncodeToString(public))
	return private
}

// signedPayload builds the signed half of an envelope, shaped like the example
// in the contract.
func signedPayload(t *testing.T, contractVersion string) []byte {
	t.Helper()

	raw, err := json.Marshal(Signed{
		ContractVersion: contractVersion,
		Subject: Subject{
			OrganizationID: "org_9f2c41ab7d30",
			UserID:         "usr_5b1e8c04a927",
		},
		Revision: 1,
		IssuedAt: time.Now().UTC(),
		State: State{
			Edition:           EditionPersonal,
			SeatStatus:        "active",
			OrganizationAdmin: true,
			EffectiveState:    "active",
		},
	})
	require.NoError(t, err)
	return raw
}

func envelopeWith(t *testing.T, keyID string, signed, signature []byte) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]interface{}{
		"signed": json.RawMessage(signed),
		"signature": map[string]string{
			"key_id":    keyID,
			"algorithm": "ed25519",
			"value":     base64.StdEncoding.EncodeToString(signature),
		},
	})
	require.NoError(t, err)
	return raw
}

// TestSigningDomainMatchesTheContract pins the prefix against a literal written
// here, independently of the constant.
//
// If the two were the same expression this would only assert that the package
// agrees with itself, which is precisely the failure being corrected: the
// verifier agreed with itself perfectly while agreeing with nothing the
// commercial service produces.
func TestSigningDomainMatchesTheContract(t *testing.T) {
	assert.Equal(t, "percy.entitlement-projection.v2\n", SigningDomain)
	assert.Len(t, SigningDomain, 32, "31 characters plus the 0x0A the contract specifies")
	assert.Equal(t, byte(0x0A), SigningDomain[len(SigningDomain)-1])
}

func TestVerifyAcceptsAContractSignature(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)

	// Built from the literal rather than from SigningInput, so this asserts
	// conformance to the contract and not agreement with our own helper.
	input := append([]byte("percy.entitlement-projection.v2\n"), signed...)

	got, err := Verify(envelopeWith(t, testKeyID, signed, ed25519.Sign(private, input)))
	require.NoError(t, err)
	assert.Equal(t, EditionPersonal, got.State.Edition)
	assert.Equal(t, int64(1), got.Revision)
	assert.True(t, got.Active())
}

// TestVerifyRejectsASignatureWithoutTheDomainPrefix is the property the prefix
// buys, and the reason it is not decoration.
//
// Without it a projection is nothing more than "some JSON this key signed", so
// every Ed25519 signature the signing key has ever produced over any other
// document becomes a candidate projection - the signer's unrelated work turns
// into this verifier's attack surface.
func TestVerifyRejectsASignatureWithoutTheDomainPrefix(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)

	_, err := Verify(envelopeWith(t, testKeyID, signed, ed25519.Sign(private, signed)))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsASignatureFromAnotherDomain shows the separation is exact
// rather than approximate: a neighbouring version of the same contract is a
// different domain and does not carry over.
func TestVerifyRejectsASignatureFromAnotherDomain(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	input := append([]byte("percy.entitlement-projection.v1\n"), signed...)

	_, err := Verify(envelopeWith(t, testKeyID, signed, ed25519.Sign(private, input)))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsReformattedJSON records a deliberate consequence rather than
// a limitation.
//
// Verification covers the octets as received, never a canonical form re-derived
// here, so an intermediary that reformats the signed member breaks it. That is
// the intended failure: re-deriving the canonical form would put this one JCS
// implementation difference away from accepting bytes nobody signed.
func TestVerifyRejectsReformattedJSON(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	signature := ed25519.Sign(private, SigningInput(signed))

	var reformatted bytes.Buffer
	require.NoError(t, json.Indent(&reformatted, signed, "", "  "))
	require.NotEqual(t, signed, reformatted.Bytes())

	_, err := Verify(envelopeWith(t, testKeyID, reformatted.Bytes(), signature))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

func TestVerifyRejectsAnUnknownSigningKey(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	signature := ed25519.Sign(private, SigningInput(signed))

	_, err := Verify(envelopeWith(t, "entproj-never-configured", signed, signature))
	require.ErrorIs(t, err, ErrUnknownSigningKey)
}

// TestVerifyRejectsAnotherContractVersion keeps a projection from a newer
// contract from being guessed at rather than refused.
func TestVerifyRejectsAnotherContractVersion(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, "3")
	signature := ed25519.Sign(private, SigningInput(signed))

	_, err := Verify(envelopeWith(t, testKeyID, signed, signature))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsATamperedPayload covers the ordinary case the signature is
// there for at all.
func TestVerifyRejectsATamperedPayload(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	signature := ed25519.Sign(private, SigningInput(signed))

	tampered := bytes.Replace(signed, []byte(EditionPersonal), []byte(EditionTeams), 1)
	require.NotEqual(t, signed, tampered)

	_, err := Verify(envelopeWith(t, testKeyID, tampered, signature))
	require.ErrorIs(t, err, ErrInvalidProjection)
}
