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
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The v2 entitlement conformance corpus, run through the production verifier
// (BRA-929).
//
// WHAT THIS IS FOR. The golden set next door proves this build agrees with the
// producer about real bytes. It cannot prove the build agrees with the contract
// about rules, because it is three envelopes and the rules are a dozen. This
// file is the other half: one case per conformance case the contract defines,
// each asserting WHICH rule refused it rather than that something did.
//
// WHY THE REASON AND NOT JUST THE REFUSAL. Every content rule below sits behind
// the signature check, so any case whose setup is subtly wrong - untrusted key,
// signature over the wrong octets, a fixture that never really carried the
// fault - is refused before its own rule is ever consulted. A test asserting
// only "an error came back" reports success for all of them. That is the third
// false-passing shape in this repository's CLAUDE.md, and asserting the named
// reason is the thing that defeats it: a refusal from the wrong rule now fails
// the assertion instead of satisfying it.
//
// WHERE THE CASES COME FROM, AND THE ONE THING TO KNOW BEFORE CHANGING THIS.
// The canonical corpus is cloud/contracts/v2/entitlements/examples/ in the
// private Percy repository. It is NOT vendored here - only the five golden
// artifacts under testdata/golden/ are, and those are a different, smaller set
// with a different job. The signed members below are transcriptions of the
// example files named in each case, and a reviewer should diff them against the
// contract.
//
// THE EXAMPLES CANNOT BE FED TO THIS VERIFIER AS FILES, and that is not only
// because the repository is private. Their signature.value fields are
// hand-written placeholders - 86 characters of the right alphabet, signed by no
// key that exists. Handed to Verify, all eleven are refused, the four valid ones
// included, every one of them for an unknown key. So a check that read those
// files and asserted "valid accepted, invalid refused" could never pass, and one
// that asserted only "invalid refused" would pass while proving nothing at all.
// The signatures here are therefore minted at test time over the contract's own
// signed members, which is what leaves each case's own rule as the only thing
// deciding its outcome.
//
// ONE EXAMPLE IS DELIBERATELY NOT TRANSCRIBED, and the omission is the point
// rather than an oversight. `entitlement-projection.invalid.state-without-
// validity.json` is invalid because the SCHEMA requires `valid_from` and
// `valid_to`, and Verify does not enforce field presence - it enforces the
// rules the contract names a conformance case for. Requiring them here would
// refuse every envelope stored before those members existed, and the frozen
// golden set with them, for a defect only a producer can commit. So the case
// belongs to the schema gate in Percy's CI and not to this table; adding it
// here would assert a refusal this verifier must not make.
//
// STILL OPEN. Acceptance criteria 3 and 4 of BRA-929 - a new example failing CI,
// and the corpus arriving without a private-repo credential - need the corpus to
// be readable from public CI. BRA-827's contract host is the named exit; until
// then a case added in Percy has to be added here by hand.

// corpusCase is one conformance case from the contract's example corpus.
type corpusCase struct {
	// file names the case in cloud/contracts/v2/entitlements/examples/.
	file string

	// signed is that file's signed member, transcribed. Key order follows the
	// example rather than the producer's canonical sort, which is deliberate:
	// verification covers the octets as received and must not care.
	signed string

	// signature builds the envelope's signature member around the signed
	// octets. nil means the conforming one; a func returning "" means the
	// envelope carries no signature member at all.
	signature func(private ed25519.PrivateKey, signed []byte) string

	// reason is the rule Verify must name. Empty means the case must verify.
	reason Reason

	// edition and active are what an accepted case must decode to.
	edition string
	active  bool
}

// corpusSigned compacts a transcribed example into the octets a producer would
// actually put on the wire, so the signature covers what the envelope carries.
func corpusSigned(t *testing.T, transcribed string) []byte {
	t.Helper()

	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, []byte(transcribed)))
	return compact.Bytes()
}

// corpusEnvelope splices the signed octets into an envelope verbatim, the one
// thing a producer must also do: re-serializing between signing and sending
// produces a message that looks right and verifies against nothing.
//
// The parameter is named signatureJSON rather than signature because the latter
// is this package's envelope struct type, and a reader who mistook one for the
// other would misread every case in the table.
func corpusEnvelope(signed []byte, signatureJSON string) []byte {
	if signatureJSON == "" {
		return []byte(`{"signed":` + string(signed) + `}`)
	}
	return []byte(`{"signed":` + string(signed) + `,"signature":` + signatureJSON + `}`)
}

func signatureMember(algorithm, value string) string {
	return `{"key_id":"` + testKeyID + `","algorithm":"` + algorithm + `","value":"` + value + `"}`
}

// conformingSignature is what a correct producer emits: ed25519 over the
// domain-prefixed octets, written as unpadded base64url.
func conformingSignature(private ed25519.PrivateKey, signed []byte) string {
	return signatureMember(algorithmEd25519, contractSignatureValue(contractSignature(private, signed)))
}

// entitlementCorpus is the projection half of the contract's example corpus.
// The acknowledgement and reconciliation families are other consumers' business
// and no verifier reads them.
func entitlementCorpus() []corpusCase {
	return []corpusCase{
		{
			file: "entitlement-projection.valid.personal-organization.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_9f2c41ab7d30", "user_id": "1"},
				"revision": 1,
				"issued_at": "2026-07-31T09:14:22Z",
				"state": {
					"edition": "personal-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-07-31T09:14:22Z",
					"valid_to": null
				}
			}`,
			edition: EditionPersonal,
			active:  true,
		},
		{
			// The end date on the wire. It is `active` here and stays `active`:
			// an entitlement with an end is not an ended one, and reading a
			// future `valid_to` as a closure would end a paid period early.
			// What the date does is cap a session token at issue - see
			// Signed.ForToken - and nothing about verification changes.
			file: "entitlement-projection.valid.cancelled-with-end-date.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_9f2c41ab7d30", "user_id": "1"},
				"revision": 2,
				"issued_at": "2026-08-01T14:03:11Z",
				"state": {
					"edition": "personal-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-07-31T09:14:22Z",
					"valid_to": "2027-07-31T09:14:22Z"
				}
			}`,
			edition: EditionPersonal,
			active:  true,
		},
		{
			file: "entitlement-projection.valid.teams-member.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_3d77e0c15a84", "user_id": "42"},
				"revision": 47,
				"issued_at": "2026-07-31T09:20:05Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": false,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			edition: EditionTeams,
			active:  true,
		},
		{
			// Valid and inactive are not opposites. This one verifies and
			// entitles nothing, which is the distinction Active() exists for.
			file: "entitlement-projection.valid.suspended-organization.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_3d77e0c15a84", "user_id": "7"},
				"revision": 48,
				"issued_at": "2026-07-31T09:41:00Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "suspended",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			edition: EditionTeams,
			active:  false,
		},
		{
			// The other half of the same distinction, from the seat's side
			// rather than the subscription's.
			file: "entitlement-projection.valid.seat-withdrawn.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_3d77e0c15a84", "user_id": "42"},
				"revision": 49,
				"issued_at": "2026-07-31T10:02:37Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "inactive",
					"organization_admin": false,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			edition: EditionTeams,
			active:  false,
		},
		{
			file: "entitlement-projection.invalid.unsigned.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_9f2c41ab7d30", "user_id": "1"},
				"revision": 12,
				"issued_at": "2026-07-31T09:14:22Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			signature: func(_ ed25519.PrivateKey, _ []byte) string { return "" },
			reason:    ReasonUnsigned,
		},
		{
			// The signature here is GENUINE and over the right octets - only
			// the algorithm name is wrong, where the example file carries a
			// placeholder value. That makes the algorithm the single thing
			// deciding the outcome: delete the check and this message verifies.
			file: "entitlement-projection.invalid.unknown-signature-algorithm.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_9f2c41ab7d30", "user_id": "1"},
				"revision": 12,
				"issued_at": "2026-07-31T09:14:22Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			signature: func(private ed25519.PrivateKey, signed []byte) string {
				return signatureMember("none", contractSignatureValue(contractSignature(private, signed)))
			},
			reason: ReasonUnknownSignatureAlgorithm,
		},
		{
			// Same signature octets as a conforming envelope, written in the
			// padded encoding the contract forbids. Nothing cryptographic
			// differs, so tolerating padding makes this verify.
			file: "entitlement-projection.invalid.padded-signature.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_9f2c41ab7d30", "user_id": "1"},
				"revision": 12,
				"issued_at": "2026-07-31T09:14:22Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			signature: func(private ed25519.PrivateKey, signed []byte) string {
				padded := base64.StdEncoding.EncodeToString(contractSignature(private, signed))
				return signatureMember(algorithmEd25519, padded)
			},
			reason: ReasonMalformedSignatureEncoding,
		},
		{
			file: "entitlement-projection.invalid.unsupported-contract-version.json",
			signed: `{
				"contract_version": "1",
				"subject": {"organization_id": "org_3d77e0c15a84", "user_id": "42"},
				"revision": 52,
				"issued_at": "2026-07-31T10:22:13Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": false,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			reason: ReasonUnsupportedContractVersion,
		},
		{
			file: "entitlement-projection.invalid.zero-revision.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_9f2c41ab7d30", "user_id": "1"},
				"revision": 0,
				"issued_at": "2026-07-31T09:14:22Z",
				"state": {
					"edition": "personal-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			reason: ReasonNonPositiveRevision,
		},
		{
			// An email is not an opaque id: it carries "@" and ".", neither of
			// which the contract's character class admits. Until BRA-929 this
			// verified, and the refusal came later from the model layer when
			// the subject failed to resolve to a local user - which is a
			// different claim about a different thing, and says nothing at all
			// about an organization_id.
			file: "entitlement-projection.invalid.email-as-user-id.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_3d77e0c15a84", "user_id": "sebastian@braznmngo.com"},
				"revision": 51,
				"issued_at": "2026-07-31T10:18:09Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": false,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null
				}
			}`,
			reason: ReasonMalformedSubjectID,
		},
		{
			// The case that mattered most and was silently accepted. Go's JSON
			// decoder ignores undeclared members by default, so an invoice id
			// and a price sailed through - and then straight into
			// brazn_entitlement_projections.envelope, where the whole message
			// is retained verbatim. Not "ignored": stored.
			file: "entitlement-projection.invalid.billing-detail-in-state.json",
			signed: `{
				"contract_version": "2",
				"subject": {"organization_id": "org_3d77e0c15a84", "user_id": "7"},
				"revision": 50,
				"issued_at": "2026-07-31T10:15:44Z",
				"state": {
					"edition": "teams-cloud",
					"seat_status": "active",
					"organization_admin": true,
					"effective_state": "active",
					"valid_from": "2026-03-01T00:00:00Z",
					"valid_to": null,
					"invoice_id": "in_2026_07_00931",
					"monthly_price_eur": 90
				}
			}`,
			reason: ReasonUndeclaredField,
		},
	}
}

// TestEntitlementCorpusConformsRuleByRule is the check BRA-929 exists to add.
//
// DELETE-THE-GUARD, for every refusing case: remove the rule the case names
// from Verify and the message verifies instead, so the case fails on
// require.Error. Weaken the rule rather than removing it - tolerate padding,
// accept any contract version, drop DisallowUnknownFields - and it fails the
// same way. Move the refusal to a different rule and it fails on the reason.
//
// The valid cases are the control that stops all of that being satisfied by a
// verifier that simply refuses everything, and they are not decoration: two of
// the four are inactive, so an Active() stuck at true fails here too.
func TestEntitlementCorpusConformsRuleByRule(t *testing.T) {
	for _, c := range entitlementCorpus() {
		t.Run(c.file, func(t *testing.T) {
			private := trustedKey(t)
			signed := corpusSigned(t, c.signed)

			signatureJSON := conformingSignature(private, signed)
			if c.signature != nil {
				signatureJSON = c.signature(private, signed)
			}

			got, err := Verify(corpusEnvelope(signed, signatureJSON))

			if c.reason == "" {
				require.NoError(t, err, "a valid example must be accepted by this build")
				assert.Equal(t, c.edition, got.State.Edition)
				assert.Equal(t, c.active, got.Active(),
					"whether this projection entitles anything is the contract's claim, not ours")
				return
			}

			require.Error(t, err, "an invalid example must be refused")
			assert.Equal(t, c.reason, RefusalReason(err),
				"refused, but by the wrong rule: the case is named for the rule it must break")
			require.ErrorIs(t, err, ErrInvalidProjection,
				"every refusal must still reach callers as the sentinel they match on")
		})
	}
}

// TestEveryContractRuleKeepsACaseOfItsOwn stops the corpus being quietly
// narrowed.
//
// BE CLEAR ABOUT WHAT THIS CAN AND CANNOT DO. Go cannot enumerate a const
// group, so the list below is hand-maintained and a reason added to Verify
// without a case here will NOT fail it - that stays a review obligation. What
// it does catch is the cheaper and likelier mistake: a case deleted, or its
// expected reason edited to whatever the verifier happened to return, which is
// the ordinary way a red conformance test becomes a green one without the bug
// being fixed. Both leave a rule with no case and both fail here.
//
// The four seeded reasons are refusals no example file describes: three are
// about input that is not a projection at all, and the key-trust failure is
// about this instance's configuration rather than about the message. Each is
// covered by its own test in entitlement_test.go or golden_test.go.
func TestEveryContractRuleKeepsACaseOfItsOwn(t *testing.T) {
	covered := map[Reason]bool{
		ReasonMalformedEnvelope:   true,
		ReasonMalformedProjection: true,
		ReasonUnknownKey:          true,
		ReasonInvalidSignature:    true,
	}
	for _, c := range entitlementCorpus() {
		if c.reason != "" {
			covered[c.reason] = true
		}
	}

	for _, reason := range []Reason{
		ReasonMalformedEnvelope,
		ReasonUnsigned,
		ReasonUnknownSignatureAlgorithm,
		ReasonUnknownKey,
		ReasonMalformedSignatureEncoding,
		ReasonInvalidSignature,
		ReasonMalformedProjection,
		ReasonUndeclaredField,
		ReasonUnsupportedContractVersion,
		ReasonNonPositiveRevision,
		ReasonMalformedSubjectID,
	} {
		assert.True(t, covered[reason], "no conformance case exercises %q", reason)
	}
}

// TestVerifyRefusesAnUndeclaredEnvelopeMember covers the outer half of the
// contract's closed field set, which no example file exercises.
//
// The envelope is additionalProperties: false too, and the whole message is
// stored verbatim, so an extra member at this level is retained exactly as an
// extra member inside state would be.
func TestVerifyRefusesAnUndeclaredEnvelopeMember(t *testing.T) {
	private := trustedKey(t)
	signed := signedPayload(t, ContractVersion)
	signatureJSON := conformingSignature(private, signed)

	_, err := Verify(corpusEnvelope(signed, signatureJSON))
	require.NoError(t, err, "control: the same envelope without the extra member must verify")

	smuggled := []byte(`{"signed":` + string(signed) + `,"signature":` + signatureJSON +
		`,"billing_reference":"in_2026_07_00931"}`)
	_, err = Verify(smuggled)
	require.ErrorIs(t, err, ErrInvalidProjection)
	assert.Equal(t, ReasonMalformedEnvelope, RefusalReason(err))
}

// TestRefusalReasonSaysNothingAboutErrorsItDidNotProduce pins the accessor's
// floor. An empty Reason has to mean "this did not come from Verify" rather
// than "Verify had no opinion", or a caller reading it would find the two
// indistinguishable.
func TestRefusalReasonSaysNothingAboutErrorsItDidNotProduce(t *testing.T) {
	assert.Empty(t, RefusalReason(nil))
	assert.Empty(t, RefusalReason(ErrInvalidProjection),
		"the bare sentinel carries no reason; only a refusal Verify built does")
	assert.Equal(t, ReasonUnsigned, RefusalReason(refuse(ReasonUnsigned, ErrInvalidProjection)))
}
