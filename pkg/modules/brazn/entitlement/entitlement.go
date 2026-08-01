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
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
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

// Subject identifies who a projection is about, in the commercial service's
// own identifiers rather than this instance's row ids.
type Subject struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
}

// State is the entitlement itself: what this subject is currently allowed.
type State struct {
	Edition           string `json:"edition"`
	SeatStatus        string `json:"seat_status"`
	OrganizationAdmin bool   `json:"organization_admin"`
	EffectiveState    string `json:"effective_state"`
}

// Signed is the signed half of the envelope, and the only half a policy
// decision may read.
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

// Stale reports whether this projection is older than the configured freshness
// window, in which case it entitles nothing and guarded operations fail closed
// exactly as they do when no projection was ever applied. Without this a
// correctly signed envelope would be trusted for the rest of the instance's
// life, however long ago the commercial service stopped talking to it.
//
// IssuedAt is read here for FRESHNESS ONLY, and never to order anything. The
// contract is explicit that Revision is the sole ordering and that comparing
// two issued_at values to decide which projection is newer is forbidden,
// because clocks skew and deliveries overtake each other
// (cloud/contracts/README.md, "The apply rule"). Nothing here compares two
// projections; it compares one against the clock.
func (s *Signed) Stale() bool {
	return time.Since(s.IssuedAt) > MaxAge()
}

// DefaultMaxAge is how long a projection stays fresh when
// brazn.entitlementmaxage carries no usable value.
const DefaultMaxAge = 7 * 24 * time.Hour

// MaxAge returns the freshness window projections are measured against.
//
// Configuration rather than a constant, so the window can be changed without an
// application release. A key viper cannot read comes back as zero, which would
// make every projection instantly stale and lock every guarded operation out of
// the instance, so an unusable value falls back to the default rather than to
// either extreme.
func MaxAge() time.Duration {
	configured := config.BraznEntitlementMaxAge.GetDuration()
	if configured <= 0 {
		return DefaultMaxAge
	}
	return configured
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
type envelope struct {
	Signed    json.RawMessage `json:"signed"`
	Signature signature       `json:"signature"`
}

// Verify checks an envelope's signature against the configured trusted keys
// and returns the state it carries. Every failure is one of two errors,
// because callers must treat all of them identically: refuse.
func Verify(raw []byte) (*Signed, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, ErrInvalidProjection
	}
	if env.Signature.Algorithm != algorithmEd25519 || len(env.Signed) == 0 {
		return nil, ErrInvalidProjection
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
	sig, err := base64.RawURLEncoding.DecodeString(env.Signature.Value)
	if err != nil || !ed25519.Verify(key, SigningInput(env.Signed), sig) {
		return nil, ErrInvalidProjection
	}

	signed := &Signed{}
	if err := json.Unmarshal(env.Signed, signed); err != nil {
		return nil, ErrInvalidProjection
	}
	if signed.ContractVersion != ContractVersion || signed.Revision <= 0 {
		return nil, ErrInvalidProjection
	}

	return signed, nil
}

// signingKey resolves a key id against brazn.entitlementkeys, a comma-separated
// list of "<key id>:<base64 ed25519 public key>" pairs. Rotation is therefore a
// config change: list the new key alongside the old one until every projection
// has been re-signed, then drop the old one.
func signingKey(keyID string) (ed25519.PublicKey, error) {
	if keyID == "" {
		return nil, ErrUnknownSigningKey
	}

	for _, pair := range strings.Split(config.BraznEntitlementKeys.GetString(), ",") {
		id, encoded, found := strings.Cut(strings.TrimSpace(pair), ":")
		if !found || id != keyID {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, ErrUnknownSigningKey
		}
		return key, nil
	}

	return nil, ErrUnknownSigningKey
}
