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

package models

import (
	"context"
	"errors"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/user"

	"xorm.io/xorm"
)

// The titles this instance gives the topology it provisions.
//
// THEY ARE LABELS AND NEVER IDENTITY, exactly as FeedbackProjectTitle is:
// nothing reads one back to decide anything, because every rule resolves a
// project's role through brazn_protected_entities and its immutable project id.
// A customer project carrying any of these words is an ordinary project.
//
// They are DEFAULTS chosen here rather than sent by the commercial service, and
// that is a deliberate ruling. The provisioning call carries a subject and a
// commercial team id and has no name to give - provisionTeamRoots has none
// either - so either the port grows a field or the fork picks. The fork picks,
// because a title is user-facing text: it is renameable in the product (a Team
// root may be renamed) and correcting a bad default costs an edit here rather
// than a contract change on both sides of the seam.
const (
	InboxProjectTitle = "Inbox"
	// PrimaryTeamTitle names both the team and its Team root, the way
	// CreateOrganizationTeam does for an additional team: one name, so the
	// Teams page and the project tree do not disagree about what a team is
	// called.
	PrimaryTeamTitle = "Team"
	// PublicRootTitle is the product's own word for it (Brazn-Tasks-Rules.md
	// section 3.4), and it is the one part of the topology whose name is load
	// bearing for a customer: Public is where anonymous read-only links may
	// exist, so anything put there can leave the organization.
	PublicRootTitle = "Public"
)

// ErrProvisioningSubjectUnknown means a provisioning request named a user this
// instance does not have.
//
// It is a REFUSAL rather than a fault: retrying the identical message cannot
// help, because nothing about it will conjure the account. The commercial
// service creates the user through create_user before it provisions anything
// for them, so reaching this means the two calls disagree about who exists -
// which is the producer's to fix and not something this fork may paper over by
// creating an account nobody asked for.
var ErrProvisioningSubjectUnknown = errors.New("the provisioning request names a user this instance does not have")

// ErrProvisioningTeamUnknown means a join_team request named a team this
// instance has not provisioned for that organization.
//
// It is a REFUSAL rather than a fault, and it is deliberately NOT repaired here
// by creating the team. provisionTeamRoots makes its subject the team's creator
// and a team ADMIN, so a join that helpfully created the missing team would hand
// the joining member the team-management ability the whole invitation design
// withholds from them. The commercial service creates a team's roots through
// create_team_roots, naming the organization's administrator as the subject, and
// reaching this means that call has not happened yet - which is the producer's
// to resolve in the right order rather than this fork's to guess at.
var ErrProvisioningTeamUnknown = errors.New("the provisioning request names a team this instance has not provisioned")

// JoinProvisionedTeam puts one subject into a team this instance has already
// provisioned, or does nothing when they are already in it.
//
// THIS IS WHAT MAKES SHARED PROJECTS REACHABLE, and nothing else does.
// grantTeamAccess gives the TEAM administrative access to the Team root and to
// the organization's Public root; a person's access to either is entirely a
// consequence of the team_members row this writes. Somebody holding a complete,
// active Teams entitlement and no such row sees an empty product.
//
// THE TEAM IS RESOLVED FROM THE (ORGANIZATION, COMMERCIAL TEAM) PAIR and never
// from a fork team id the caller names. That is the difference between joining a
// team an administrator invited somebody to and joining any team on the
// instance: the pair has to match a row create_team_roots wrote, so a request
// naming another organization's team resolves to nothing and is refused rather
// than crossing between customers.
//
// THE MEMBER IS NEVER AN ADMIN. TeamMember.Admin decides who may add and remove
// members and toggle other members' admin status, and an invited member is
// precisely the person who must not be able to add themselves anywhere. The
// field is written false explicitly rather than left to Go's zero value, because
// what it means is a rule rather than an absence.
//
// IT INSERTS THE ROW DIRECTLY RATHER THAN THROUGH TeamMember.Create, for two
// reasons that are both about what that method does beyond the insert. It
// resolves the member by USERNAME, and a provisioning request carries this
// instance's own users.id - looking a person up by name to reach an id the
// caller already holds would be a name lookup on a channel that has no business
// performing one (docs/Brazn-Tasks-Rules.md section 5.1). And it dispatches
// TeamMemberAddedEvent, which announces a new colleague to whatever is
// listening; an invitation acceptance is not the moment to mail a team, and
// nothing asked for that.
//
// A REPEAT IS A SUCCESS, as it is for every creating operation on this channel.
// The commercial service calls this after an admission it may retry, so a second
// call for somebody already in the team must commit rather than refuse.
func JoinProvisionedTeam(ctx context.Context, subject, organizationID, teamID string) error {
	return provisionInTransaction(ctx, func(s *xorm.Session) error {
		u, err := provisioningSubject(s, subject)
		if err != nil {
			return err
		}

		root, err := provisionedTeamRoot(s, organizationID, teamID)
		if err != nil {
			return err
		}
		if root == nil {
			return ErrProvisioningTeamUnknown
		}

		member := &TeamMember{}
		has, err := s.Where("team_id = ? AND user_id = ?", root.TeamID, u.ID).Get(member)
		if err != nil || has {
			return err
		}

		_, err = s.Insert(&TeamMember{TeamID: root.TeamID, UserID: u.ID, Admin: false})
		return err
	})
}

// ProvisionPersonalInbox creates the protected Inbox for one subject, or does
// nothing when they already have one.
//
// IDEMPOTENCE IS DECIDED ON THE PROTECTED ROW, not on the title and not on the
// user's default project. Identity in the managed topology is the immutable
// project id plus its kind (see ProtectedKind), and both alternatives are
// things a customer can change: two members may each call a project "Inbox",
// and default_project_id is a user setting. Deciding on either would let a
// customer's own choices determine which project becomes permanently protected.
//
// The consequence, stated because it is not free: an account that arrived here
// by adoption - one Google sign-in created on the development instance before
// managed mode existed - has an Inbox project that was never registered, and
// this creates a second one rather than adopting it. Those accounts are
// BRA-1021's to settle deliberately, and adopting whatever project they happen
// to point default_project_id at would be a guess this cannot check.
func ProvisionPersonalInbox(ctx context.Context, subject string) error {
	return provisionInTransaction(ctx, func(s *xorm.Session) error {
		u, err := provisioningSubject(s, subject)
		if err != nil {
			return err
		}
		return ensurePersonalInbox(s, u)
	})
}

// ProvisionTeamRoots creates a commercial team's topology - the team, its Team
// root and the organization's Public root - and returns the Team root's
// protected entity, whose TeamID and ProjectID are the references the caller
// answers with.
//
// IT IS NOT GATED ON SEAT CAPACITY, unlike CreateOrganizationTeam, and that is
// the difference between the two. This provisions the organization's PRIMARY
// team, which is part of what was bought rather than an addition to it: a seat
// rule that refused it would refuse the first team of every organization whose
// projection had not arrived yet, and leave a paying customer with nowhere at
// all to work. Capacity governs teams an administrator ADDS, and that route
// still enforces it.
//
// A REPEAT ANSWERS WITH THE FIRST CALL'S REFERENCES rather than minting fresh
// ones. The commercial record coalesces a team's pair in exactly once, so a
// second set of roots would not merely be untidy - they would be unreferenced,
// permanently, while every log line said the topology was provisioned.
func ProvisionTeamRoots(ctx context.Context, subject, organizationID, teamID string) (
	*ProtectedEntity, error,
) {
	var root *ProtectedEntity
	err := provisionInTransaction(ctx, func(s *xorm.Session) error {
		var err error
		root, err = provisionTeamRoots(s, subject, organizationID, teamID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return root, nil
}

// provisionInTransaction runs one provisioning operation in its own
// transaction, because the channel's caller has no session to lend it.
//
// Events queued by a rolled-back operation are discarded rather than
// dispatched: half a topology must not announce itself as a created project to
// everything listening.
func provisionInTransaction(ctx context.Context, do func(s *xorm.Session) error) error {
	s := db.NewSession()
	defer s.Close()
	defer events.CleanupPending(s)

	if err := do(s); err != nil {
		_ = s.Rollback()
		return err
	}
	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return err
	}

	events.DispatchPending(ctx, s)
	return nil
}

// provisioningSubject resolves the local user a provisioning request names.
//
// The subject is this instance's own users.id in decimal form. It is decided on
// the commercial side, once, when the account is created, and nothing here
// derives it from anything else.
//
// A DISABLED OR LOCKED ACCOUNT IS STILL THAT SUBJECT, which is why a status
// error is not treated as absence - the same reading userByID takes on the
// create_user path. Refusing to provision an Inbox for a locked account would
// leave the topology incomplete for someone whose account is later unlocked.
//
// See parseSubjectID: a leading zero ("01") parses to the same id a correct
// sender's bare form ("1") would, so refusing only id <= 0 would provision
// topology for that aliased, unintended account instead of refusing the
// malformed request.
func provisioningSubject(s *xorm.Session, subject string) (*user.User, error) {
	id, ok := parseSubjectID(subject)
	if !ok {
		return nil, ErrProvisioningSubjectUnknown
	}

	u, err := user.GetUserByID(s, id)
	if err != nil {
		if user.IsErrUserDoesNotExist(err) {
			return nil, ErrProvisioningSubjectUnknown
		}
		if !user.IsErrUserStatusError(err) {
			return nil, err
		}
	}
	return u, nil
}

// ensurePersonalInbox gives u a protected Inbox unless they already have one.
func ensurePersonalInbox(s *xorm.Session, u *user.User) error {
	existing, err := personalInbox(s, u.ID)
	if err != nil || existing != nil {
		return err
	}

	inbox := &Project{Title: InboxProjectTitle}
	if err := inbox.Create(s, u); err != nil {
		return err
	}
	if err := RegisterProtectedProject(s, ProtectedKindInbox, inbox.ID, 0); err != nil {
		return err
	}

	if u.DefaultProjectID != 0 {
		return nil
	}
	// One column, by id, rather than user.UpdateUser - and the difference is not
	// style. UpdateUser writes baseUserUpdateColumns, which includes `email`,
	// and user.GetUserByID deliberately returns a user with Email blanked. So
	// handing this struct to UpdateUser would erase the mailbox: the one value
	// this entire seam identifies a customer by, and the one thing
	// CreateOrResolveUserForMailbox can never recover from.
	_, err = s.ID(u.ID).Cols("default_project_id").
		Update(&user.User{DefaultProjectID: inbox.ID})
	return err
}

// personalInbox returns the protected Inbox this user owns, or nil.
//
// The owner is what makes it theirs: brazn_protected_entities records the kind
// and the project, and a project's owner_id is the only place "whose Inbox is
// this" is written down.
func personalInbox(s *xorm.Session, userID int64) (*ProtectedEntity, error) {
	inbox := &ProtectedEntity{}
	has, err := s.Where(
		"kind = ? AND project_id IN (SELECT id FROM projects WHERE owner_id = ?)",
		string(ProtectedKindInbox), userID).Get(inbox)
	if err != nil || !has {
		return nil, err
	}
	return inbox, nil
}

// provisionTeamRoots is ProvisionTeamRoots inside the caller's transaction, so
// the team, both roots and their registrations either all exist or none do.
//
// A team WITHOUT its root is the failure this ordering guards against, the same
// one CreateOrganizationTeam names: every placement rule asks whether a project
// sits beneath a Team or Public root, so a team whose root was never created is
// a team inside which nothing can be created.
func provisionTeamRoots(s *xorm.Session, subject, organizationID, teamID string) (
	*ProtectedEntity, error,
) {
	// The subject is resolved before the idempotence check rather than after,
	// so a request naming a user this instance does not have is refused whether
	// or not the topology it asks for happens to exist already. An operation
	// that answered 200 to a subject it could not resolve would report success
	// for a customer it knows nothing about.
	admin, err := provisioningSubject(s, subject)
	if err != nil {
		return nil, err
	}

	existing, err := provisionedTeamRoot(s, organizationID, teamID)
	if err != nil || existing != nil {
		return existing, err
	}

	team := &Team{Name: PrimaryTeamTitle}
	if err := team.CreateNewTeam(s, admin, true); err != nil {
		return nil, err
	}

	root := &Project{Title: PrimaryTeamTitle}
	if err := root.Create(s, admin); err != nil {
		return nil, err
	}

	// Inserted here rather than through RegisterProtectedProjectForOrganization
	// because that function cannot carry the commercial team id, and because
	// its idempotence has nothing to do here: the project was minted two lines
	// above, so it cannot already hold a role. What must be idempotent is the
	// OPERATION, and provisionedTeamRoot above is where that is decided.
	entity := &ProtectedEntity{
		Kind:             ProtectedKindTeamRoot,
		ProjectID:        root.ID,
		TeamID:           team.ID,
		OrganizationID:   organizationID,
		CommercialTeamID: teamID,
	}
	if _, err := s.Insert(entity); err != nil {
		return nil, err
	}
	if err := grantTeamAccess(s, admin, team.ID, root.ID); err != nil {
		return nil, err
	}

	if err := ensurePublicRoot(s, admin, organizationID, team.ID); err != nil {
		return nil, err
	}
	return entity, nil
}

// provisionedTeamRoot returns the Team root already provisioned for this
// commercial team, or nil.
//
// Both halves of the key are required. The organization alone would match
// another team's root; the commercial team id alone is minted by a service this
// fork does not own the namespace of, and scoping to the organization is what
// keeps one customer's provisioning from ever resolving to another's topology.
func provisionedTeamRoot(s *xorm.Session, organizationID, teamID string) (*ProtectedEntity, error) {
	root := &ProtectedEntity{}
	has, err := s.Where("kind = ? AND organization_id = ? AND commercial_team_id = ?",
		string(ProtectedKindTeamRoot), organizationID, teamID).Get(root)
	if err != nil || !has {
		return nil, err
	}
	return root, nil
}

// ensurePublicRoot gives the organization its Public root if it has none, and
// gives the named team access to it either way.
//
// ONE PER ORGANIZATION, not one per team (Brazn-Tasks-Rules.md section 3.4).
// Public is where the organization's shared work lives and the only place an
// anonymous read-only link may exist, so a second one would split it in two and
// neither half would be "the" Public root that decideTeamsLinkShare admits.
// That is why it is keyed on the organization and carries no team.
func ensurePublicRoot(s *xorm.Session, admin *user.User, organizationID string, teamID int64) error {
	existing := &ProtectedEntity{}
	has, err := s.Where("kind = ? AND organization_id = ?",
		string(ProtectedKindPublicRoot), organizationID).Get(existing)
	if err != nil {
		return err
	}
	if has {
		return grantTeamAccess(s, admin, teamID, existing.ProjectID)
	}

	public := &Project{Title: PublicRootTitle}
	if err := public.Create(s, admin); err != nil {
		return err
	}
	_, err = s.Insert(&ProtectedEntity{
		Kind:           ProtectedKindPublicRoot,
		ProjectID:      public.ID,
		OrganizationID: organizationID,
	})
	if err != nil {
		return err
	}
	return grantTeamAccess(s, admin, teamID, public.ID)
}

// grantTeamAccess gives a team administrative access to a root, treating access
// it already holds as success.
//
// TeamProject.Create refuses a duplicate rather than ignoring it, and this runs
// on a path whose whole contract is that repeating it is safe.
func grantTeamAccess(s *xorm.Session, admin *user.User, teamID, projectID int64) error {
	access := &TeamProject{TeamID: teamID, ProjectID: projectID, Permission: PermissionAdmin}
	err := access.Create(s, admin)
	if IsErrTeamAlreadyHasAccess(err) {
		return nil
	}
	return err
}
