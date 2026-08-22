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
	"strings"

	"code.vikunja.io/api/pkg/config"
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

	project := &Project{Title: FeedbackProjectTitle}
	if err := project.Create(s, owner); err != nil {
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

		sub := &Project{Title: FeedbackProjectTitle, ParentProjectID: Ptr(rootID)}
		if err := sub.Create(s, owner); err != nil {
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

	sub := &Project{Title: FeedbackProjectTitle, ParentProjectID: Ptr(rootID)}
	if err := sub.Create(s, owner); err != nil {
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
