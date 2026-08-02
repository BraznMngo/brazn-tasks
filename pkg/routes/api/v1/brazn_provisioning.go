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
// neither service-managed nor gated on an acting user. BRA-1026 provisions an
// organization's primary team and its roots through this door: it adds an
// operation constant, a payload type and a case below, and touches nothing
// about authentication, trust or classification.
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
