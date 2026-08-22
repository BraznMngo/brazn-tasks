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
	"strings"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/user"

	"xorm.io/xorm"
)

// FeedbackProjectTitle is what this instance calls Percy Feedback when it
// creates it.
//
// IT IS A LABEL AND NEVER AN IDENTITY. Nothing reads it back to decide
// anything: the exemption is bound to a brazn_protected_entities row keyed by
// the immutable project id, so a customer project carrying this exact title is
// an ordinary project and is refused like one. See ProtectedKind for why a
// title cannot be identity - it is neither unique nor stable, and a policy that
// trusted one would hand the exemption to whoever typed the right words.
const FeedbackProjectTitle = "Percy Feedback"

// FeedbackProject returns the protected entity for this instance's single Percy
// Feedback project, or nil when none has been provisioned.
//
// There is at most one row and ensureFeedbackProject is what keeps it that way:
// it looks here before creating anything, so a second call finds the first
// project instead of making a second one.
//
// A general "the protected entity of kind X" lookup is deliberately not offered
// alongside this. Inbox has one row per member and Team root one per team, so
// such a helper would return an arbitrary row for two of the four kinds and be
// wrong the first time anyone reached for it.
func FeedbackProject(s *xorm.Session) (*ProtectedEntity, error) {
	protected := &ProtectedEntity{}
	has, err := s.Where("kind = ?", string(ProtectedKindFeedback)).Get(protected)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return protected, nil
}

// ProvisionFeedbackAccess makes sure this instance has its Percy Feedback
// project and that u can submit into a project of their own beneath it,
// returning that sub-project's id.
//
// It mirrors the Inbox where the Inbox's reasoning carries over and departs
// from it where it does not. Like the Inbox it is created at the single point
// every account passes through (CreateNewProjectForUser) and bound to managed
// mode by its immutable id, because that is the only place the binding can be
// made reliably. Unlike the Inbox there is ONE root for the whole instance
// rather than one per account - but each account is given its own PROJECT
// beneath that root, rather than being enrolled into a project shared with
// every other account (BRA-1180/A1). See ensureFeedbackSubProject for why.
//
// THE ROOT ITSELF STAYS WHAT KEEPS THE PERSONAL EDITION HONEST. Every
// sub-project this returns is owned by the same Brazn account as the root,
// never by the customer, so "a personal account has exactly one
// customer-owned project" stays literally true - the second project in their
// list is not theirs.
//
// A missing or unresolvable owner is not an error. brazn.feedbackowner names a
// Brazn staff account, and an instance where the operator has not created it
// yet must still be able to register customers. Skipping is also the safe
// direction to be wrong in: no project means no access, where failing here
// would mean no account. Returns 0 in that case.
//
// Enrolment is Write, which is the least permission that can file a task. Read
// could submit nothing and Admin would hand the project to the customer.
func ProvisionFeedbackAccess(s *xorm.Session, u *user.User) (int64, error) {
	owner, err := feedbackOwner(s)
	if err != nil || owner == nil {
		return 0, err
	}

	rootID, err := ensureFeedbackProject(s, owner)
	if err != nil {
		return 0, err
	}

	return ensureFeedbackSubProject(s, rootID, owner, u)
}

// feedbackRootMarker is the fixed value every row of brazn_feedback_root
// carries in its Marker column - there is nothing else to make it unique
// against, since the whole point of the table is that only one root may
// exist across the instance.
const feedbackRootMarker = 1

// feedbackRootConstraint and feedbackReporterConstraint are the fragments
// every supported database puts in its unique-violation message for the two
// claim tables below - see provisionedMailboxConstraint (brazn_provisioning.go)
// for why this is a substring match rather than a typed error.
//
// NEITHER CLAIM'S UNIQUE COLUMN IS ITS PRIMARY KEY, deliberately, matching
// ProvisionedUser's own Email (not its autoincrement ID). MySQL and MariaDB
// name a duplicate-PRIMARY-KEY violation only "for key 'PRIMARY'" - the table
// name IsUniqueConstraintError's MySQL branch requires never appears - while a
// separately named unique index's violation carries it. A PRIMARY KEY claim
// would compile, pass on SQLite and PostgreSQL, and silently stop the retry
// from ever firing on MySQL and MariaDB.
const (
	feedbackRootConstraint     = "brazn_feedback_root"
	feedbackReporterConstraint = "brazn_feedback_reporters"
)

// FeedbackRootClaim is the atomic claim on "the" Percy Feedback root project.
// Marker is always feedbackRootMarker, so a second concurrent INSERT collides
// on that unique column instead of racing ensureFeedbackProject's own
// read-then-create - see ProvisionFeedbackAccessRetrying for who relies on
// that collision.
type FeedbackRootClaim struct {
	ID     int64 `xorm:"bigint autoincr not null unique pk"`
	Marker int64 `xorm:"bigint not null unique"`
	// ProjectID is 0 for the moment between the claim being taken and the
	// project it names existing - the same reason ProvisionedUser.UserID
	// starts at 0, so the claim can be taken before the expensive half of
	// provisioning ever runs.
	ProjectID int64     `xorm:"bigint not null default 0"`
	Created   time.Time `xorm:"created not null"`
}

// TableName is the table name for the Percy Feedback root claim.
func (FeedbackRootClaim) TableName() string { return "brazn_feedback_root" }

// FeedbackReporterClaim is the same atomic claim, one row per reporter,
// keyed by UserID rather than a fixed constant.
type FeedbackReporterClaim struct {
	ID        int64     `xorm:"bigint autoincr not null unique pk"`
	UserID    int64     `xorm:"bigint not null unique"`
	ProjectID int64     `xorm:"bigint not null default 0"`
	Created   time.Time `xorm:"created not null"`
}

// TableName is the table name for Percy Feedback reporter claims.
func (FeedbackReporterClaim) TableName() string { return "brazn_feedback_reporters" }

// maxFeedbackProvisioningAttempts and feedbackProvisioningRetryDelay mirror
// maxMailboxProvisioningAttempts and mailboxProvisioningRetryDelay
// (brazn_provisioning.go) for the same reason: the race this waits out is one
// request's own commit or rollback, not a human process.
const maxFeedbackProvisioningAttempts = 3
const feedbackProvisioningRetryDelay = 50 * time.Millisecond

// ProvisionFeedbackAccessRetrying is ProvisionFeedbackAccess for a caller that can be asked for the same account's feedback access more than once, including concurrently - the on-demand lookup route (BRA-1414) and the commercial create_personal_inbox channel; CreateNewProjectForUser's registration-time call keeps using ProvisionFeedbackAccess directly, since CreateOrResolveUserForMailbox already serialises that call upstream.
func ProvisionFeedbackAccessRetrying(ctx context.Context, u *user.User) (int64, error) {
	// Every self-hosted or pre-rollout instance takes this path, so it is
	// checked before opening a session at all rather than inside one.
	if strings.TrimSpace(config.BraznFeedbackOwner.GetString()) == "" {
		return 0, nil
	}

	var lastErr error
	for attempt := 0; attempt < maxFeedbackProvisioningAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(feedbackProvisioningRetryDelay):
			}
		}
		projectID, lostRace, err := provisionFeedbackAccessOnce(ctx, u)
		if err == nil || !lostRace {
			return projectID, err
		}
		lastErr = err
	}
	return 0, lastErr
}

// provisionFeedbackAccessOnce is one attempt at ProvisionFeedbackAccessRetrying,
// reporting whether it lost a race a retry can recover from.
func provisionFeedbackAccessOnce(ctx context.Context, u *user.User) (int64, bool, error) {
	s := db.NewSession()
	defer s.Close()
	defer events.CleanupPending(s)

	projectID, err := ProvisionFeedbackAccess(s, u)
	if err != nil {
		_ = s.Rollback()
		if db.IsUniqueConstraintError(err, feedbackRootConstraint) ||
			db.IsUniqueConstraintError(err, feedbackReporterConstraint) {
			return 0, true, err
		}
		return 0, false, err
	}

	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return 0, false, err
	}

	events.DispatchPending(ctx, s)
	return projectID, false, nil
}

// feedbackOwner resolves the Brazn account that owns Percy Feedback, or nil
// when this instance has not been told about one.
//
// The config names the OWNER, not the project. Which project is Percy Feedback
// is answered by the protected-entity row and by nothing else, so renaming or
// repointing this key cannot move the exemption onto another project.
func feedbackOwner(s *xorm.Session) (*user.User, error) {
	username := strings.TrimSpace(config.BraznFeedbackOwner.GetString())
	if username == "" {
		return nil, nil
	}

	owner, err := user.GetUserByUsername(s, username)
	if err != nil {
		if user.IsErrUserDoesNotExist(err) {
			log.Warningf("brazn.feedbackowner names %q, which is not an account on this instance; Percy Feedback was not provisioned",
				username)
			return nil, nil
		}
		return nil, err
	}
	return owner, nil
}

// ensureFeedbackProject returns the id of this instance's Percy Feedback
// project, creating and registering it on first use.
//
// The lookup comes first and is what makes this idempotent. Creating the
// project and recording its role are two statements, so a version that created
// first would leave an untracked second project behind on every call - and the
// managed rules would treat that one as an ordinary customer project, which is
// exactly the leak the single-project exemption exists to prevent.
//
// It returns the id rather than the entity so the caller has nothing to
// dereference. An entity return would be nil in one unreachable case, and the
// guard for it would be a branch no test could ever reach.
func ensureFeedbackProject(s *xorm.Session, owner *user.User) (int64, error) {
	existing, err := FeedbackProject(s)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return existing.ProjectID, nil
	}

	// The claim is taken before the project it will name even exists - the
	// same order CreateOrResolveUserForMailbox claims a mailbox before
	// creating its user. A second concurrent caller that also read "no root
	// yet" above collides on brazn_feedback_root's unique marker here, before
	// creating anything it would have to undo.
	claim := &FeedbackRootClaim{Marker: feedbackRootMarker}
	if _, err := s.Insert(claim); err != nil {
		return 0, err
	}

	project := &Project{Title: FeedbackProjectTitle}
	if err := project.Create(s, owner); err != nil {
		return 0, err
	}
	if _, err := s.ID(claim.ID).Cols("project_id").
		Update(&FeedbackRootClaim{ProjectID: project.ID}); err != nil {
		return 0, err
	}
	if err := RegisterProtectedProject(s, ProtectedKindFeedback, project.ID, 0); err != nil {
		return 0, err
	}
	return project.ID, nil
}

// ensureFeedbackSubProject returns the id of u's own project beneath the
// Percy Feedback root, creating it and granting u Write on first use.
//
// ONE SUB-PROJECT PER REPORTER IS THE WHOLE OF THEIR ISOLATION (BRA-1180/A1).
// Vikunja's permissions are project-wide with no per-task layer, so "each
// reporter reaches their own submissions and no others" cannot be expressed
// inside one project shared by every reporter - it has to be a project of its
// own. THIS GRANTS NO PER-REPORTER ISOLATION was the warning this replaces;
// per-reporter isolation is now structural rather than something downstream
// must not assume.
//
// NESTING UNDER THE ROOT, RATHER THAN MAKING EACH A SECOND TOP-LEVEL PROJECT,
// IS WHAT GIVES THE OWNER A TRIAGE VIEW WITHOUT A SECOND GRANT PER REPORTER.
// Project permission checks already walk a project's ancestors to resolve the
// acting user's level (the same mechanism Teams topology relies on), so Admin
// on the root - which the owner holds by owning it - is Admin on every child
// with no membership row of its own. See ProtectedRootOf for the same walk
// applied to the managed-topology check that keeps this the only project a
// personal account may move a task into.
//
// NOT OWNED BY u, deliberately - matching the root, and for the reason
// ProvisionFeedbackAccess gives: "a personal account has exactly one
// customer-owned project" (their Inbox) must stay true. u holds a Write
// membership and nothing else; sub.Create(s, owner) is what keeps ownership on
// the Brazn account rather than the reporter creating it.
//
// SAME DISPLAY TITLE AS THE ROOT, deliberately, so a client still finding
// "the" Percy Feedback project by title - the only way anything outside this
// package did so before GET /brazn/feedback/project (BRA-1414) existed -
// keeps working unmodified: every reporter's own sub-project is the one
// project so named that they can see.
//
// The lookup is the idempotence: CreateNewProjectForUser runs this on every
// registration attempt an account makes, and a repeat must find the
// sub-project already made rather than growing a second one.
//
// THE OWNER REGISTERING THEIR OWN ACCOUNT TAKES A SEPARATE PATH, because for
// them the join below can never find a row to make idempotence work.
// ProjectUser.Create refuses to add a project's own owner as a member of it
// (l.OwnerID == lu.UserID, checked before any insert) - which is exactly
// right for every other reporter's sub-project, where the owner already
// holds Admin by ownership, but it means the owner's OWN sub-project never
// gets a users_projects row for the join to find. Without this branch, every
// repeat call for the owner's account would find nothing and create a second
// sub-project.
func ensureFeedbackSubProject(s *xorm.Session, rootID int64, owner, u *user.User) (int64, error) {
	if u.ID == owner.ID {
		existing := &Project{}
		has, err := s.Where("parent_project_id = ? AND owner_id = ?", rootID, owner.ID).Get(existing)
		if err != nil {
			return 0, err
		}
		if has {
			return existing.ID, nil
		}

		// Claimed before creation for the same reason ensureFeedbackProject's
		// root claim is: a second concurrent call for this SAME reporter
		// collides on brazn_feedback_reporters' unique user_id here, rather
		// than both creating a sub-project.
		claim := &FeedbackReporterClaim{UserID: u.ID}
		if _, err := s.Insert(claim); err != nil {
			return 0, err
		}

		sub := &Project{Title: FeedbackProjectTitle, ParentProjectID: Ptr(rootID)}
		if err := sub.Create(s, owner); err != nil {
			return 0, err
		}
		if _, err := s.ID(claim.ID).Cols("project_id").
			Update(&FeedbackReporterClaim{ProjectID: sub.ID}); err != nil {
			return 0, err
		}
		return sub.ID, nil
	}

	existing := &Project{}
	has, err := s.
		Join("INNER", "users_projects", "users_projects.project_id = projects.id").
		Where("projects.parent_project_id = ? AND users_projects.user_id = ?", rootID, u.ID).
		Get(existing)
	if err != nil {
		return 0, err
	}
	if has {
		return existing.ID, nil
	}

	claim := &FeedbackReporterClaim{UserID: u.ID}
	if _, err := s.Insert(claim); err != nil {
		return 0, err
	}

	sub := &Project{Title: FeedbackProjectTitle, ParentProjectID: Ptr(rootID)}
	if err := sub.Create(s, owner); err != nil {
		return 0, err
	}
	if _, err := s.ID(claim.ID).Cols("project_id").
		Update(&FeedbackReporterClaim{ProjectID: sub.ID}); err != nil {
		return 0, err
	}

	member := &ProjectUser{
		ProjectID:  sub.ID,
		Username:   u.Username,
		Permission: PermissionWrite,
	}
	if err := member.Create(s, owner); err != nil && !IsErrUserAlreadyHasAccess(err) {
		return 0, err
	}
	return sub.ID, nil
}
