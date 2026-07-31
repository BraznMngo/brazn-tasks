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
	"time"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"

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
// user. Revision is the anti-rollback anchor the sync endpoint maintains: an
// envelope that disagrees with it has been swapped for an older signed one and
// is refused, even though its signature is genuine.
type EntitlementProjection struct {
	ID       int64     `xorm:"bigint autoincr not null unique pk" json:"id"`
	UserID   int64     `xorm:"bigint not null unique" json:"user_id"`
	Revision int64     `xorm:"bigint not null" json:"revision"`
	Envelope string    `xorm:"text not null" json:"-"`
	Created  time.Time `xorm:"created not null" json:"created"`
	Updated  time.Time `xorm:"updated not null" json:"updated"`
}

// TableName holds the table name
func (*EntitlementProjection) TableName() string {
	return "brazn_entitlement_projections"
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

	return signed, nil
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
