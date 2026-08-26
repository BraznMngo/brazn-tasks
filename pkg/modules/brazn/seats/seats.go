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

// Package seats reports an organization's team count to the commercial
// service, which raises the purchased seat count to whatever the count needs
// (BRA-1439 Story 9, decided by Sebastian on 2026-08-26: creating a team is
// never refused for seats - the seat count rises instead).
//
// THE FORMULA DOES NOT LIVE HERE, and that is the one design rule of this
// package. The commercial service raises the purchased count to
// max(current, active_teams x 3, users); this package only reports
// `active_teams` and never computes what the rise will be, because a formula
// duplicated either side of a boundary is checked by neither
// (pkg/models/brazn_organization.go, SeatsPerTeam).
//
// THE CALL NEVER GATES ANYTHING. A team creation that has committed is a fact,
// and this is how the fact reaches billing - so the caller logs a failure and
// carries on, and nothing here can make a creation fail. The endpoint's
// semantics make that safe: it is a converging ensure (the count is raised,
// never lowered, and a replay answers `unchanged`), so a missed report is
// corrected by the next successful one, and a retry needs no idempotency key.
// The endpoint contract - path, body, credential, answers - was posted by the
// commercial half of BRA-1439 on the ticket on 2026-08-26.
package seats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/log"

	"github.com/labstack/echo/v5"
)

// ErrUnavailable means the ensure endpoint gave no usable answer: not
// configured, unreachable, refusing, or answering something this build cannot
// read. The caller logs it and continues - see the package comment for why it
// must never surface as a refusal of the team creation itself.
var ErrUnavailable = errors.New("the seat-ensure endpoint could not be reached")

// ensureClient is a plain client for the same reason signup's redemptionClient
// is: the SSRF guard exists for URLs a USER supplies, and this one comes from
// the instance's own configuration, pointing at the commercial service on the
// deployed compose network - a private range the guard would block.
var ensureClient = &http.Client{Timeout: 15 * time.Second}

// maxAnswerBytes bounds what is read back. The answer is one small JSON
// object; anything past this is not it, and reading unbounded would let
// whatever is on the other end of a misconfigured URL decide how much memory
// a team creation costs.
const maxAnswerBytes = 4096

// ensureRequest is the whole body, as the commercial half declared it on
// BRA-1439: both members required, nothing else accepted. `active_teams` is
// this fork's OWN count of the organization's teams AFTER the creation being
// reported - the fork is the source of truth for how many teams exist, and the
// commercial service is the source of truth for what that costs.
type ensureRequest struct {
	OrganizationID string `json:"organization_id"`
	ActiveTeams    int    `json:"active_teams"`
}

// ensureAnswer is the part of the 200 body this build acts on. The full answer
// carries the same field set as the seats-purchase route ({organization_id,
// outcome, seats, users, active_teams, max_active_teams, proration}); only the
// outcome decides anything here, and the declared union is
// `"changed" | "unchanged"` - both mean the count now covers the teams.
type ensureAnswer struct {
	Outcome string `json:"outcome"`
}

// EnsureOrganizationSeats reports that the organization now holds activeTeams
// teams, so the commercial service can raise the purchased seat count if the
// formula requires it. Returns nil only when the service answered an outcome
// this build understands; every other path is ErrUnavailable, which the caller
// logs and does not act on.
//
// Callers should pass a context that survives the client going away
// (context.WithoutCancel of the request context): the creation has committed
// by the time this runs, and a report lost to a closed browser tab would leave
// the seat count lagging until the next team creation converges it.
func EnsureOrganizationSeats(ctx context.Context, organizationID string, activeTeams int) error {
	if organizationID == "" || activeTeams < 0 {
		// Both come from this build, so either being wrong is a defect here,
		// not anything about the organization. Refusing locally keeps a body
		// the contract calls malformed off the wire.
		log.Errorf("[seats] refusing to report %d teams for organization id of length %d",
			activeTeams, len(organizationID))
		return ErrUnavailable
	}

	endpoint := config.BraznSeatsEnsureURL.GetString()
	credential := config.BraznServiceToken.GetString()
	if endpoint == "" || credential == "" {
		// Not configured. The creation stands either way; what is lost is the
		// automatic seat rise, so say so where an operator will read it.
		log.Error("[seats] brazn.seatsensureurl or brazn.servicetoken is unset, " +
			"so team creations are not reported and the purchased seat count will not rise automatically")
		return ErrUnavailable
	}

	payload, err := json.Marshal(ensureRequest{
		OrganizationID: organizationID,
		ActiveTeams:    activeTeams,
	})
	if err != nil {
		log.Errorf("[seats] could not encode the ensure request: %s", err)
		return ErrUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		log.Errorf("[seats] could not build the ensure request: %s", err)
		return ErrUnavailable
	}
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	// The shared service credential - the same bearer the signup redemption
	// presents. Team creation runs inside this fork's server under a Vikunja
	// session, so there is no commercial user bearer to forward from here.
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+credential)

	resp, err := ensureClient.Do(req)
	if err != nil {
		log.Errorf("[seats] the ensure call did not complete: %s", err)
		return ErrUnavailable
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		log.Errorf("[seats] the ensure answer could not be read: %s", err)
		return ErrUnavailable
	}

	if resp.StatusCode != http.StatusOK {
		// 401 is a credential this instance got wrong, 400 a body this build
		// composed wrong, 404 an organization the service does not know. All
		// three are defects on one side or the other of this seam, none is
		// actionable by the administrator whose creation triggered it, and the
		// ensure semantics mean the next report converges the count.
		log.Errorf("[seats] the ensure endpoint answered %d for organization %q", resp.StatusCode, organizationID)
		return ErrUnavailable
	}

	var answer ensureAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		log.Errorf("[seats] a 200 answer that is not the response shape: %s", err)
		return ErrUnavailable
	}
	if answer.Outcome != "changed" && answer.Outcome != "unchanged" {
		// A value outside the declared union is an answer this build has not
		// understood, and acting on it would be guessing. The count may well
		// have moved; the next read of the projection will show it.
		log.Errorf("[seats] a 200 answer this build does not understand: outcome %q", answer.Outcome)
		return ErrUnavailable
	}
	return nil
}
