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
	_ "embed"
	"encoding/json"
	"errors"
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

type classifiedRouteEntry struct {
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Class   string      `json:"class"`
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

// decideManagedRule resolves the acting user and their entitlement, then hands
// the request to the rule bound for that edition.
//
// The database session is opened after the user is resolved and closed before
// the handler runs: holding a read session across the handler's write
// deadlocks SQLite, and GetAuthFromClaims opens its own session.
func decideManagedRule(c *echo.Context, rule managedRule) error {
	e := &managedEval{c: c, rule: rule}

	if decide, isPreflight := preflightRules[rule]; isPreflight {
		return decide(e)
	}

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

	decide, hasPolicy := editionRules[rule][projection.State.Edition]
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
// only decideManagedRule fills it in.
type managedEval struct {
	c    *echo.Context
	s    *xorm.Session
	rule managedRule

	user       *user.User
	projection *entitlement.Signed
}

// refuse logs why a request was turned down - a policy refusal is otherwise
// invisible to whoever has to explain it to a customer - and returns the
// response the caller sees.
func (e *managedEval) refuse(reason string) error {
	log.Debugf("[managed] %s %s refused by rule %q for user %d on %q: %s",
		e.c.Request().Method, e.c.Path(), e.rule, e.user.ID, e.projection.State.Edition, reason)
	return errManagedUnavailable()
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
func projectIDParam(path string) string {
	switch {
	case strings.Contains(path, "/projects/:project"):
		return "project"
	case strings.Contains(path, "/projects/:projectid"):
		return "projectid"
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
