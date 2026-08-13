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

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/user"
)

// EraseSubject destroys everything this instance holds for one commercial
// subject: their account and, through DeleteUser, every category of their data.
//
// This is step 5 of the commercial service's erasure sequence (GDPR Art. 17,
// BRA-805 scope 2), reached over the provisioning channel. Percy Cloud has by
// this point redacted its own record, revoked the credentials it holds and
// suppressed the mailbox; the tasks, projects and content live here, and nothing
// in the commercial layer can reach them except through this call.
//
// # A SUBJECT ALREADY GONE IS A SUCCESS
//
// The guard below - reading the row and returning nil when there is none - is
// the whole reason this function exists rather than the handler calling
// DeleteUser directly. DeleteUser IS NOT IDEMPOTENT. For an id with no row it
// does not no-op: it reaches its account-deleted notification, whose
// ShouldNotify looks the user up and returns user.ErrUserDoesNotExist, and
// user_delete.go returns that BEFORE it ever deletes the users row.
//
// On this channel every refusal is one flat 400, and the consumer maps a 400 to
// a non-retryable invalid_state (cloud/service/src/fork.ts, forkRefused). The
// erasure sequence is RESUMABLE AND RETRIES FROM THE TOP, so forwarding that
// error would leave every interrupted erasure failing here forever - against an
// Art. 12(3) one-month clock, while every earlier step went on succeeding. That
// is a stable attractor rather than a transient, and it is the single most
// consequential thing about this operation.
//
// # A MALFORMED SUBJECT IS NOT THE SAME CASE, and is refused
//
// "Already gone" means a well-formed id with no row. An id that is not a decimal
// number at all was never addressable here, and answering 200 for it would
// report an erasure that could not have happened - marking the sequence complete
// while nothing was destroyed. The commercial service validates subject ids
// against ^[1-9][0-9]{0,18}$ before it ever stores one, so this cannot arise
// from a correct sender; it means the producer is broken, and it should be loud.
// See parseSubjectID: a leading zero ("01") is exactly such a case, and without
// its round-trip check would alias the real subject ("1") rather than refuse.
func EraseSubject(ctx context.Context, subject string) error {
	id, ok := parseSubjectID(subject)
	if !ok {
		return ErrProvisioningSubjectUnknown
	}

	s := db.NewSession()
	defer s.Close()
	// The project cascade inside DeleteUser queues its ProjectDeletedEvents on
	// this session with DispatchOnCommit, so they are delivered by
	// DispatchPending below and discarded by this line when the transaction is
	// rolled back instead. It is a no-op once DispatchPending has run, exactly
	// as on CreateOrResolveUserForMailbox.
	//
	// The deletion cron does neither, which leaves its queued events undelivered
	// and in the pending map. That is upstream's gap and is not repeated here.
	defer events.CleanupPending(s)

	// READ THE STORED ROW, and not user.GetUserByID. That helper BLANKS Email on
	// the way out unless asked for it, and DeleteUser hands the value it is
	// given to notifications.Notify, which routes the account-deleted mail
	// through u.Email. Reusing the helper sitting right there would send that
	// mail to the empty address while every assertion about rows still passed -
	// the same trap BRA-1099 hit from the other direction.
	//
	// This also matches what the deletion cron passes: a fully loaded user, not
	// a bare id. The two callers of DeleteUser should differ in what triggers
	// them, not in how completely the subject is loaded.
	u := &user.User{}
	has, err := s.Where("id = ?", id).Get(u)
	if err != nil {
		_ = s.Rollback()
		return err
	}
	if !has {
		// Nothing to erase, and nothing went wrong. A disabled or locked account
		// is NOT this case - s.Get applies no status rule, so it is found and
		// erased like any other, which is what erasing a suspended customer
		// requires.
		_ = s.Rollback()
		return nil
	}

	if err := DeleteUser(s, u); err != nil {
		_ = s.Rollback()
		return err
	}

	// One transaction for the whole erasure: db.NewSession begins one, so a
	// failure part way through leaves the subject wholly intact rather than
	// half-destroyed, and the retry that follows starts from a state this
	// function has seen before.
	if err := s.Commit(); err != nil {
		_ = s.Rollback()
		return err
	}

	events.DispatchPending(ctx, s)
	return nil
}
