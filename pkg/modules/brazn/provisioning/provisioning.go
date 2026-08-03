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
//
// RESOLVING A MAILBOX IS THE SAME HAZARD WITH MORE AT STAKE. Its payload
// carries exactly the two identifiers create_personal_inbox does, so under a
// shared name the switch would route a READ to a WRITE - a question about an
// address answered by provisioning one. Its name says resolve rather than get
// because this build may answer that there is nothing to resolve.
const (
	OperationCreateUser          = "create_user"
	OperationCreatePersonalInbox = "create_personal_inbox"
	OperationCreateTeamRoots     = "create_team_roots"
	OperationResolveMailbox      = "resolve_mailbox"
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

// ResolveMailbox is the whole signed payload of a resolve_mailbox operation:
// which mailbox does this subject reach?
//
// It declares its own complete payload for the reason CreateUser gives, and on
// this operation that reason has teeth rather than being a convention: this
// payload and CreatePersonalInbox's carry THE SAME TWO IDENTIFIERS, so nothing
// in the bytes tells them apart. The operation name is the whole of the
// difference, which is why it is a separate constant above and why the switch,
// not this type, is what keeps a read from being carried out as a write.
type ResolveMailbox struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// OrganizationID is the commercial organization the caller believes the
	// subject belongs to. It DECIDES NOTHING, exactly as CreatePersonalInbox's
	// does and for the same reason: a mailbox belongs to a PERSON, and no
	// answer here changes with the organization named. It is carried so the
	// audit line records the pair rather than a bare id, and so this payload
	// stays the same shape as its sibling's.
	OrganizationID string `json:"organization_id"`
	// UserID is this instance's own users.id in decimal form.
	//
	// The contract states a TIGHTER pattern than this build checks -
	// ^[1-9][0-9]{0,18}$ against commercialID's class - and the difference is
	// deliberate on both sides: strict producers, tolerant consumers. What the
	// id resolves to is models' question, and an id no user carries is an
	// answer here rather than a refusal.
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

// DecodeResolveMailbox reads a verified payload as a resolve_mailbox request
// and checks the two identifiers it carries.
//
// It is the same shape as DecodeCreatePersonalInbox because the payload is, and
// that similarity is the reason the two operations must never share a name: if
// this decoder is ever reached for a create_personal_inbox payload, or the
// reverse, the decode will succeed and only the switch will have been wrong.
func DecodeResolveMailbox(payload json.RawMessage) (*ResolveMailbox, error) {
	request := &ResolveMailbox{}
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
