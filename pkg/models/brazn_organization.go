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
	"sort"

	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/user"

	"xorm.io/xorm"
)

// SeatsPerTeam is the capacity arithmetic, and it is here rather than in a
// handler because two callers must agree on it exactly: what the Organization
// area SHOWS and what the route REFUSES. A number the display derives
// separately from the enforcement is the same defect as a test that computes
// its own expectation.
//
// Product rule 2.3, restated: an organization may hold one team per three
// PURCHASED seats. Purchased, never occupied - an organization that buys nine
// seats builds three teams before the first invitation goes out, because
// nobody can be invited into a team that does not exist.
const SeatsPerTeam = 3

var (
	// ErrNotOrganizationAdministrator means the acting user does not administer
	// the organization they are asking about - including the case where they
	// have no readable projection at all. The two are deliberately one error:
	// a caller who could tell "you are not the administrator" from "we could
	// not read your entitlement" would learn something about an organization
	// they do not administer.
	ErrNotOrganizationAdministrator = errors.New("this user does not administer an organization")
	// ErrOrganizationAdministratorAmbiguous means more than one active
	// projection in this organization claims administration.
	//
	// It is a REFUSAL and never a choice. `organization_admin` asserts that a
	// user holds administration, not that they are the only one, so uniqueness
	// is a producer obligation the contract states in prose and cannot yet
	// enforce in schema. Picking one of two claimants - the lower user id, the
	// higher revision, the one who asked - would invent an ordering the
	// contract does not define, and BRA-917 AC3 requires exactly one
	// administrator to exist at all times. Refusing is what makes that
	// checkable here instead of assumed.
	ErrOrganizationAdministratorAmbiguous = errors.New("more than one user claims to administer this organization")
)

// OrganizationMember is one person as the Organization area sees them.
//
// It carries no seat price, no invoice and no payment state: those belong to
// the commercial service (BRA-917 AC5) and a second copy of them here is how
// the two drift apart.
type OrganizationMember struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	// Administrator is read from this member's own projection and is never
	// written by this product. The contract is explicit that the fork must
	// never grant, infer or repair it locally, because it is the flag that
	// gates every access-expanding and protected-topology operation.
	Administrator bool `json:"administrator"`
}

// OrganizationTeam is one of the organization's teams as the Teams page sees
// it.
//
// Primary is carried by the SERVER rather than left for a client to work out,
// and it is the same answer RemoveOrganizationTeam refuses on. A client that
// decided "the first one in the list" would be right until a list arrived in a
// different order, and would then draw a removal control on the one team that
// can never be removed.
type OrganizationTeam struct {
	TeamID    int64  `json:"team_id"`
	Name      string `json:"name"`
	ProjectID int64  `json:"project_id"`
	Primary   bool   `json:"primary"`
}

// Organization is the read model behind the seven Organization pages. One
// query set, one answer, so Overview and Teams cannot disagree about how many
// teams are in use.
type Organization struct {
	ID      string `json:"id"`
	Edition string `json:"edition"`
	// Administrator is the single administrator. Never nil in a value returned
	// by OrganizationFor, which refuses rather than return an organization
	// without exactly one.
	Administrator *OrganizationMember   `json:"administrator"`
	Members       []*OrganizationMember `json:"members"`
	// SeatsOccupied counts the active projections this instance holds for the
	// organization. It is a fact about who is here, not about what was bought.
	SeatsOccupied int `json:"seats_occupied"`
	// SeatsPurchased is null when the projection does not carry one. Null is
	// not zero and not unlimited: it is "this instance cannot answer", and
	// every capacity decision taken against it refuses.
	SeatsPurchased *int `json:"seats_purchased"`
	// TeamsUsed counts the organization's provisioned Team roots. It is
	// len(Teams) and is carried separately so a client showing only the number
	// - Overview, Seats - does not have to know that.
	TeamsUsed int `json:"teams_used"`
	// Teams is the organization's teams, oldest first, so the Teams page can
	// offer removal on exactly the ones that may be removed.
	Teams []*OrganizationTeam `json:"teams"`
	// TeamsAllowed is null exactly when SeatsPurchased is, for the same reason.
	TeamsAllowed *int `json:"teams_allowed"`
	// CanCreateTeam is the capacity decision itself, so a client renders the
	// same answer the route enforces rather than recomputing it. See
	// SeatsPerTeam.
	CanCreateTeam bool `json:"can_create_team"`
}

// OrganizationFor returns the organization the given user administers.
//
// IT IS THE AUTHORIZATION CHECK AS WELL AS THE READ, and that is deliberate:
// BRA-917 AC1 sets the bar at the route, not at the button. Every organization
// surface goes through this one function, so there is no second path that
// reads the roster without first establishing who is asking. A member gets
// ErrNotOrganizationAdministrator whether they arrive by URL, by a stale tab,
// or by calling the API with curl.
//
// The projection is read from the DATABASE here rather than from the session
// token, unlike the edition. That is not an oversight and not a regression of
// BRA-913's "the edition comes off the token": the token carries an edition
// because every guarded task route needs it, and organization administration
// is not on that path. These routes are an administrator opening a settings
// page. One indexed read on a page nobody loads in a loop is the correct trade
// against stamping a second claim into every session token in the product -
// and it means a role change takes effect on the next request rather than on
// the next login, which is the direction that matters for a role being
// REMOVED.
func OrganizationFor(s *xorm.Session, userID int64) (*Organization, error) {
	acting, err := GetEntitlement(s, userID)
	if err != nil {
		return nil, ErrNotOrganizationAdministrator
	}
	if !acting.Active() || !acting.State.OrganizationAdmin {
		return nil, ErrNotOrganizationAdministrator
	}

	organizationID := acting.Subject.OrganizationID
	members, err := organizationMembers(s, organizationID)
	if err != nil {
		return nil, err
	}

	var administrators []*OrganizationMember
	for _, member := range members {
		if member.Administrator {
			administrators = append(administrators, member)
		}
	}
	// Zero cannot happen - the acting user's own projection was read above and
	// says otherwise - so this is the >1 case, and refusing it is the whole
	// point. See ErrOrganizationAdministratorAmbiguous.
	if len(administrators) != 1 {
		log.Errorf("Organization %q has %d administrators; refusing every organization operation until that is one",
			organizationID, len(administrators))
		return nil, ErrOrganizationAdministratorAmbiguous
	}

	teams, err := organizationTeams(s, organizationID)
	if err != nil {
		return nil, err
	}
	teamsUsed := len(teams)

	organization := &Organization{
		ID:             organizationID,
		Edition:        acting.State.Edition,
		Administrator:  administrators[0],
		Members:        members,
		SeatsOccupied:  len(members),
		SeatsPurchased: acting.State.SeatsPurchased,
		TeamsUsed:      teamsUsed,
		Teams:          teams,
		TeamsAllowed:   teamsAllowed(acting.State.SeatsPurchased),
		CanCreateTeam:  CanCreateTeam(acting.State.SeatsPurchased, teamsUsed),
	}
	return organization, nil
}

// organizationTeams lists an organization's teams, oldest first.
//
// The ORDER IS THE DEFINITION OF `Primary`, and it is by protected-entity id
// rather than by the created timestamp: the primary team and its root are
// provisioned with the organization and every additional one is created later,
// but two rows written in the same second carry the same timestamp while an
// autoincrement id still separates them. "Which of these is the primary team"
// must have exactly one answer, and RemoveOrganizationTeam refuses on the same
// one - see primaryTeamRoot.
//
// A root whose team row has gone is skipped rather than listed with an empty
// name. It should not happen, and if it does, the honest thing is not to offer
// a removal control for something that is already half gone.
func organizationTeams(s *xorm.Session, organizationID string) ([]*OrganizationTeam, error) {
	if organizationID == "" {
		return nil, nil
	}

	roots := []*ProtectedEntity{}
	err := s.Where("kind = ? AND organization_id = ?",
		string(ProtectedKindTeamRoot), organizationID).Asc("id").Find(&roots)
	if err != nil {
		return nil, err
	}

	teams := make([]*OrganizationTeam, 0, len(roots))
	for i, root := range roots {
		team, err := GetTeamByID(s, root.TeamID)
		if err != nil {
			continue
		}
		teams = append(teams, &OrganizationTeam{
			TeamID:    team.ID,
			Name:      team.Name,
			ProjectID: root.ProjectID,
			Primary:   i == 0,
		})
	}
	return teams, nil
}

// teamsAllowed converts a purchased seat count into a team allowance, or nil
// when there is no count to convert.
func teamsAllowed(seatsPurchased *int) *int {
	if seatsPurchased == nil {
		return nil
	}
	allowed := *seatsPurchased / SeatsPerTeam
	return &allowed
}

// CanCreateTeam answers the seat rule for ONE MORE team:
// `purchased seats >= SeatsPerTeam x (teams after this one)`.
//
// A NIL COUNT IS A REFUSAL, which is the settled reading of the contract and
// the opposite of how an absent `valid_to` is read. The two absences do
// opposite things: an absent end date taken as "no end" would grant time
// nobody paid for, where an absent count taken as "no limit" would grant
// capacity nobody paid for. Both errors point the same way, so both are closed
// the same way - by refusing.
func CanCreateTeam(seatsPurchased *int, teamsUsed int) bool {
	if seatsPurchased == nil {
		return false
	}
	return *seatsPurchased >= SeatsPerTeam*(teamsUsed+1)
}

// ErrOrganizationTeamCapacity is the seat rule refusing, and it carries the
// three numbers a customer needs to act: what they have, what they bought, and
// what buying more would give them. BRA-917 AC2 requires the refusal to return
// actionable increase-seat guidance rather than a flat no, and a message that
// says only "capacity reached" cannot be acted on without going to look
// something up.
type ErrOrganizationTeamCapacity struct {
	TeamsUsed      int
	SeatsPurchased *int
	SeatsPerTeam   int
}

// Error distinguishes the two refusals, because they have different remedies.
// "Buy more seats" is wrong advice for an organization whose seat count this
// instance could not read at all, and the numbers are carried on the struct
// rather than formatted into this string so a caller renders them itself.
func (e ErrOrganizationTeamCapacity) Error() string {
	if e.SeatsPurchased == nil {
		return "this instance cannot read how many seats the organization has bought, so no team can be created"
	}
	return "the organization has bought too few seats for another team"
}

// ErrOrganizationTeamProtected means the named team is the organization's
// primary team, which has no removal control anywhere. Its root is provisioned
// with the organization and dismantling it would leave members navigating by a
// name that is gone.
var ErrOrganizationTeamProtected = errors.New("the primary team cannot be removed")

// ErrOrganizationTeamNotFound means the named team has no Team root belonging
// to this organization. It is the same answer for a team that does not exist
// and for one belonging to somebody else, because an administrator of one
// organization may not learn the team ids of another.
var ErrOrganizationTeamNotFound = errors.New("this organization has no such team")

// CreateOrganizationTeam provisions an additional team: the team, its Team
// root, and the team's access to that root, in the caller's session so the
// three either all exist or none do.
//
// A team WITHOUT a root is the failure this guards against, not a tidiness
// concern. Every placement rule in managed_rules_teams.go asks whether a
// project sits beneath a Team or Public root, so a team whose root was never
// created is a team inside which nothing can be created - and the customer
// would have spent capacity on it.
func CreateOrganizationTeam(
	s *xorm.Session,
	admin *user.User,
	organization *Organization,
	name string,
) (*Team, error) {
	if !CanCreateTeam(organization.SeatsPurchased, organization.TeamsUsed) {
		return nil, ErrOrganizationTeamCapacity{
			TeamsUsed:      organization.TeamsUsed,
			SeatsPurchased: organization.SeatsPurchased,
			SeatsPerTeam:   SeatsPerTeam,
		}
	}

	team := &Team{Name: name}
	if err := team.CreateNewTeam(s, admin, true); err != nil {
		return nil, err
	}

	root := &Project{Title: name}
	if err := root.Create(s, admin); err != nil {
		return nil, err
	}

	err := RegisterProtectedProjectForOrganization(
		s, ProtectedKindTeamRoot, root.ID, team.ID, organization.ID)
	if err != nil {
		return nil, err
	}

	access := &TeamProject{TeamID: team.ID, ProjectID: root.ID, Permission: PermissionAdmin}
	if err := access.Create(s, admin); err != nil {
		return nil, err
	}
	return team, nil
}

// RemoveOrganizationTeam removes an additional team and the work that lives
// inside it, and NOTHING ELSE. That last clause is BRA-917 AC4 and it is the
// only part of this function worth reading twice.
//
// What "nothing else" rests on, stated because each half is somebody else's
// code and either could change:
//
//   - Team.Delete removes the team, its members and its project relations. It
//     deletes no project at all, so a project that some member also had access
//     to through another route is untouched.
//   - Project.Delete recurses into a project's CHILDREN. Deleting the Team root
//     therefore takes exactly the subtree beneath it - which is the definition
//     of the team's own work - and a project that was moved out from under this
//     root earlier is, by then, not a child of it. It survives, and it should:
//     moving it out was a decision somebody made.
//
// The primary team is refused outright. AC4's original wording named BRA-915's
// "trusted reconciliation flow"; that ticket was cancelled and its surviving
// half is BRA-787's protected topology, which is what this uses - a Team root
// is a protected entity, and the protection is what says which teams may go.
func RemoveOrganizationTeam(
	s *xorm.Session,
	admin *user.User,
	organization *Organization,
	teamID int64,
) error {
	root := &ProtectedEntity{}
	has, err := s.Where("kind = ? AND team_id = ? AND organization_id = ?",
		string(ProtectedKindTeamRoot), teamID, organization.ID).Get(root)
	if err != nil {
		return err
	}
	if !has {
		return ErrOrganizationTeamNotFound
	}

	primary, err := primaryTeamRoot(s, organization.ID)
	if err != nil {
		return err
	}
	if primary != nil && primary.TeamID == teamID {
		return ErrOrganizationTeamProtected
	}

	// The protected row goes FIRST, so that a failure part-way leaves an
	// unprotected project rather than a protected entity naming a project that
	// no longer exists. The second is the worse of the two: every placement
	// rule resolves a project's root through this table, so a dangling row is a
	// topology answer about something that is gone.
	//
	// Note what this is NOT. decideTeamsProjectDelete refuses to delete a
	// protected root, but that rule is middleware on an HTTP route and this is
	// a model call, so nothing here was ever going to be refused by it. The
	// ordering is about what a half-completed transaction leaves behind, not
	// about getting past a guard.
	if _, err := s.ID(root.ID).Delete(&ProtectedEntity{}); err != nil {
		return err
	}

	rootProject, err := GetProjectSimpleByID(s, root.ProjectID)
	if err != nil {
		return err
	}
	if err := rootProject.Delete(s, admin); err != nil {
		return err
	}

	team := &Team{ID: teamID}
	return team.Delete(s, admin)
}

// primaryTeamRoot returns the organization's oldest Team root, which is the
// primary team's: the primary team and its root are provisioned with the
// organization and every additional one is created later.
//
// Ordered by id rather than by the created timestamp. Two rows written in the
// same second have the same timestamp and an autoincrement id still orders
// them, and "which of these is the primary team" must have exactly one answer.
func primaryTeamRoot(s *xorm.Session, organizationID string) (*ProtectedEntity, error) {
	root := &ProtectedEntity{}
	has, err := s.Where("kind = ? AND organization_id = ?", string(ProtectedKindTeamRoot), organizationID).
		Asc("id").Get(root)
	if err != nil || !has {
		return nil, err
	}
	return root, nil
}

// organizationMembers lists the ACTIVE members of an organization, in a stable
// order.
//
// Every envelope is re-verified through GetEntitlement rather than trusted
// because it is already stored. A roster is what an administrator acts on -
// removing someone, transferring the role - so reading it through the same
// check every policy decision uses is what keeps one tampered row out of the
// list. An organization is capped at 100 seats and this is a settings page, so
// the verification cost is bounded and paid once per page load.
//
// An unreadable or inactive projection is SKIPPED rather than fatal. A single
// bad row must not make the whole Organization area unreachable, and an
// inactive member holds no seat by definition - `seat_status: inactive` is a
// membership record that confers nothing, which is the contract's own wording.
func organizationMembers(s *xorm.Session, organizationID string) ([]*OrganizationMember, error) {
	if organizationID == "" {
		return nil, ErrNotOrganizationAdministrator
	}

	rows := []*EntitlementProjection{}
	if err := s.Where("organization_id = ?", organizationID).Find(&rows); err != nil {
		return nil, err
	}

	members := make([]*OrganizationMember, 0, len(rows))
	for _, row := range rows {
		signed, err := GetEntitlement(s, row.UserID)
		if err != nil || !signed.Active() {
			continue
		}

		u, err := user.GetUserByID(s, row.UserID)
		if err != nil {
			continue
		}

		members = append(members, &OrganizationMember{
			UserID:        u.ID,
			Username:      u.Username,
			Name:          u.Name,
			Email:         u.Email,
			Administrator: signed.State.OrganizationAdmin,
		})
	}

	sort.Slice(members, func(i, j int) bool { return members[i].UserID < members[j].UserID })
	return members, nil
}
