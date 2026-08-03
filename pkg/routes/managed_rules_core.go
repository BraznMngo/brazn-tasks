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

	// Registration IS gated on a managed instance - by a signup token from
	// Percy Cloud, and by nothing else (BRA-1071). The gate is not here, and
	// that is forced rather than chosen: redeeming a token both consumes it and
	// reports the users.id it binds to, so it cannot be asked before a user
	// exists, and no user exists until the handler has created one. Splitting
	// it into a check here and a redemption there would put back exactly the
	// window the contract removes - a check that does not consume, which two
	// registrations can both pass.
	//
	// So this returns nil and shared.RegisterUser holds the whole of it,
	// inside the transaction that creates the user, committing only on a
	// redeemed answer. What this table still does is make the route REACHABLE:
	// it was "service-managed" until BRA-1071, which refused registration
	// before any handler ran.
	registerPreflightRule(ruleSignupToken, func(_ *managedEval) error {
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
// Ordinary task work - retitling, checking off, rescheduling - must keep
// working even when no entitlement can be read at all. That is not a loophole
// in fail-closed: the classification calls these routes access-expanding
// because they *can* move a task out of a private Inbox, and it is that move
// which is guarded, not the edit that travels the same way.
//
// Two things count as not-a-move, and both matter. A request that names no
// destination is obviously one. So is a request that names the project the
// task is already in - the v1 update sends the whole task back, project_id
// included, so treating a restatement as a move would put every ordinary edit
// made from a browser behind an entitlement check.
//
// Everything else, including a question this cannot answer, is a move, and
// moves are decided per edition.
func decideTaskMove(e *managedEval) error {
	body, err := e.requestBody()
	if err != nil {
		return e.refuseUnreadableBody(err)
	}

	destination := body.destinationProjectID()
	if destination == nil {
		return nil
	}

	moves, err := e.movesTaskBetweenProjects(body, *destination)
	if err != nil {
		return e.refuse("could not establish whether this request moves a task")
	}
	if !moves {
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
