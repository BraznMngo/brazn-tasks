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

// contractPrefix is written out here rather than taken from SigningDomain.
//
// Every test that signs uses this literal, so what they assert is conformance
// to the contract and not agreement with our own constant. A suite that builds
// its input from the code under test can only prove the code agrees with
// itself, which is exactly how the wrong signing input came to be written.
const contractPrefix = "percy.entitlement-projection.v2\n"

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

// contractSignature signs a payload the way the contract says to.
func contractSignature(private ed25519.PrivateKey, signed []byte) []byte {
	return ed25519.Sign(private, append([]byte(contractPrefix), signed...))
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

// envelopedSigned returns the signed octets as they appear inside an envelope,
// which is what Verify will check - and not necessarily what was handed to
// envelopeWith.
//
// json.Marshal compacts a json.RawMessage on the way out, so a difference that
// is only whitespace is erased before it ever reaches the verifier. A
// byte-level test that asserts on its own intermediate buffer rather than on
// this is asserting about bytes nothing will ever look at.
func envelopedSigned(t *testing.T, envelope []byte) []byte {
	t.Helper()

	var parsed struct {
		Signed json.RawMessage `json:"signed"`
	}
	require.NoError(t, json.Unmarshal(envelope, &parsed))
	return parsed.Signed
}

// TestSigningDomainMatchesTheContract pins the constant against the literal.
func TestSigningDomainMatchesTheContract(t *testing.T) {
	assert.Equal(t, contractPrefix, SigningDomain)
	assert.Len(t, SigningDomain, 32, "31 characters plus the 0x0A the contract specifies")
	assert.Equal(t, byte(0x0A), SigningDomain[len(SigningDomain)-1])
}

func TestVerifyAcceptsAContractSignature(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)

	got, err := Verify(envelopeWith(t, testKeyID, signed, contractSignature(private, signed)))
	require.NoError(t, err)
	assert.Equal(t, EditionPersonal, got.State.Edition)
	assert.Equal(t, int64(1), got.Revision)
	assert.True(t, got.Active())
}

// TestVerifyRejectsASignatureWithoutTheDomainPrefix is the property the prefix
// buys: without it a projection is nothing more than "some JSON this key
// signed", so every Ed25519 signature the key has ever produced over any other
// document becomes a candidate projection.
//
// The control is the point of the shape. "Returns an error" is a weak claim on
// its own - a malformed envelope or an untrusted key would satisfy it just as
// well - so an otherwise identical envelope is verified first, leaving the
// prefix as the only difference between the two outcomes.
func TestVerifyRejectsASignatureWithoutTheDomainPrefix(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)

	_, err := Verify(envelopeWith(t, testKeyID, signed, contractSignature(private, signed)))
	require.NoError(t, err, "control: the same envelope, prefixed, must verify")

	_, err = Verify(envelopeWith(t, testKeyID, signed, ed25519.Sign(private, signed)))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsASignatureFromAnotherDomain shows the separation is exact
// rather than approximate: a neighbouring version of the same contract is a
// different domain and does not carry over.
func TestVerifyRejectsASignatureFromAnotherDomain(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)

	_, err := Verify(envelopeWith(t, testKeyID, signed, contractSignature(private, signed)))
	require.NoError(t, err, "control: the same envelope, in the right domain, must verify")

	otherDomain := ed25519.Sign(private, append([]byte("percy.entitlement-projection.v1\n"), signed...))
	_, err = Verify(envelopeWith(t, testKeyID, signed, otherDomain))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsReformattedJSON records a deliberate consequence rather than
// a limitation: verification covers the octets as received, never a canonical
// form re-derived here, so an intermediary that reformats the signed member
// breaks it. Re-deriving the canonical form would put this one JCS
// implementation difference away from accepting bytes nobody signed.
//
// The reformat has to reorder keys, and the first version of this test found
// that out the hard way by using whitespace. json.Marshal compacts a
// json.RawMessage when the envelope is built, so the indented payload reached
// the verifier byte-identical to the original and verified correctly. The test
// asserted that its own intermediate buffer had changed - which was true, and
// said nothing about what the verifier saw. The difference is now asserted
// where it decides the outcome: on the octets inside the envelope.
func TestVerifyRejectsReformattedJSON(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	signature := contractSignature(private, signed)

	_, err := Verify(envelopeWith(t, testKeyID, signed, signature))
	require.NoError(t, err, "control: the unaltered payload must verify under this signature")

	var generic map[string]interface{}
	require.NoError(t, json.Unmarshal(signed, &generic))
	reformatted, err := json.Marshal(generic)
	require.NoError(t, err)

	envelope := envelopeWith(t, testKeyID, reformatted, signature)
	require.NotEqual(t, string(signed), string(envelopedSigned(t, envelope)),
		"the payload must really differ inside the envelope, or this proves nothing")

	_, err = Verify(envelope)
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsAnUnknownSigningKey needs no control: it asserts a sentinel
// no other failure returns.
func TestVerifyRejectsAnUnknownSigningKey(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)

	_, err := Verify(envelopeWith(t, "entproj-never-configured", signed, contractSignature(private, signed)))
	require.ErrorIs(t, err, ErrUnknownSigningKey)
}

// TestVerifyRejectsAnotherContractVersion keeps a projection from a newer
// contract from being guessed at rather than refused. Both envelopes are
// correctly signed, so the version is the only difference between them.
func TestVerifyRejectsAnotherContractVersion(t *testing.T) {
	private := trustedKey(t)

	accepted := signedPayload(t, ContractVersion)
	_, err := Verify(envelopeWith(t, testKeyID, accepted, contractSignature(private, accepted)))
	require.NoError(t, err, "control: the same payload at the accepted version must verify")

	newer := signedPayload(t, "3")
	_, err = Verify(envelopeWith(t, testKeyID, newer, contractSignature(private, newer)))
	require.ErrorIs(t, err, ErrInvalidProjection)
}

// TestVerifyRejectsATamperedPayload covers the ordinary case a signature exists
// for at all. The edited bytes are checked inside the envelope, for the same
// reason the reformat test checks them there.
func TestVerifyRejectsATamperedPayload(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	signature := contractSignature(private, signed)

	_, err := Verify(envelopeWith(t, testKeyID, signed, signature))
	require.NoError(t, err, "control: the unaltered payload must verify under this signature")

	tampered := bytes.Replace(signed, []byte(EditionPersonal), []byte(EditionTeams), 1)
	envelope := envelopeWith(t, testKeyID, tampered, signature)
	require.NotEqual(t, string(signed), string(envelopedSigned(t, envelope)),
		"the edit must survive into the envelope, or this proves nothing")

	_, err = Verify(envelope)
	require.ErrorIs(t, err, ErrInvalidProjection)
}
