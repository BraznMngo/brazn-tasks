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
	"code.vikunja.io/api/pkg/user"
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
//
// THE SAME ARGUMENT APPLIES WITH MORE FORCE TO erase_subject, whose payload is
// field-for-field identical to create_personal_inbox's: one organization, one
// subject. Under a shared name each would decode cleanly as the other and the
// switch would route a DESTRUCTION to a creation, or the reverse. It is a
// separate value for that reason and must stay one.
//
// AND MOST OF ALL TO resolve_user, whose recognition form carries EXACTLY THE
// MEMBERS create_user carries - a contract version, an operation and a mailbox.
// Under a shared name a question about an address would be answered by
// provisioning one, which is BRA-1106's defect arriving from this side of the
// seam: the read must never become the write. It is a separate value here, and
// DecodeResolveUser checks the member as well, for the reason
// DecodeEraseSubject gives about its own.
const (
	OperationCreateUser          = "create_user"
	OperationCreatePersonalInbox = "create_personal_inbox"
	OperationCreateTeamRoots     = "create_team_roots"
	OperationResolveMailbox      = "resolve_mailbox"
	OperationEraseSubject        = "erase_subject"
	OperationResolveUser         = "resolve_user"
	// OperationRevokeSession is BRA-1014: destroying one session by id, the
	// fork's half of a device revocation the account page has been showing as
	// already-done since the row it writes was added. Its payload carries the
	// same two members ResolveUser's recognition form does - a subject and one
	// other string - but the other string is a session id rather than a
	// mailbox, so a shared name with anything on this channel is not a
	// question this operation shares.
	OperationRevokeSession = "revoke_session"
	// OperationCreateUserWithPassword is BRA-1335: a brand-new Brazn Tasks
	// account for somebody who chose a username and a password at the commercial service
	// checkout, so the account exists before they ever open this instance.
	//
	// IT IS A SEPARATE OPERATION FROM create_user AND MUST STAY ONE.
	// create_user carries only an email, deliberately - see CreateUser's own
	// comment - because its callers are OAuth-adopted identities with no
	// password to arrive with. Widening create_user to also accept a username
	// and a password would let its payload decode as this one's, one field
	// short, with the switch below the only thing keeping an OAuth-adoption
	// caller's request from being read as a password-signup request. A
	// distinct name makes that impossible rather than merely unlikely.
	OperationCreateUserWithPassword = "create_user_with_password"
	// OperationJoinTeam is BRA-1475: putting one subject INTO a team this
	// instance has already provisioned, which is the step that makes a team's
	// shared projects reachable by an invited member. Access is granted to the
	// TEAM as a group (see grantTeamAccess), so a member who is not in the team
	// row sees an empty product however complete their entitlement is.
	//
	// ⚠ ITS PAYLOAD IS FIELD-FOR-FIELD create_team_roots'S - a contract version,
	// an operation, an organization, a subject and a commercial team - so the
	// two are indistinguishable in the bytes and the operation member is the
	// whole of the difference. That is the same hazard erase_subject and
	// create_personal_inbox carry, with the same consequence in one direction:
	// under a shared name a request to JOIN an existing team would be carried
	// out as a request to CREATE one, and models.provisionTeamRoots makes its
	// subject the team's creator and a team ADMIN (CreateNewTeam's third
	// argument). An invited member would then hold the team-management ability
	// their invitation deliberately does not grant. DecodeJoinTeam therefore
	// checks the operation member, as DecodeEraseSubject does and for the same
	// reason.
	OperationJoinTeam = "join_team"
)

// maxMailboxLength is users.email's column width. An address past it is
// refused rather than stored, because the database would truncate it into a
// DIFFERENT mailbox - which is another person's account, silently. This is the
// same reasoning the entitlement contract's opaqueId length bound is written
// for.
const maxMailboxLength = 250

// maxUsernameLength is users.username's column width, for the same reason
// maxMailboxLength bounds the mailbox: a value past it is one a store would
// truncate into a DIFFERENT username - which is another person's account,
// silently.
const maxUsernameLength = 250

// minPasswordBytes and maxPasswordBytes are the exact bounds
// pkg/user/validator.go's own "bcrypt_password" rule already applies to every
// other password this fork ever accepts - not a policy this package is
// inventing, but the one hard technical fact it must not ignore: bcrypt
// refuses anything over 72 bytes outright (golang.org/x/crypto/bcrypt), so
// admitting a longer value here would only turn user.HashPassword's error
// into this channel's flat, unhelpful 400 further downstream.
const (
	minPasswordBytes = 8
	maxPasswordBytes = 72
)

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

// ResolveUser is the whole signed payload of a resolve_user operation: is this
// person already a user here, and is their mailbox confirmed?
//
// TWO REQUEST FORMS, EXACTLY ONE IDENTIFIER PER REQUEST. Both members are
// declared on one type because the contract states one `oneOf` over one
// operation - it is one lookup on one row with one projection, not two
// operations - so which of the two arrived is a PRESENCE rule, checked in the
// decoder. DisallowUnknownFields cannot see it: both members are declared, so
// a payload carrying both decodes perfectly and means something the contract
// refuses to define.
//
// IT IS ALSO THE ONE PAYLOAD ON THIS CHANNEL THAT IS A SUPERSET OF ANOTHER
// OPERATION'S. Drop user_id and this is CreateUser, member for member. The
// operation name is the whole of the difference, which is why the decoder below
// checks it rather than trusting the switch alone.
type ResolveUser struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// Email is the address to look up, UNTRANSFORMED, for the reason CreateUser
	// gives about the mailbox on the way in.
	//
	// ⚠ IT IS MATCHED AGAINST brazn_provisioned_users.email AND NEVER
	// users.email, which is the OPPOSITE of the answer resolve_mailbox gives
	// and the one thing an implementer here is most likely to get wrong. Which
	// column is a property of the schema rather than a preference, and
	// models.ResolveUserByMailbox carries the whole argument and the address
	// change it decides.
	Email string `json:"email"`
	// UserID is this instance's own users.id in decimal form, for the form
	// requireVerifiedAccount asks by - it holds a bearer and an id and NO
	// address, because the commercial layer stores none.
	//
	// The contract states a TIGHTER pattern than this build checks -
	// ^[1-9][0-9]{0,18}$ against commercialID's class - which is the same
	// deliberate split ResolveMailbox's user_id documents: strict producers,
	// tolerant consumers. An id no user carries is an ANSWER here rather than a
	// refusal, so nothing is lost by admitting a shape Percy never sends.
	UserID string `json:"user_id"`
}

// RevokeSession is the revoke_session operation's payload (BRA-1014): destroy
// one session, naming both the session and the subject it must belong to.
//
// UserID IS NOT ADVISORY, unlike OrganizationID on EraseSubject. There it
// decides nothing and exists only for the audit line; here it is one half of
// the WHERE the deletion runs under (models.DeleteSessionForUser), because a
// session id alone would let a caller who mistyped - or a caller this
// instance should never have trusted with somebody else's session in the
// first place - remove a row it named without also being right about whose it
// is.
type RevokeSession struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// UserID is this instance's own users.id in decimal form, the same
	// producer-strict pattern CreateUser's and EraseSubject's carry -
	// ^[1-9][0-9]{0,18}$ - checked against commercialID's wider class below.
	UserID string `json:"user_id"`
	// SessionID is models.Session.ID: the UUID the OAuth exchange minted and
	// embedded as the JWT `sid` claim. commercialID's class - letters, digits,
	// underscore, hyphen, up to 64 - already covers a UUID's alphabet and
	// length without a second pattern to keep in step with the first.
	SessionID string `json:"session_id"`
}

// CreateUserWithPassword is the whole signed payload of a
// create_user_with_password operation (BRA-1335): a brand-new Brazn Tasks
// account for somebody who set a username and a password at the commercial service
// checkout.
//
// It declares its own complete payload rather than embedding CreateUser's, for
// the reason every sibling on this channel gives: a member belonging to
// another operation must be undeclared here and refused, instead of accepted
// and ignored because a sibling happens to declare it. That matters more here
// than usual, because dropping username and password from this payload leaves
// exactly CreateUser's shape - see OperationCreateUserWithPassword's own
// comment on why the two must never share a name.
type CreateUserWithPassword struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// Email is the mailbox to provision, treated as an OPAQUE KEY exactly as
	// CreateUser's is - see that type's comment for why nothing here
	// transforms it.
	Email string `json:"email"`
	// Username is validated by the exact rule /register applies
	// (user.CheckUsernameFormat), reused rather than reimplemented so the two
	// entry points can never quietly disagree about what a username looks
	// like.
	Username string `json:"username"`
	// Password is PLAINTEXT and arrives exactly once. This package's only
	// obligation toward it is to decode it and hand it on: it is never logged,
	// never echoed into a refusal, and this type is the last place in the
	// process that can see it before models.CreateProvisionedUserWithPassword
	// turns it into a bcrypt hash and lets the plaintext go out of scope.
	Password string `json:"password"`
}

// JoinTeam is the whole signed payload of a join_team operation (BRA-1475): one
// subject, put into one team this instance has already provisioned.
//
// IT DECLARES ITS OWN COMPLETE PAYLOAD rather than sharing CreateTeamRoots',
// even though the two are member for member identical, and here that is not the
// usual forward-compatibility argument - it is the whole safety property. One
// type used by both would make a join and a topology creation the same value to
// everything below the switch, and the operation member would be the only thing
// between adding a member to a team and minting a second team with that member
// as its administrator. See OperationJoinTeam.
type JoinTeam struct {
	ContractVersion string `json:"contract_version"`
	Operation       string `json:"operation"`
	// OrganizationID is one half of the key the team is resolved by, and unlike
	// CreatePersonalInbox's it DECIDES SOMETHING. A commercial team id is minted
	// by a service this fork does not own the namespace of, so scoping the
	// lookup to the organization is what keeps one customer's join from ever
	// resolving to another customer's team - see models.provisionedTeamRoot,
	// which this reuses rather than repeating.
	OrganizationID string `json:"organization_id"`
	// UserID is this instance's own users.id in decimal form: the person being
	// put into the team, and never the administrator who invited them.
	UserID string `json:"user_id"`
	// TeamID is the COMMERCIAL team id and never this instance's, exactly as
	// CreateTeamRoots' is. Nothing derives one from the other in either
	// direction; the pair (organization, commercial team) is looked up against
	// the row create_team_roots wrote.
	TeamID string `json:"team_id"`
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

// DecodeResolveUser reads a verified payload as a resolve_user request and
// checks that it names exactly one subject, in one of the two forms.
//
// IT CHECKS THE OPERATION MEMBER, and it is the second decoder to do so after
// DecodeEraseSubject. The asymmetry has the same cause: routing already
// guarantees the value, so for the three creating operations the check would be
// dead weight - but this payload's recognition form is create_user's payload
// exactly, so the single editing error that would matter is a switch case
// pointing one at the other's decoder. One comparison makes that a refusal
// instead of an unasked-for account.
//
// EXACTLY ONE IDENTIFIER, AND BOTH IS REFUSED RATHER THAN RESOLVED BY
// PRECEDENCE. A caller sending both is asserting a pairing rather than asking
// about one, and neither side of this seam should have to decide which member
// wins. Neither is refused for the plainer reason that it asks nothing.
//
// NOTHING BELOW READS OR WRITES STORED STATE, here as everywhere else in this
// package. The lookup happens in models, after the route's switch, which is
// after Verify - so an unsigned caller reaches the flat 400 without this
// instance ever being asked about the address it named.
func DecodeResolveUser(payload json.RawMessage) (*ResolveUser, error) {
	request := &ResolveUser{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	if request.Operation != OperationResolveUser {
		return nil, ErrInvalidRequest
	}
	// Both present, or neither. Written as an equality between the two
	// emptiness tests so that the two refusals cannot drift apart.
	if (request.Email == "") == (request.UserID == "") {
		return nil, ErrInvalidRequest
	}
	if request.Email != "" {
		// The same bound and the same shape check DecodeCreateUser makes, for
		// the same reason: an address past the column width is one a store
		// would truncate into a DIFFERENT mailbox, which is another person's
		// account.
		if len(request.Email) > maxMailboxLength || !strings.Contains(request.Email, "@") {
			return nil, ErrInvalidRequest
		}
		return request, nil
	}
	if !commercialID.MatchString(request.UserID) {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// DecodeRevokeSession reads a verified payload as a revoke_session request
// and checks the two identifiers it carries.
//
// IT CHECKS THE OPERATION MEMBER, matching DecodeEraseSubject and
// DecodeResolveUser. This payload's shape has no sibling on the channel it
// could be confused with today - the check costs one comparison and keeps the
// rule uniform rather than "only where a collision has already been found."
func DecodeRevokeSession(payload json.RawMessage) (*RevokeSession, error) {
	request := &RevokeSession{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	if request.Operation != OperationRevokeSession {
		return nil, ErrInvalidRequest
	}
	if !commercialID.MatchString(request.UserID) {
		return nil, ErrInvalidRequest
	}
	if !commercialID.MatchString(request.SessionID) {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// DecodeCreateUserWithPassword reads a verified payload as a
// create_user_with_password request and checks the three members it carries.
//
// IT DOES NOT CHECK THE OPERATION MEMBER, matching create_user's own decoder
// and for the reason given there: routing already guarantees the value, and
// this payload has no sibling it could be mistaken for on the way IN - it is a
// strict superset of create_user's shape, so a payload actually meant for
// create_user is refused here as soon as decodeExactly sees the two members it
// does not declare. See OperationCreateUserWithPassword for the direction that
// does need a name check (create_user must never widen into this).
func DecodeCreateUserWithPassword(payload json.RawMessage) (*CreateUserWithPassword, error) {
	request := &CreateUserWithPassword{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	// The same mailbox shape check DecodeCreateUser makes, for the same
	// reason: an address past the column width is one a store would truncate
	// into a DIFFERENT mailbox, which is another person's account.
	if len(request.Email) > maxMailboxLength || !strings.Contains(request.Email, "@") {
		return nil, ErrInvalidRequest
	}
	// user.CheckUsernameFormat is the exact rule /register applies, reused
	// rather than reimplemented; maxUsernameLength is this decoder's own
	// addition, for the truncation reason its comment gives.
	if len(request.Username) > maxUsernameLength {
		return nil, ErrInvalidRequest
	}
	if err := user.CheckUsernameFormat(request.Username); err != nil {
		return nil, ErrInvalidRequest
	}
	if len(request.Password) < minPasswordBytes || len([]byte(request.Password)) > maxPasswordBytes {
		return nil, ErrInvalidRequest
	}
	return request, nil
}

// DecodeJoinTeam reads a verified payload as a join_team request and checks the
// three identifiers it carries.
//
// IT CHECKS THE OPERATION MEMBER, and on this decoder that check is doing more
// work than on any of the other three that make it. DecodeEraseSubject,
// DecodeResolveUser and DecodeRevokeSession check it against an editing error in
// the switch; so does this, and the error it catches is the one that would grant
// an invited member the team administration their invitation withholds. See
// OperationJoinTeam for the whole argument.
//
// The team id is checked here rather than left to the write, for
// DecodeCreateTeamRoots' reason: it is what the stored row is matched against,
// so an id in some other shape than the one create_team_roots stored resolves to
// no team at all and the join is refused for a team that plainly exists.
func DecodeJoinTeam(payload json.RawMessage) (*JoinTeam, error) {
	request := &JoinTeam{}
	if err := decodeExactly(payload, request); err != nil {
		return nil, err
	}
	if request.ContractVersion != ContractVersion {
		return nil, ErrInvalidRequest
	}
	if request.Operation != OperationJoinTeam {
		return nil, ErrInvalidRequest
	}
	if !commercialID.MatchString(request.OrganizationID) ||
		!commercialID.MatchString(request.UserID) ||
		!commercialID.MatchString(request.TeamID) {
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
