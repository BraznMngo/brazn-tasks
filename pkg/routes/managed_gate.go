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
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	auth2 "code.vikunja.io/api/pkg/modules/auth"
	"code.vikunja.io/api/pkg/modules/brazn/entitlement"
	"code.vikunja.io/api/pkg/user"

	"github.com/labstack/echo/v5"
	"xorm.io/xorm"
)

// route-classification.json is the policy table, not a copy of it. The harness
// derives the real route list from RegisterRoutes and fails CI when the two
// disagree, so embedding the same file the test reads is what keeps
// classification and enforcement from drifting apart.
//
//go:embed route-classification.json
var routeClassificationJSON []byte

// managedRule names one policy decision. Every route classified
// protected-topology or access-expanding carries exactly one.
type managedRule string

// The rule vocabulary. This list is the whole of it: a name that is not here
// is a typo, and the harness says so rather than letting it fail silently
// closed at runtime.
const (
	ruleDisabled       managedRule = "disabled"
	ruleServiceManaged managedRule = "service-managed"
	ruleAuthentication managedRule = "authentication"
	ruleAccessRevoke   managedRule = "access-revoke"

	ruleProjectCreate    managedRule = "project-create"
	ruleProjectDuplicate managedRule = "project-duplicate"
	ruleProjectUpdate    managedRule = "project-update"
	ruleProjectDelete    managedRule = "project-delete"
	ruleProjectShare     managedRule = "project-share"
	ruleLinkShare        managedRule = "link-share"
	ruleTeamsOnly        managedRule = "teams-only"
	ruleTaskMove         managedRule = "task-move"
)

var allManagedRules = []managedRule{
	ruleDisabled,
	ruleServiceManaged,
	ruleAuthentication,
	ruleAccessRevoke,
	ruleProjectCreate,
	ruleProjectDuplicate,
	ruleProjectUpdate,
	ruleProjectDelete,
	ruleProjectShare,
	ruleLinkShare,
	ruleTeamsOnly,
	ruleTaskMove,
}

// KnownManagedRules returns every rule name route-classification.json may use,
// sorted. Exported for the classification harness.
func KnownManagedRules() []string {
	names := make([]string, 0, len(allManagedRules))
	for _, r := range allManagedRules {
		names = append(names, string(r))
	}
	sort.Strings(names)
	return names
}

// managedRuleFunc decides one guarded request. Returning nil allows it.
type managedRuleFunc func(e *managedEval) error

// preflightRules are decided without reading any entitlement state, either
// because the answer never depends on it or because - for authentication -
// the subject is not known yet.
var preflightRules = map[managedRule]managedRuleFunc{}

// editionRules holds the per-edition decision for everything else, keyed by
// rule and then by entitlement.Edition*. A guarded route whose rule has no
// entry for the active edition is refused: unmapped means deny, never skip, so
// a route classified before its policy exists is closed rather than open.
var editionRules = map[managedRule]map[string]managedRuleFunc{}

// registerPreflightRule binds a rule that is decided before any entitlement
// state is read.
func registerPreflightRule(rule managedRule, decide managedRuleFunc) {
	preflightRules[rule] = decide
}

// registerEditionRule binds one rule's decision for one edition. Policy is
// added this way so a new edition's rules never have to reopen an existing
// one's.
func registerEditionRule(rule managedRule, edition string, decide managedRuleFunc) {
	if editionRules[rule] == nil {
		editionRules[rule] = map[string]managedRuleFunc{}
	}
	editionRules[rule][edition] = decide
}

// classifiedRouteEntry reads only what enforcement needs from the
// classification file. The class itself is the harness's business.
type classifiedRouteEntry struct {
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Managed managedRule `json:"managed"`
}

// managedRouteRules maps "METHOD /registered/path" to the rule that decides it.
var managedRouteRules = loadManagedRouteRules()

func loadManagedRouteRules() map[string]managedRule {
	var file struct {
		Routes []classifiedRouteEntry `json:"routes"`
	}
	if err := json.Unmarshal(routeClassificationJSON, &file); err != nil {
		// The file is compiled into the binary, so this can only be a broken
		// build, and a build that cannot enforce policy must not start.
		panic("could not parse the embedded route-classification.json: " + err.Error())
	}

	rules := make(map[string]managedRule, len(file.Routes))
	for _, route := range file.Routes {
		if route.Managed != "" {
			rules[route.Method+" "+route.Path] = route.Managed
		}
	}
	return rules
}

// errNotAUser is returned when the request is authenticated as something other
// than a user account.
var errNotAUser = errors.New("request is not authenticated as a user")

// RequireManagedPolicy evaluates Brazn managed-mode policy for the routes
// route-classification.json marks as protected-topology or access-expanding.
//
// It mirrors RequireFeature: one middleware, attached once per route group,
// deciding before the handler runs. Because it sits in front of the handler
// rather than inside the frontend, a browser, Percy and a raw API client all
// get the same answer for the same request.
func RequireManagedPolicy() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !config.BraznManagedMode.GetBool() {
				return next(c)
			}

			rule, guarded := managedRouteRules[c.Request().Method+" "+c.Path()]
			if !guarded {
				return next(c)
			}

			if err := decideManagedRule(c, rule); err != nil {
				return err
			}
			return next(c)
		}
	}
}

// decideManagedRule runs a rule's preflight decision if it has one, and
// otherwise goes straight to the per-edition policy.
func decideManagedRule(c *echo.Context, rule managedRule) error {
	e := &managedEval{c: c, rule: rule}

	if decide, isPreflight := preflightRules[rule]; isPreflight {
		return decide(e)
	}
	return e.decideByEdition()
}

// decideByEdition resolves the acting user and their entitlement, then hands
// the request to the rule bound for that edition. A preflight rule calls it
// when it has worked out that this particular request needs entitlement after
// all - a route can carry both an ordinary and a guarded meaning, and only the
// request itself says which one it is.
//
// The database session is opened after the user is resolved and closed before
// the handler runs: holding a read session across the handler's write
// deadlocks SQLite, and GetAuthFromClaims opens its own session.
func (e *managedEval) decideByEdition() error {
	c := e.c

	acting, err := actingUser(c)
	if err != nil {
		log.Debugf("[managed] %s %s refused: no acting user (%s)", c.Request().Method, c.Path(), err)
		return errManagedUnavailable()
	}
	e.user = acting

	s := db.NewSession()
	defer s.Close()
	e.s = s

	projection, err := models.GetEntitlement(s, acting.ID)
	if err != nil {
		log.Debugf("[managed] %s %s refused for user %d: no valid entitlement projection",
			c.Request().Method, c.Path(), acting.ID)
		return errManagedUnavailable()
	}
	e.projection = projection

	if !projection.Active() {
		return e.refuse("the subscription or seat is not active")
	}

	decide, hasPolicy := editionRules[e.rule][projection.State.Edition]
	if !hasPolicy {
		return e.refuse("no policy is defined for this edition")
	}
	return decide(e)
}

// actingUser resolves the authenticated user. A link share is deliberately not
// one: a share token must never create projects or teams, grant rights, or
// mint further shares, whatever the project it was issued for allows.
func actingUser(c *echo.Context) (*user.User, error) {
	a, err := auth2.GetAuthFromClaims(c)
	if err != nil {
		return nil, err
	}
	u, isUser := a.(*user.User)
	if !isUser {
		return nil, errNotAUser
	}
	return u, nil
}

// managedEval carries what a policy rule needs for one request. Rules read it;
// only decideManagedRule and decideByEdition fill it in.
type managedEval struct {
	c    *echo.Context
	s    *xorm.Session
	rule managedRule

	user       *user.User
	projection *entitlement.Signed

	body     *managedBody
	bodyRead bool
}

// maxManagedBodyPeek bounds what the gate will read to decide a request.
// Anything larger is not a policy payload - the routes that carry real uploads
// are refused before their body is touched.
const maxManagedBodyPeek = 64 << 10

// managedBody is the small slice of a request body that policy needs. Fields
// are pointers so "absent" - a PATCH that does not touch them - stays distinct
// from an explicit zero.
type managedBody struct {
	ProjectID       *int64  `json:"project_id"`
	ParentProjectID *int64  `json:"parent_project_id"`
	Permission      *int    `json:"permission"`
	Username        *string `json:"username"`
	// The bulk update route carries the values it applies one level down, names
	// the fields it writes, and names the tasks it writes them to.
	Values  *managedBodyValues `json:"values"`
	Fields  []string           `json:"fields"`
	TaskIDs []int64            `json:"task_ids"`
}

type managedBodyValues struct {
	ProjectID *int64 `json:"project_id"`
}

// destinationProjectID returns the project a request asks a task to end up in,
// or nil when it names none - which is what tells an ordinary edit apart from
// a move.
func (b *managedBody) destinationProjectID() *int64 {
	if b.ProjectID != nil {
		return b.ProjectID
	}
	if b.Values == nil || b.Values.ProjectID == nil {
		return nil
	}
	// A value carried in `values` but not named in `fields` is not applied, so
	// it is not a move. With no `fields` at all, treat it as one: refusing a
	// request that would not have moved anything is the safe direction to be
	// wrong in.
	if len(b.Fields) == 0 {
		return b.Values.ProjectID
	}
	for _, field := range b.Fields {
		if field == "project_id" {
			return b.Values.ProjectID
		}
	}
	return nil
}

// requestBody reads the body once, puts it back for the handler, and decodes
// the few fields policy looks at.
//
// A body that is missing, oversized, or not JSON decodes to no fields at all.
// That is the honest answer - the request did not state the thing being asked
// about - and a rule that needs a field to permit something must refuse when
// it is absent.
func (e *managedEval) requestBody() *managedBody {
	if e.bodyRead {
		return e.body
	}
	e.bodyRead = true
	e.body = &managedBody{}

	r := e.c.Request()
	if r.Body == nil {
		return e.body
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxManagedBodyPeek+1))
	// Put back everything that was read, plus whatever is still unread, so the
	// handler sees the stream it would have seen.
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), r.Body))
	if err != nil || len(raw) == 0 || len(raw) > maxManagedBodyPeek {
		return e.body
	}

	if err := json.Unmarshal(raw, e.body); err != nil {
		log.Debugf("[managed] %s %s: body is not policy-readable JSON", r.Method, e.c.Path())
	}
	return e.body
}

// ownsProject reports whether the acting user owns a project. Ownership is
// checked directly and no administrator flag bypasses it: neither another
// member nor an organization administrator may reach a member's Inbox.
func (e *managedEval) ownsProject(projectID int64) (bool, error) {
	project, err := models.GetProjectSimpleByID(e.s, projectID)
	if err != nil {
		return false, err
	}
	return project.OwnerID == e.user.ID, nil
}

// refuse logs why a request was turned down - a policy refusal is otherwise
// invisible to whoever has to explain it to a customer - and returns the
// response the caller sees.
//
// A preflight rule refuses before there is a user or a projection to name, so
// both are read defensively: a policy check must not be the thing that panics.
func (e *managedEval) refuse(reason string) error {
	userID := int64(0)
	if e.user != nil {
		userID = e.user.ID
	}
	edition := "unknown"
	if e.projection != nil {
		edition = e.projection.State.Edition
	}

	log.Debugf("[managed] %s %s refused by rule %q for user %d on %q: %s",
		e.c.Request().Method, e.c.Path(), e.rule, userID, edition, reason)
	return errManagedUnavailable()
}

// affectedTaskIDs returns the tasks a request would change: the one named in
// the path on the single-task routes, or the list the bulk route carries.
func (e *managedEval) affectedTaskIDs() []int64 {
	if id, err := strconv.ParseInt(e.c.Param("projecttask"), 10, 64); err == nil && id > 0 {
		return []int64{id}
	}
	return e.requestBody().TaskIDs
}

// movesTaskBetweenProjects reports whether a request would put a task
// somewhere other than where it already is.
//
// It runs before any entitlement is read, so it opens and closes its own
// session; decideByEdition opens a second one afterwards if the answer is yes.
// Two short sequential reads, both closed before the handler runs, which is
// what SQLite needs.
func (e *managedEval) movesTaskBetweenProjects(destination int64) (bool, error) {
	s := db.NewSession()
	defer s.Close()

	return models.TasksOutsideProject(s, e.affectedTaskIDs(), destination)
}

// projectID returns the id of the project a guarded route acts on, or 0 when
// the route does not name one.
func (e *managedEval) projectID() int64 {
	param := projectIDParam(e.c.Path())
	if param == "" {
		return 0
	}
	id, err := strconv.ParseInt(e.c.Param(param), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// projectIDParam returns the path parameter naming the project a route acts
// on, or "" when it targets none. Derived from the registered path because the
// two API versions name the same thing :project, :projectid and :id, and a
// per-route table of that would be one more thing to keep in step.
// The order matters: ":projectid" is tested first because ":project" is a
// prefix of it, and matching that loosely would silently resolve the duplicate
// routes to an empty parameter - a policy check reading id 0 and finding
// nothing to protect.
func projectIDParam(path string) string {
	switch {
	case strings.Contains(path, "/projects/:projectid"):
		return "projectid"
	case strings.Contains(path, "/projects/:project/"), strings.HasSuffix(path, "/projects/:project"):
		return "project"
	case strings.Contains(path, "/projects/:id"):
		return "id"
	}
	return ""
}

// errManagedUnavailable is the refusal every policy rule returns. The wording
// is deliberately flat: it never names another plan and never implies an
// upgrade path, because this release has none to offer.
func errManagedUnavailable() error {
	return echo.NewHTTPError(http.StatusForbidden,
		"This operation is managed by Brazn and is not available for this account.")
}

// errManagedDisabled mirrors RequireFeature: something the managed edition
// does not offer answers exactly like a route that was never registered.
func errManagedDisabled() error {
	return echo.ErrNotFound
}
