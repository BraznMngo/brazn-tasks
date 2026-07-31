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
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
)

// The rules whose answer is the same in every edition. Edition-specific policy
// registers itself from its own file, so adding an edition never reopens one
// that already works.
func init() {
	// A feature the managed edition does not offer answers exactly like a
	// route that was never registered, so probing cannot map what is behind
	// the gate. This covers imports and migrations, webhooks, the testing
	// endpoints, and the provisioning of API tokens, CalDAV tokens and bots -
	// credentials whose lifecycle belongs to the commercial service.
	registerPreflightRule(ruleDisabled, func(e *managedEval) error {
		log.Debugf("[managed] %s %s is not available in the managed edition",
			e.c.Request().Method, e.c.Path())
		return errManagedDisabled()
	})

	// Account lifecycle, instance-admin user management, project ownership
	// transfer and team creation are performed by Brazn's commercial service
	// against its own records. They are refused here for everyone, including
	// an instance administrator, because a change made through this API would
	// leave the service's view of the account behind.
	registerPreflightRule(ruleServiceManaged, func(e *managedEval) error {
		log.Debugf("[managed] %s %s is performed by the commercial service, not by request",
			e.c.Request().Method, e.c.Path())
		return errManagedUnavailable()
	})

	// Signing in is never gated by managed mode, for two reasons that both
	// have to hold. The subject of a login is not known until the handler has
	// checked the credentials, so there is no projection to read yet; and a
	// projection that is missing or unreadable would then lock every user out
	// of the instance entirely, taking ordinary task work with it. Seat and
	// subscription state is enforced on the operations that actually expand
	// access, which is where refusing it changes what a user can reach.
	registerPreflightRule(ruleAuthentication, func(_ *managedEval) error {
		return nil
	})

	// Revocation reduces access, so it stays available in both editions - but
	// it still requires a valid, active projection, because "every
	// access-expanding operation fails when entitlement state is missing"
	// admits no exception that a caller could aim at.
	registerEditionRule(ruleAccessRevoke, entitlement.EditionPersonal, decideAccessRevoke)
	registerEditionRule(ruleAccessRevoke, entitlement.EditionTeams, decideAccessRevoke)

	// The task routes are the one place where a single route carries two
	// different meanings, so which one this request has is decided first.
	registerPreflightRule(ruleTaskMove, decideTaskMove)
}

// decideTaskMove separates the two things the task update and bulk-update
// routes do.
//
// A request that does not name a destination project is ordinary task work -
// retitling, checking off, rescheduling - and must keep working even when no
// entitlement can be read at all. That is not a loophole in fail-closed: the
// classification calls these routes access-expanding because they *can* move a
// task out of a private Inbox, and it is that move which is guarded, not the
// edit that travels the same way.
//
// A request that does name one is a move, and moves are decided per edition.
func decideTaskMove(e *managedEval) error {
	if e.requestBody().destinationProjectID() == nil {
		return nil
	}
	return e.decideByEdition()
}

// decideAccessRevoke allows a share, membership or right to be withdrawn,
// except on the protected roots themselves. Removing the team that holds a
// Team root, or the user that holds an Inbox, is not a revocation: it strips
// the structure the product guarantees, which is a topology change wearing a
// DELETE.
func decideAccessRevoke(e *managedEval) error {
	projectID := e.projectID()
	if projectID == 0 {
		return nil
	}

	protected, err := models.GetProtectedEntityForProject(e.s, projectID)
	if err != nil {
		return e.refuse("the target project could not be resolved")
	}
	if protected != nil {
		return e.refuse("the target project is part of the protected topology")
	}
	return nil
}
