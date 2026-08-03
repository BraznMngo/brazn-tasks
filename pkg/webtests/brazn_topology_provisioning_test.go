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

package webtests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/builder"
)

// The two protected-topology operations (BRA-1050), asserted against what this
// instance ACTUALLY HOLDS afterwards - the roots, their protected registrations
// and the refusals they earn - rather than against a handler having been
// reached. A test of the latter would pass against a build that answered
// correctly and stored nothing.
//
// managedTopologySubject is user 1: a fixture account with no default project,
// which is what lets the Inbox test assert the invariant every other Inbox on
// this instance carries. Nothing about these tests needs that user in
// particular; naming it once is so the assertions read as being about one
// person.
const managedTopologySubject = "1"

// The commercial team ids these tests provision for. TWO of them, because one
// is not enough to tell a correct implementation from one keyed on the
// organization: with a single team, "have I provisioned this team" and "does
// this organization have a Team root" give the same answer every time.
const (
	managedTestPrimaryTeam = "team_primary"
	managedTestSecondTeam  = "team_second"
)

// The payloads, in canonical JSON - members sorted by key, which is what a
// producer emits and therefore what the signature is made over.
func personalInboxPayload(userID string) string {
	return `{"contract_version":"1","operation":"create_personal_inbox","organization_id":"` +
		managedTestOrganization + `","user_id":"` + userID + `"}`
}

func teamRootsPayload(userID, teamID string) string {
	return `{"contract_version":"1","operation":"create_team_roots","organization_id":"` +
		managedTestOrganization + `","team_id":"` + teamID + `","user_id":"` + userID + `"}`
}

// teamTopologyRefs is the reply the consumer is written against
// (cloud/service/src/model.ts, TeamTopologyRefs), redeclared here rather than
// imported so the test asserts the WIRE shape.
type teamTopologyRefs struct {
	TaskTeamRef    string `json:"task_team_ref"`
	TaskProjectRef string `json:"task_project_ref"`
}

func provisionedTeamRoots(t *testing.T, rec *httptest.ResponseRecorder) teamTopologyRefs {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := teamTopologyRefs{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	// The consumer validates both against this and throws on anything else. It
	// has more teeth than the equivalent check on a user id: these two go
	// straight into a mapping that coalesces a team's pair in exactly once, so
	// an empty or malformed reference is not an exception somewhere later - it
	// is stored, permanently, under a log line saying the team was provisioned.
	assert.Regexp(t, `^[1-9][0-9]{0,18}$`, out.TaskTeamRef)
	assert.Regexp(t, `^[1-9][0-9]{0,18}$`, out.TaskProjectRef)
	return out
}

// storedEntities reads every protected entity of one kind, oldest first.
func storedEntities(t *testing.T, kind models.ProtectedKind) []*models.ProtectedEntity {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	rows := []*models.ProtectedEntity{}
	require.NoError(t, s.Where("kind = ?", string(kind)).Asc("id").Find(&rows))
	return rows
}

func projectTitle(t *testing.T, projectID int64) string {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	project, err := models.GetProjectSimpleByID(s, projectID)
	require.NoError(t, err)
	return project.Title
}

func projectOwner(t *testing.T, projectID int64) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	project, err := models.GetProjectSimpleByID(s, projectID)
	require.NoError(t, err)
	return project.OwnerID
}

func defaultProjectOf(t *testing.T, userID int64) int64 {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	u, err := user.GetUserByID(s, userID)
	require.NoError(t, err)
	return u.DefaultProjectID
}

// removeTeam asks the fork to dismantle a team, which is the question the
// protection is really about. The session is never committed, so a call that
// wrongly succeeded still leaves the instance as it was.
func removeTeam(t *testing.T, teamID int64) error {
	t.Helper()

	s := db.NewSession()
	defer s.Close()

	admin, err := user.GetUserByID(s, 1)
	require.NoError(t, err)
	return models.RemoveOrganizationTeam(
		s, admin, &models.Organization{ID: managedTestOrganization}, teamID)
}

func mustParseRef(t *testing.T, ref string) int64 {
	t.Helper()

	id, err := strconv.ParseInt(ref, 10, 64)
	require.NoError(t, err)
	return id
}

// TestBraznProvisioningCreatesAProtectedInbox is the create_personal_inbox
// operation: a project that exists, is registered as this person's Inbox, and
// is the account's default destination the way every other Inbox on this
// instance is.
//
// The reply is asserted as `{}` rather than as "some 200", because that is the
// channel's contract for an operation with nothing to report: the consumer
// cannot tell an empty 200 from a truncated one and refuses both alike, so a
// handler answering 204 or an empty body would be refused by the caller it had
// just succeeded for.
func TestBraznProvisioningCreatesAProtectedInbox(t *testing.T) {
	env := newManagedEnv(t)

	rec := env.provision(personalInboxPayload(managedTopologySubject))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{}`, rec.Body.String())

	inboxes := storedEntities(t, models.ProtectedKindInbox)
	require.Len(t, inboxes, 1)
	// Owned by the subject and titled the way this product titles an Inbox. The
	// owner is the assertion that matters: the protected row records a kind and
	// a project, and owner_id is the only place "whose Inbox is this" is
	// written down.
	db.AssertExists(t, "projects", map[string]interface{}{
		"id":       inboxes[0].ProjectID,
		"owner_id": 1,
		"title":    "Inbox",
	}, false)
	// The invariant CreateNewProjectForUser keeps for every other account here.
	// user 1 has no default project in the fixtures, so this cannot pass by
	// having been true beforehand.
	assert.Equal(t, inboxes[0].ProjectID, defaultProjectOf(t, 1))
}

// TestBraznProvisioningTheInboxTwiceLeavesOne is the idempotence obligation,
// and it is the assertion that matters rather than "the second call succeeded".
//
// A second 200 would also be answered by a build that created a SECOND Inbox -
// which is the worse bug of the two, because the customer then has two private
// projects and only one of them is where anything gets filed. The counts are
// what rule it out.
//
// Deleting the `existing != nil` early return in ensurePersonalInbox makes this
// test fail: the second call would create another project and register another
// protected row, so both counts would read 2.
func TestBraznProvisioningTheInboxTwiceLeavesOne(t *testing.T) {
	env := newManagedEnv(t)

	first := env.provision(personalInboxPayload(managedTopologySubject))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	second := env.provision(personalInboxPayload(managedTopologySubject))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	assert.JSONEq(t, `{}`, second.Body.String())

	require.Len(t, storedEntities(t, models.ProtectedKindInbox), 1)
	db.AssertCount(t, "projects", builder.Eq{"owner_id": 1, "title": "Inbox"}, 1)
}

// TestBraznProvisioningCreatesATeamsTopology is the create_team_roots
// operation: the team, its Team root, the organization's Public root, and the
// team's access to both.
//
// IT PROVISIONS FOR AN ACCOUNT HOLDING NO PROJECTION AT ALL, and that is
// deliberate. newManagedEnv starts with the entitlement table empty, so this
// organization has no purchased seat count; CanCreateTeam reads a nil count as
// a refusal. Putting the capacity gate on this path would therefore make this
// test fail - which is the point, because the primary team is part of what was
// bought rather than an addition to it, and refusing it would leave a paying
// customer with nowhere to work.
func TestBraznProvisioningCreatesATeamsTopology(t *testing.T) {
	env := newManagedEnv(t)

	refs := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestPrimaryTeam)))
	teamID := mustParseRef(t, refs.TaskTeamRef)
	rootID := mustParseRef(t, refs.TaskProjectRef)

	// The reply and the stored row are the same team and the same project. This
	// is the join: either half read alone would agree with an implementation
	// that answered with one team's ids and registered another's.
	roots := storedEntities(t, models.ProtectedKindTeamRoot)
	require.Len(t, roots, 1)
	assert.Equal(t, teamID, roots[0].TeamID)
	assert.Equal(t, rootID, roots[0].ProjectID)
	assert.Equal(t, managedTestOrganization, roots[0].OrganizationID)
	// The commercial id, stored verbatim and never derived from the fork's.
	assert.Equal(t, managedTestPrimaryTeam, roots[0].CommercialTeamID)

	db.AssertExists(t, "teams", map[string]interface{}{"id": teamID}, false)
	assert.Equal(t, "Team", projectTitle(t, rootID))

	// One Public root, belonging to the ORGANIZATION rather than to this team -
	// which is why it carries no team id.
	publics := storedEntities(t, models.ProtectedKindPublicRoot)
	require.Len(t, publics, 1)
	assert.Equal(t, managedTestOrganization, publics[0].OrganizationID)
	assert.Zero(t, publics[0].TeamID)
	assert.Equal(t, "Public", projectTitle(t, publics[0].ProjectID))

	// Both roots are reachable by the team. A root nobody can see is a root
	// nothing can be created beneath, and Public is where the organization's
	// only anonymous read-only links may live.
	db.AssertExists(t, "team_projects",
		map[string]interface{}{"team_id": teamID, "project_id": rootID}, false)
	db.AssertExists(t, "team_projects",
		map[string]interface{}{"team_id": teamID, "project_id": publics[0].ProjectID}, false)

	// And it is the primary team, which has no removal control anywhere. The
	// refusal comes from the protected row this call wrote: RemoveOrganizationTeam
	// resolves the organization's oldest Team root and refuses that team, so
	// this passing means the row is both present and attributed to the right
	// organization.
	require.ErrorIs(t, removeTeam(t, teamID), models.ErrOrganizationTeamProtected)
}

// TestBraznProvisioningTheTeamRootsTwiceLeavesOne is the other half of
// requirement 2, and it is the assertion the commercial record depends on.
//
// A second 200 alone proves nothing: a build that minted a fresh team and fresh
// roots on every call would answer 200 every time. What makes that outcome
// unrecoverable is that Repository.mapTeamTopology coalesces a team's pair in
// exactly once, so the second set would be orphaned permanently while the
// customer looked at two of everything.
//
// Deleting the provisionedTeamRoot early return in provisionTeamRoots makes
// this test fail three separate ways: two Team roots instead of one, two
// projects titled "Team", and two replies carrying different references.
func TestBraznProvisioningTheTeamRootsTwiceLeavesOne(t *testing.T) {
	env := newManagedEnv(t)

	first := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestPrimaryTeam)))
	second := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestPrimaryTeam)))

	assert.Equal(t, first.TaskTeamRef, second.TaskTeamRef,
		"a repeat answers with the references the first call minted")
	assert.Equal(t, first.TaskProjectRef, second.TaskProjectRef)

	require.Len(t, storedEntities(t, models.ProtectedKindTeamRoot), 1)
	require.Len(t, storedEntities(t, models.ProtectedKindPublicRoot), 1)
	db.AssertCount(t, "projects", builder.Eq{"title": "Team"}, 1)
	db.AssertCount(t, "projects", builder.Eq{"title": "Public"}, 1)
}

// TestBraznProvisioningASecondTeamGetsItsOwnRoots is what tells the idempotence
// key apart from the organization.
//
// An implementation that answered "does this organization have a Team root"
// would pass both tests above and fail here, handing the second team the first
// team's references - and the commercial record would coalesce them in, once,
// permanently, for the wrong team. The Public root is the opposite assertion in
// the same test: it is one per ORGANIZATION, so a second team must not get a
// second one.
func TestBraznProvisioningASecondTeamGetsItsOwnRoots(t *testing.T) {
	env := newManagedEnv(t)

	primary := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestPrimaryTeam)))
	second := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestSecondTeam)))

	assert.NotEqual(t, primary.TaskTeamRef, second.TaskTeamRef,
		"a different commercial team gets a team of its own")
	assert.NotEqual(t, primary.TaskProjectRef, second.TaskProjectRef)

	roots := storedEntities(t, models.ProtectedKindTeamRoot)
	require.Len(t, roots, 2)
	assert.Equal(t, managedTestPrimaryTeam, roots[0].CommercialTeamID)
	assert.Equal(t, managedTestSecondTeam, roots[1].CommercialTeamID)

	require.Len(t, storedEntities(t, models.ProtectedKindPublicRoot), 1)
}

// TestBraznProvisioningGivesASecondTeamTheOrganizationsPublicRoot covers the
// branch ensurePublicRoot takes when the Public root ALREADY EXISTS - the one
// where it grants the arriving team access to the root a previous call minted.
//
// Added by independent QA (BRA-1050). Nothing reached that branch before: every
// test that provisions a second team asserts the Public root was not
// DUPLICATED, and none asserts the second team can SEE it. Deleting the
// grantTeamAccess call on that path therefore left the whole suite green while
// leaving every team but the first unable to reach the one place the
// organization's shared work and its only anonymous read-only links may live.
//
// The first team's grant is asserted alongside it because the two come from
// different statements - the created path and the existing path - and a test
// that checked only the survivor would not say which one ran.
func TestBraznProvisioningGivesASecondTeamTheOrganizationsPublicRoot(t *testing.T) {
	env := newManagedEnv(t)

	primary := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestPrimaryTeam)))
	second := provisionedTeamRoots(t,
		env.provision(teamRootsPayload(managedTopologySubject, managedTestSecondTeam)))

	publics := storedEntities(t, models.ProtectedKindPublicRoot)
	require.Len(t, publics, 1)

	db.AssertExists(t, "team_projects", map[string]interface{}{
		"team_id":    mustParseRef(t, primary.TaskTeamRef),
		"project_id": publics[0].ProjectID,
	}, false)
	db.AssertExists(t, "team_projects", map[string]interface{}{
		"team_id":    mustParseRef(t, second.TaskTeamRef),
		"project_id": publics[0].ProjectID,
	}, false)
}

// TestBraznProvisioningGivesEachSubjectTheirOwnInbox is what tells the Inbox's
// idempotence key apart from "this instance has an Inbox".
//
// Added by independent QA (BRA-1050). Every existing test provisions for user 1
// alone, so personalInbox's owner scoping - the `owner_id = ?` subquery that
// makes the lookup about THIS person - was never exercised. Dropping it would
// have kept all of them green: the second subject's call would find the first
// subject's Inbox, decide the work was done, and answer 200 having created
// nothing. A member with no Inbox is not a visible failure either, because the
// operation reported success.
func TestBraznProvisioningGivesEachSubjectTheirOwnInbox(t *testing.T) {
	env := newManagedEnv(t)

	first := env.provision(personalInboxPayload("1"))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := env.provision(personalInboxPayload("2"))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	inboxes := storedEntities(t, models.ProtectedKindInbox)
	require.Len(t, inboxes, 2)

	// Asserted as a set rather than by position: which row is which is not the
	// property under test, and one Inbox per subject is.
	assert.ElementsMatch(t, []int64{1, 2}, []int64{
		projectOwner(t, inboxes[0].ProjectID),
		projectOwner(t, inboxes[1].ProjectID),
	})
}

// TestBraznProvisioningRefusesATopologyItCannotPlace checks the door on both
// new operations, and checks it by the only thing that matters: nothing was
// stored. A 400 on its own would also be produced by a router that never
// reached the handler, which is why every case below runs against an
// environment where the SAME endpoint and the same key answer 200 for a
// well-formed request.
func TestBraznProvisioningRefusesATopologyItCannotPlace(t *testing.T) {
	refused := func(t *testing.T, payload string) {
		t.Helper()

		env := newManagedEnv(t)
		rec := env.provision(payload)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Empty(t, storedEntities(t, models.ProtectedKindInbox))
		assert.Empty(t, storedEntities(t, models.ProtectedKindTeamRoot))
		assert.Empty(t, storedEntities(t, models.ProtectedKindPublicRoot))
	}

	// The producer creates the account through create_user before it provisions
	// anything for it, so a subject this instance does not have means the two
	// calls disagree about who exists. Inventing the account would be this fork
	// deciding who its customers are.
	t.Run("an Inbox for a subject this instance does not have", func(t *testing.T) {
		refused(t, personalInboxPayload("99999"))
	})

	t.Run("team roots for a subject this instance does not have", func(t *testing.T) {
		refused(t, teamRootsPayload("99999", managedTestPrimaryTeam))
	})

	// An id no producer may mint. It is refused before anything is written
	// rather than stored and matched against later, because what a repeat is
	// matched against is exactly this value.
	t.Run("team roots naming a team id the contract cannot express", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"create_team_roots","organization_id":"`+
			managedTestOrganization+`","team_id":"","user_id":"`+managedTopologySubject+`"}`)
	})

	// The two operations are separate values for this reason: both payloads
	// carry an organization and a subject, so a create_personal_inbox that also
	// named a team would decode cleanly as the other operation if the types
	// shared a header. It does not, and the strict decode is what says so.
	t.Run("an Inbox request carrying a member this build cannot act on", func(t *testing.T) {
		refused(t, `{"contract_version":"1","operation":"create_personal_inbox","organization_id":"`+
			managedTestOrganization+`","team_id":"`+managedTestPrimaryTeam+
			`","user_id":"`+managedTopologySubject+`"}`)
	})
}
