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
	"errors"
	"fmt"
	"strconv"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"

	"xorm.io/xorm"
)

// ProtectedKind names the role an entity plays in the managed topology.
//
// Identity is the immutable entity id plus this kind, never the title. Titles
// may be duplicated and may change; two members can both call a project
// "Inbox" and neither fact may decide what the policy allows.
type ProtectedKind string

// The roles the product guarantees. There is deliberately no generic "system
// project" role: Percy Feedback is one named project with one named exemption,
// so a second Brazn-owned project cannot inherit its allowances by accident.
const (
	ProtectedKindInbox      ProtectedKind = "inbox"
	ProtectedKindPublicRoot ProtectedKind = "public-root"
	ProtectedKindTeamRoot   ProtectedKind = "team-root"
	ProtectedKindFeedback   ProtectedKind = "feedback"
)

// maxTopologyDepth bounds the walk to a project's top-level ancestor. Upstream
// allows deep nesting and a corrupted parent chain could cycle; a policy check
// must terminate, and refusing is the safe outcome when it cannot.
const maxTopologyDepth = 25

var (
	// ErrNoEntitlement is the single answer to every way reading a projection
	// can fail. Callers must not distinguish them: a missing row, an
	// unreachable database, a bad signature and a rolled-back revision all mean
	// the same thing, which is that no operation may be permitted on its basis.
	ErrNoEntitlement = errors.New("no valid entitlement projection for this user")
	// ErrTopologyTooDeep means the parent chain did not reach a top-level
	// project within maxTopologyDepth.
	ErrTopologyTooDeep = errors.New("project nesting exceeds the managed topology depth limit")
	// ErrEntitlementRefused means a delivered projection was not applied because
	// of what it said, rather than because the instance is unwell. Every
	// refusal wraps it with the reason, which is logged and never returned to
	// the caller: see BraznApplyEntitlementProjection for why the reply is flat.
	ErrEntitlementRefused = errors.New("the delivered entitlement projection was refused")
)

// ProtectedEntity binds one immutable entity id to its role in the managed
// topology. A project row sets ProjectID; a team root additionally records the
// team it belongs to, which is what makes "one root per team" checkable.
type ProtectedEntity struct {
	ID        int64         `xorm:"bigint autoincr not null unique pk" json:"id"`
	Kind      ProtectedKind `xorm:"varchar(20) not null INDEX" json:"kind"`
	ProjectID int64         `xorm:"bigint not null default 0 INDEX" json:"project_id"`
	TeamID    int64         `xorm:"bigint not null default 0 INDEX" json:"team_id"`
	Created   time.Time     `xorm:"created not null" json:"created"`
}

// TableName holds the table name
func (*ProtectedEntity) TableName() string {
	return "brazn_protected_entities"
}

// EntitlementProjection stores the signed entitlement envelope for one local
// user. Revision is the anti-rollback anchor ApplyEntitlement maintains: an
// envelope that disagrees with it has been swapped for an older signed one and
// is refused, even though its signature is genuine.
//
// OrganizationID is the other half of the contract's subject key. It is a
// column rather than something read back out of the envelope because the apply
// rule has to decide on it inside the UPDATE statement; deriving it would mean
// reading, deciding, and then writing across three round trips, which is
// exactly the race the compare-and-set exists to close.
//
// RevisionReceived is when this instance last accepted a delivery that ADVANCED
// the revision, on its own clock. It is what the freshness window measures, and
// three things about it are load-bearing:
//
//   - It answers the question the window actually asks. "An instance the
//     commercial service stopped talking to stops expanding access" is a
//     question about how long since we last heard, not about how old an
//     assertion claims to be. The envelope's issued_at answers the second, and
//     the contract designates it audit data precisely because it is the
//     sender's wall clock; measuring against it would make this instance's
//     security window depend on their clock, and one timestamp minted far
//     enough ahead would disable the window outright.
//   - It advances ONLY where the revision advances. A replay of the current
//     revision is byte-identical to a legitimate retry and cannot be told
//     apart, so if a replay refreshed this, anyone holding one captured
//     envelope could keep a subject entitled for ever by resending it - which
//     is exactly the severed-sync case the window exists to catch. An
//     equal-revision delivery stays a successful no-op and does not touch it.
//   - Nothing refreshes it while a subject's entitlement is genuinely
//     unchanged, and that is why expiry ships off (see EntitlementMaxAge). The
//     producer is idempotent on state: an account that has not changed
//     re-sends the bytes already minted and is answered `duplicate`, and only
//     a real entitlement change mints a higher revision. So there is no push
//     that keeps this clock moving, by design rather than by omission.
//
// The contract's refresh mechanism is a PULL, not a push: an
// entitlement-reconciliation-request with reason `periodic_audit`. That is
// better than a heartbeat for a reason worth stating, because it is the whole
// argument for the shape - under a push, a producer with nothing to say and a
// producer that has been cut off emit exactly the same thing, which is nothing,
// and liveness inferred from absence is not liveness. A failed pull is
// unambiguous. Whoever builds that responder is who turns expiry on.
//
// A row that predates this column carries the zero time and reads as expired
// once expiry is enabled, which is correct rather than convenient: nothing
// recorded when that projection was last confirmed, so nothing can claim it is
// fresh.
type EntitlementProjection struct {
	ID               int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	UserID           int64     `xorm:"bigint not null unique" json:"user_id"`
	OrganizationID   string    `xorm:"varchar(64) not null default ''" json:"organization_id"`
	Revision         int64     `xorm:"bigint not null" json:"revision"`
	RevisionReceived time.Time `xorm:"DATETIME not null" json:"revision_received"`
	Envelope         string    `xorm:"text not null" json:"-"`
	Created          time.Time `xorm:"created not null" json:"created"`
	Updated          time.Time `xorm:"updated not null" json:"updated"`
}

// TableName holds the table name
func (*EntitlementProjection) TableName() string {
	return "brazn_entitlement_projections"
}

// EntitlementMaxAge returns how long a projection stays usable after the last
// revision-advancing delivery, or zero when expiry is switched off.
//
// ZERO IS THE SHIPPED DEFAULT, and it is a decision rather than a placeholder.
// The only thing that can refresh RevisionReceived on an unchanged subject is a
// reconciliation `periodic_audit`, and no service answers one yet. Turning
// expiry on before that responder exists would not protect anything; it would
// expire every legitimate customer on a timer. Whoever builds the responder
// turns this on, and initialize.LightInit warns for as long as nobody has.
//
// Configuration rather than a constant, so the window can be set without an
// application release once there is something to refresh against.
func EntitlementMaxAge() time.Duration {
	return config.BraznEntitlementMaxAge.GetDuration()
}

// GetEntitlement reads one user's projection inside the caller's session, so a
// policy decision sees a single consistent snapshot of local state, and returns
// it only when the signature, the contract version and the stored revision all
// agree. Nothing here reaches the network.
func GetEntitlement(s *xorm.Session, userID int64) (*entitlement.Signed, error) {
	if userID <= 0 {
		return nil, ErrNoEntitlement
	}

	row := &EntitlementProjection{}
	has, err := s.Where("user_id = ?", userID).Get(row)
	if err != nil {
		// Worth a log line: unlike the other failures this one means the
		// instance is unhealthy, not that the user is unentitled.
		log.Errorf("Could not read the entitlement projection for user %d: %s", userID, err)
		return nil, ErrNoEntitlement
	}
	if !has {
		return nil, ErrNoEntitlement
	}

	signed, err := entitlement.Verify([]byte(row.Envelope))
	if err != nil {
		log.Errorf("Refused the entitlement projection for user %d: %s", userID, err)
		return nil, ErrNoEntitlement
	}
	if signed.Revision != row.Revision {
		log.Errorf("Refused the entitlement projection for user %d: envelope is at revision %d but the row anchor is %d",
			userID, signed.Revision, row.Revision)
		return nil, ErrNoEntitlement
	}
	// Nothing has advanced this subject's revision for longer than the window,
	// so the projection is treated as one that was never applied: existing work
	// continues, expansion stops. Measured from when this instance last received
	// an advancing revision, on its own clock - never from the envelope's
	// issued_at, which is the sender's clock and audit data.
	//
	// This is a NORMAL state rather than an alarm - it is what an instance the
	// commercial service stopped reaching looks like after a while - so it is
	// not logged at error level.
	if maxAge := EntitlementMaxAge(); maxAge > 0 && time.Since(row.RevisionReceived) > maxAge {
		log.Debugf("Ignored the entitlement projection for user %d: revision %d was received %s, past the %s window",
			userID, row.Revision, row.RevisionReceived, maxAge)
		return nil, ErrNoEntitlement
	}

	return signed, nil
}

// ApplyEntitlement stores one already-verified projection under the contract's
// apply rule: monotonic per subject, idempotent, and safe to retry in any order
// (cloud/contracts/README.md, "The apply rule").
//
// The caller has verified the signature before reaching here, which is the
// contract's ordering: an unverifiable message must never read the stored
// revision, let alone write one.
//
// MONOTONICITY IS ENFORCED IN THE STATEMENT, not by the caller and not by a
// read taken beforehand. The UPDATE carries `revision < ?` in its own WHERE
// clause, so two concurrent deliveries cannot both win: the database serializes
// them and re-evaluates the condition against the committed value, which means
// the lower revision writes nothing rather than overwriting the higher one. A
// read that decided this first and then wrote would be correct only until two
// deliveries arrived at once, which is precisely when it matters.
//
// Nothing written here is decided by the read below it. That read establishes
// which of the three no-write outcomes happened, and the one write it leads to -
// the first projection for a subject - is made safe by the unique constraint on
// user_id rather than by the read: a second inserter loses on the constraint,
// and its delivery is retried against the row that won.
func ApplyEntitlement(s *xorm.Session, signed *entitlement.Signed, envelope string) error {
	organization := signed.Subject.OrganizationID
	if organization == "" {
		return fmt.Errorf("%w: the subject names no organization", ErrEntitlementRefused)
	}

	// subjectExists rather than has: the `has` further down means "a projection
	// row exists", which is a different fact about a different table. One name
	// for both in one function is how the next reader conflates them.
	userID, subjectExists, err := subjectUserID(s, signed.Subject.UserID)
	if err != nil {
		return err
	}
	// The subject has been erased, so there is nothing left to entitle and
	// nothing is stored. This is a SUCCESS and it has to be: account erasure
	// (BRA-805) is resumable and its acknowledgement guard is fail-closed, so a
	// retry after this instance's user row is already gone re-delivers the
	// closure projection for a subject nobody holds. A refusal there is read as
	// "the fork did not accept this", which blocks the erasure permanently - the
	// customer's tasks are already destroyed, the commercial record is never
	// redacted, and every retry reproduces it exactly. Refusing is the one
	// answer with no way back.
	//
	// Storing a placeholder row instead would be worse than useless. Nothing
	// could read it - GetEntitlement keys on user_id and no user has this one -
	// and the envelope column would preserve the erased subject's organization,
	// state and timestamps as the retention of exactly the record the erasure
	// was performed to destroy. There is no foreign key to stop it, so this is
	// the only thing that does. The log line below omits the subject id for the
	// same reason, and it is the one line in this file that does.
	//
	// This stops a NEW row being written; it does not clean up an old one.
	// DeleteUser's relatedEntities list in user_delete.go does not include
	// EntitlementProjection, so erasing a user who already had a projection
	// leaves that row behind - unreachable, because no session ever resolves to
	// that user id again, but still holding the erased subject's envelope. That
	// is a defect in the deletion path, it is recorded under BRA-913, and it is
	// sequenced after this rather than folded into it: this function stops new
	// rows appearing and is correct on its own, where the cleanup is a separate
	// write on a separate path needing its own test. It is NOT out of bounds -
	// an entry in relatedEntities sits inside patch-surface area 4, entitlement
	// synchronization - so nothing but sequencing is holding it.
	//
	// It is deliberately narrower than "the subject did not resolve": a user id
	// that is not a well-formed reference at all never reaches here, because
	// subjectUserID returns that as a refusal above. An erased subject is a
	// well-formed id with no user behind it, and nothing else.
	if !subjectExists {
		log.Debugf("Entitlement revision %d names a subject this instance no longer has; nothing to apply",
			signed.Revision)
		return nil
	}

	// The receipt is stamped in the same statement as the revision, and in no
	// other statement anywhere, so "when a revision last advanced" cannot drift
	// from "which revision is stored". A delivery that does not advance the
	// revision writes nothing at all, which is what stops a replayed envelope
	// from refreshing the freshness window.
	received := time.Now()
	applied, err := s.
		Where("user_id = ? AND organization_id = ? AND revision < ?", userID, organization, signed.Revision).
		Cols("revision", "revision_received", "envelope").
		Update(&EntitlementProjection{
			Revision:         signed.Revision,
			RevisionReceived: received,
			Envelope:         envelope,
		})
	if err != nil {
		return err
	}
	if applied > 0 {
		log.Debugf("Applied entitlement revision %d for user %d in organization %q",
			signed.Revision, userID, organization)
		return nil
	}

	row := &EntitlementProjection{}
	has, err := s.Where("user_id = ?", userID).Get(row)
	if err != nil {
		return err
	}
	if !has {
		// Revision 1 against a stored 0 is the general rule's first iteration
		// and needs no separate bootstrap path, so the only thing special about
		// a first projection is that there is no row to update.
		_, err = s.Insert(&EntitlementProjection{
			UserID:           userID,
			OrganizationID:   organization,
			Revision:         signed.Revision,
			RevisionReceived: received,
			Envelope:         envelope,
		})
		if err != nil {
			return err
		}
		log.Debugf("Applied the first entitlement revision %d for user %d in organization %q",
			signed.Revision, userID, organization)
		return nil
	}

	// A live projection under a different organization. Phase 1 has no
	// legitimate path to this: a Personal user cannot be invited into an
	// organization and a removed member is not converted back (BRA-786), and a
	// deleted user is erased rather than reassigned (BRA-805), so a re-signup
	// arrives as a different local user entirely. Multi-organization membership
	// is unmodelled rather than decided, so this refuses loudly. Relaxing a
	// refusal later is one deliberate change; accepting now would leave two
	// valid-looking rows with nothing to say which is authoritative.
	if row.OrganizationID != organization {
		return fmt.Errorf("%w: user %d already holds a projection for organization %q, not %q",
			ErrEntitlementRefused, userID, row.OrganizationID, organization)
	}

	// Same subject, and the revision did not advance. Both remaining cases -
	// the sender replaying a revision already applied, and the sender being
	// behind - change no state, and neither is an error: delivery is
	// at-least-once, so a retry has to be safe to repeat.
	//
	// "No state change" includes RevisionReceived, deliberately. A replay is
	// byte-identical to a legitimate retry, so refreshing the window here would
	// let anyone who captured one valid envelope keep this subject entitled
	// indefinitely by resending it.
	log.Debugf("Entitlement revision %d for user %d changed nothing; the stored anchor is %d",
		signed.Revision, userID, row.Revision)
	return nil
}

// subjectUserID resolves the contract's opaque subject user id to a local user.
//
// THIS IS THE COUPLING POINT BETWEEN THE TWO HALVES. The contract calls the
// field opaque, meaning it carries no structure the contract interprets, but
// the commercial service documents its own column as "the immutable Brazn Tasks
// user ID" - it references a user of this instance rather than naming one of
// its own. The decimal form of the user's primary key is that reference: it
// fits the contract's character class, and it is the one identifier here that
// is never reissued, where a username can be freed and taken again.
//
// THE TWO WAYS A SUBJECT CAN FAIL TO RESOLVE ARE NOT THE SAME FAILURE, and the
// three return values exist to keep them apart:
//
//   - A subject that is not a well-formed reference to any user - non-numeric,
//     negative, empty - is a REFUSAL. No such user has ever existed, so nothing
//     can have erased one, and a producer sending this is broken.
//   - A well-formed id with no user behind it resolves to (0, false, nil): the
//     subject is gone. Callers answer that successfully; see ApplyEntitlement
//     for why a refusal there deadlocks account erasure.
//
// Check the error before the boolean. A refusal reports has=false too, and a
// caller that read the boolean first would treat a malformed subject as an
// erased one - which is the conflation this split exists to prevent.
//
// Existence is all that is asked, deliberately. user.GetUserByID also reports
// disabled and locked accounts as errors, and neither is this endpoint's
// business: the projection is what says whether a subject is entitled, and a
// locally disabled account whose delivery failed would have the commercial
// service retrying a message that can never land.
func subjectUserID(s *xorm.Session, subject string) (int64, bool, error) {
	userID, err := strconv.ParseInt(subject, 10, 64)
	if err != nil || userID <= 0 {
		return 0, false, fmt.Errorf("%w: %q is not a local user id", ErrEntitlementRefused, subject)
	}

	has, err := s.Where("id = ?", userID).Get(&user.User{})
	if err != nil {
		return 0, false, err
	}
	if !has {
		return 0, false, nil
	}
	return userID, true, nil
}

// TasksOutsideProject reports whether any of the given tasks currently lives
// somewhere other than the given project.
//
// This is how the gate tells an edit from a move. A client that sends the whole
// task back - which the v1 update does on every keystroke's worth of change -
// restates project_id every time, and restating where something already is
// moves nothing. Reading that as a move would put ordinary editing behind an
// entitlement check.
//
// Anything it cannot answer counts as a move, because a move is the guarded
// reading and the safe one to be wrong about.
func TasksOutsideProject(s *xorm.Session, taskIDs []int64, projectID int64) (bool, error) {
	if len(taskIDs) == 0 || projectID <= 0 {
		return true, nil
	}

	count, err := s.In("id", taskIDs).Where("project_id != ?", projectID).Count(&Task{})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetProtectedEntityForProject returns the role a project plays in the managed
// topology, or nil when it plays none.
func GetProtectedEntityForProject(s *xorm.Session, projectID int64) (*ProtectedEntity, error) {
	if projectID <= 0 {
		return nil, nil
	}

	protected := &ProtectedEntity{}
	has, err := s.Where("project_id = ?", projectID).Get(protected)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	return protected, nil
}

// ProtectedRootOf walks up to a project's top-level ancestor and returns that
// ancestor's role, or nil when the tree it belongs to is not part of the
// managed topology. This is what makes placement checkable: "may this live
// here" is a question about the root, not about the immediate parent.
func ProtectedRootOf(s *xorm.Session, projectID int64) (*ProtectedEntity, error) {
	current := projectID
	for range maxTopologyDepth {
		if current <= 0 {
			return nil, nil
		}

		project, err := GetProjectSimpleByID(s, current)
		if err != nil {
			return nil, err
		}
		if project.ParentProjectID == nil || *project.ParentProjectID <= 0 {
			return GetProtectedEntityForProject(s, project.ID)
		}
		current = *project.ParentProjectID
	}

	return nil, ErrTopologyTooDeep
}

// RegisterProtectedProject records a project's role in the managed topology.
// It is idempotent: a project that already has a role keeps it, so re-running
// provisioning cannot silently reclassify an existing structure.
func RegisterProtectedProject(s *xorm.Session, kind ProtectedKind, projectID, teamID int64) error {
	existing, err := GetProtectedEntityForProject(s, projectID)
	if err != nil || existing != nil {
		return err
	}

	_, err = s.Insert(&ProtectedEntity{Kind: kind, ProjectID: projectID, TeamID: teamID})
	return err
}
