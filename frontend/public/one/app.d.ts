/**
 * Hand-written declarations for ./app.js (ruling C5).
 *
 * `frontend/tsconfig.app.json` includes `src/**\/*`, so the unit tests under
 * `frontend/src/brazn/one/` are type-checked by `vue-tsc --build`. TypeScript resolves the
 * test's `import {...} from '../../../public/one/app.js'` to this file with no config change,
 * which is what lets the role-matrix tests be plain `.test.ts` with no `@ts-expect-error`
 * scattered through them. Same mechanism, same reason, as `api.d.ts` and `i18n.d.ts`.
 *
 * THIS IS ALSO THE VIEW MODULES' CONTRACT. `view-task.js` and `view-settings.js` may import
 * exactly what is declared here and nothing else; anything absent from this file is private to
 * app.js by intent, not by oversight. Fork payloads stay `any` for the reason api.d.ts gives:
 * modelling them here would be a second, unchecked copy of shapes that live in Go.
 */

/* --- routing (ruling C9) ------------------------------------------ */

export type ViewName = 'task' | 'settings'
export type SettingsTab = 'account' | 'organization' | 'team'

export const VIEWS: readonly ViewName[]
export const SETTINGS_TABS: readonly SettingsTab[]

export interface Route {
	/** null when `?task=` was absent or not a positive integer. */
	taskId: number | null
	view: ViewName
	tab: SettingsTab
}

/** PURE over a `location.search`-shaped string. No DOM, no state, no `location`. */
export function parseRoute(search: string): Route
/** PURE. Clamps `?tab=organization|team` to `account` when the org read did not return 200. */
export function resolveRoute(route: Route, facts: GateFacts): Route
export function routeToSearch(route: Route): string
/** Merge a patch over the current route, push it to history, and re-render. */
export function navigate(patch: Partial<Route>, options?: {replace?: boolean}): void
export function getRoute(): Route

/* --- gating (ruling C4) ------------------------------------------- */

/** The tokens `data-requires` may carry. Anything else fails closed as `unknown-gate`. */
export const GATES: readonly string[]
/** The subset whose failure HIDES rather than disables. Everything else disables with a reason. */
export const GATES_THAT_HIDE: readonly string[]

export interface DenyReasons {
	readonly NOT_ADMIN: 'not-administrator'
	readonly NO_EDITION: 'no-edition'
	readonly PERSONAL: 'personal-edition'
	readonly WRITE_RESTRICTED: 'write-restricted'
	readonly TEAM_UNREADABLE: 'team-unreadable'
	/** The organization has no team at all — distinct from a team that could not be read. */
	readonly NO_TEAM: 'no-team'
	readonly TEAM_NOT_ADMIN: 'team-not-administrator'
	readonly UNKNOWN_GATE: 'unknown-gate'
	readonly NO_ROUTE: 'no-route'
	readonly COMMERCIAL: 'commercial-unavailable'
	readonly SERVER: 'server-refusal'
}
/** Machine reasons. They land in `data-deny-reason`; they are never rendered or translated. */
export const DENY: DenyReasons

/**
 * Reason -> `t()` key, or null where nothing is rendered to explain the state. Exported so a
 * test can assert that every key in it resolves in the shipped catalogue — including the two
 * (`COMMERCIAL`, `SERVER`) that only the refusal describers can produce, which a sweep driven
 * from `decideGate` alone can never reach.
 */
export const DENY_MESSAGE_KEY: Readonly<Record<string, string | null>>

export interface TeamFact {
	/** `GET /api/v2/teams/{id}` returned 200. A 403 here is EXPECTED (ruling C11). */
	readable: boolean
	/** `members[].admin` for the acting user. Never inferred from organization administration. */
	admin: boolean
}

export interface GateFacts {
	/** A `brazn_edition` claim is present at all. False for U — including every CI session. */
	hasEdition: boolean
	/** The claim is exactly `personal-cloud`, the only defined constant. */
	personalEdition: boolean
	/** `GET /api/v1/brazn/organization` returned 200. Never a claim, never a config flag. */
	orgAdmin: boolean
	/** `brazn_write_restricted === true`. Absence is the permitting case. */
	writeRestricted: boolean
	/** Keyed by team id as a string. A missing key reads as unreadable. */
	teams: Record<string, TeamFact>
}

export interface GateRequest {
	/** The raw `data-requires` value: a space-separated token list. */
	requires?: string | null
	/** The raw `data-team` value. Required by the `team` and `team-admin` tokens. */
	team?: string | number | null
}

export interface GateDecision {
	state: 'enabled' | 'disabled' | 'hidden'
	/** A `DENY` value; null when enabled. */
	reason: string | null
	/** A `t()` key; null when enabled or hidden. Deliberately unresolved so this stays pure. */
	messageKey: string | null
}

/**
 * THE PURE DECISION FUNCTION. No DOM, no module state, no `t()`, no clock, no network — so the
 * whole role matrix can be driven as a table with nothing mounted and no catalogue loaded.
 */
export function decideGate(request: GateRequest, facts: GateFacts): GateDecision

/** IMPURE: reads the live JWT claims and the loaded organization. The only impure half. */
export function readGateFacts(): GateFacts

/** The thin DOM applier. Walks `[data-requires]` under `root` and writes each decision out. */
export function applyGates(root?: ParentNode | null, facts?: GateFacts): void

/** `one.edition.personal` / `one.edition.teams`, or null when there is no edition to name. */
export function editionMessageKey(facts?: GateFacts): string | null

/** The acting user's numeric id, for `members[].id === me.id`. */
export function currentUserId(): number | null

/* --- refusal rendering (ruling C4) -------------------------------- */

export interface Refusal {
	/** The SERVER'S OWN sentence. Rendered verbatim; never paraphrased, never translated. */
	message?: string | null
	/** A `t()` key, used only when the server sent no sentence. */
	messageKey?: string | null
	messageParams?: Record<string, string | number>
	reason?: string | null
	/** `gate` sentences are cleared by a re-gate; `server` ones survive it. */
	source?: 'gate' | 'server'
}

/** The one shared path for fork managed-gate refusals and commercial `/v1` outcome refusals. */
export function renderRefusal(el: Element, refusal: Refusal): Element | null
export function clearRefusal(el: Element, options?: {source?: 'gate' | 'server'}): void
/** The same sentence as a plain string, for the toast and the live region. */
export function refusalText(refusal: Refusal): string

/**
 * Turn a `CommercialResult` refusal into a `Refusal`. Bar 8 is already handled in api.js.
 *
 * `body` is read, not ignored: a 200-with-refusal carries the service's own `outcome`, and
 * `COMMERCIAL_OUTCOME_MESSAGE_KEY` turns it into a sentence. A bare 403 or 404 resolves to the
 * `one.error.http` SENTINEL, which the call site refines with the operation it knows about
 * (`describeCommercial` in view-settings.js); no status number ever reaches a rendered string.
 */
export function describeCommercialRefusal(result: {
	status?: number
	message?: string | null
	reason?: string | null
	body?: unknown
}): Refusal

/**
 * The service's refusal `outcome` values -> `t()` keys. The mirror of `api.COMMERCIAL_OPS`'s
 * affirmative sets, and exported for the same reason `DENY_MESSAGE_KEY` is: a test can assert
 * every key in it resolves in the shipped catalogue, which no sweep driven from the call sites
 * could do.
 */
export const COMMERCIAL_OUTCOME_MESSAGE_KEY: Readonly<Record<string, string>>

/** Turn a `ForkError` (or any thrown value) into a `Refusal`. */
export function describeForkError(err: unknown): Refusal

/* --- the seat meter ----------------------------------------------- */

/**
 * The server rule is `seats_purchased >= 3 * (teams_used + 1)` and it IGNORES member count.
 * The literal is exported so a test asserts the CONTRACT rather than this page's arithmetic.
 */
export const SEATS_PER_TEAM: number
export function requiredSeatsForTeams(teamCount: number, seatsPerTeam?: number): number

export interface SeatMeter {
	occupied: number | null
	/** null means "this instance cannot answer" — neither zero nor unlimited. */
	purchased: number | null
	teamsUsed: number | null
	teamsAllowed: number | null
	/** The SERVER's `seats_per_team`, or null when it sent none. Never filled in locally. */
	seatsPerTeam: number | null
	/** null when the ratio or the team count is unknown — the page states no number it was not sent. */
	requiredForNextTeam: number | null
	/** Display only. null when the purchased count is unreadable. */
	meetsNextTeamRule: boolean | null
	/** `organization.can_create_team`, read as sent. NEVER recomputed. */
	canCreateTeam: boolean
	/** 0..1, or null when there is no denominator to divide by. */
	fillRatio: number | null
}

/** Reads the FORK organization payload. The commercial service is never the source here. */
export function readSeatMeter(org: any): SeatMeter

/* --- state accessors ---------------------------------------------- */

export function isReady(): boolean
export function isStale(): boolean
export function getUser(): any
/** `settings` from `GET /api/v2/user`. snake_case on the wire. */
export function getSettings(): any
/** `settings.frontend_settings`. `color_schema` / `time_format` — snake_case, nested. */
export function getFrontendSettings(): any
/** The organization read model, or null. Null means "no organization surface", never "error". */
export function getOrganization(): any
/**
 * Non-null ONLY when the read was neither 200 nor 403. A 403 never reaches here.
 *
 * Rendered by `app.js` itself, above whichever view is drawn, as the `organization.unavailable.*`
 * notice with a retry — so a 500 no longer looks byte-identical to the 403 that hides the tabs.
 */
export function getOrganizationError(): unknown
export interface TeamState extends TeamFact {
	id: string
	/** The `GET /api/v2/teams/{id}` body, or null when that read was refused. */
	team: any
	/** The rejection from `Promise.allSettled`, typically a 403 ForkError. Null when readable. */
	error: unknown
}
export function getTeamState(teamId: string | number): TeamState | null
/** Every team the organization read listed, in payload order. */
export function getTeamStates(): TeamState[]

/** Per-view scratch, created on first read. app.js never reads inside it. */
export function getViewState(ns: string): Record<string, any>
export function setViewState(ns: string, patch: Record<string, any>): void

/** Re-read `GET /api/v1/brazn/organization` and every roster, then render. */
export function reloadOrganization(): Promise<void>
/**
 * Re-read `GET /api/v2/user`, adopt it as the page's account state, re-derive the colour scheme
 * and the date/time formatters from it, and render when the body actually changed.
 *
 * The user half of `reloadOrganization()`, and the supported way to make a section reflect a value
 * that was just written. AWAIT IT BEFORE TOASTING so the success message never lands ahead of the
 * value it describes. Concurrent callers share one in-flight request.
 *
 * Resolves `false` and logs when the re-read itself fails — a failed re-read is not a failed
 * write. Rejects only with `SessionLostError`, which app.js's terminal surface owns.
 */
export function reloadUser(): Promise<boolean>
/**
 * Re-render the current view, and — on the settings view only — schedule a coalesced, throttled
 * `reloadUser()` so a section redrawn after a write is redrawn from the server's copy rather than
 * from boot's.
 */
export function requestRender(): void

/* --- formatters --------------------------------------------------- */

/**
 * Built from the negotiated locale, `timezone` and `frontend_settings.time_format` — on boot, and
 * again on every `reloadUser()`, so a saved timezone takes effect without a page reload.
 */
export function buildFormatters(
	locale: string,
	timezone: string | null | undefined,
	timeFormatPreference: string | null | undefined,
): void
export function getDateTimeFormat(): Intl.DateTimeFormat | null
/** All four return '' for null, undefined, an unparsable value, and the Go zero time. */
export function formatDateTime(value: string | number | Date | null | undefined): string
export function formatDate(value: string | number | Date | null | undefined): string
export function formatTime(value: string | number | Date | null | undefined): string
export function formatNumber(value: number | null | undefined): string

/* --- theme and i18n hydration ------------------------------------- */

/** `'auto' | 'light' | 'dark'`. Anything else, including undefined, resolves to `auto`. */
export function applyColorScheme(schema: string | null | undefined): void

/** Replaces `data-i18n`, `-aria`, `-placeholder`, `-alt` and `-title` under `root`. */
export function hydrateI18n(root: ParentNode | null | undefined): void

/* --- the header identity block (PM round 1b, item 3) --------------- */

/** A user's display name, falling back to the username the fork always sends. */
export function personName(person: any): string
/** Two letters at most, from the display name. The avatar circle's fallback face. */
export function initials(person: any): string

/**
 * Avatar circle + name + role + subscription line, as ONE block, identical on both documents.
 * `render()` adopts it into whichever header the view drew — see app.js section 13b. Exported so
 * a test can assert the markup without a DOM, and so the two view modules can drop their own
 * copies once they are free to edit.
 */
export function identityBlock(facts?: GateFacts): string

/**
 * The object URL for this user's avatar, or null for "paint the initials". Synchronous: the
 * read is kicked off by the render that needs it and this answers from what has landed.
 *
 * The bytes come from `api.getAvatarBlob` and the cache key from `api.getAvatarGeneration()`,
 * which `api.saveAvatar` bumps — so no surface keeps a private notion of "current" and no
 * circle can show a stale face after an upload made anywhere on the page.
 */
export function headerAvatarUrl(user: any): string | null

/**
 * The same answer for ANYONE, not only the signed-in user — a comment's author, say. The header
 * circle above is this function applied to the account, and both read the one cache app.js keeps
 * per username, so an upload invalidates every circle at once through the shared generation.
 */
export function avatarUrlFor(user: any): string | null

/**
 * Ask for one person's avatar bytes, once per person per generation. Fire-and-forget and a no-op
 * after the first call for a key: the picture is decorative, so a slow or refused read must never
 * delay the page. Call it from `mount`, never from a render — a render stays synchronous.
 */
export function ensureAvatarFor(user: any): void

/* --- chrome the views share --------------------------------------- */

/**
 * Replaces `#modalRoot`'s contents, hydrates, gates, and MOVES FOCUS into the dialog — the first
 * non-refused field, else the dialog itself. A caller that focuses its own field afterwards wins.
 * Focus landing inside is also what makes Enter-to-commit reachable (see app.js `commitOnEnter`).
 */
export function openModal(html: string): Element | null
export function closeModal(): void
/** Mirrored into `#a11yLive`: bar 8 makes failure reporting perceivable, not just visible. */
export function toast(message: string): void
export function announce(message: string): void

/* --- the delegated action registry -------------------------------- */

export type ActionHandler = (event: Event, el: Element) => void | Promise<void>

/**
 * Register delegated click handlers keyed on `data-action`, or on one of `ATTRIBUTE_HOOKS`
 * (where the key is the attribute name and the handler reads its value). THROWS on a duplicate
 * name. Handlers never fire on a control inside `.is-refused` / `[aria-disabled="true"]`.
 */
export function registerActions(map: Record<string, ActionHandler>): void
export function actionNames(): string[]
export const ATTRIBUTE_HOOKS: readonly string[]
export function isRefused(el: Element | null | undefined): boolean

/* --- the view registry -------------------------------------------- */

export interface ViewContext {
	/** Already clamped by `resolveRoute`. */
	route: Route
	facts: GateFacts
}

export interface ViewModule {
	/**
	 * The HTML for `#app`. MUST EMIT gated nodes and let `applyGates` decide their fate —
	 * never omit a node because a gate is false (ruling C4).
	 */
	render(ctx: ViewContext): string
	/** Runs after insertion, before gates are applied. Select options, meter widths, and so on. */
	mount?(root: Element, ctx: ViewContext): void
}

/**
 * Called by `view-task.js` / `view-settings.js` at import time. They import app.js statically;
 * app.js imports them dynamically after it has finished evaluating, so there is no static cycle.
 */
export function registerView(name: ViewName, view: ViewModule): void

/* --- boot --------------------------------------------------------- */

/**
 * Self-schedules only when `#app` exists, so importing this module in a test with no shell
 * mounted issues no request and renders nothing.
 */
export function boot(): Promise<void>

/**
 * Whether a no-session boot may navigate to `/login`. PURE, and exported for exactly that
 * reason: `/login` is a vue-router path that the restricted-UI lockout redirects back to this
 * page, so an unconditional hand-off loops until the browser gives up. See app.js.
 */
export function shouldHandOffToLogin(input?: {
	/** The sessionStorage marker; null when storage cannot answer at all. */
	marker?: boolean | null
	/** `PerformanceNavigationTiming.redirectCount` for this document. */
	redirects?: number
	/** A person pressed Sign in. Always hands off. */
	force?: boolean
}): boolean

/**
 * The localStorage key /one/join.html writes a pending invitation under, and boot() resumes
 * from — the join return leg (BRA-1439 Story 5). See app.js for why localStorage and what
 * bounds it.
 */
export const PENDING_JOIN_KEY: string

/**
 * PURE decision for the join return leg: the stored marker string (or null) and the clock in,
 * the invitation id to resume (or null) out. Malformed, incomplete and stale markers all
 * answer null.
 */
export function pendingJoinRedirect(raw: string | null, now: number): string | null
