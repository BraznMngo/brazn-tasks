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

package routes

import (
	"net/http"
	"strings"

	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"
)

// The Teams edition's policy table (BRA-787).
//
// A Teams organization has a shape: one private Inbox per member, one Public
// root for the organization, one root per team. Everything a customer creates
// hangs beneath a Team or Public root, which is what makes an unmanaged
// top-level project impossible rather than merely discouraged.
//
// The roots are not equivalent to each other. A team may name its own root;
// the Inbox and the Public root may not be renamed, because every member
// navigates by them. Only Public and its descendants can be handed to the
// anonymous internet. And an Inbox belongs to exactly one member: nothing in
// this table, and no administrator flag anywhere, opens someone else's.
func init() {
	teams := entitlement.EditionTeams

	registerEditionRule(ruleProjectCreate, teams, decideTeamsProjectCreate)
	registerEditionRule(ruleProjectDuplicate, teams, decideTeamsProjectDuplicate)
	registerEditionRule(ruleProjectUpdate, teams, decideTeamsProjectUpdate)
	registerEditionRule(ruleProjectDelete, teams, decideTeamsProjectDelete)
	registerEditionRule(ruleProjectShare, teams, decideTeamsProjectShare)
	registerEditionRule(ruleLinkShare, teams, decideTeamsLinkShare)
	registerEditionRule(ruleTeamsOnly, teams, decideTeamsMembership)
	registerEditionRule(ruleTaskMove, teams, decideTeamsTaskMove)
}

// decideTeamsProjectCreate requires a home. Creating, duplicating and importing
// all end in the same question - where would this live - and a project with no
// authorized root has no answer to it.
func decideTeamsProjectCreate(e *managedEval) error {
	return e.requireManagedParent(statedParentID(e.requestBody()))
}

// decideTeamsProjectDuplicate additionally refuses to copy a protected root.
// A duplicate of the Public root would be a second Public root wearing a
// different id, which is exactly the ambiguity the protected-entity mapping
// exists to prevent.
func decideTeamsProjectDuplicate(e *managedEval) error {
	source, err := models.GetProtectedEntityForProject(e.s, e.projectID())
	if err != nil {
		return e.refuse("the project being duplicated could not be resolved")
	}
	if source != nil {
		return e.refuse("a protected root cannot be duplicated")
	}
	return e.requireManagedParent(statedParentID(e.requestBody()))
}

// decideTeamsProjectUpdate lets teams name their own root and manage their own
// subprojects, and refuses everything that would move a structure out from
// under the topology.
//
// The question it asks is about the end state, not the intent: wherever this
// project sits once the request has been applied, that place must be beneath a
// Team or Public root. That covers a move, a rename that would detach as a
// side effect, and a request that changes neither - one check instead of three
// that have to agree with each other.
func decideTeamsProjectUpdate(e *managedEval) error {
	target := e.projectID()

	protected, err := models.GetProtectedEntityForProject(e.s, target)
	if err != nil {
		return e.refuse("the target project could not be resolved")
	}

	current, err := models.GetProjectSimpleByID(e.s, target)
	if err != nil {
		return e.refuse("the target project could not be resolved")
	}
	currentParent := int64(0)
	if current.ParentProjectID != nil {
		currentParent = *current.ParentProjectID
	}
	wanted := e.requestedParentID(currentParent)

	if protected != nil {
		if wanted != currentParent {
			return e.refuse("a protected root cannot be moved")
		}
		// A team may name its own root. The Inbox and the Public root may not
		// be renamed: every member navigates by those names.
		if protected.Kind == models.ProtectedKindTeamRoot {
			return nil
		}
		return e.refuse("the Inbox, the Public root and Percy Feedback cannot be renamed")
	}

	return e.requireManagedParent(wanted)
}

// decideTeamsProjectDelete lets a team remove its own work and refuses to
// remove the structures the product guarantees. A protected root is dismantled
// by the commercial service, if at all - not by an ordinary DELETE that happens
// to arrive with admin rights.
func decideTeamsProjectDelete(e *managedEval) error {
	target := e.projectID()

	protected, err := models.GetProtectedEntityForProject(e.s, target)
	if err != nil {
		return e.refuse("the target project could not be resolved")
	}
	if protected != nil {
		return e.refuse("a protected root cannot be deleted")
	}
	return e.requireManagedRoot(target)
}

// decideTeamsProjectShare confines sharing to the collaborative part of the
// topology and to accounts that belong there.
//
// requireManagedRoot is what keeps an Inbox out of reach: an Inbox's root is
// the Inbox, so no share can ever be attached to one, and an organization
// administrator gets the same refusal a colleague does.
func decideTeamsProjectShare(e *managedEval) error {
	if err := e.requireManagedRoot(e.projectID()); err != nil {
		return err
	}
	return e.requireEntitledTarget()
}

// decideTeamsLinkShare allows anonymous read-only links, and only beneath the
// organization's Public root. Public is the one part of the topology the
// organization has decided is public; a team's work is not, and an Inbox
// certainly is not.
func decideTeamsLinkShare(e *managedEval) error {
	root, err := models.ProtectedRootOf(e.s, e.projectID())
	if err != nil {
		return e.refuse("the target project could not be resolved")
	}
	if root == nil || root.Kind != models.ProtectedKindPublicRoot {
		return e.refuse("only the Public root and its subprojects can be shared by link")
	}

	// An omitted permission is read-only, which is the default the API already
	// documents; anything else asks the anonymous internet to be able to write.
	if permission := e.requestBody().Permission; permission != nil && *permission != 0 {
		return e.refuse("a link share may only be read-only")
	}
	return nil
}

// decideTeamsMembership covers renaming a team and changing who is in it. The
// team itself is ordinary Teams work; who is added to it is not, because a
// member added here reaches everything the team can reach.
func decideTeamsMembership(e *managedEval) error {
	return e.requireEntitledTarget()
}

// decideTeamsTaskMove allows a task into the member's own Inbox, into Percy
// Feedback, or anywhere inside the collaborative topology - and nowhere else.
func decideTeamsTaskMove(e *managedEval) error {
	stated := e.requestBody().destinationProjectID()
	if stated == nil {
		return nil
	}
	destination := *stated

	protected, err := models.GetProtectedEntityForProject(e.s, destination)
	if err != nil {
		return e.refuse("the destination project could not be resolved")
	}
	if protected != nil && protected.Kind == models.ProtectedKindFeedback {
		return nil
	}
	if protected != nil && protected.Kind == models.ProtectedKindInbox {
		owns, err := e.ownsProject(destination)
		if err != nil {
			return e.refuse("the destination project could not be resolved")
		}
		if owns {
			return nil
		}
		return e.refuse("the destination is another member's Inbox")
	}

	return e.requireManagedRoot(destination)
}

// requireManagedParent refuses unless a project would sit beneath an authorized
// Team or Public root. A top-level project is therefore not something a
// customer can create: outside the provisioned topology there is no legal
// parent to name.
func (e *managedEval) requireManagedParent(parent int64) error {
	if parent <= 0 {
		return e.refuse("a project must live beneath a Team or Public root")
	}
	return e.requireManagedRoot(parent)
}

// statedParentID reads parent_project_id from a create or duplicate request.
// Absence means the top level, which is what the API already documents.
func statedParentID(body *managedBody) int64 {
	if body.ParentProjectID == nil {
		return 0
	}
	return *body.ParentProjectID
}

// requestedParentID returns the parent a project would have once an update has
// been applied.
//
// Absence is not silence on a full update: upstream treats an omitted
// parent_project_id exactly like an explicit 0 and detaches the project to the
// top level. Reading absence as "leave it alone" would therefore have let any
// client move a project out of the topology just by not mentioning it. PATCH is
// the one method that genuinely merges.
func (e *managedEval) requestedParentID(current int64) int64 {
	if stated := e.requestBody().ParentProjectID; stated != nil {
		return *stated
	}
	if e.c.Request().Method == http.MethodPatch {
		return current
	}
	return 0
}

// requireManagedRoot refuses unless a project already sits inside the
// collaborative topology. An Inbox-rooted or Feedback-rooted project fails
// here, which is how one check keeps private space private across sharing,
// moving and deleting alike.
func (e *managedEval) requireManagedRoot(projectID int64) error {
	root, err := models.ProtectedRootOf(e.s, projectID)
	if err != nil {
		return e.refuse("the project's place in the topology could not be resolved")
	}
	if root == nil {
		return e.refuse("the project is not part of the managed topology")
	}
	if root.Kind != models.ProtectedKindPublicRoot && root.Kind != models.ProtectedKindTeamRoot {
		return e.refuse("only a Team or Public root may hold shared work")
	}
	return nil
}

// requireEntitledTarget refuses when the account being given access is not an
// active Teams account.
//
// This is the receiving half of "a personal account can neither send nor
// receive project or team shares": a personal account cannot be invited, and
// the refusal happens where the invitation is issued, so every alternate route
// - project share, team membership, admin promotion, either API version -
// closes at the same point.
//
// A team share needs no equivalent check: a team can only contain accounts
// that passed this one when they were added to it.
func (e *managedEval) requireEntitledTarget() error {
	if !e.targetsAnAccount() {
		return nil
	}

	username := e.c.Param("user")
	if username == "" {
		if stated := e.requestBody().Username; stated != nil {
			username = *stated
		}
	}
	if username == "" {
		return e.refuse("the request does not name the account it would give access to")
	}

	target, err := user.GetUserByUsername(e.s, username)
	if err != nil {
		return e.refuse("the named account could not be resolved")
	}

	projection, err := models.GetEntitlement(e.s, target.ID)
	if err != nil {
		return e.refuse("the named account has no valid entitlement projection")
	}
	if !projection.Active() || projection.State.Edition != entitlement.EditionTeams {
		return e.refuse("the named account is not an active member of a Teams organization")
	}
	return nil
}

// targetsAnAccount reports whether a route names an individual account rather
// than a team.
func (e *managedEval) targetsAnAccount() bool {
	path := e.c.Path()
	return strings.Contains(path, "/users") || strings.Contains(path, "/members")
}
