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
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
)

// The Personal edition's policy table (BRA-782).
//
// A personal account is one customer-owned protected Inbox and nothing else.
// It has no second project, no subprojects, no teams, no shares in either
// direction and no public links, so most of this table is a refusal - which is
// the point: the shape of a personal account is fixed, and every route that
// could change it says no.
//
// The one exemption is Percy Feedback, and it is exactly one named project,
// not a category. Nothing here grants anything to a "system project".
//
// None of these refusals mentions another plan or hints at an upgrade. There
// is no Personal-to-Teams path in this release, and a policy response that
// implied otherwise would be a promise the product cannot keep.
func init() {
	personal := entitlement.EditionPersonal

	registerEditionRule(ruleProjectCreate, personal,
		denyPersonal("a personal account has one protected Inbox and creates no further projects"))
	registerEditionRule(ruleProjectDuplicate, personal,
		denyPersonal("duplicating would produce a second project, which a personal account does not have"))
	registerEditionRule(ruleProjectUpdate, personal,
		denyPersonal("the Inbox and Percy Feedback cannot be renamed, moved, nested or converted"))
	registerEditionRule(ruleProjectDelete, personal,
		denyPersonal("the Inbox and Percy Feedback cannot be deleted"))
	registerEditionRule(ruleProjectShare, personal,
		denyPersonal("a personal account can neither send nor receive project shares"))
	registerEditionRule(ruleLinkShare, personal,
		denyPersonal("a personal account has no public links"))
	registerEditionRule(ruleTeamsOnly, personal,
		denyPersonal("a personal account has no teams, and can neither send nor receive team invitations"))

	registerEditionRule(ruleTaskMove, personal, decidePersonalTaskMove)
}

// denyPersonal builds a rule that always refuses, recording why. The reason
// reaches the log, not the caller: the response is deliberately the same flat
// refusal everywhere, so a caller cannot map the topology by reading it.
func denyPersonal(reason string) managedRuleFunc {
	return func(e *managedEval) error {
		return e.refuse(reason)
	}
}

// decidePersonalTaskMove allows a task to be moved only into the account's own
// Inbox or into Percy Feedback.
//
// Percy Feedback is the whole of "controlled task submission": a customer can
// send work into it, and that is all. Everything that would weaken the
// account's isolation - renaming it, deleting it, sharing it, nesting anything
// under it - is refused by the rules above, which treat it exactly like the
// Inbox because both are protected entities.
//
// Ownership of the destination Inbox is checked against the acting user, so
// "move a task into someone else's Inbox" fails here and not only in the
// permission layer.
func decidePersonalTaskMove(e *managedEval) error {
	body, err := e.requestBody()
	if err != nil {
		return e.refuseUnreadableBody(err)
	}

	stated := body.destinationProjectID()
	if stated == nil {
		// decideTaskMove only reaches here when the request named one, but a
		// policy check must not be the thing that panics.
		return nil
	}
	destination := *stated

	protected, err := models.GetProtectedEntityForProject(e.s, destination)
	if err != nil {
		return e.refuse("the destination project could not be resolved")
	}
	if protected == nil {
		return e.refuse("the destination project is not part of the managed topology")
	}

	if protected.Kind == models.ProtectedKindFeedback {
		return nil
	}

	if protected.Kind == models.ProtectedKindInbox {
		owns, err := e.ownsProject(destination)
		if err != nil {
			return e.refuse("the destination project could not be resolved")
		}
		if owns {
			return nil
		}
		return e.refuse("the destination is another account's Inbox")
	}

	return e.refuse("a personal account can only hold tasks in its Inbox or in Percy Feedback")
}
