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

// Package entitlement decodes the signed entitlement projection Brazn's
// commercial service writes into this instance.
//
// It only verifies and decodes an envelope. Storage is a plain table in
// pkg/models and the endpoint that writes it is a separate concern: nothing
// here reaches the network, so no request can ever wait on billing.
package entitlement

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/config"
)

// The editions defined by the entitlement contract
// (cloud/contracts/v2/entitlements). An edition this build does not know is
// not a third behaviour: callers must refuse it, the same as no projection.
//
// Community is the floor an unentitled subject already sits at, so no policy
// rule is registered for it and none needs to be. It is named here because the
// contract's enum has three members and the ingest endpoint must be able to
// tell "an edition this contract defines" from "a value nobody has agreed on".
const (
	EditionCommunity = "community"
	EditionPersonal  = "personal-cloud"
	EditionTeams     = "teams-cloud"
)

// KnownEdition reports whether a value is one of the three the v2 contract
// defines. Anything else is refused rather than interpreted, in exactly the
// same way an unsupported contract version is.
func KnownEdition(edition string) bool {
	return edition == EditionCommunity || edition == EditionPersonal || edition == EditionTeams
}

// ContractVersion is the only projection contract version this build accepts.
// A projection from a newer contract is refused rather than guessed at.
const ContractVersion = "2"

const (
	algorithmEd25519 = "ed25519"
	stateActive      = "active"
)

// Two errors, on purpose: a policy decision cannot act on the difference
// between a bad signature and an unsupported contract version, and inviting
// callers to branch on it would invite one of them to treat a subset as
// recoverable.
//
// The sync acknowledgement is a different consumer with a different need. Its
// contract defines four rejection reasons - invalid_signature, unknown_key,
// unsupported_contract_version, malformed_projection - which these two cannot
// be mapped onto without losing information. Whoever builds the ack should
// widen the vocabulary here rather than guess from the sentinel; the
// distinctions already exist at the point each error is returned in Verify,
// and only the return type collapses them. Deliberately not built now: nothing
// writes projections yet, so there is no ack to send.
var (
	// ErrInvalidProjection means the envelope could not be trusted: malformed,
	// wrongly signed, or from a contract version this build does not accept.
	ErrInvalidProjection = errors.New("entitlement projection is not valid")
	// ErrUnknownSigningKey means the envelope names a key this instance is not
	// configured to trust.
	ErrUnknownSigningKey = errors.New("entitlement projection was signed with an unknown key")
)

// Reason names which of the contract's rules a refused projection broke.
//
// IT IS DIAGNOSTIC AND NEVER A BRANCH. The two sentinels above remain what
// callers match on, every refusal wraps one of them, and errors.Is keeps
// working exactly as it did - nothing in the product reads a Reason to decide
// anything, and the endpoint's reply stays the same flat 400 for all of them.
//
// It exists because "the message was refused" is a claim a test can satisfy by
// accident. Every one of these rules sits behind the signature check, so a
// conformance case aimed at the revision or the contract version will be
// refused for an unknown key or a bad signature if anything about its setup is
// wrong - and an assertion that only asked whether an error came back would
// report success. Naming the reason is what makes the difference visible, and
// it is the whole point of BRA-929.
//
// This is also the vocabulary the acknowledgement contract needs
// (entitlement-projection-ack.schema.json distinguishes invalid_signature,
// unknown_key, unsupported_contract_version and malformed_projection). These
// are finer-grained than those four and map onto them; building the ack itself
// is a separate piece of work and nothing here presumes its shape.
type Reason string

// The rules Verify enforces, named after the contract's own conformance cases
// in cloud/contracts/v2/entitlements/examples/ wherever one exists.
const (
	ReasonMalformedEnvelope          Reason = "malformed_envelope"
	ReasonUnsigned                   Reason = "unsigned"
	ReasonUnknownSignatureAlgorithm  Reason = "unknown_signature_algorithm"
	ReasonUnknownKey                 Reason = "unknown_key"
	ReasonMalformedSignatureEncoding Reason = "malformed_signature_encoding"
	ReasonInvalidSignature           Reason = "invalid_signature"
	ReasonMalformedProjection        Reason = "malformed_projection"
	ReasonUndeclaredField            Reason = "undeclared_field"
	ReasonUnsupportedContractVersion Reason = "unsupported_contract_version"
	ReasonNonPositiveRevision        Reason = "non_positive_revision"
	ReasonMalformedSubjectID         Reason = "malformed_subject_id"
)

// RefusedError carries the named reason alongside the sentinel every caller
// already matches on.
type RefusedError struct {
	Reason   Reason
	sentinel error
}

// Error names the rule after the sentinel. The endpoint logs this and never
// returns it, so it is the operator's only channel for why a delivery was
// turned down - see BraznApplyEntitlementProjection on why the reply is flat.
func (e *RefusedError) Error() string {
	return e.sentinel.Error() + ": " + string(e.Reason)
}

// Unwrap is what keeps errors.Is(err, ErrInvalidProjection) and
// errors.Is(err, ErrUnknownSigningKey) true for exactly the errors they were
// true for before this type existed.
func (e *RefusedError) Unwrap() error {
	return e.sentinel
}

func refuse(reason Reason, sentinel error) error {
	return &RefusedError{Reason: reason, sentinel: sentinel}
}

// RefusalReason returns the rule an error says was broken, or the empty Reason
// for anything that did not come from Verify.
func RefusalReason(err error) Reason {
	var refused *RefusedError
	if errors.As(err, &refused) {
		return refused.Reason
	}
	return ""
}

// opaqueID is the contract's $defs/opaqueId, quoted from the schema rather than
// derived from anything here. Both halves of the subject key are constrained by
// it, and an id that fails it is not a subject any producer may send.
//
// The upper bound is not decoration: EntitlementProjection.OrganizationID is
// varchar(64), so an id past this length is one a store could silently truncate
// into a different subject.
var opaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Subject identifies who a projection is about, in the commercial service's
// own identifiers rather than this instance's row ids.
type Subject struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
}

// State is the entitlement itself: what this subject is currently allowed.
//
// ValidFrom and ValidTo are the validity window, and they are what let this
// record answer "until when" without anybody being asked. That is why there is
// no revocation channel, no refresh protocol and no staleness sweep anywhere in
// this package: ending a subscription sets the date, the outstanding token runs
// out, and the next login gets nothing.
//
// ValidTo is a POINTER because the contract carries it as an explicit null when
// no end has been recorded, and "no end" is a different fact from "an end,
// which is now". A projection minted before these members existed carries
// neither and decodes to a zero ValidFrom and a nil ValidTo - which reads as an
// entitlement with no recorded end, exactly the behaviour those subjects
// already had. Their presence is deliberately NOT enforced in Verify, which
// enforces the rules the contract names a conformance case for: requiring them
// would refuse every envelope already stored, and the frozen golden set with
// them.
type State struct {
	Edition           string     `json:"edition"`
	SeatStatus        string     `json:"seat_status"`
	OrganizationAdmin bool       `json:"organization_admin"`
	EffectiveState    string     `json:"effective_state"`
	ValidFrom         time.Time  `json:"valid_from"`
	ValidTo           *time.Time `json:"valid_to"`
}

// TokenEntitlement is what a session token carries about its holder, and the
// only thing a guarded request reads afterwards.
type TokenEntitlement struct {
	// Edition is the contract's edition, stamped into the token as a claim.
	Edition string
	// EndsAt is the instant the token must not outlive. ZERO means the
	// entitlement records no end, so the token keeps its normal lifetime.
	EndsAt time.Time
}

// Signed is the signed half of the envelope, and the only half a policy
// decision may read.
//
// IssuedAt is AUDIT DATA and nothing may decide anything from it. The contract
// says so in as many words: it is recorded with a delivery outcome so a dispute
// has something to reconstruct, and it must never order two projections, which
// is revision's job exclusively.
type Signed struct {
	ContractVersion string    `json:"contract_version"`
	Subject         Subject   `json:"subject"`
	Revision        int64     `json:"revision"`
	IssuedAt        time.Time `json:"issued_at"`
	State           State     `json:"state"`
}

// Active reports whether this projection currently entitles anything at all.
// Both the seat and the subscription must be active; any other value - including
// one this build does not recognise - is inactive.
func (s *Signed) Active() bool {
	return s.State.EffectiveState == stateActive && s.State.SeatStatus == stateActive
}

// ForToken decides what a session token issued at `at` may carry for this
// subject. Nil means it may carry no entitlement at all, which leaves the token
// its normal lifetime and no edition claim - the holder can still do ordinary
// task work, and every guarded route refuses them.
//
// THE LAST SENTENCE IS THE WHOLE DESIGN, and the easy half to get backwards: an
// entitlement whose end has ALREADY PASSED is refused here, never capped to.
// Capping to it would mint a token that expired before it was issued, and the
// same token authenticates ordinary task work - which the contract's failure
// policy says continues - so a customer whose subscription lapsed would lose
// access to their own tasks rather than to the features they stopped paying
// for. The refusal is what makes the ending affect only what it should.
//
// `grace` is configuration rather than a constant because how long a session
// may run on past a paid period is a commercial decision, not a property of the
// protocol.
func (s *Signed) ForToken(at time.Time, grace time.Duration) *TokenEntitlement {
	if !s.Active() {
		return nil
	}
	// Not in force yet. Nothing in Phase 1 emits a future start - every state
	// change is delivered when it takes effect - so this guards against a
	// producer that changes rather than a case that arises today.
	if s.State.ValidFrom.After(at) {
		return nil
	}
	if s.State.ValidTo == nil {
		return &TokenEntitlement{Edition: s.State.Edition}
	}
	endsAt := s.State.ValidTo.Add(grace)
	if !endsAt.After(at) {
		return nil
	}
	return &TokenEntitlement{Edition: s.State.Edition, EndsAt: endsAt}
}

// SigningDomain is the domain-separation prefix the v2 entitlement contract
// puts in front of the signed bytes, terminated by the 0x0A the contract
// specifies. It is 31 characters and a newline.
//
// It is what stops the signing key's other work from becoming this verifier's
// attack surface: without it, every Ed25519 signature that key has ever made
// over any JSON document is a candidate projection, because a projection would
// be nothing more than "some JSON this key signed".
const SigningDomain = "percy.entitlement-projection.v2\n"

// SigningInput returns the exact bytes a projection signature covers: the
// domain prefix, then the signed member as received.
//
// The signed member is used verbatim and is deliberately never
// re-canonicalized here. The producer splices its JCS output into the envelope
// unchanged, so the two agree by construction; a verifier that re-derived the
// canonical form instead would be one JCS implementation difference away from
// accepting bytes nobody signed.
//
// The consequence is intended: any intermediary that reformats the JSON - a
// proxy that pretty-prints, a store that round-trips the envelope through a
// generic JSON type - breaks verification rather than being silently tolerated.
func SigningInput(signed []byte) []byte {
	input := make([]byte, 0, len(SigningDomain)+len(signed))
	input = append(input, SigningDomain...)
	return append(input, signed...)
}

type signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// envelope keeps the signed half as raw bytes on purpose: the signature covers
// the domain prefix followed by exactly those bytes, so verifying them as
// received removes any need for a canonical re-encoding to agree with the
// signer's. See SigningInput.
//
// Signature is a pointer so that "there is no signature" and "there is one, and
// it says something wrong" are different facts. The contract has a conformance
// case for each - invalid.unsigned and invalid.unknown-signature-algorithm -
// and a value type would have collapsed them into one empty algorithm string.
type envelope struct {
	Signed    json.RawMessage `json:"signed"`
	Signature *signature      `json:"signature"`
}

// hasUndeclaredField reports whether raw carries a member the contract does not
// declare. Every level of the projection schema is additionalProperties: false,
// and this is how that is enforced.
//
// IT IS ONLY EVER ASKED AFTER A LENIENT DECODE HAS ALREADY SUCCEEDED. That
// ordering is what makes the answer exact: with malformed JSON and type errors
// already ruled out, an unknown field is the only thing left for the strict
// decode to fail on, so no error string has to be inspected to tell the two
// apart. Trailing content is likewise already excluded, because json.Unmarshal
// rejects it and json.Decoder would not.
//
// This matters beyond conformance. The whole envelope is stored verbatim in
// brazn_entitlement_projections.envelope, so a tolerated extra member is not
// ignored - it is retained. The contract's invalid.billing-detail-in-state case
// is exactly that: an invoice id and a price, which this instance has no
// business holding, arriving inside a message it would otherwise accept.
func hasUndeclaredField(raw []byte, into interface{}) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(into) != nil
}

// Verify checks an envelope's signature against the configured trusted keys
// and returns the state it carries. Every failure still reaches callers as one
// of two sentinels, because callers must treat all of them identically:
// refuse. Which rule refused it is carried alongside, for logs and for
// conformance tests, and for nothing that branches - see Reason.
func Verify(raw []byte) (*Signed, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, refuse(ReasonMalformedEnvelope, ErrInvalidProjection)
	}
	if env.Signature == nil {
		return nil, refuse(ReasonUnsigned, ErrInvalidProjection)
	}
	if len(env.Signed) == 0 || hasUndeclaredField(raw, &envelope{}) {
		return nil, refuse(ReasonMalformedEnvelope, ErrInvalidProjection)
	}
	if env.Signature.Algorithm != algorithmEd25519 {
		return nil, refuse(ReasonUnknownSignatureAlgorithm, ErrInvalidProjection)
	}

	key, err := signingKey(env.Signature.KeyID)
	if err != nil {
		return nil, err
	}

	// base64url WITHOUT padding, which is the contract's exact wording and the
	// only encoding a conforming producer emits: signature.value is constrained
	// to ^[A-Za-z0-9_-]{86}$, and Percy's producer returns
	// Buffer.toString("base64url") having asserted that same pattern before it
	// will put a message on the wire (cloud/service/src/entitlement-projection.ts).
	//
	// This was StdEncoding until BRA-913, which decoded no conforming message at
	// all: 86 characters is not a multiple of four, so padded decoding failed on
	// length before the -/_ alphabet ever mattered. The two halves could not
	// exchange a single projection.
	//
	// Padding is rejected rather than tolerated for the reason the contract
	// gives: one signature must have exactly one encoding, so two encodings of
	// the same signature cannot both be in flight. This is deliberately NOT the
	// same encoding as brazn.entitlementkeys, which is our own config format and
	// stays standard base64; do not unify them.
	//
	// The encoding and the cryptography are separated because the contract has
	// a conformance case for each, and because they failed separately: the
	// padded-signature break was an encoding fault that never reached
	// ed25519.Verify at all, and reporting it as a bad signature would have
	// pointed the next reader at the key instead of at the alphabet.
	sig, err := base64.RawURLEncoding.DecodeString(env.Signature.Value)
	if err != nil {
		return nil, refuse(ReasonMalformedSignatureEncoding, ErrInvalidProjection)
	}
	if !ed25519.Verify(key, SigningInput(env.Signed), sig) {
		return nil, refuse(ReasonInvalidSignature, ErrInvalidProjection)
	}

	// EVERYTHING BELOW READS THE MESSAGE, so everything below is after the
	// signature. That ordering is the contract's and is not an accident of
	// where the checks were easiest to write.
	signed := &Signed{}
	if err := json.Unmarshal(env.Signed, signed); err != nil {
		return nil, refuse(ReasonMalformedProjection, ErrInvalidProjection)
	}
	if hasUndeclaredField(env.Signed, &Signed{}) {
		return nil, refuse(ReasonUndeclaredField, ErrInvalidProjection)
	}
	if signed.ContractVersion != ContractVersion {
		return nil, refuse(ReasonUnsupportedContractVersion, ErrInvalidProjection)
	}
	if signed.Revision <= 0 {
		return nil, refuse(ReasonNonPositiveRevision, ErrInvalidProjection)
	}
	if !opaqueID.MatchString(signed.Subject.OrganizationID) ||
		!opaqueID.MatchString(signed.Subject.UserID) {
		return nil, refuse(ReasonMalformedSubjectID, ErrInvalidProjection)
	}

	return signed, nil
}

// signingKey resolves a key id against brazn.entitlementkeys, a comma-separated
// list of "<key id>:<base64 ed25519 public key>" pairs. Rotation is therefore a
// config change: list the new key alongside the old one until every projection
// has been re-signed, then drop the old one.
func signingKey(keyID string) (ed25519.PublicKey, error) {
	if keyID == "" {
		return nil, refuse(ReasonUnknownKey, ErrUnknownSigningKey)
	}

	for _, pair := range strings.Split(config.BraznEntitlementKeys.GetString(), ",") {
		id, encoded, found := strings.Cut(strings.TrimSpace(pair), ":")
		if !found || id != keyID {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, refuse(ReasonUnknownKey, ErrUnknownSigningKey)
		}
		return key, nil
	}

	return nil, refuse(ReasonUnknownKey, ErrUnknownSigningKey)
}
