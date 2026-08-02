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

// The organization-administration policy (BRA-917 AC1).
//
// THE ACCEPTANCE CRITERION IS ABOUT THE ROUTE, NOT THE BUTTON. An ordinary
// member must not be able to discover or call an administrator-only control,
// and a frontend that renders no menu entry satisfies the first half of that
// and none of the second. This rule is the second half: it decides before the
// handler runs, so a browser, Percy, a stale tab and curl all get the same
// answer for the same request.
//
// Registered for the Teams edition only. A Personal account reaching one of
// these routes is refused by decideByEdition's "no policy is defined for this
// edition" - unmapped means deny - which is the correct answer for an edition
// with no organization to administer, and it is one fewer rule to keep in step
// with the other.
func init() {
	registerEditionRule(ruleOrganizationAdmin, entitlement.EditionTeams, decideOrganizationAdmin)
}

// decideOrganizationAdmin refuses everyone who is not the organization's single
// administrator.
//
// It reads the projection through models.OrganizationFor, which is the SAME
// function the read handler calls. One check with two callers is deliberate: a
// rule and a handler that each decide "is this person the administrator"
// separately are two things that can disagree, and the disagreement would be
// discovered by whoever found the one that says yes.
//
// It also refuses an organization with more than one claimed administrator,
// which is where AC3 - "administrator transfer leaves exactly one
// administrator" - is enforced in the half this product owns. The fork cannot
// PERFORM the transfer: `organization_admin` is authoritative from the
// commercial service and the contract forbids granting, inferring or repairing
// it locally. What the fork can do is refuse to act while the answer is
// ambiguous, so a botched transfer that left two administrators stops both of
// them rather than letting either one carry on.
func decideOrganizationAdmin(e *managedEval) error {
	if _, err := models.OrganizationFor(e.s, e.user.ID); err != nil {
		return e.refuse("the acting user does not administer this organization: " + err.Error())
	}
	return nil
}
