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
	"errors"
	"io"
	"net/http"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

	"github.com/labstack/echo/v5"
)

// maxProjectionBytes bounds what this endpoint will read. A projection is a few
// hundred bytes of closed enums and opaque identifiers - the contract's shape
// forbids free text - so anything approaching this is not one.
const maxProjectionBytes = 16 << 10

// BraznApplyEntitlementProjection receives one signed entitlement projection
// from Brazn's commercial service and applies it.
//
// AUTHENTICATION IS THE SIGNATURE, not the connection. The message is
// authenticated rather than the caller: entitlement.Verify checks an Ed25519
// signature over "percy.entitlement-projection.v2" || 0x0A || the received
// signed octets, against the keys brazn.entitlementkeys names. Someone who
// cannot sign with a trusted key changes nothing here, and someone who can
// needs nothing else - which is what the contract means by "a verifier needs
// exactly three things". The trust store is empty by default, so an instance
// nobody configured accepts nothing.
//
// That is a reuse rather than a new scheme, and it is a reuse of the only thing
// that fits: this fork has no service principal. Its JWT, OIDC and link-share
// paths all resolve to a user, which the commercial service is not; API and
// CalDAV tokens are user-scoped and turned off in managed mode; and
// service.testingtoken is a harness secret that must be empty in production.
//
// THE REPLY IS DELIBERATELY FLAT. Every refusal answers the same 400 with the
// same sentence, and the actual reason is logged instead. Anyone can reach this
// endpoint - authentication happens inside the message, so a caller who fails
// it was never authenticated at all - and a reply that distinguished "wrong
// organization" from "revision already applied" would answer questions about
// the instance's entitlement state for whoever asked. The contract makes the
// same point about the acknowledgement: a rejection carries its reason and
// nothing else, precisely so this seam is not an oracle.
//
// ONE DISTINCTION IS VISIBLE THROUGH THE REPLY, and it is worth stating rather
// than leaving to be rediscovered. A projection for a subject this instance no
// longer has answers 204 rather than 400 (models.ApplyEntitlement, where a
// refusal would deadlock account erasure), so the two codes do separate "there
// is no user N here" from "there is, and something else was wrong".
//
// That is not an oracle anyone can consult. Reaching the distinction at all
// requires entitlement.Verify to have already succeeded, which requires an
// Ed25519 signature from a key brazn.entitlementkeys names; every caller
// without one gets the same 400 they got before, for every subject. So the
// party who can ask is the party holding the producer's signing key, about
// subjects that party assigned. Someone replaying a captured envelope can ask
// only about the single subject it names, because the id is inside the signed
// octets and changing it invalidates the signature. Widening this would mean
// answering 204 before verification, which nothing here does.
//
// NO ACKNOWLEDGEMENT IS RETURNED, and that is a decision rather than an
// oversight. entitlement-projection-ack.schema.json distinguishes applied,
// duplicate, stale and rejected, and carries current_revision so a sender that
// has fallen behind learns to reconcile. Building it needs the four rejection
// reasons Verify currently collapses into two errors, so it is a change to the
// verifier's vocabulary and not something to guess from a sentinel here. Until
// that exists, an outcome that leaves the store correct - applied, duplicate or
// stale - answers 204, and the sender learns nothing beyond "this instance has
// processed your message".
func BraznApplyEntitlementProjection(c *echo.Context) error {
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, maxProjectionBytes+1))
	if err != nil {
		return refuseUnverified("the request body could not be read")
	}
	if len(raw) > maxProjectionBytes {
		return refuseUnverified("the request body is larger than any projection")
	}

	// First, and before anything reads or writes stored state: an unverifiable
	// message must not be able to observe the instance, let alone change it.
	signed, err := entitlement.Verify(raw)
	if err != nil {
		return refuseUnverified("the envelope did not verify: " + err.Error())
	}
	if !entitlement.KnownEdition(signed.State.Edition) {
		return refuseProjection("the projection names an edition this build does not define")
	}

	s := db.NewSession()
	defer s.Close()

	if err := models.ApplyEntitlement(s, signed, string(raw)); err != nil {
		_ = s.Rollback()
		if errors.Is(err, models.ErrEntitlementRefused) {
			return refuseProjection(err.Error())
		}
		return err
	}

	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return err
	}

	return c.NoContent(http.StatusNoContent)
}

// refuseProjection logs why a delivery was turned down and returns the one
// reply every refusal gets.
//
// Error level is right for the case it was written for: a delivery this
// instance VERIFIED and then refused means a producer is broken or someone is
// trying it on, which the contract calls an alarm state and distinguishes from
// a subject that simply has no projection yet - that one is normal, is where
// every subject begins, and is logged nowhere near here.
//
// It is not right for the call sites that fire BEFORE verification - an
// unreadable body, an oversized one, a bad signature, an unknown key id. Those
// are what any unauthenticated caller produces at will, and this route carries
// no rate limit under the shipped config, so they arrive at error level in
// whatever volume someone cares to send and bury the alarm this level exists
// for. Debug before Verify returns and error after is the split to make, and
// refuseUnverified below is the other half of it.
func refuseProjection(reason string) error {
	log.Errorf("Refused an entitlement projection delivery: %s", reason)
	return refusal()
}

// refuseUnverified is refuseProjection for everything decided before the
// signature is known good, at debug level and for the reasons above.
//
// The volume argument is the lesser one. The real cost of logging these at
// error level is that the reply is deliberately flat and there is no
// acknowledgement, so this log is the ONLY channel an operator has for "why was
// that delivery refused" - and noise from the open internet is exactly what
// makes a channel like that useless.
func refuseUnverified(reason string) error {
	log.Debugf("Refused an unverified entitlement projection delivery: %s", reason)
	return refusal()
}

// refusal is the one reply every refusal gets, whatever caused it.
func refusal() error {
	return echo.NewHTTPError(http.StatusBadRequest,
		"This is not an entitlement projection this instance accepts.")
}
