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

package v1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/modules/brazn/provisioning"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
)

// maxProvisioningBytes bounds what this endpoint will read. A provisioning
// request is a handful of closed enums and one mailbox; anything approaching
// this is not one.
const maxProvisioningBytes = 8 << 10

// provisionedUserReply is the answer to a create_user operation, in the shape
// the consumer is already written against (cloud/service/src/identity.ts,
// TaskUser).
//
// ID IS A STRING, and it has to be: the contract says "the DECIMAL STRING form
// of users.id" and validates it against ^[1-9][0-9]{0,18}$. A JSON number would
// pass that check today by coercion and fail the type it is declared with.
type provisionedUserReply struct {
	ID string `json:"id"`
	// Created reports whether this call created the user or resolved one that
	// already existed. The consumer checks it against its own records, so an
	// inaccurate value is worse than an error: see identity.ts obligation (2).
	Created bool `json:"created"`
	// EmailVerified is the fork's own notion of it - an account still waiting
	// on its confirmation mail is not verified - and nothing more.
	EmailVerified bool `json:"email_verified"`
}

// provisionedPasswordAccountReply is the answer to a create_user_with_password
// operation (BRA-1335).
//
// ID IS A STRING for the identical reason provisionedUserReply's is: the
// contract validates it against ^[1-9][0-9]{0,18}$, and a Go int64 marshals to
// 42 rather than "42".
//
// "created" DOES NOT APPEAR HERE, and that is not an oversight: create_user's
// create-or-resolve contract needs to say which of the two happened, and this
// operation never resolves - see models.CreateProvisionedUserWithPassword's
// comment on why a collision refuses rather than adopting - so the member
// would always read true and carry no information a caller could act on.
type provisionedPasswordAccountReply struct {
	ID string `json:"id"`
}

// nothingToReport is the answer to a performed operation that has nothing to
// say, and it marshals to `{}`.
//
// AN EMPTY BODY WOULD NOT DO, and that is the channel's contract for every
// operation rather than a preference: the consumer cannot tell an empty 200
// from a truncated one, so its transport refuses both alike
// (cloud/service/src/fork.ts, ProvisioningChannel). A handler that answered
// c.NoContent here would be refused by the caller it just succeeded for.
type nothingToReport struct{}

// teamRootsReply carries THIS INSTANCE'S OWN identifiers for the topology it
// provisioned, for the commercial record to map against the team it minted.
//
// Both are decimal strings for the reason provisionedUserReply's id is: the
// consumer validates them against ^[1-9][0-9]{0,18}$ and declares them as
// strings, so a JSON number would pass the pattern by coercion and fail the
// type. It has more teeth here than there - these two go straight into a
// mapping that coalesces once, so a value that arrives in the wrong shape is
// stored wrong permanently.
type teamRootsReply struct {
	// TaskTeamRef is this instance's teams.id.
	TaskTeamRef string `json:"task_team_ref"`
	// TaskProjectRef is this instance's protected Team ROOT project - not the
	// Public root, which is provisioned by the same call and deliberately not
	// reported: it belongs to the organization rather than to this team, and
	// the commercial record has one column per team for a team's own pair.
	TaskProjectRef string `json:"task_project_ref"`
}

// mailboxResolution is the answer to a resolve_mailbox operation, in the shape
// the contract fixes (cloud/contracts/v1/mailbox/, BRA-1096).
//
// Email is omitempty because AN UNRESOLVABLE ANSWER CARRIES NOTHING AND THE
// EMPTINESS IS THE GUARANTEE: the contract's response schema refuses an
// `unresolvable` that names an address, and gives no `reason` member for an
// implementation to write "the user was erased" into.
type mailboxResolution struct {
	Result string `json:"result"`
	Email  string `json:"email,omitempty"`
}

// noMailbox is the ONE answer to every absence: a subject this instance never
// had, and one an erasure destroyed - which are the same absence, because
// DeleteUser takes the claim row holding the address away with the user.
//
// It is a single value rather than the same literal constructed on two paths,
// so the two cases are byte-identical BY CONSTRUCTION rather than by two pieces
// of code happening to agree. Whoever holds the provisioning signing key can
// walk a sequential autoincrement; what they must not be able to learn from it
// is which of those ids were once customers.
var noMailbox = &mailboxResolution{Result: "unresolvable"}

// resolvedUser is the answer to a resolve_user operation that found somebody,
// in the shape the contract fixes (cloud/contracts/v1/user/, BRA-1109).
//
// USER_ID IS A STRING, for the reason provisionedUserReply's id is and with the
// same consequence: the consumer validates it against ^[1-9][0-9]{0,18}$, a
// JSON number passes that pattern by coercion and fails the type it is declared
// with, and a Go handler marshalling an int64 emits 42 rather than "42". The
// response schema names this the single most likely defect on this seam.
//
// NEITHER MEMBER CARRIES omitempty AND NEITHER MAY. email_verified is false for
// every account still waiting on its confirmation mail, and omitempty drops a
// false bool - which would emit a `resolved` with no verification member, read
// as `undefined` by the consumer, and refuse or admit that customer depending
// on how the check was written. The schema requires both members on this branch
// for exactly that reason.
type resolvedUser struct {
	Result string `json:"result"`
	UserID string `json:"user_id"`
	// EmailVerified is the row's own confirmation status - see
	// models.UserResolution, which is where it is derived and why it can never
	// be anything the caller said.
	EmailVerified bool `json:"email_verified"`
}

// unresolvableUser is the answer to every absence, and it is A SEPARATE TYPE
// rather than resolvedUser with empty members.
//
// THE EMPTINESS IS THE GUARANTEE, so it is made structural: this type has no
// field a user id or a verification state could be written into, and no field a
// later change could put a reason in. mailboxResolution reaches the same
// property with `omitempty` on one string, which works there because an empty
// address is never a resolution - it cannot work here, because `false` is a
// real answer that omitempty would drop.
//
// A single value rather than the same literal built on two paths, exactly as
// noMailbox is: an erased subject and one this instance never minted then
// answer byte-identically BY CONSTRUCTION rather than by two pieces of code
// happening to agree. Whoever holds the signing key can walk a sequential
// autoincrement, and what they must not learn from it is which ids were once
// customers.
type unresolvableUser struct {
	Result string `json:"result"`
}

var noUser = &unresolvableUser{Result: "unresolvable"}

// usernameAvailability is the whole reply to username_available: ONE MEMBER
// carrying one of three words.
//
// THE SHAPE IS THE PRIVACY GUARANTEE and it is made structural, on
// unresolvableUser's reasoning. There is no member here for a user id, a
// mailbox, a display name or a created date, so no future edit can add one to a
// branch by accident and no caller can come to depend on one. What a taken name
// discloses is that the name is taken, which is the entire question asked.
type usernameAvailability struct {
	Status string `json:"status"`
}

// BraznProvision performs one provisioning operation for Brazn's commercial
// service.
//
// AUTHENTICATION IS THE SIGNATURE, exactly as it is for the entitlement ingest
// and for the same reason: this fork has no service principal, so the message
// is authenticated rather than the connection. See
// BraznApplyEntitlementProjection for the full argument, and
// entitlement.VerifyEnvelope for why one key can sign for both channels without
// either accepting the other's messages - the signing domain differs, and the
// signature covers it.
//
// IT IS ONE ROUTE FOR EVERY OPERATION, which is a decision rather than an
// accident of there being one today. A second endpoint would need a second
// classification entry, and that entry would have to re-make the argument in
// route-classification.json's _readme about why a service-plane route can be
// neither service-managed nor gated on an acting user. The two protected
// topology operations came through this door and demonstrate the claim: each
// added an operation constant, a payload type, a decoder and a case below, and
// touched nothing about authentication, trust or classification.
//
// THE REPLY IS FLAT FOR EVERY REFUSAL, and for the same reason it is on the
// entitlement ingest: anyone can reach this route, so a reply that named the
// rule that refused would answer questions about the instance for whoever
// asked. A SUCCESSFUL reply does carry a user id, and that is not an oracle -
// reaching it at all requires an Ed25519 signature from a key
// brazn.entitlementkeys names, over a payload naming the mailbox, so the only
// party who can ask is the party that provisions.
func BraznProvision(c *echo.Context) error {
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, maxProvisioningBytes+1))
	if err != nil {
		return refuseUnverifiedProvisioning("the request body could not be read")
	}
	if len(raw) > maxProvisioningBytes {
		return refuseUnverifiedProvisioning("the request body is larger than any provisioning request")
	}

	// First, and before anything reads or writes stored state: an unverifiable
	// message must not be able to observe the instance, let alone change it.
	operation, payload, err := provisioning.Verify(raw)
	if err != nil {
		// The reason rather than the error text: the vocabulary is shared with
		// the entitlement channel and its error strings still say "entitlement
		// projection", which is not what was refused here.
		return refuseUnverifiedProvisioning("the envelope did not verify: " +
			string(entitlement.RefusalReason(err)))
	}

	switch operation {
	case provisioning.OperationCreateUser:
		return provisionUser(c, payload)
	case provisioning.OperationCreatePersonalInbox:
		return provisionPersonalInbox(c, payload)
	case provisioning.OperationCreateTeamRoots:
		return provisionTeamRoots(c, payload)
	case provisioning.OperationResolveMailbox:
		return resolveMailbox(c, payload)
	case provisioning.OperationEraseSubject:
		return eraseSubject(c, payload)
	case provisioning.OperationResolveUser:
		return resolveUser(c, payload)
	case provisioning.OperationRevokeSession:
		return revokeSession(c, payload)
	case provisioning.OperationCreateUserWithPassword:
		return provisionUserWithPassword(c, payload)
	case provisioning.OperationJoinTeam:
		return joinTeam(c, payload)
	case provisioning.OperationUsernameAvailable:
		return usernameAvailable(c, payload)
	default:
		// An operation this build does not define is refused rather than
		// guessed at, in exactly the way an unknown edition is on the
		// entitlement channel.
		return refuseProvisioning("the request names an operation this build does not define")
	}
}

// provisionUser is the create_user operation: the Brazn Tasks user for one
// mailbox, created or resolved as one step.
func provisionUser(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeCreateUser(payload)
	if err != nil {
		return refuseProvisioning("the create_user request is not one this build accepts")
	}

	u, created, err := models.CreateOrResolveUserForMailbox(c.Request().Context(), request.Email)
	if err != nil {
		// A refusal rather than a fault, so it answers like every other one and
		// says why in the log instead of the reply. It is not retryable and the
		// sender cannot fix it: two mailboxes resolve to one account here, and
		// only a human looking at both records can say which is whose.
		if errors.Is(err, models.ErrUserAlreadyProvisionedForAnotherMailbox) {
			return refuseProvisioning(
				"the mailbox resolves to a user another mailbox is already provisioned for")
		}
		return err
	}

	// The mailbox is deliberately absent from this line, as it is from every
	// log line on this seam.
	log.Debugf("Provisioned Brazn Tasks user %d (created: %t)", u.ID, created)

	return c.JSON(http.StatusOK, &provisionedUserReply{
		ID:            strconv.FormatInt(u.ID, 10),
		Created:       created,
		EmailVerified: u.Status != user.StatusEmailConfirmationRequired,
	})
}

// provisionUserWithPassword is the create_user_with_password operation
// (BRA-1335): a brand-new Brazn Tasks account for somebody who chose a
// username and a password at the commercial service checkout.
//
// IT NEVER ADOPTS, unlike provisionUser above. See
// models.CreateProvisionedUserWithPassword and
// models.ErrPasswordAccountEmailOrUsernameTaken for why a collision on the
// mailbox OR the username must refuse rather than resolve to whoever is
// already there: this operation always arrives with a password somebody
// chose, and adopting an existing account would either hand a stranger's
// account a caller's password or tell the caller an account was made for them
// that was really somebody else's.
func provisionUserWithPassword(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeCreateUserWithPassword(payload)
	if err != nil {
		return refuseProvisioning("the create_user_with_password request is not one this build accepts")
	}

	u, err := models.CreateProvisionedUserWithPassword(
		c.Request().Context(), request.Email, request.Username, request.Password)
	if err != nil {
		if errors.Is(err, models.ErrPasswordAccountEmailOrUsernameTaken) {
			return refuseProvisioning(
				"the create_user_with_password request names an email or username this instance already has")
		}
		return err
	}

	// Neither the mailbox nor the username on this line, as on every other
	// log line on this seam - and the password never reaches this function at
	// all past the call above, which is what makes "never logged" true by
	// construction rather than by remembering to leave it out here.
	log.Debugf("Provisioned a Brazn Tasks password account: user %d", u.ID)

	return c.JSON(http.StatusOK, &provisionedPasswordAccountReply{
		ID: strconv.FormatInt(u.ID, 10),
	})
}

// provisionPersonalInbox is the create_personal_inbox operation: one subject's
// protected Inbox, created or already there.
func provisionPersonalInbox(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeCreatePersonalInbox(payload)
	if err != nil {
		return refuseProvisioning("the create_personal_inbox request is not one this build accepts")
	}

	err = models.ProvisionPersonalInbox(c.Request().Context(), request.UserID)
	if err != nil {
		if errors.Is(err, models.ErrProvisioningSubjectUnknown) {
			return refuseProvisioning(
				"the create_personal_inbox request names a user this instance does not have")
		}
		return err
	}

	log.Debugf("Provisioned the Inbox for Brazn Tasks user %s", request.UserID)
	return c.JSON(http.StatusOK, &nothingToReport{})
}

// provisionTeamRoots is the create_team_roots operation: a commercial team's
// topology on this instance, and this instance's own references to it.
func provisionTeamRoots(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeCreateTeamRoots(payload)
	if err != nil {
		return refuseProvisioning("the create_team_roots request is not one this build accepts")
	}

	root, err := models.ProvisionTeamRoots(
		c.Request().Context(), request.UserID, request.OrganizationID, request.TeamID)
	if err != nil {
		if errors.Is(err, models.ErrProvisioningSubjectUnknown) {
			return refuseProvisioning(
				"the create_team_roots request names a user this instance does not have")
		}
		return err
	}

	log.Debugf("Provisioned the topology for organization %q: team %d, Team root %d",
		request.OrganizationID, root.TeamID, root.ProjectID)

	return c.JSON(http.StatusOK, &teamRootsReply{
		TaskTeamRef:    strconv.FormatInt(root.TeamID, 10),
		TaskProjectRef: strconv.FormatInt(root.ProjectID, 10),
	})
}

// usernameAvailable is the username_available operation: is this one exact name
// free right now?
//
// ⚠ THE LOG LINE NEVER CARRIES THE NAME, and that is the point of the seam
// rather than tidiness. Writing the value here would put a stream of candidate
// usernames - typed by somebody who is not signed in, at a rate the form
// chooses - into this instance's logs, which is a directory of attempted names
// assembled in exactly the place §5.1 forbids one. The status alone is enough
// to tell whether the operation is working.
func usernameAvailable(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeUsernameAvailable(payload)
	if err != nil {
		return refuseProvisioning("the username_available request is not one this build accepts")
	}

	status, err := models.CheckUsernameAvailability(request.Username)
	if err != nil {
		return err
	}

	log.Debugf("Answered a Brazn Tasks username availability question (status: %s)", status)

	return c.JSON(http.StatusOK, &usernameAvailability{Status: status})
}

// joinTeam is the join_team operation (BRA-1475): one subject, put into a team
// this instance has already provisioned.
//
// IT IS THE NINTH OPERATION ON THIS CHANNEL, and it added exactly what every
// one of the eight before it added: a constant, a payload type, a decoder and
// this case, touching nothing about authentication, the trust store, the route
// set or route-classification.json. That is the claim BraznProvision's own
// comment makes about its extension point, and this is the sixth arm to hold it.
//
// IT REFUSES AN UNKNOWN TEAM RATHER THAN CREATING ONE, which is the one decision
// here worth stating at the route as well as at the model. See
// models.ErrProvisioningTeamUnknown: creating the missing team would make the
// JOINING member its administrator, which is the ability an invitation
// deliberately withholds.
//
// The reply is `{}` like its creating siblings. There is nothing for the caller
// to read: the commercial record already holds this team's fork references, put
// there by create_team_roots, and a membership row has no identifier the
// commercial layer has any use for.
func joinTeam(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeJoinTeam(payload)
	if err != nil {
		return refuseProvisioning("the join_team request is not one this build accepts")
	}

	err = models.JoinProvisionedTeam(
		c.Request().Context(), request.UserID, request.OrganizationID, request.TeamID)
	if err != nil {
		if errors.Is(err, models.ErrProvisioningSubjectUnknown) {
			return refuseProvisioning(
				"the join_team request names a user this instance does not have")
		}
		if errors.Is(err, models.ErrProvisioningTeamUnknown) {
			return refuseProvisioning(
				"the join_team request names a team this instance has not provisioned")
		}
		return err
	}

	log.Debugf("Joined Brazn Tasks user %s to the team of organization %q, commercial team %q",
		request.UserID, request.OrganizationID, request.TeamID)

	return c.JSON(http.StatusOK, &nothingToReport{})
}

// resolveMailbox is the resolve_mailbox operation: the address a subject
// reaches, or the one answer every absence gets.
//
// IT ANSWERS 200 EITHER WAY, and that is a deliberate departure from the two
// topology operations above, which refuse an unknown subject with the flat 400.
// The 400 is right for them - the caller created that user moments earlier, so
// an unknown subject IS a producer defect. Here an absent subject is an
// expected, legitimate state: erasure suppresses the mailbox at step 4 and
// destroys the user at step 5, so a resumed erasure asks about a subject that is
// already gone.
//
// The consumer decides purely on status - cloud/service/src/fork.ts maps 5xx to
// a retryable `unavailable` and every other non-2xx to a terminal
// `invalid_state` - so folding "this subject has no mailbox" into the flat 400
// would make it indistinguishable from a malformed request, and every resumed
// erasure would refuse at step 4 forever, against a one-month statutory clock.
// Refusals keep the flat 400; unreachable keeps its 5xx; absence is an ANSWER.
//
// A read rather than a write, which is why nothing here is transactional and
// why no part of it may become one: the caller for step 4 of an erasure is
// asking what to suppress, not asking this instance to change.
func resolveMailbox(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeResolveMailbox(payload)
	if err != nil {
		return refuseProvisioning("the resolve_mailbox request is not one this build accepts")
	}

	mailbox, err := models.MailboxForSubject(request.UserID)
	if err != nil {
		return err
	}

	// The pair the request named and whether an address was found - never the
	// address itself, as on every other log line on this seam. Reporting
	// found-or-not discloses nothing the reply does not: it is resolved against
	// unresolvable, and not erased against never-minted, which this instance
	// could not tell apart if the line asked it to.
	log.Debugf("Resolved the mailbox for Brazn Tasks user %s in organization %q (found: %t)",
		request.UserID, request.OrganizationID, mailbox != "")

	if mailbox == "" {
		return c.JSON(http.StatusOK, noMailbox)
	}
	return c.JSON(http.StatusOK, &mailboxResolution{Result: "resolved", Email: mailbox})
}

// resolveUser is the resolve_user operation: is this person already a user
// here, and is their mailbox confirmed?
//
// IT IS THE SIXTH OPERATION ON THIS CHANNEL - after create_user, the two
// topology operations, resolve_mailbox and erase_subject - and it added exactly
// what each of those added: a constant, a payload type, a decoder and this case,
// touching nothing about authentication, the trust store, the route set or
// route-classification.json. That is the claim the comment on BraznProvision
// makes about its own extension point, and this is the fifth arm to hold it.
//
// ⚠ IT NEVER CREATES, AND NOTHING IT CALLS DOES. There is no write path in
// either request form and there must be none: a resolve that helpfully created
// the missing user would reintroduce the total signup outage BRA-1106 fixed,
// from this side of the seam. The response schema states it as an obligation
// the consumer cannot enforce, which is what makes it this handler's.
//
// ⚠ WHICH COLUMN THE MAILBOX FORM MATCHES is the one decision here that is easy
// to get backwards, because resolve_mailbox above answers the OPPOSITE way and
// is right to. models.ResolveUserByMailbox carries the argument and the address
// change it decides; the short version is that users.email cannot be a
// zero-or-one lookup and this operation answers with one id or nothing.
//
// IT ANSWERS 200 EITHER WAY, as resolve_mailbox does and for a sharper reason:
// an unknown address is the ordinary answer for EVERY FIRST-TIME CUSTOMER, on
// the busiest path the commercial service has. Folding it into the channel's
// flat 400 would make every signup indistinguishable from a malformed request
// and from an operation a deployed build does not define - and cloud/service/
// src/fork.ts decides purely on status, mapping every non-5xx refusal to a
// terminal invalid_state.
//
// ⚠ THE ORACLE. Unlike resolve_mailbox this request CARRIES an address, so
// reaching this function at all is a membership answer for whoever holds the
// signing key. Three things bound it, and the one that is this code's to keep
// is that the signature is verified BEFORE the lookup runs: provisioning.Verify
// runs above the switch, so an unverifiable caller reaches the flat 400 without
// this instance ever being asked about the address it named. That property is
// structural here rather than remembered, and the only way to lose it is to
// deviate from the four-part shape - a lookup hoisted out of the switch, or a
// short circuit for a "cheap" read. Do neither.
//
// Nothing below varies its STATUS with whether the address matched. Its timing
// varies by one indexed read - a resolution reads the user row and an absence
// does not - which is inherent in answering the question at all and is the same
// on resolve_mailbox; no constant-time mechanism is invented here, and claiming
// one would be worse than saying which property is actually held.
//
// A read rather than a write, which is why nothing here is transactional and
// why no part of it may become one.
func resolveUser(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeResolveUser(payload)
	if err != nil {
		return refuseProvisioning("the resolve_user request is not one this build accepts")
	}

	// Exactly one of the two, which the decoder has already established - both
	// and neither are refused there rather than resolved by a precedence this
	// branch would otherwise be inventing.
	var resolution *models.UserResolution
	if request.Email != "" {
		resolution, err = models.ResolveUserByMailbox(request.Email)
	} else {
		resolution, err = models.ResolveUserBySubject(request.UserID)
	}
	if err != nil {
		return err
	}

	// What the question was asked BY and whether it resolved - never the
	// address, as on every log line on this seam. The verification form logs
	// its subject the way resolve_mailbox logs its own; the recognition form
	// logs only that it was a mailbox, because the value it carries is a
	// customer's address and this is the operation that could leak one.
	//
	// Reporting found-or-not discloses nothing the reply does not: it is
	// resolved against unresolvable, and never erased against never-minted,
	// which this instance could not tell apart if the line asked it to.
	subject := "a mailbox"
	if request.Email == "" {
		subject = "user " + request.UserID
	}
	log.Debugf("Resolved a Brazn Tasks user by %s (found: %t)", subject, resolution != nil)

	if resolution == nil {
		return c.JSON(http.StatusOK, noUser)
	}
	return c.JSON(http.StatusOK, &resolvedUser{
		Result:        "resolved",
		UserID:        strconv.FormatInt(resolution.UserID, 10),
		EmailVerified: resolution.EmailVerified,
	})
}

// eraseSubject is the erase_subject operation: everything this instance holds
// for one commercial subject, destroyed.
//
// IT IS THE ONLY OPERATION ON THIS CHANNEL THAT DESTROYS ANYTHING, and the only
// one for which "there is nothing here" is a success rather than a refusal. The
// three creating operations answer ErrProvisioningSubjectUnknown with a 400
// because a topology cannot be built for a subject this instance does not have;
// erasure of a subject this instance does not have is exactly what a retry after
// a successful erasure looks like, and the consumer treats a 400 as permanent.
// See models.EraseSubject for why that would strand every resumed erasure.
//
// The reply is `{}` like its siblings, and carries no count. The consumer does
// not read it (cloud/service/src/fork.ts) and must not: a number of destroyed
// rows would be a fact about this schema for the commercial layer to depend on,
// and it would raise the one question the contract settles the other way - what
// a zero means.
func eraseSubject(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeEraseSubject(payload)
	if err != nil {
		return refuseProvisioning("the erase_subject request is not one this build accepts")
	}

	if err := models.EraseSubject(c.Request().Context(), request.UserID); err != nil {
		// Reached only for a subject id that is not a decimal number at all,
		// which no correct sender can produce - never for one that is merely
		// absent. models.EraseSubject draws that line and says why.
		if errors.Is(err, models.ErrProvisioningSubjectUnknown) {
			return refuseProvisioning(
				"the erase_subject request names a subject this instance could not have minted")
		}
		return err
	}

	// The subject id and the organization, and nothing else - no mailbox, as on
	// every log line on this seam. The organization is here because it is the
	// caller's statement of which subject it believed it was erasing, and this
	// is the line an erasure is read back from afterwards.
	log.Debugf("Erased Brazn Tasks subject %s for organization %q",
		request.UserID, request.OrganizationID)

	return c.JSON(http.StatusOK, &nothingToReport{})
}

// revokeSession is the revoke_session operation (BRA-1014): destroy one
// session, so the device it belongs to can no longer refresh into a new
// access token.
//
// A SESSION ALREADY GONE IS A SUCCESS, matching eraseSubject's own rule and
// for the same reason: the commercial service calls this before it marks its
// device-authorization row revoked (cloud/service/src/service.ts), so a retry
// after a response it lost must be able to commit rather than fail against a
// row this instance already removed.
//
// A SUBJECT THIS INSTANCE COULD NOT HAVE MINTED IS NOT THE SAME CASE, and
// answers a 400 rather than the success above. See
// models.RevokeSessionForSubject for why.
//
// The reply is `{}` like every other operation on this channel that destroys
// rather than reports - there is nothing here for the caller to read, and a
// count would be a fact about this schema the commercial layer must not come
// to depend on.
func revokeSession(c *echo.Context, payload json.RawMessage) error {
	request, err := provisioning.DecodeRevokeSession(payload)
	if err != nil {
		return refuseProvisioning("the revoke_session request is not one this build accepts")
	}

	if err := models.RevokeSessionForSubject(c.Request().Context(), request.UserID, request.SessionID); err != nil {
		// Reached only for a subject id that is not a decimal number at all,
		// which no correct sender can produce - never for one that is merely
		// absent. models.RevokeSessionForSubject draws that line and says why.
		if errors.Is(err, models.ErrProvisioningSubjectUnknown) {
			return refuseProvisioning(
				"the revoke_session request names a subject this instance could not have minted")
		}
		return err
	}

	// The subject and nothing else - no session id, which on every other
	// device that subject holds is exactly as sensitive as the one being
	// revoked, and this line is not the audit trail for which one that was.
	log.Debugf("Revoked a session for Brazn Tasks subject %s", request.UserID)

	return c.JSON(http.StatusOK, &nothingToReport{})
}

// refuseProvisioning logs why a verified request was turned down and returns
// the one reply every refusal gets. Error level, because a request this
// instance verified and then refused means the producer is broken - the same
// split refuseProjection and refuseUnverified make.
func refuseProvisioning(reason string) error {
	log.Errorf("Refused a provisioning request: %s", reason)
	return provisioningRefusal()
}

// refuseUnverifiedProvisioning is refuseProvisioning for everything decided
// before the signature is known good, at debug level: those are what any
// unauthenticated caller produces at will, and this route carries no rate limit
// under the shipped config.
func refuseUnverifiedProvisioning(reason string) error {
	log.Debugf("Refused an unverified provisioning request: %s", reason)
	return provisioningRefusal()
}

// provisioningRefusal is the one reply every refusal gets, whatever caused it.
func provisioningRefusal() error {
	return echo.NewHTTPError(http.StatusBadRequest,
		"This is not a provisioning request this instance accepts.")
}
