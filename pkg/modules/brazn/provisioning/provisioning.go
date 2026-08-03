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

// Package provisioning decodes the signed requests Brazn's commercial service
// uses to provision on this instance.
//
// IT IS A CHANNEL, NOT AN ENDPOINT'S HELPER. Creating a user was its first
// operation and deliberately not its only shape: an organization's primary
// team, its roots and a member's Inbox come through the same door. Everything
// that would otherwise have to be decided twice is decided here once - the
// authentication scheme, the trust store, the signing domain, the route and
// its classification - and an operation is a value in the signed payload
// rather than a second endpoint.
//
// Like the entitlement ingest, nothing here reaches the network or the
// database. It verifies and decodes; models does the writing.
package provisioning

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
)

// SigningDomain is the domain-separation prefix a provisioning signature
// covers, terminated by 0x0A exactly as the entitlement contract's is.
//
// IT IS THE ONLY THING SEPARATING THIS CHANNEL FROM THE ENTITLEMENT ONE. Both
// verify against the same key list (brazn.entitlementkeys), so without a
// distinct prefix every entitlement projection Percy has ever signed would be
// a candidate provisioning request and the reverse would hold too. Adding a
// third channel means adding a third prefix here and nothing else.
const SigningDomain = "percy.provisioning.v1\n"

// ContractVersion is the only provisioning contract version this build
// accepts. A request from a newer contract is refused rather than guessed at,
// for the reason the strict decode below gives: a field this build cannot see
// is a field it would silently ignore.
const ContractVersion = "1"

// The operations this channel carries. The switch that reads these is the
// extension point, and a value it does not know is refused rather than treated
// as the default.
//
// THE TWO TOPOLOGY OPERATIONS ARE SEPARATE VALUES and must stay so. One name
// for both would have the switch route a per-USER Inbox to the per-TEAM roots
// and back, and neither call would notice: both payloads carry an organization
// and a subject, so each would decode cleanly as the other.
// THE SAME ARGUMENT APPLIES WITH MORE FORCE TO erase_subject, whose payload is
// field-for-field identical to create_personal_inbox's: one organization, one
// subject. Under a shared name each would decode cleanly as the other and the
// switch would route a DESTRUCTION to a creation, or the reverse. It is a
// separate value for that reason and must stay one.
const (
	OperationCreateUser          = "create_user"
	OperationCreatePersonalInbox = "create_personal_inbox"
	OperationCreateTeamRoots     = "create_team_roots"
	OperationEraseSubject        = "erase_subject"
)

// maxMailboxLength is users.email's column width. An address past it is
// refused rather than stored, because the database would truncate it into a
// DIFFERENT mailbox - which is another person's account, silently. This is the
// same reasoning the entitlement contract's opaqueId length bound is written
// for.
const maxMailboxLength = 250

// commercialID is the entitlement contract's $defs/opaqueId, which is the shape
// every identifier minted by the commercial service carries - an organization
// id, a subject id, a team id.
//
// It is quoted from the schema here rather than shared with the entitlement
// package's copy of it, deliberately. That one decides what a projection's
// subject is and this one decides what a provisioning request may name; they
// agree today because the contract says one thing, and a single definition
// would mean neither side was checked against the contract at all - a widened
// rule there would silently widen this.
//
// The upper bound is not decoration. Both values guarded by it reach
// varchar(64) columns, so an id past this length is one a store could truncate
// into a different organization or a different team.
var commercialID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ErrInvalidRequest means a provisioning request could not be acted on: its
// envelope did not verify, or what it carried was not something this build
// accepts. Callers must not distinguish the two - see the endpoint on why the
// reply is flat.
var ErrInvalidRequest = errors.New("provisioning request is not valid")

// CreateUser is the whole signed payload of a create_user operation, including
// the two members every operation carries.
//
// Each operation declares its own complete payload type rather than embedding
// a shared header, and that is what keeps the strict decode below exact: a
// member that belongs to another operation is undeclared here and refused,
// instead of being accepted and ignored because some sibling operation happens
// to declare it.
type CreateUser struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// Email is the mailbox to provision, treated as an OPAQUE KEY: nothing
	// here transforms it. Case folding, plus-address stripping and the rest
	// belong to the commercial layer, which owns the mailbox and knows which
	// two spellings it considers one customer; a fork that folded them would
	// merge two accounts the sender believes are separate, and nothing
	// downstream could tell that it had.
	//
	// How two mailboxes are COMPARED is the database's collation and not this
	// package's, so the property is "never transformed" rather than "matched
	// byte for byte": MySQL and MariaDB compare case-insensitively by default,
	// PostgreSQL and SQLite do not, and CI runs all of them. That difference
	// only ever makes the fork resolve where it would otherwise create, which
	// is the safe direction - it can never split one customer across two users.
	Email string `json:"email"`
}

// CreatePersonalInbox is the whole signed payload of a create_personal_inbox
// operation: one subject's protected Inbox.
//
// It declares its own complete payload rather than sharing a header with
// CreateTeamRoots, for the reason CreateUser gives - a member belonging to
// another operation must be undeclared here and refused, rather than accepted
// and ignored because a sibling happens to declare it. That is what stops a
// create_personal_inbox carrying a team_id from being carried out as if the
// team were not there.
type CreatePersonalInbox struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// OrganizationID is the commercial organization the subject belongs to. It
	// is carried for the log and the refusal, and deliberately decides nothing:
	// an Inbox belongs to a PERSON, and the protected entity that records one
	// carries no organization for exactly that reason.
	OrganizationID string `json:"organization_id"`
	// UserID is this instance's own users.id in decimal form - the same value
	// the entitlement contract's subject.user_id carries. What it resolves to
	// is models' question; this only checks it is an identifier the contract
	// could have minted.
	UserID string `json:"user_id"`
}

// CreateTeamRoots is the whole signed payload of a create_team_roots
// operation: a team, its Team root, and the organization's Public root.
type CreateTeamRoots struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	OrganizationID  string `json:"organization_id"`
	UserID          string `json:"user_id"`
	// TeamID is the COMMERCIAL team id and never this instance's. Nothing
	// derives one from the other in either direction: it is stored beside the
	// roots it provisioned so a repeat can answer with the same references, and
	// the reply carries this fork's own ids instead.
	TeamID string `json:"team_id"`
}

// EraseSubject is the whole signed payload of an erase_subject operation: one
// subject, and everything this instance holds for them, destroyed.
//
// IT IS THE ONLY IRREVERSIBLE OPERATION ON THIS CHANNEL. Its four members are
// character for character those of CreatePersonalInbox, which is exactly why
// they are declared separately here rather than shared: a single type used by
// both would make the two requests indistinguishable to everything below the
// switch, and the one place the difference is recorded - the operation member -
// would then be the only thing between provisioning somebody an Inbox and
// deleting their account.
type EraseSubject struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// OrganizationID decides nothing, exactly as on CreatePersonalInbox. It is
	// the caller's statement of which subject it BELIEVES it is erasing, and it
	// is carried for the audit line so that a wrong erasure can be read back
	// afterwards against the organization it was requested for.
	OrganizationID string `json:"organization_id"`
	// UserID is this instance's own users.id in decimal form. Whether a user of
	// that id exists is models' question and deliberately not this one - for
	// erasure, "no such subject" is a SUCCESS rather than a refusal, and a
	// decoder that tried to answer it here would turn the resumable case into a
	// permanent failure.
	UserID string `json:"user_id"`
}

// operation is the lenient first read of a signed payload: enough to route it,
// and deliberately nothing else. It ignores unknown members because at this
// point every member of every operation is unknown to it.
type operation struct {
	Operation string `json:"operation"`
}

// Verify authenticates a provisioning envelope and returns the operation it
// names together with the signed payload, for the caller to decode into that
// operation's own type.
//
// It is two steps rather than one because the payload's shape depends on the
// operation, and the operation is inside the signed bytes - so it cannot be
// read before the signature has been checked, and the payload cannot be
// decoded before the operation is known.
func Verify(raw []byte) (string, json.RawMessage, error) {
	payload, err := entitlement.VerifyEnvelope(SigningDomain, raw)
	if err != nil {
		return "", nil, err
	}

	// EVERYTHING BELOW READS THE MESSAGE, so everything below is after the
	// signature, exactly as on the entitlement channel.
	var op operation
	if err := json.Unmarshal(payload, &op); err != nil {
		return "", nil, ErrInvalidRequest
	}
	return op.Operation, payload, nil
}

// DecodeCreateUser reads a verified payload as a create_user request and
// checks the one field it carries.
func DecodeCreateUser(payload json.RawMessage) (*CreateUser, error) {
	request := &CreateUser{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	// A mailbox with no "@" is not one, and user.CreateUser will not say so:
	// it requires the field to be non-empty and validates nothing about its
	// shape, so whatever arrives becomes a real user's address.
	if len(request.Email) > maxMailboxLength || !strings.Contains(request.Email, "@") {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// DecodeCreatePersonalInbox reads a verified payload as a create_personal_inbox
// request and checks the two identifiers it carries.
func DecodeCreatePersonalInbox(payload json.RawMessage) (*CreatePersonalInbox, error) {
	request := &CreatePersonalInbox{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	if !commercialID.MatchString(request.OrganizationID) ||
		!commercialID.MatchString(request.UserID) {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// DecodeCreateTeamRoots reads a verified payload as a create_team_roots request
// and checks the three identifiers it carries.
//
// The team id is checked here rather than left to the write, because it is what
// a later call is matched against: an id this build stored in some other shape
// than the one that arrives next time would provision a SECOND set of roots for
// one team, and the commercial record coalesces a team's pair in exactly once.
func DecodeCreateTeamRoots(payload json.RawMessage) (*CreateTeamRoots, error) {
	request := &CreateTeamRoots{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	if !commercialID.MatchString(request.OrganizationID) ||
		!commercialID.MatchString(request.UserID) ||
		!commercialID.MatchString(request.TeamID) {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// DecodeEraseSubject reads a verified payload as an erase_subject request and
// checks the two identifiers it carries.
//
// IT IS THE ONE DECODER THAT CHECKS THE OPERATION MEMBER, and the asymmetry is
// deliberate. Routing already guarantees the value - Verify reads it from the
// same signed bytes the switch dispatches on - so for the three creating
// operations the check would be dead weight. This one is irreversible and its
// payload is structurally identical to create_personal_inbox's, so the single
// mistake that would matter is an editing error in the switch pointing a
// create_personal_inbox case at this decoder, or the reverse. One comparison
// makes that a refusal instead of a deletion.
func DecodeEraseSubject(payload json.RawMessage) (*EraseSubject, error) {
	request := &EraseSubject{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	if request.Operation != OperationEraseSubject {
		return nil, ErrInvalidRequest
	}
	if !commercialID.MatchString(request.OrganizationID) ||
		!commercialID.MatchString(request.UserID) {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// decodeExactly reads a payload into an operation's own type and refuses
// anything the type does not declare, or anything after it.
//
// THE STRICTNESS IS FORWARD COMPATIBILITY, not tidiness. A member this build
// cannot see is a member it would silently drop, and the sender would have no
// way to learn that its request had been carried out differently from how it
// was written - a create_user that grows an organization it must belong to is
// exactly that, and it is filed (BRA-1026). Refusing turns a silent
// half-application into a visible failure.
//
// json.Unmarshal runs first because it rejects trailing content, which
// json.Decoder does not; the decoder then runs for the unknown-member check
// alone.
func decodeExactly(payload []byte, into interface{}) error {
	if err := json.Unmarshal(payload, into); err != nil {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return ErrInvalidRequest
	}
	return nil
}
