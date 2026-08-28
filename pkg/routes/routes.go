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

// swaggo joins @description lines within a single comment block and, because of
// the blank line between the two blocks below, emits only the second one — so
// anything that must actually reach the served spec belongs in that block.
// pkg/swagger/{docs.go,swagger.json,swagger.yaml} are generated from these
// annotations by `mage generate:swagger-docs`, and there is no CI drift check,
// so any change here must be mirrored into all three by hand or the served v1
// spec silently stays stale until the next release run.

// @title Brazn Tasks API
// @description This is the documentation for the Brazn Tasks API. Brazn Tasks is a cross-platform To-do-application with a lot of features, such as sharing projects with users or teams. <!-- ReDoc-Inject: <security-definitions> -->

// @description Brazn Tasks is a modified fork of [Vikunja](https://vikunja.io) v2.4.0. Source: https://github.com/BraznMngo/brazn-tasks
// @description # Pagination
// @description Every endpoint capable of pagination will return two headers:
// @description * `x-pagination-total-pages`: The total number of available pages for this request
// @description * `x-pagination-result-count`: The number of items returned for this request.
// @description # Permissions
// @description All endpoints which return a single item (project, task, etc.) - no array - will also return a `x-max-permission` header with the max permission the user has on this item as an int where `0` is `Read Only`, `1` is `Read & Write` and `2` is `Admin`.
// @description This can be used to show or hide ui elements based on the permissions the user has.
// @description # Errors
// @description All errors have an error code and a human-readable error message in addition to the http status code. You should always check for the status code in the response, not only the http status code.
// @description Due to limitations in the swagger library we're using for this document, only one error per http status code is documented here. Make sure to check the [error docs](https://vikunja.io/docs/errors/) in the upstream Vikunja documentation for a full list of available error codes. Brazn Tasks is a modified fork of Vikunja and the error codes are unchanged.
// @description # Authorization
// @description **JWT-Auth:** Main authorization method, used for most of the requests. Needs `Authorization: Bearer <jwt-token>`-header to authenticate successfully.
// @description
// @description **API Token:** You can create scoped API tokens for your user and use the token to make authenticated requests in the context of that user. The token must be provided via an `Authorization: Bearer <token>` header, similar to jwt auth. See the documentation for the `api` group to manage token creation and revocation.
// @description
// @description **BasicAuth:** Only used when requesting tasks via CalDAV.
// @description <!-- ReDoc-Inject: <security-definitions> -->
// @BasePath /api/v1

// @license.url https://github.com/BraznMngo/brazn-tasks/blob/main/LICENSE
// @license.name AGPL-3.0-or-later

// @contact.url https://github.com/BraznMngo/brazn-tasks/issues
// @contact.name Brazn Tasks contact
// @contact.email hello@braznmngo.com

// @securityDefinitions.basic BasicAuth

// @securityDefinitions.apikey JWTKeyAuth
// @in header
// @name Authorization

package routes

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/license"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/models"
	"code.vikunja.io/api/pkg/modules/auth/oauth2server"
	"code.vikunja.io/api/pkg/modules/auth/openid"
	"code.vikunja.io/api/pkg/modules/background"
	backgroundHandler "code.vikunja.io/api/pkg/modules/background/handler"
	"code.vikunja.io/api/pkg/modules/background/unsplash"
	"code.vikunja.io/api/pkg/modules/background/upload"
	"code.vikunja.io/api/pkg/modules/migration"
	csvmigrator "code.vikunja.io/api/pkg/modules/migration/csv"
	migrationHandler "code.vikunja.io/api/pkg/modules/migration/handler"
	microsofttodo "code.vikunja.io/api/pkg/modules/migration/microsoft-todo"
	"code.vikunja.io/api/pkg/modules/migration/ticktick"
	"code.vikunja.io/api/pkg/modules/migration/todoist"
	"code.vikunja.io/api/pkg/modules/migration/trello"
	vikunja_file "code.vikunja.io/api/pkg/modules/migration/vikunja-file"
	"code.vikunja.io/api/pkg/modules/migration/wekan"
	"code.vikunja.io/api/pkg/plugins"
	apiv1 "code.vikunja.io/api/pkg/routes/api/v1"
	adminapi "code.vikunja.io/api/pkg/routes/api/v1/admin"
	apiv2 "code.vikunja.io/api/pkg/routes/api/v2"
	"code.vikunja.io/api/pkg/routes/caldav"
	"code.vikunja.io/api/pkg/routes/feeds"
	vmiddleware "code.vikunja.io/api/pkg/routes/middleware"
	"code.vikunja.io/api/pkg/version"
	"code.vikunja.io/api/pkg/web/handler"
	ws "code.vikunja.io/api/pkg/websocket"

	"github.com/getsentry/sentry-go"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/ulule/limiter/v3"
)

// matchCORSOrigin checks if an origin matches any of the allowed origin patterns.
// It supports wildcards in the port position (e.g., "http://127.0.0.1:*").
func matchCORSOrigin(origin string, allowedOrigins []string) (string, bool, error) {
	for _, pattern := range allowedOrigins {
		// Exact match
		if origin == pattern {
			return origin, true, nil
		}
		// Allow all
		if pattern == "*" {
			return origin, true, nil
		}
		// Handle wildcard port patterns like "http://127.0.0.1:*" or "http://localhost:*"
		if strings.HasSuffix(pattern, ":*") {
			prefix := strings.TrimSuffix(pattern, ":*")
			// Check if the origin starts with the prefix and has a port after
			if strings.HasPrefix(origin, prefix+":") {
				return origin, true, nil
			}
			// Also match if origin has no port but pattern allows any port
			if origin == prefix {
				return origin, true, nil
			}
		}
	}
	return "", false, nil
}

// NewEcho registers a new Echo instance
func NewEcho() *echo.Echo {
	// Configure Echo with a router that unescapes path parameters.
	// This is needed because Echo v5 does not unescape path params by default.
	// Without this, path parameters like usernames with spaces or apostrophes
	// would remain URL-encoded (e.g., "John%20D%27Urso" instead of "John D'Urso").
	// See https://kolaente.dev/vikunja/vikunja/issues/1224
	e := echo.NewWithConfig(echo.Config{
		Router: echo.NewRouter(echo.RouterConfig{
			UnescapePathParamValues: true,
		}),
		// Since echo v5.3.0 groups implicitly register 404 routes when middleware
		// is added. Our route setup creates multiple groups with the same prefix
		// (e.g. rate-limit subgroups of /api/v1), which would panic as duplicates.
		NoGroupAutoRegister404Routes: true,
	})

	// Configure IP extraction to prevent rate limit bypass via spoofed headers.
	// Echo's default RealIP() trusts X-Forwarded-For and X-Real-IP unconditionally,
	// which allows attackers to bypass IP-based rate limits.
	// See: https://echo.labstack.com/docs/ip-address
	switch config.ServiceIPExtractionMethod.GetString() {
	case "xff":
		trustOptions := parseTrustedProxies(config.ServiceTrustedProxies.GetString())
		e.IPExtractor = echo.ExtractIPFromXFFHeader(trustOptions...)
		log.Debugf("IP extraction: X-Forwarded-For with %d trusted proxy ranges", len(trustOptions))
	case "realip":
		trustOptions := parseTrustedProxies(config.ServiceTrustedProxies.GetString())
		e.IPExtractor = echo.ExtractIPFromRealIPHeader(trustOptions...)
		log.Debugf("IP extraction: X-Real-IP with %d trusted proxy ranges", len(trustOptions))
	default:
		e.IPExtractor = echo.ExtractIPDirect()
		log.Debugf("IP extraction: direct (TCP remote address)")
	}

	e.Logger = log.NewEchoLogger(config.LogEnabled.GetBool(), config.LogHTTP.GetString(), config.LogFormat.GetString())

	// First middleware in the chain so every request has an ID — reuses the
	// X-Request-Id header from a proxy or generates one — and everything
	// downstream (logging, audit) sees the same value.
	e.Use(middleware.RequestID())

	// Logger
	if config.LogEnabled.GetBool() && config.LogHTTP.GetString() != "off" {
		httpLogger := log.NewHTTPLogger(config.LogEnabled.GetBool(), config.LogHTTP.GetString(), config.LogFormat.GetString())
		e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
			LogStatus:    true,
			LogURI:       true,
			LogMethod:    true,
			LogLatency:   true,
			LogRemoteIP:  true,
			LogUserAgent: true,
			HandleError:  true,
			LogValuesFunc: func(_ *echo.Context, v middleware.RequestLoggerValues) error {
				if v.Error == nil {
					httpLogger.LogAttrs(context.Background(), slog.LevelInfo, "",
						slog.String("remote_ip", v.RemoteIP),
						slog.String("method", v.Method),
						slog.String("uri", v.URI),
						slog.Int("status", v.Status),
						slog.Duration("latency", v.Latency),
						slog.String("user_agent", v.UserAgent),
					)
				} else {
					httpLogger.LogAttrs(context.Background(), slog.LevelError, "",
						slog.String("remote_ip", v.RemoteIP),
						slog.String("method", v.Method),
						slog.String("uri", v.URI),
						slog.Int("status", v.Status),
						slog.Duration("latency", v.Latency),
						slog.String("user_agent", v.UserAgent),
						slog.String("err", v.Error.Error()),
					)
				}
				return nil
			},
		}))
	}

	// panic recover
	e.Use(middleware.Recover())

	// Normalize PHP-style `foo[]=...` query params to `foo=...` before any
	// handler binds them. Runs globally so both /api/v1 and /api/v2 benefit.
	e.Use(vmiddleware.NormalizeArrayParams())

	if config.AuditEnabled.GetBool() {
		e.Use(vmiddleware.RequestMeta())
	}

	setupSentry(e)

	// Validation
	e.Validator = &CustomValidator{}

	// Set body limit to allow file uploads up to the configured size
	// Add some overhead for multipart form data (headers, boundaries, etc.)
	maxFileSize := config.GetMaxFileSizeInMBytes()
	// #nosec G115 - maxFileSize is a configuration value that won't exceed int64 max in practice
	e.Use(middleware.BodyLimit((int64(maxFileSize) + 2) * 1024 * 1024))

	// Set up centralized error handler
	e.HTTPErrorHandler = CreateHTTPErrorHandler(e, config.SentryEnabled.GetBool())

	return e
}

func parseTrustedProxies(proxies string) []echo.TrustOption {
	if proxies == "" {
		return nil
	}

	var options []echo.TrustOption
	for _, cidr := range strings.Split(proxies, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Warningf("Invalid trusted proxy CIDR %q: %v", cidr, err)
			continue
		}
		options = append(options, echo.TrustIPRange(ipNet))
	}
	return options
}

func setupSentry(e *echo.Echo) {
	if !config.SentryEnabled.GetBool() {
		return
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              config.SentryDsn.GetString(),
		AttachStacktrace: true,
		Release:          version.Version,
	}); err != nil {
		log.Criticalf("Sentry init failed: %s", err)
	}
	defer sentry.Flush(5 * time.Second)

	e.Use(SentryMiddleware(SentryOptions{
		Repanic: true,
	}))
}

// RegisterRoutes registers all routes for the application
func RegisterRoutes(e *echo.Echo) {

	if config.ServiceEnableCaldav.GetBool() {
		// Caldav routes
		wkg := e.Group("/.well-known")
		wkg.Use(middleware.BasicAuth(caldav.BasicAuth))
		// AFTER the basic auth it depends on and BEFORE the routes it guards.
		// Group middleware is snapshotted when a route is added, so one attached
		// below these two lines would silently guard nothing. See
		// registerCalDavRoutes for why this surface needs the gate at all.
		wkg.Use(RequireManagedPolicy())
		wkg.Any("/caldav", caldav.PrincipalHandler)
		wkg.Any("/caldav/", caldav.PrincipalHandler)
		c := e.Group("/dav")
		registerCalDavRoutes(c)
	}

	// Feeds routes (Atom feed for user notifications)
	f := e.Group("/feeds")
	f.Use(middleware.BasicAuth(feeds.BasicAuth))
	f.GET("/notifications.atom", feeds.NotificationsAtomFeed)

	// healthcheck
	e.GET("/health", HealthcheckHandler)

	setupStaticFrontendFilesHandler(e)

	// CORS
	if config.CorsEnable.GetBool() {
		allowedOrigins := config.CorsOrigins.GetStringSlice()
		log.Infof("CORS enabled with origins: %s", strings.Join(allowedOrigins, ", "))

		// Echo v5 CORS middleware is stricter and doesn't accept wildcards in ports like "http://127.0.0.1:*"
		// We use UnsafeAllowOriginFunc to handle these patterns for backwards compatibility
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: []string{}, // Empty because we use UnsafeAllowOriginFunc
			UnsafeAllowOriginFunc: func(_ *echo.Context, origin string) (string, bool, error) {
				return matchCORSOrigin(origin, allowedOrigins)
			},
			AllowCredentials: true,
			MaxAge:           config.CorsMaxAge.GetInt(),
			Skipper: func(context *echo.Context) bool {
				// Since it is not possible to register this middleware just for the api group,
				// we just disable it when for caldav requests.
				// Caldav requires OPTIONS requests to be answered in a specific manner,
				// not doing this would break the caldav implementation.
				// Feed readers are server-side and don't need CORS either.
				p := context.Path()
				return strings.HasPrefix(p, "/dav") || strings.HasPrefix(p, "/feeds")
			},
		}))
	}

	// API Routes
	a := e.Group("/api/v1")
	registerAPIRoutes(a)

	// /api/v2 — Huma-backed API, scaffolded alongside /api/v1.
	a2 := e.Group("/api/v2")
	registerAPIRoutesV2(e, a2)

	// Collect routes for API token permissions
	// In Echo v5, we collect routes after registration using e.Router().Routes()
	collectRoutesForAPITokens(e)
}

// unauthenticatedAPIPaths contains paths that don't require JWT authentication
var unauthenticatedAPIPaths = map[string]bool{
	"/api/v1/register":                       true,
	"/api/v1/user/password/token":            true,
	"/api/v1/user/password/reset":            true,
	"/api/v1/user/confirm":                   true,
	"/api/v1/user/confirm/resend":            true,
	"/api/v1/login":                          true,
	"/api/v1/user/token/refresh":             true,
	"/api/v1/auth/openid/:provider/callback": true,
	"/api/v1/test/:table":                    true,
	"/api/v1/info":                           true,
	"/api/v1/shares/:share/auth":             true,
	"/api/v1/docs.json":                      true,
	"/api/v1/docs":                           true,
	"/api/v1/docs/redoc.standalone.js":       true,
	"/api/v1/metrics":                        true,
	"/api/v1/oauth/token":                    true,

	// The entitlement projection ingest authenticates the signed message, not
	// the caller, so it takes no JWT and must not be offered as something an
	// API token can be scoped to. The provisioning channel is the same shape
	// for the same reason.
	"/api/v1/brazn/entitlements": true,
	"/api/v1/brazn/provisioning": true,

	"/api/v2/openapi.json":              true,
	"/api/v2/openapi.yaml":              true,
	"/api/v2/openapi-3.0.json":          true,
	"/api/v2/openapi-3.0.yaml":          true,
	"/api/v2/docs":                      true,
	"/api/v2/docs/scalar.standalone.js": true,
	"/api/v2/schemas/:schema":           true,
	"/api/v2/info":                      true,

	"/api/v2/register":                       true,
	"/api/v2/user/password/token":            true,
	"/api/v2/user/password/reset":            true,
	"/api/v2/user/confirm":                   true,
	"/api/v2/user/confirm/resend":            true,
	"/api/v2/shares/:share/auth":             true,
	"/api/v2/oauth/token":                    true,
	"/api/v2/login":                          true,
	"/api/v2/user/token/refresh":             true,
	"/api/v2/auth/openid/:provider/callback": true,

	// Testing endpoints authenticate with the testing token via a custom
	// Authorization header, not a JWT; mounted only when that token is set.
	"/api/v2/test/all":    true,
	"/api/v2/test/:table": true,

	// Public infra healthcheck (a Huma op that opts out of the global auth).
	"/api/v2/health": true,

	// Atom feed (a Huma op) authenticates itself with HTTP Basic auth (a
	// feeds-scoped API token), like its /feeds counterpart, not a JWT.
	"/api/v2/notifications.atom": true,

	// WebSocket upgrade (a raw echo route — OpenAPI can't model WebSockets);
	// it authenticates via its first message, so the upgrade needs no JWT.
	"/api/v2/ws": true,
}

// collectRoutesForAPITokens collects all routes for API token permission checking.
// In Echo v5, OnAddRouteHandler was removed, so we collect routes after registration.
func collectRoutesForAPITokens(e *echo.Echo) {
	routeList := e.Router().Routes()
	log.Debugf("Collecting %d routes for API token usage", len(routeList))
	for _, route := range routeList {
		// Only process API routes
		if !strings.HasPrefix(route.Path, "/api/v1") && !strings.HasPrefix(route.Path, "/api/v2") {
			continue
		}

		// Check if this route requires JWT authentication
		requiresJWT := !unauthenticatedAPIPaths[route.Path]

		models.CollectRoutesForAPITokenUsage(route, requiresJWT)
	}
}

// noStoreCacheControl returns middleware that sets `Cache-Control: no-store`
// on all responses. Without this, browsers may heuristically cache JSON
// responses which causes stale data (e.g. newly team-shared projects not
// appearing until a hard refresh). Applied to both /api/v1 and /api/v2.
func noStoreCacheControl() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set("Cache-Control", "no-store")
			return next(c)
		}
	}
}

const v2AdminPathPrefix = "/api/v2/admin"

// gateV2AdminRoutes reuses v1's RequireFeature/RequireInstanceAdmin gate (both
// 404-on-failure) as path-scoped middleware: splitting v2 into a gated Echo
// sub-group would split the Huma API and drop admin ops from the OpenAPI spec.
func gateV2AdminRoutes() echo.MiddlewareFunc {
	feature := RequireFeature(license.FeatureAdminPanel)
	admin := RequireInstanceAdmin()
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		gated := feature(admin(next))
		return func(c *echo.Context) error {
			if strings.HasPrefix(c.Request().URL.Path, v2AdminPathPrefix) {
				return gated(c)
			}
			return next(c)
		}
	}
}

// registerAPIRoutesV2 wires the /api/v2 Echo group. Token middleware is
// attached before any route so Huma's spec and Scalar docs share the
// resource handlers' stack; unauthenticatedAPIPaths keeps them public.
func registerAPIRoutesV2(e *echo.Echo, a *echo.Group) {
	a.Use(noStoreCacheControl())
	a.Use(SetupTokenMiddleware())
	// Match the authenticated v1 group: rate limiting and route metrics
	// apply to v2 resource endpoints too.
	setupRateLimit(a, config.RateLimitKind.GetString())
	setupMetricsMiddleware(a)
	// v2 registers everything on this one group, authenticated or not, so a
	// single attachment covers the whole version. It runs before the admin
	// gate so an admin route refused by managed-mode policy answers the same
	// on v2 as it does on v1, where the gate is inherited from the parent
	// group and therefore already runs first.
	a.Use(RequireManagedPolicy())
	// Must come after rate limiting: the gate does a per-request admin DB read,
	// so an unauthenticated flood to /api/v2/admin/* would otherwise be unbounded.
	a.Use(gateV2AdminRoutes())

	api := apiv2.NewAPI(e, a)

	// Scalar docs UI — embedded, no CDN. See pkg/routes/api/v2/docs.go.
	a.GET("/docs", apiv2.ScalarUI)
	a.GET("/docs/scalar.standalone.js", apiv2.ScalarJS)

	// WebSockets can't be modeled in OpenAPI and Huma has no WS support, so the
	// upgrade endpoint stays a raw echo route (outside the Huma spec). It
	// authenticates via its first message, so unauthenticatedAPIPaths exempts it
	// from the group's JWT middleware. Health and the Atom feed are Huma ops and
	// self-register via init()/RegisterAll.
	a.GET("/ws", ws.UpgradeHandler)

	// Resources self-register via init(); RegisterAll runs them all + AutoPatch.
	apiv2.RegisterAll(api)
}

func registerAPIRoutes(a *echo.Group) {

	// Prevent browsers from caching API responses. Without an explicit
	// Cache-Control header browsers may heuristically cache JSON responses
	// which causes stale data (e.g. newly team-shared projects not appearing
	// until a hard refresh).
	a.Use(noStoreCacheControl())

	// This is the group with no auth
	// It is its own group to be able to rate limit this based on different heuristics
	n := a.Group("")
	setupRateLimit(n, "ip")
	// v1 splits into three groups that are created before the auth middleware
	// is attached, so the managed-mode gate is attached to each of them. It is
	// still one mechanism reading one table; only the plumbing is threefold.
	n.Use(RequireManagedPolicy())

	// Docs
	n.GET("/docs.json", apiv1.DocsJSON)
	n.GET("/docs", apiv1.RedocUI)
	n.GET("/docs/redoc.standalone.js", apiv1.RedocJS)

	// WebSocket (auth happens after upgrade via first message)
	n.GET("/ws", ws.UpgradeHandler)

	// Prometheus endpoint
	setupMetrics(n)

	// Separate route for unauthenticated routes to enable rate limits for it
	ur := a.Group("")
	rate := limiter.Rate{
		Period: 60 * time.Second,
		Limit:  config.RateLimitNoAuthRoutesLimit.GetInt64(),
	}
	rateLimiter := createRateLimiter(rate)
	ur.Use(RateLimit(rateLimiter, "ip"))
	ur.Use(RequireManagedPolicy())

	// The commercial ingest routes' own group (BRA-1208), unconditional like
	// ur's for the same reason: a protection that exists only when an
	// unrelated switch (ratelimit.enabled) happens to be on is a protection
	// nobody can rely on. Not ur itself - ur's limit is sized for humans
	// guessing a password, and every request on these two routes legitimately
	// arrives from one address (the commercial service), so a per-human-guesser budget
	// would throttle the one real caller long before it bothered an attacker.
	bi := a.Group("")
	braznIngestRate := limiter.Rate{
		Period: 60 * time.Second,
		Limit:  config.RateLimitBraznIngestLimit.GetInt64(),
	}
	bi.Use(RateLimit(createRateLimiter(braznIngestRate), "ip"))
	bi.Use(RequireManagedPolicy())

	if config.AuthLocalEnabled.GetBool() {
		ur.POST("/register", apiv1.RegisterUser)
		ur.POST("/user/password/token", apiv1.UserRequestResetPasswordToken)
		ur.POST("/user/password/reset", apiv1.UserResetPassword)
		ur.POST("/user/confirm", apiv1.UserConfirmEmail)
		ur.POST("/user/confirm/resend", apiv1.UserResendEmailConfirmation)
	}

	if config.AuthLocalEnabled.GetBool() || config.AuthLdapEnabled.GetBool() {
		ur.POST("/login", apiv1.Login)
	}

	// Refresh token endpoint — unauthenticated because it uses the refresh
	// token cookie instead of a JWT bearer token.
	ur.POST("/user/token/refresh", apiv1.RefreshToken)

	if config.AuthOpenIDEnabled.GetBool() {
		ur.POST("/auth/openid/:provider/callback", openid.HandleCallback)
	}

	// OAuth 2.0 token endpoint — unauthenticated because it validates
	// credentials (authorization code or refresh token) itself.
	ur.POST("/oauth/token", oauth2server.HandleToken)

	// Testing
	if config.ServiceTestingtoken.GetString() != "" {
		n.DELETE("/test/all", apiv1.HandleTestingTruncateAll)
		n.PATCH("/test/:table", apiv1.HandleTesting)
	}

	// Info endpoint
	n.GET("/info", apiv1.Info)

	// Entitlement projection ingest (BRA-913). The commercial service pushes
	// here; the handler authenticates the message rather than the connection,
	// and refuses everything while brazn.entitlementkeys is empty - which is
	// the default, so an instance nobody configured is inert rather than open.
	//
	// Registered on bi and neither n nor ur (BRA-1208). Not ur, even though it
	// takes no JWT: ur's ten-requests-per-minute-per-IP cap is sized for
	// humans guessing passwords, and every projection in the system arrives
	// from one address. Not n either any more - n's own limiter is attached
	// only when ratelimit.enabled is set, and that defaults to false, which
	// used to mean this route had no rate limit at all on a stock instance
	// while still writing a log line per refusal. bi's limiter has no such
	// switch.
	//
	// Registered unconditionally, like every other route here. Gating it on
	// brazn.managedmode would make the route table depend on an operator's
	// config, and the classification harness derives the mutating surface from
	// this function - a route that appears only sometimes is a route that is
	// sometimes unclassified.
	bi.POST("/brazn/entitlements", apiv1.BraznApplyEntitlementProjection)

	// The provisioning channel (BRA-1018). Registered on bi (BRA-1208),
	// unconditionally, and unauthenticated at the transport for every reason
	// the entitlement ingest above is: it is the same seam, authenticated the
	// same way, and refuses everything while brazn.entitlementkeys is empty.
	//
	// ONE ROUTE, NOT ONE PER OPERATION. The operation is a value inside the
	// signed payload, so the second one (BRA-1026's organization roots) needs
	// no second route, no second classification entry and no second argument
	// about how a service-plane route is classified.
	//
	// This is the recorded exception to AGENTS.md's v1-freeze: it is a
	// signature-authenticated service-plane channel for the commercial service, not a
	// JWT-authenticated resource for product clients, so new operations land
	// here rather than on /api/v2.
	bi.POST("/brazn/provisioning", apiv1.BraznProvision)

	// Link share auth
	if config.ServiceEnableLinkSharing.GetBool() {
		ur.POST("/shares/:share/auth", apiv1.AuthenticateLinkShare)
	}

	// ===== Routes with Authentication =====
	a.Use(SetupTokenMiddleware())

	// Rate limit
	setupRateLimit(a, config.RateLimitKind.GetString())

	// Middleware to collect metrics
	setupMetricsMiddleware(a)

	// Must come after the auth middleware: policy is decided for the
	// authenticated user, and every group created below inherits this.
	a.Use(RequireManagedPolicy())

	a.GET("/token/test", apiv1.TestToken)
	a.POST("/token/test", apiv1.CheckToken)
	a.GET("/routes", models.GetAvailableAPIRoutesForToken)

	// OAuth 2.0 authorize endpoint — requires authentication.
	a.POST("/oauth/authorize", oauth2server.HandleAuthorize)

	// The Organization area (BRA-917). Registered on `a`, so the two mutating
	// routes are decided by RequireManagedPolicy before their handler runs -
	// they are classified organization-admin in route-classification.json.
	//
	// The GET is NOT classified and cannot be: the classification harness
	// derives its inventory from the mutating surface and treats a read-only
	// method as out of scope, so an entry for it would be reported as stale.
	// Its refusal therefore lives in the handler, which calls the same
	// models.OrganizationFor the rule does. That is the one place on this
	// surface where the check is not middleware, and it is why it is a shared
	// function rather than a rule body.
	a.GET("/brazn/organization", apiv1.BraznGetOrganization)
	a.PUT("/brazn/organization/teams", apiv1.BraznCreateOrganizationTeam)
	a.DELETE("/brazn/organization/teams/:team", apiv1.BraznDeleteOrganizationTeam)

	// Whether a project sits beneath the organization's Public root (BRA-1343),
	// so the link-share toggle can be drawn honestly. Also unclassified and for
	// the same reason as the GET above: it is a read, gated by the handler's own
	// ordinary project-read check rather than by the managed-policy table.
	a.GET("/brazn/projects/:project/public-root", apiv1.BraznGetProjectRoot)

	// Resolve the caller's Percy Feedback sub-project (BRA-1414). Unclassified
	// like the GETs above: it is a read that may create the sub-project once,
	// gated by authentication only. When brazn.feedbackowner is unset it
	// answers available=false rather than inventing a project.
	a.GET("/brazn/feedback/project", apiv1.BraznGetFeedbackProject)

	// Avatar endpoint
	a.GET("/avatar/:username", apiv1.GetAvatar)

	// User stuff
	u := a.Group("/user")

	u.GET("", apiv1.UserShow)
	u.POST("/password", apiv1.UserChangePassword)
	u.GET("s", apiv1.UserList)
	u.POST("/token", apiv1.RenewToken)
	u.POST("/logout", apiv1.Logout)
	u.POST("/settings/email", apiv1.UpdateUserEmail)
	u.GET("/settings/avatar", apiv1.GetUserAvatarProvider)
	u.POST("/settings/avatar", apiv1.ChangeUserAvatarProvider)
	u.PUT("/settings/avatar/upload", apiv1.UploadAvatar)
	u.POST("/settings/general", apiv1.UpdateGeneralUserSettings)
	u.POST("/export/request", apiv1.RequestUserDataExport)
	u.POST("/export/download", apiv1.DownloadUserDataExport)
	u.GET("/export", apiv1.GetUserExportStatus)
	u.GET("/timezones", apiv1.GetAvailableTimezones)
	u.PUT("/settings/token/caldav", apiv1.GenerateCaldavToken)
	u.GET("/settings/token/caldav", apiv1.GetCaldavTokens)
	u.DELETE("/settings/token/caldav/:id", apiv1.DeleteCaldavToken)

	sessionProvider := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Session{}
		},
	}
	u.GET("/sessions", sessionProvider.ReadAllWeb)
	u.DELETE("/sessions/:session", sessionProvider.DeleteWeb)

	// User-level webhooks
	if config.WebhooksEnabled.GetBool() {
		u.GET("/settings/webhooks", apiv1.GetUserWebhooks)
		u.GET("/settings/webhooks/events", apiv1.GetUserDirectedWebhookEvents)
		u.PUT("/settings/webhooks", apiv1.CreateUserWebhook)
		u.POST("/settings/webhooks/:webhook", apiv1.UpdateUserWebhook)
		u.DELETE("/settings/webhooks/:webhook", apiv1.DeleteUserWebhook)
	}

	if config.ServiceEnableTotp.GetBool() {
		u.GET("/settings/totp", apiv1.UserTOTP)
		u.POST("/settings/totp/enroll", apiv1.UserTOTPEnroll)
		u.POST("/settings/totp/enable", apiv1.UserTOTPEnable)
		u.POST("/settings/totp/disable", apiv1.UserTOTPDisable)
		u.GET("/settings/totp/qrcode", apiv1.UserTOTPQrCode)
	}

	// User deletion
	if config.ServiceEnableUserDeletion.GetBool() {
		u.POST("/deletion/request", apiv1.UserRequestDeletion)
		u.POST("/deletion/confirm", apiv1.UserConfirmDeletion)
		u.POST("/deletion/cancel", apiv1.UserCancelDeletion)
	}

	// Bot users
	botHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.BotUser{}
		},
	}
	u.PUT("/bots", botHandler.CreateWeb)
	u.GET("/bots", botHandler.ReadAllWeb)
	u.GET("/bots/:bot", botHandler.ReadOneWeb)
	u.POST("/bots/:bot", botHandler.UpdateWeb)
	u.DELETE("/bots/:bot", botHandler.DeleteWeb)

	projectHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Project{}
		},
	}
	a.GET("/projects", projectHandler.ReadAllWeb)
	a.GET("/projects/:project", projectHandler.ReadOneWeb)
	a.POST("/projects/:project", projectHandler.UpdateWeb)
	a.DELETE("/projects/:project", projectHandler.DeleteWeb)
	a.PUT("/projects", projectHandler.CreateWeb)
	a.GET("/projects/:project/projectusers", apiv1.ListUsersForProject)

	if config.ServiceEnableLinkSharing.GetBool() {
		projectSharingHandler := &handler.WebHandler{
			EmptyStruct: func() handler.CObject {
				return &models.LinkSharing{}
			},
		}
		a.PUT("/projects/:project/shares", projectSharingHandler.CreateWeb)
		a.GET("/projects/:project/shares", projectSharingHandler.ReadAllWeb)
		a.GET("/projects/:project/shares/:share", projectSharingHandler.ReadOneWeb)
		a.DELETE("/projects/:project/shares/:share", projectSharingHandler.DeleteWeb)
	}

	taskCollectionHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskCollection{}
		},
	}
	a.GET("/projects/:project/views/:view/tasks", taskCollectionHandler.ReadAllWeb)
	a.GET("/projects/:project/tasks", taskCollectionHandler.ReadAllWeb)

	kanbanBucketHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Bucket{}
		},
	}
	a.GET("/projects/:project/views/:view/buckets", kanbanBucketHandler.ReadAllWeb)
	a.PUT("/projects/:project/views/:view/buckets", kanbanBucketHandler.CreateWeb)
	a.POST("/projects/:project/views/:view/buckets/:bucket", kanbanBucketHandler.UpdateWeb)
	a.DELETE("/projects/:project/views/:view/buckets/:bucket", kanbanBucketHandler.DeleteWeb)

	projectDuplicateHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.ProjectDuplicate{}
		},
	}
	a.PUT("/projects/:projectid/duplicate", projectDuplicateHandler.CreateWeb)

	taskHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Task{}
		},
	}
	a.PUT("/projects/:project/tasks", taskHandler.CreateWeb)
	a.GET("/tasks/:projecttask", taskHandler.ReadOneWeb)
	a.GET("/projects/:project/tasks/by-index/:index", taskHandler.ReadOneWeb, ResolveProjectIdentifier())
	a.GET("/tasks", taskCollectionHandler.ReadAllWeb)
	a.DELETE("/tasks/:projecttask", taskHandler.DeleteWeb)
	a.POST("/tasks/:projecttask", taskHandler.UpdateWeb)

	taskDuplicateHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskDuplicate{}
		},
	}
	a.PUT("/tasks/:projecttask/duplicate", taskDuplicateHandler.CreateWeb)

	taskUnreadStatusHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskUnreadStatus{}
		},
	}

	a.POST("/tasks/:projecttask/read", taskUnreadStatusHandler.UpdateWeb)

	taskPositionHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskPosition{}
		},
	}
	a.POST("/tasks/:task/position", taskPositionHandler.UpdateWeb)

	bulkTaskHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.BulkTask{}
		},
	}
	a.POST("/tasks/bulk", bulkTaskHandler.UpdateWeb)

	assigneeTaskHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskAssginee{}
		},
	}
	a.PUT("/tasks/:projecttask/assignees", assigneeTaskHandler.CreateWeb)
	a.DELETE("/tasks/:projecttask/assignees/:user", assigneeTaskHandler.DeleteWeb)
	a.GET("/tasks/:projecttask/assignees", assigneeTaskHandler.ReadAllWeb)

	bulkAssigneeHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.BulkAssignees{}
		},
	}
	a.POST("/tasks/:projecttask/assignees/bulk", bulkAssigneeHandler.CreateWeb)

	labelTaskHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.LabelTask{}
		},
	}
	a.PUT("/tasks/:projecttask/labels", labelTaskHandler.CreateWeb)
	a.DELETE("/tasks/:projecttask/labels/:label", labelTaskHandler.DeleteWeb)
	a.GET("/tasks/:projecttask/labels", labelTaskHandler.ReadAllWeb)

	bulkLabelTaskHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.LabelTaskBulk{}
		},
	}
	a.POST("/tasks/:projecttask/labels/bulk", bulkLabelTaskHandler.CreateWeb)

	taskRelationHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskRelation{}
		},
	}
	a.PUT("/tasks/:task/relations", taskRelationHandler.CreateWeb)
	a.DELETE("/tasks/:task/relations/:relationKind/:otherTask", taskRelationHandler.DeleteWeb)

	if config.ServiceEnableTaskAttachments.GetBool() {
		taskAttachmentHandler := &handler.WebHandler{
			EmptyStruct: func() handler.CObject {
				return &models.TaskAttachment{}
			},
		}
		a.GET("/tasks/:task/attachments", taskAttachmentHandler.ReadAllWeb)
		a.DELETE("/tasks/:task/attachments/:attachment", taskAttachmentHandler.DeleteWeb)
		a.PUT("/tasks/:task/attachments", apiv1.UploadTaskAttachment)
		a.GET("/tasks/:task/attachments/:attachment", apiv1.GetTaskAttachment)
	}

	if config.ServiceEnableTaskComments.GetBool() {
		taskCommentHandler := &handler.WebHandler{
			EmptyStruct: func() handler.CObject {
				return &models.TaskComment{}
			},
		}
		a.GET("/tasks/:task/comments", taskCommentHandler.ReadAllWeb)
		a.PUT("/tasks/:task/comments", taskCommentHandler.CreateWeb)
		a.DELETE("/tasks/:task/comments/:commentid", taskCommentHandler.DeleteWeb)
		a.POST("/tasks/:task/comments/:commentid", taskCommentHandler.UpdateWeb)
		a.GET("/tasks/:task/comments/:commentid", taskCommentHandler.ReadOneWeb)
	}

	labelHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Label{}
		},
	}
	a.GET("/labels", labelHandler.ReadAllWeb)
	a.GET("/labels/:label", labelHandler.ReadOneWeb)
	a.PUT("/labels", labelHandler.CreateWeb)
	a.DELETE("/labels/:label", labelHandler.DeleteWeb)
	a.POST("/labels/:label", labelHandler.UpdateWeb)

	projectTeamHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TeamProject{}
		},
	}
	a.GET("/projects/:project/teams", projectTeamHandler.ReadAllWeb)
	a.PUT("/projects/:project/teams", projectTeamHandler.CreateWeb)
	a.DELETE("/projects/:project/teams/:team", projectTeamHandler.DeleteWeb)
	a.POST("/projects/:project/teams/:team", projectTeamHandler.UpdateWeb)

	projectUserHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.ProjectUser{}
		},
	}
	a.GET("/projects/:project/users", projectUserHandler.ReadAllWeb)
	a.PUT("/projects/:project/users", projectUserHandler.CreateWeb)
	a.DELETE("/projects/:project/users/:user", projectUserHandler.DeleteWeb)
	a.POST("/projects/:project/users/:user", projectUserHandler.UpdateWeb)

	savedFiltersHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.SavedFilter{}
		},
	}
	a.GET("/filters/:filter", savedFiltersHandler.ReadOneWeb)
	a.PUT("/filters", savedFiltersHandler.CreateWeb)
	a.DELETE("/filters/:filter", savedFiltersHandler.DeleteWeb)
	a.POST("/filters/:filter", savedFiltersHandler.UpdateWeb)

	teamHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Team{}
		},
	}
	a.GET("/teams", teamHandler.ReadAllWeb)
	a.GET("/teams/:team", teamHandler.ReadOneWeb)
	a.PUT("/teams", teamHandler.CreateWeb)
	a.POST("/teams/:team", teamHandler.UpdateWeb)
	a.DELETE("/teams/:team", teamHandler.DeleteWeb)

	teamMemberHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TeamMember{}
		},
	}
	a.PUT("/teams/:team/members", teamMemberHandler.CreateWeb)
	a.DELETE("/teams/:team/members/:user", teamMemberHandler.DeleteWeb)
	a.POST("/teams/:team/members/:user/admin", teamMemberHandler.UpdateWeb)

	// Subscriptions
	subscriptionHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Subscription{}
		},
	}
	a.PUT("/subscriptions/:entity/:entityID", subscriptionHandler.CreateWeb)
	a.DELETE("/subscriptions/:entity/:entityID", subscriptionHandler.DeleteWeb)

	// Notifications
	notificationHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.DatabaseNotifications{}
		},
	}
	a.GET("/notifications", notificationHandler.ReadAllWeb)
	a.POST("/notifications/:notificationid", notificationHandler.UpdateWeb)
	a.POST("/notifications", apiv1.MarkAllNotificationsAsRead)

	// Migrations
	m := a.Group("/migration")
	registerMigrations(m)

	// Project Backgrounds
	if config.BackgroundsEnabled.GetBool() {
		a.GET("/projects/:project/background", backgroundHandler.GetProjectBackground)
		a.DELETE("/projects/:project/background", backgroundHandler.RemoveProjectBackground)
		if config.BackgroundsUploadEnabled.GetBool() {
			uploadBackgroundProvider := &backgroundHandler.BackgroundProvider{
				Provider: func() background.Provider {
					return &upload.Provider{}
				},
			}
			a.PUT("/projects/:project/backgrounds/upload", uploadBackgroundProvider.UploadBackground)
		}
		if config.BackgroundsUnsplashEnabled.GetBool() {
			unsplashBackgroundProvider := &backgroundHandler.BackgroundProvider{
				Provider: func() background.Provider {
					return &unsplash.Provider{}
				},
			}
			a.GET("/backgrounds/unsplash/search", unsplashBackgroundProvider.SearchBackgrounds)
			a.POST("/projects/:project/backgrounds/unsplash", unsplashBackgroundProvider.SetBackground)
			a.GET("/backgrounds/unsplash/images/:image/thumb", unsplash.ProxyUnsplashThumb)
			a.GET("/backgrounds/unsplash/images/:image", unsplash.ProxyUnsplashImage)
		}
	}

	// API Tokens
	apiTokenProvider := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.APIToken{}
		},
	}
	a.GET("/tokens", apiTokenProvider.ReadAllWeb)
	a.PUT("/tokens", apiTokenProvider.CreateWeb)
	a.DELETE("/tokens/:token", apiTokenProvider.DeleteWeb)

	// Webhooks
	if config.WebhooksEnabled.GetBool() {
		webhookProvider := &handler.WebHandler{
			EmptyStruct: func() handler.CObject {
				return &models.Webhook{}
			},
		}
		a.GET("/projects/:project/webhooks", webhookProvider.ReadAllWeb)
		a.PUT("/projects/:project/webhooks", webhookProvider.CreateWeb)
		a.DELETE("/projects/:project/webhooks/:webhook", webhookProvider.DeleteWeb)
		a.POST("/projects/:project/webhooks/:webhook", webhookProvider.UpdateWeb)
		a.GET("/webhooks/events", apiv1.GetAvailableWebhookEvents)
	}

	// Reactions
	reactionProvider := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.Reaction{}
		},
	}
	a.GET("/:entitykind/:entityid/reactions", reactionProvider.ReadAllWeb)
	a.POST("/:entitykind/:entityid/reactions/delete", reactionProvider.DeleteWeb)
	a.PUT("/:entitykind/:entityid/reactions", reactionProvider.CreateWeb)

	// Project views
	projectViewProvider := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.ProjectView{}
		},
	}
	a.GET("/projects/:project/views", projectViewProvider.ReadAllWeb)
	a.GET("/projects/:project/views/:view", projectViewProvider.ReadOneWeb)
	a.PUT("/projects/:project/views", projectViewProvider.CreateWeb)
	a.DELETE("/projects/:project/views/:view", projectViewProvider.DeleteWeb)
	a.POST("/projects/:project/views/:view", projectViewProvider.UpdateWeb)

	// Kanban Task Bucket Relation
	taskBucketProvider := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.TaskBucket{}
		},
	}
	a.POST("/projects/:project/views/:view/buckets/:bucket/tasks", taskBucketProvider.UpdateWeb)

	admin := a.Group("/admin",
		RequireFeature(license.FeatureAdminPanel),
		RequireInstanceAdmin(),
	)
	adminProjectListHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &models.AdminProjectList{}
		},
	}
	adminUserListHandler := &handler.WebHandler{
		EmptyStruct: func() handler.CObject {
			return &adminapi.UserList{}
		},
	}
	admin.GET("/overview", adminapi.GetOverview)
	admin.GET("/users", adminUserListHandler.ReadAllWeb)
	admin.POST("/users", adminapi.CreateUser)
	admin.PATCH("/users/:id/admin", adminapi.PatchAdmin)
	admin.PATCH("/users/:id/status", adminapi.PatchStatus)
	admin.DELETE("/users/:id", adminapi.DeleteUser)
	admin.GET("/projects", adminProjectListHandler.ReadAllWeb)
	admin.PATCH("/projects/:id/owner", adminapi.PatchProjectOwner)

	// Plugin routes
	if config.PluginsEnabled.GetBool() {
		// Authenticated plugin routes
		authenticatedPluginGroup := a.Group("/plugins")

		// Unauthenticated plugin routes (with basic IP rate limiting)
		unauthenticatedPluginGroup := n.Group("/plugins")

		plugins.RegisterPluginRoutes(authenticatedPluginGroup, unauthenticatedPluginGroup)
	}
}

func registerMigrations(m *echo.Group) {
	// Todoist
	if config.MigrationTodoistEnable.GetBool() {
		todoistMigrationHandler := &migrationHandler.MigrationWeb{
			MigrationStruct: func() migration.Migrator {
				return &todoist.Migration{}
			},
		}
		todoistMigrationHandler.RegisterMigrator(m)
	}

	// Trello
	if config.MigrationTrelloEnable.GetBool() {
		trelloMigrationHandler := &migrationHandler.MigrationWeb{
			MigrationStruct: func() migration.Migrator {
				return &trello.Migration{}
			},
		}
		trelloMigrationHandler.RegisterMigrator(m)
	}

	// Microsoft Todo
	if config.MigrationMicrosoftTodoEnable.GetBool() {
		microsoftTodoMigrationHandler := &migrationHandler.MigrationWeb{
			MigrationStruct: func() migration.Migrator {
				return &microsofttodo.Migration{}
			},
		}
		microsoftTodoMigrationHandler.RegisterMigrator(m)
	}

	// Vikunja File Migrator
	vikunjaFileMigrationHandler := &migrationHandler.FileMigratorWeb{
		MigrationStruct: func() migration.FileMigrator {
			return &vikunja_file.FileMigrator{}
		},
	}
	vikunjaFileMigrationHandler.RegisterRoutes(m)

	// TickTick File Migrator
	tickTickFileMigrator := migrationHandler.FileMigratorWeb{
		MigrationStruct: func() migration.FileMigrator {
			return &ticktick.Migrator{}
		},
	}
	tickTickFileMigrator.RegisterRoutes(m)

	// WeKan File Migrator
	wekanFileMigrator := migrationHandler.FileMigratorWeb{
		MigrationStruct: func() migration.FileMigrator {
			return &wekan.Migrator{}
		},
	}
	wekanFileMigrator.RegisterRoutes(m)

	// CSV File Migrator (always enabled - generic import)
	csvFileMigrator := &csvmigrator.MigratorWeb{}
	csvFileMigrator.RegisterRoutes(m)
}

func registerCalDavRoutes(c *echo.Group) {

	// Basic auth middleware
	c.Use(middleware.BasicAuth(caldav.BasicAuth))

	// CalDAV IS A WRITE SURFACE AND HAS TO BE GATED LIKE ONE. Without this the
	// only enforcement point is on the /api/v1 and /api/v2 groups, so a customer
	// whose entitlement restricts them to settings could point any CalDAV client
	// at /dav with their ordinary username and password and carry on creating
	// and editing tasks - the browser refusing them the whole time. Reads are
	// untouched: RequireManagedPolicy refuses only mutating methods, and the
	// PROPFIND, REPORT and GET a client actually spends its traffic on are not
	// among them.
	//
	// Attached after the basic auth it reads the subject from, and before the
	// routes below, because echo copies a group's middleware into each route as
	// that route is added.
	c.Use(RequireManagedPolicy())

	// THIS is the entry point for caldav clients, otherwise projects will show up double
	c.Any("", caldav.EntryHandler)
	c.Any("/", caldav.EntryHandler)
	c.Any("/principals/*", caldav.PrincipalHandler)
	c.Any("/principals/*/", caldav.PrincipalHandler)
	c.Any("/projects", caldav.ProjectHandler)
	c.Any("/projects/", caldav.ProjectHandler)
	c.Any("/projects/:project", caldav.ProjectHandler)
	c.Any("/projects/:project/", caldav.ProjectHandler)
	c.Any("/projects/:project/:task", caldav.TaskHandler) // Mostly used for editing
}
