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
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"code.vikunja.io/api/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The frozen golden artifact set, vendored from the Percy repository. See
// testdata/golden/README.md for what it is and why a copy of it lives here.
//
// Constants rather than a path built at call time, deliberately: this is the
// form gosec resolves, and it is the one already used for the route
// classification fixture elsewhere in this repository.
const (
	goldenKeyIDPath      = "testdata/golden/key-id.txt"
	goldenPublicKeyPath  = "testdata/golden/signing-key.pub.pem"
	goldenVerifiesPath   = "testdata/golden/projection.verifies.json"
	goldenUnprefixedPath = "testdata/golden/projection.rejects.unprefixed-signature.json"
	goldenPaddedPath     = "testdata/golden/projection.rejects.padded-signature.json"
)

// goldenSet is the three envelopes of the frozen set. The key material is not
// here because it is not asserted on - it is loaded straight into the trust
// store by loadGolden.
type goldenSet struct {
	verifies   []byte
	unprefixed []byte
	padded     []byte
}

// loadGolden reads the frozen set and configures this instance to trust exactly
// the key it was signed under.
//
// THE KEY ID COMES FROM ITS OWN FILE, never from the envelope being checked.
// That separation is the whole reason the contract ships key-id.txt rather than
// leaving a test to read signature.key_id out of the message: a verifier that
// learned which key to trust from the message it is verifying would trust
// whatever the message named, which is not a trust store.
func loadGolden(t *testing.T) goldenSet {
	t.Helper()

	config.InitDefaultConfig()

	keyID, err := os.ReadFile(goldenKeyIDPath)
	require.NoError(t, err)

	encodedKey, err := os.ReadFile(goldenPublicKeyPath)
	require.NoError(t, err)

	block, _ := pem.Decode(encodedKey)
	require.NotNil(t, block, "signing-key.pub.pem must be a PEM block")

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	require.NoError(t, err)

	public, ok := parsed.(ed25519.PublicKey)
	require.True(t, ok, "the frozen signing key must be Ed25519")

	// brazn.entitlementkeys is our own config format and carries STANDARD
	// base64, which is deliberately not the contract's signature encoding. The
	// two are unrelated and must not be unified; see Verify.
	config.BraznEntitlementKeys.Set(
		strings.TrimSpace(string(keyID)) + ":" + base64.StdEncoding.EncodeToString(public),
	)

	set := goldenSet{}
	set.verifies, err = os.ReadFile(goldenVerifiesPath)
	require.NoError(t, err)
	set.unprefixed, err = os.ReadFile(goldenUnprefixedPath)
	require.NoError(t, err)
	set.padded, err = os.ReadFile(goldenPaddedPath)
	require.NoError(t, err)

	return set
}

// TestGoldenProjectionVerifies is the one assertion in this package that is not
// a conversation with ourselves.
//
// Every other test here mints its own keypair and signs with Go's ed25519, so
// what it proves is that this code agrees with this code. These bytes were
// produced by the actual TypeScript producer, under a key whose private half
// was destroyed with the runner that made it, and they are checked through
// Verify and SigningInput exactly as a delivery would be - nothing in this file
// rebuilds the signed octets from a literal of its own, because a test that
// composed its own input would prove nothing about what production composes.
// That was the specific gap Percy's Gate 2 caught in the equivalent test on
// their side.
//
// This failing means this build cannot accept a real projection, whatever the
// rest of the suite says. It has been true twice: verification omitted the
// domain-separation prefix, and then decoded the signature as padded base64.
// Either alone meant no conforming message decoded at all, and both halves were
// green throughout, because each was testing against its own constant.
func TestGoldenProjectionVerifies(t *testing.T) {
	golden := loadGolden(t)

	signed, err := Verify(golden.verifies)
	require.NoError(t, err,
		"the frozen positive artifact must verify, or this build cannot accept a real projection")

	assert.Equal(t, ContractVersion, signed.ContractVersion)
	assert.Equal(t, "org_9f2c41ab7d30", signed.Subject.OrganizationID)
	assert.Equal(t, "1", signed.Subject.UserID)
	assert.Equal(t, int64(1), signed.Revision)
	assert.Equal(t, EditionPersonal, signed.State.Edition)
	assert.True(t, signed.Active())
}

// TestGoldenUnprefixedSignatureIsRejected is the frozen regression for the
// first shipped bug: a real signature over the signed member alone, with no
// domain prefix.
//
// REMOVING SigningDomain FROM SigningInput BREAKS THIS TEST FROM BOTH ENDS, and
// that symmetry is the point of keeping the positive here as a control. The
// negative would start verifying, because its signature covers exactly the
// unprefixed octets a stripped composer would hand to ed25519.Verify. The
// positive would simultaneously stop verifying, because its signature covers
// the prefixed ones. No single-sided mistake produces that pair, so neither
// assertion can be satisfied by accident.
//
// TestVerifyRejectsASignatureWithoutTheDomainPrefix makes the same claim
// against a locally minted signature. It is not redundant with this one and
// neither replaces the other: that test proves the rule is applied
// consistently, this one proves the rule is the one the producer implemented.
func TestGoldenUnprefixedSignatureIsRejected(t *testing.T) {
	golden := loadGolden(t)

	_, err := Verify(golden.verifies)
	require.NoError(t, err, "control: the frozen positive artifact must verify under this trust store")

	_, err = Verify(golden.unprefixed)
	require.ErrorIs(t, err, ErrInvalidProjection,
		"a signature with no domain prefix must not verify: without the prefix a projection is "+
			"merely some JSON this key signed")
}

// TestGoldenPaddedSignatureIsRejected is the frozen regression for the second
// shipped bug, from the other direction.
//
// The artifact carries the SAME signature octets as the positive, written down
// in the padded encoding the contract forbids - so nothing about the
// cryptography differs between the two files and only the encoding decides the
// outcome.
//
// TWO DIFFERENT MUTATIONS BREAK THIS TEST, at two different assertions, and
// which one is which is worth stating because the value here is 86 base64url
// characters containing "_" and no "-":
//
//   - Tolerating padding - base64.URLEncoding, or a decoder that falls back to
//     the padded form - makes the padded artifact decode to those same 64
//     octets, so it verifies and the assertion below fails. That is the
//     relaxation this test exists to stop.
//   - Restoring base64.StdEncoding, which is the bug as it actually shipped,
//     fails at the CONTROL instead: "_" is not in the standard alphabet and 86
//     is not a multiple of four, so the positive stops decoding. The padded
//     artifact would still be refused, but for the alphabet rather than for the
//     padding - the assertion below would pass for the wrong reason, and only
//     the control catches it.
//
// So the control is not decoration. Without it the second mutation restores a
// build that can accept no real projection at all, and this test stays green.
func TestGoldenPaddedSignatureIsRejected(t *testing.T) {
	golden := loadGolden(t)

	_, err := Verify(golden.verifies)
	require.NoError(t, err, "control: the frozen positive artifact must verify under this trust store")

	_, err = Verify(golden.padded)
	require.ErrorIs(t, err, ErrInvalidProjection,
		"one signature must have exactly one encoding, so the padded form must not verify")
}
