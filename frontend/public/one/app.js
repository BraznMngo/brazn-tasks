/**
 * ONE Tasks restricted views — bootstrap, router, gating engine (BRA-1357 / BRA-1358).
 *
 * Plain ES module. No framework, no build step: Vite copies `public/` verbatim, so every
 * import below has to be resolvable by the browser as written.
 *
 * WHAT THIS FILE IS. It is the page's spine and nothing else. It boots the session, resolves
 * the language, derives the role, decides every gate, renders the refusals, owns the one
 * delegated click listener and the modal/toast/live-region plumbing, and hands the two view
 * modules a small, documented surface. IT RENDERS NO VIEW MARKUP. `view-task.js` and
 * `view-settings.js` own that, and they import from here — never the other way round as a
 * static import (see `loadViews`).
 *
 * ONE EXCEPTION, ADDED IN ROUND 1B AND WRITTEN DOWN RATHER THAN SLIPPED IN: section 13b renders
 * the header identity block — the avatar circle, the name, the role and the subscription line.
 * The PM finding is "in settings just like in tasks", and identical-on-both is a property no
 * single view module can hold: the two headers were drawing two different blocks, and whichever
 * moved first the other would drift. It is page chrome rather than view content, it is
 * byte-identical on both documents, and it is adopted INTO the header each view already draws
 * rather than replacing that header. Section 13b carries the full reasoning.
 *
 * LAYOUT CHANGE, SAID LOUDLY (BRIEF.md, "Locked file layout — do not change without saying so
 * loudly"): this file adds TWO files to the locked layout, `frontend/public/one/view-task.js`
 * and `frontend/public/one/view-settings.js`. The locked layout names only `app.js` for
 * "bootstrap, routing between views, wiring, role gating". Splitting the two views out is what
 * keeps this file reviewable and what lets a unit test import the gating engine without
 * dragging two full view renderers into the module graph. Both are loaded with a DYNAMIC
 * import inside `Promise.allSettled`, so a missing one degrades to a single broken view rather
 * than a blank page.
 *
 * IMPORT-TIME PURITY, same contract as api.js. Importing this module issues no request, reads
 * no global and touches no DOM. `boot()` self-schedules only when a real shell is present
 * (`#app` exists) — see the bottom of the file — so a unit test that imports the pure
 * functions gets zero side effects.
 *
 * NO HARDCODED USER-FACING STRINGS. Everything the user reads is either a `t()` key or the
 * SERVER'S OWN SENTENCE rendered verbatim (ruling C4). The gating engine deliberately returns
 * `messageKey`s rather than resolved strings: that keeps `decideGate` pure, and it lets the
 * whole role matrix be driven in a test with no DOM and no catalogue loaded.
 */

'use strict';

import * as api from './api.js';
import {t, init as initI18n, currentLocale, projectTitle} from './i18n.js';

/* ------------------------------------------------------------------ *
 * 1. Vocabulary — the frozen literals a test asserts against
 * ------------------------------------------------------------------ */

/**
 * The `?view=` values. `task` needs a task id; `settings` never does (ruling C9).
 */
export const VIEWS = Object.freeze(['task', 'settings']);

/**
 * The `?tab=` values. `account` is the tab every role has; `organization` and `team` are the
 * two the organization read gates (ruling C1.5). Spelled `account`, not the prototype's
 * `settings`, because `?view=settings&tab=settings` reads as a typo in a URL a human pastes.
 */
export const SETTINGS_TABS = Object.freeze(['account', 'organization', 'team']);

/**
 * Every gate token `data-requires` may carry. An unrecognised token FAILS CLOSED
 * (`DENY.UNKNOWN_GATE`) rather than resolving to "no requirement": a typo in a gate name must
 * refuse the control and be visible, not silently enable it.
 *
 *   teams       edition is not personal-cloud. Source is the `brazn_edition` JWT CLAIM, never
 *               the organization read — that read 403s for every user this gate covers, which
 *               is the whole of ruling C1.
 *   admin       GET /api/v1/brazn/organization returned 200.
 *   write       the write-restricted overlay is off (`brazn_write_restricted`).
 *   edition     an edition claim is present at all. U (no claim — every CI session) has no
 *               edition to name, so the edition line is not drawn.
 *   team        `data-team="{id}"` must be READABLE (GET /api/v2/teams/{id} → 200).
 *   team-admin  readable AND `members[].admin === true` for the acting user.
 */
export const GATES = Object.freeze(['teams', 'admin', 'write', 'edition', 'team', 'team-admin']);

/**
 * HIDE vs DISABLE, ruling C4. Nodes are ALWAYS emitted; this table is the only thing that
 * decides which of the two states a failed gate produces, and the markup never decides
 * (task.html, "GATE + REFUSAL CONTRACT").
 *
 * HIDE is reserved for "the whole surface is absent for this user":
 *   admin    — the Organization and Team tabs when the organization read 403s. That 403 is the
 *              ORDINARY answer for P, M, TA and U; rendering a disabled tab strip would
 *              advertise an organization concept to accounts that have none.
 *   edition  — there is no edition claim, so there is no true edition to print. A disabled
 *              "your subscription" line saying nothing is worse than no line.
 *
 * Everything else DISABLES WITH A REASON. RECORDED DEVIATION: SPEC-ROLES T6/T7 make the label
 * line and the assignee select **H** for a personal account, copying the prototype's
 * `isTeams() ? … : ''`. Under this table they render disabled with `one.deny.personalEdition`
 * instead. Ruling C4 puts HIDE behind "the whole surface is absent", and neither routes nor
 * permissions refuse a personal account here — the prototype's own hiding is a product choice.
 * Telling that user why the control is unavailable is the state C4 exists to create. Flagged
 * rather than smuggled: if the product wants them hidden, add `teams` to this list, which is
 * the entire change.
 */
export const GATES_THAT_HIDE = Object.freeze(['admin', 'edition']);

/**
 * Evaluation order for the gates that disable. FIXED so the reason a control carries is
 * deterministic when two gates fail at once: `teams` before `write` because a Team-Edition
 * boundary survives paying the invoice and the write restriction does not, so it is the
 * sentence that is still true tomorrow.
 */
const DISABLE_ORDER = Object.freeze(['teams', 'team', 'team-admin', 'write']);

/**
 * Machine-readable refusal reasons. These land in `data-deny-reason` and are the hook a test
 * asserts on — never rendered, never translated.
 */
export const DENY = Object.freeze({
  NOT_ADMIN: 'not-administrator',
  NO_EDITION: 'no-edition',
  PERSONAL: 'personal-edition',
  WRITE_RESTRICTED: 'write-restricted',
  TEAM_UNREADABLE: 'team-unreadable',
  /** The organization has no team at all — distinct from a team we could not read. */
  NO_TEAM: 'no-team',
  TEAM_NOT_ADMIN: 'team-not-administrator',
  UNKNOWN_GATE: 'unknown-gate',
  NO_ROUTE: 'no-route',
  COMMERCIAL: 'commercial-unavailable',
  SERVER: 'server-refusal',
});

/**
 * Reason -> `t()` key. Hidden reasons have no key: nothing is rendered to explain them.
 *
 * EXPORTED so a test can assert that EVERY key in it resolves in the shipped catalogue, not
 * only the ones `decideGate` happens to reach. `DENY.COMMERCIAL` and `DENY.SERVER` are written
 * by `describeCommercialRefusal` / `describeForkError` rather than by the gating engine, so a
 * matrix-derived sweep can never see them — which is exactly how a renamed key would ship as a
 * raw key path with only a console warning behind it.
 */
export const DENY_MESSAGE_KEY = Object.freeze({
  [DENY.NOT_ADMIN]: null,
  [DENY.NO_EDITION]: null,
  [DENY.PERSONAL]: 'one.deny.personalEdition',
  [DENY.WRITE_RESTRICTED]: 'one.deny.writeRestricted',
  [DENY.TEAM_UNREADABLE]: 'one.deny.rosterUnavailable',
  [DENY.NO_TEAM]: 'one.deny.noTeams',
  [DENY.TEAM_NOT_ADMIN]: 'one.deny.notAdministrator',
  [DENY.UNKNOWN_GATE]: 'one.deny.noRoute',
  [DENY.NO_ROUTE]: 'one.deny.noRoute',
  [DENY.COMMERCIAL]: 'one.deny.commercial',
  [DENY.SERVER]: 'one.error.requestFailed',
});

/**
 * SEATS PER TEAM — the server rule, written out as a literal so a test asserts against the
 * CONTRACT rather than against this file's arithmetic.
 *
 *   seats_purchased >= 3 * (teams_used + 1)
 *
 * `SeatsPerTeam` in pkg/models/brazn_organization.go:265-270. MEMBER COUNT IS NOT IN IT. The
 * prototype gets this wrong twice over at lines 602-604: it invents `seats_per_team || 3` (the
 * `||` turns a legitimate 0 into 3) AND folds `members + pending` into the requirement. Both
 * are deleted, not adapted.
 *
 * The literal is a fallback, not the source of truth: `readSeatMeter` prefers the payload's own
 * `seats_per_team` when it is a number, and warns when the two disagree — that warning is the
 * only thing on either side of the boundary that would catch the constant drifting.
 */
export const SEATS_PER_TEAM = 3;

/**
 * Attribute hooks the delegated listener treats exactly like `data-action`, except that the
 * ACTION NAME IS THE ATTRIBUTE and its value is what the handler switches on. The prototype
 * keyed three off the attribute rather than off `data-action` (1049, 1095) and SPEC-UI §5.3
 * keeps the spellings verbatim, so the registry accommodates them instead of renaming hooks for
 * tidiness. `data-settings-tab` is registered at the bottom of this file; `data-resource` is the
 * task view's own tab strip and is registered there.
 *
 * The prototype's third, `data-nav`, is GONE with its handler: no view emits `data-nav=`, and
 * this page has no cross-view navigation control to emit it — the task view is reachable only by
 * deep link (ruling C19a). Leaving the hook registered would advertise a navigation affordance
 * the page does not have.
 */
export const ATTRIBUTE_HOOKS = Object.freeze(['data-settings-tab', 'data-resource']);

/* ------------------------------------------------------------------ *
 * 2. State
 * ------------------------------------------------------------------ */

/**
 * One module-private object. The accessors below hand out the pieces; nothing outside this
 * file gets the object itself, because a view that mutated `state.organization` would put the
 * seat meter out of step with the server with no call in between.
 *
 * `viewState` is the one slice the views own: a namespace per view for the data they load
 * (the task, the comment list, the timezone list). This file never reads inside it.
 */
const state = {
  ready: false,
  /** Boot did not finish. Separate from `fatalMessage`, which is often null: an error with no
   *  server sentence is still fatal, and folding the two would render a working-looking page. */
  failed: false,
  fatalMessage: null,
  sessionEnded: false,
  stale: false,
  user: null,
  settings: {},
  frontendSettings: {},
  /** The 200 body, or null when the read 403d — which is the ORDINARY answer (ruling C1.5). */
  organization: null,
  /** A ForkError only when the status was NEITHER 200 nor 403. That distinction is the whole
   *  reason this field exists: 403 is not an error and must never reach an error surface. */
  organizationError: null,
  /** teamId (string) -> {id, readable, admin, team, error} */
  teams: new Map(),
  route: {taskId: null, view: 'settings', tab: 'account'},
  viewState: Object.create(null),
};

/** The facts the last render was drawn against — the baseline for stale detection (F2). */
let renderedFacts = null;

export function isReady() {
  return state.ready;
}

export function isStale() {
  return state.stale;
}

export function getUser() {
  return state.user;
}

/** `settings` from GET /api/v2/user. Keys are snake_case on the wire. */
export function getSettings() {
  return state.settings;
}

/**
 * `settings.frontend_settings` — arbitrary JSON the server stores and returns VERBATIM
 * (pkg/models/user_settings.go:45), written by the Vue app through `objectToSnakeCase`, which
 * RECURSES (frontend/src/helpers/case.ts:74-77). So the wire keys are `color_schema` and
 * `time_format`, nested one level down. Reading `colorSchema` returns undefined and falls back
 * to light / 24-hour in complete silence, which is why every read here is snake_case.
 */
export function getFrontendSettings() {
  return state.frontendSettings;
}

/** The organization read model, or null. Null means "no organization surface", never "error". */
export function getOrganization() {
  return state.organization;
}

/**
 * The error from the organization read, and ONLY when it was neither 200 nor 403.
 *
 * 403 never reaches here. It is the ordinary answer for P, M, TA and U — and for every CI
 * session — so it produces no toast, no banner, no console line and no retry (F3). This field
 * is non-null only for the cases that genuinely are broken (500, a network failure, an HTML
 * error page).
 *
 * WHAT RENDERS IT: `organizationNotice()` in this file, above whichever view is drawn, as
 * `organization.unavailable.*` with a retry. It used to say `organization.error.*` here and
 * nothing rendered anything — the comment described a surface that did not exist, so a 500 and
 * a 403 were byte-identical on screen (both tabs gone, silently) on the one call from which F3
 * draws its single distinction. `organization.error.*` — "This page did not load." — was the
 * wrong sentence for it anyway: the page loaded, one read on it did not.
 */
export function getOrganizationError() {
  return state.organizationError;
}

/** `{id, readable, admin, team, error}` for one team, or null when the team is unknown. */
export function getTeamState(teamId) {
  return state.teams.get(String(teamId)) ?? null;
}

/** Every team the organization read listed, in payload order. */
export function getTeamStates() {
  return [...state.teams.values()];
}

/** The resolved route currently on screen. */
export function getRoute() {
  return {...state.route};
}

/** Per-view scratch. `ns` is the view name; the object is created on first read. */
export function getViewState(ns) {
  if (state.viewState[ns] === undefined) state.viewState[ns] = {};
  return state.viewState[ns];
}

/** Shallow-merge into a view's scratch. Does NOT render — call `requestRender()` for that. */
export function setViewState(ns, patch) {
  Object.assign(getViewState(ns), patch);
}

/* ------------------------------------------------------------------ *
 * 3. Router (ruling C9)
 * ------------------------------------------------------------------ */

/**
 * Query parameters, not a hash. The desktop app opens this page with
 * `tauri_plugin_opener::open_url` (system browser, top-level navigation), and a query survives
 * every link-rewriting layer between here and there; a fragment does not reliably.
 *
 * PURE over the query string. No DOM, no state, no `location` — so the whole routing table can
 * be driven in a test as a list of strings.
 *
 *   ?task=<id>                selects the task
 *   ?view=task|settings       selects the view; defaults to `task` when ?task= is present and
 *                             `settings` otherwise
 *   ?tab=account|organization|team   selects the settings tab
 *
 * A MISSING OR NON-NUMERIC ?task= RENDERS SETTINGS, NOT AN ERROR. The desktop app is the only
 * thing that builds these links; a malformed one is a bug on that side and the user gets a working page
 * rather than a stack trace about someone else's mistake.
 *
 * @param {string} search a `location.search`-shaped string, with or without the leading `?`
 * @returns {{taskId: number|null, view: string, tab: string}}
 */
export function parseRoute(search, defaultView) {
  const params = new URLSearchParams(typeof search === 'string' ? search : '');
  const taskId = parseTaskId(params.get('task'));

  // THE DOCUMENT CHOOSES THE VIEW; `?view=` only overrides it.
  //
  // Settings is its own page, `settings.html`, and the task detail is its own, `task.html`.
  // Each carries `data-default-view` on <body>. Serving both surfaces from one filename was
  // the earlier shape and it read backwards: the general entry point is Settings, the task is
  // a deep link, and a settings screen answering to a URL called `task.html` is a URL nobody
  // can hand to anyone.
  //
  // `?view=` is kept because it costs one line and makes every route reachable from either
  // document, which is what the unit tests drive. It is not what a link should carry.
  const fallback = VIEWS.includes(defaultView) ? defaultView : (taskId !== null ? 'task' : 'settings');
  const requestedView = params.get('view');
  const view = VIEWS.includes(requestedView) ? requestedView : fallback;

  const requestedTab = params.get('tab');
  const tab = SETTINGS_TABS.includes(requestedTab) ? requestedTab : SETTINGS_TABS[0];

  return {taskId, view: clampView(view, taskId), tab};
}

/**
 * The task view has nothing to render without an id, and an error surface for a link the user
 * did not type is a dead end. Settings is always reachable, so it is the floor. Applied on both
 * the cold-load path and every `navigate()`, so a programmatic `{view:'task'}` cannot reach a
 * state the URL form refuses.
 */
function clampView(view, taskId) {
  return view === 'task' && taskId === null ? 'settings' : view;
}

/**
 * Task ids are positive int64 on the wire. `Number()` alone accepts '1e3', ' 12 ', '0x0c' and
 * '' (which is 0), every one of which would build a URL the API answers 404 for — so the shape
 * is matched before it is converted.
 */
function parseTaskId(raw) {
  if (typeof raw !== 'string' || !/^[1-9][0-9]*$/.test(raw)) return null;
  const id = Number(raw);
  return Number.isSafeInteger(id) ? id : null;
}

/**
 * Clamp a parsed route against what this user may actually see. PURE.
 *
 * The Organization and Team tabs are hidden when the organization read 403d, so a link to
 * `?tab=organization` from an account that lost administration must land on `account` rather
 * than on an empty tab. Deliberately silent: 403 is the ordinary answer and produces no toast,
 * no banner and no console noise (F3).
 */
export function resolveRoute(route, facts) {
  const tab = (route.tab === 'organization' || route.tab === 'team') && !facts.orgAdmin
    ? SETTINGS_TABS[0]
    : route.tab;
  return {...route, view: clampView(route.view, route.taskId), tab};
}

/** Serialise a route back to a query string. Defaults are omitted to keep pasted URLs short. */
export function routeToSearch(route) {
  const params = new URLSearchParams();
  if (route.taskId !== null && route.taskId !== undefined) params.set('task', String(route.taskId));
  params.set('view', route.view);
  if (route.view === 'settings' && route.tab !== SETTINGS_TABS[0]) params.set('tab', route.tab);
  return `?${params.toString()}`;
}

/**
 * Change the route and re-render. `patch` is merged over the current route, so
 * `navigate({tab: 'team'})` keeps the task id in the address bar and a later `?view=task`
 * still works.
 */
export function navigate(patch, {replace = false} = {}) {
  state.route = {...state.route, ...patch};
  const url = routeToSearch(state.route);
  if (typeof history !== 'undefined') {
    if (replace) history.replaceState(null, '', url);
    else history.pushState(null, '', url);
  }
  render();
}

/* ------------------------------------------------------------------ *
 * 4. Role derivation (ruling C1)
 * ------------------------------------------------------------------ */

/**
 * @typedef {Object} GateFacts
 * @property {boolean} hasEdition       an edition claim is present at all
 * @property {boolean} personalEdition  the claim is exactly `personal-cloud`
 * @property {boolean} orgAdmin         the organization read returned 200
 * @property {boolean} writeRestricted  `brazn_write_restricted === true`
 * @property {Record<string, {readable: boolean, admin: boolean}>} teams
 */

/**
 * Read the current facts. IMPURE by design — it is the one place the live JWT claims and the
 * loaded organization meet — and the only impure half of the gating engine. `decideGate` takes
 * the result as an argument, so a test drives the entire role matrix by building this object
 * by hand.
 *
 * Edition and write-restriction come from the JWT CLAIMS via api.js, NOT from the organization
 * read (ruling C1). The organization read 403s for P, M, TA and U — that is every user the
 * `teams` gate covers — so deriving `teams` from it would leave the most common user's label
 * line and assignee select decided by whichever way an implementer resolved `undefined`.
 *
 * `orgAdmin` is the org read returning 200 and nothing else: never a claim, never a config
 * flag, and never `brazn_managed_mode`, which is stuck on the unmerged PR #50 and which this
 * page is designed not to need.
 */
export function readGateFacts() {
  const teams = Object.create(null);
  for (const [id, entry] of state.teams) {
    teams[id] = {readable: entry.readable, admin: entry.admin};
  }
  return Object.freeze({
    hasEdition: api.hasEditionClaim(),
    personalEdition: api.isPersonalEdition(),
    orgAdmin: state.organization !== null,
    writeRestricted: api.isWriteRestricted(),
    teams,
  });
}

/**
 * THE HEADER'S SUBSCRIPTION LINE — the line under the name, top right, on both documents. Returns
 * the `t()` key that names the edition, or null when there is nothing true to print.
 *
 * Both headers route through this one function and neither may be edited from here, so this is
 * where the contract is written down. `view-settings.js` renders it as
 * `one.edition.withRole` — "{edition} · {role}" — inside `.settings-role`; `view-task.js` renders
 * the edition alone as the `<small>` of `.task-user-meta`. Both nodes carry
 * `data-requires="edition"`, and `edition` is in `GATES_THAT_HIDE`, so a null here does not draw
 * an empty line: the node is removed.
 *
 * THE LOOKUP, and it is a lookup rather than string-matching (ruling C10). `personal-cloud` is the
 * ONLY defined constant (api.js `PERSONAL_EDITION`, itself the sibling copy of
 * `useManagedCapabilities.ts`'s `PERSONAL_EDITION`); every other non-null value is Teams. The wire
 * strings are NEVER displayed — `personal-cloud` is an identifier, not copy, and the two keys
 * below are the only text a person ever sees for it.
 *
 * ABSENT IS NOT TEAMS *HERE*, AND THE DISTINCTION IS DELIBERATE. For CAPABILITY purposes absence
 * is the permissive case and behaves exactly like Teams — that is ruling C1 and `decideGate`'s
 * `teams` token implements it. For DISPLAY it is not: an account whose token carries no
 * `brazn_edition` claim has no edition we have been told about, and printing "ONE Teams" for it
 * would be stating a subscription the page never read. So the line is absent rather than wrong.
 *
 * PM FINDING, RECORDED RATHER THAN PAPERED OVER: "the header shows the name but is missing the
 * line under it that names the subscription". The line is present in both headers and correct;
 * what is missing on that session is the CLAIM. Every session without an entitlement projection —
 * which includes every CI run and every instance with managed mode off — takes the null branch
 * here and gets no line. If the product decides a claimless account should read as Teams anyway,
 * the entire change is `if (!facts.hasEdition) return 'one.edition.teams';` on the next line and
 * dropping `edition` from `GATES_THAT_HIDE`; it is not made here because it would print an
 * edition nobody sent (bar 7).
 */
export function editionMessageKey(facts = readGateFacts()) {
  if (!facts.hasEdition) return null;
  return facts.personalEdition ? 'one.edition.personal' : 'one.edition.teams';
}

/**
 * The acting user's id, for `members[].id === me.id`. Prefers the GET /user body over the JWT
 * `id` claim: both carry it, but the body is the one the same request returned the roster
 * against.
 */
export function currentUserId() {
  const fromBody = state.user?.id;
  if (typeof fromBody === 'number') return fromBody;
  const claims = api.parseJwt(api.getToken());
  return typeof claims?.id === 'number' ? claims.id : null;
}

/* ------------------------------------------------------------------ *
 * 5. The gating engine — one pure function, one thin DOM applier
 * ------------------------------------------------------------------ */

/**
 * @typedef {Object} GateRequest
 * @property {string|null} [requires] the raw `data-requires` value: a space-separated token list
 * @property {string|number|null} [team] the raw `data-team` value
 */

/**
 * @typedef {Object} GateDecision
 * @property {'enabled'|'disabled'|'hidden'} state
 * @property {string|null} reason      a DENY.* token; null when enabled
 * @property {string|null} messageKey  a `t()` key; null when enabled or hidden
 */

/**
 * DECIDE ONE GATE. PURE: no DOM, no module state, no `t()`, no clock, no network.
 *
 * This is the highest-risk logic on the page — roughly forty rows across five roles plus the
 * write-restricted overlay — so it is one small function a test can drive as a table, and the
 * DOM applier below is deliberately dumb enough to need no test of its own beyond "it wrote
 * what the decision said".
 *
 * It returns a `messageKey`, never a resolved sentence, precisely so the matrix can be driven
 * with no catalogue loaded.
 *
 * Order of resolution:
 *   1. hide gates first (`GATES_THAT_HIDE`) — a hidden node has no reason to render, so no
 *      later gate can matter;
 *   2. then the disable gates in `DISABLE_ORDER`, first failure wins;
 *   3. unknown tokens fail closed at the end, so a typo refuses rather than enables.
 *
 * @param {GateRequest} request
 * @param {GateFacts} facts
 * @returns {GateDecision}
 */
export function decideGate(request, facts) {
  const tokens = String(request?.requires ?? '').trim().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return ENABLED;

  for (const token of GATES_THAT_HIDE) {
    if (!tokens.includes(token)) continue;
    if (token === 'admin' && !facts.orgAdmin) return hidden(DENY.NOT_ADMIN);
    if (token === 'edition' && !facts.hasEdition) return hidden(DENY.NO_EDITION);
  }

  const scope = teamFact(request?.team, facts);

  for (const token of DISABLE_ORDER) {
    if (!tokens.includes(token)) continue;
    switch (token) {
      case 'teams':
        if (facts.personalEdition) return disabled(DENY.PERSONAL);
        break;
      case 'team':
        if (!scope.readable) return disabled(scope.absent ? DENY.NO_TEAM : DENY.TEAM_UNREADABLE);
        break;
      case 'team-admin':
        // Unreadable outranks not-admin: we do not know the admin bit of a team we could not
        // read, and claiming "you are not an administrator of it" would be a fact we never saw.
        if (!scope.readable) return disabled(scope.absent ? DENY.NO_TEAM : DENY.TEAM_UNREADABLE);
        if (!scope.admin) return disabled(DENY.TEAM_NOT_ADMIN);
        break;
      case 'write':
        if (facts.writeRestricted) return disabled(DENY.WRITE_RESTRICTED);
        break;
    }
  }

  for (const token of tokens) {
    if (!GATES.includes(token)) return disabled(DENY.UNKNOWN_GATE);
  }

  return ENABLED;
}

const ENABLED = Object.freeze({state: 'enabled', reason: null, messageKey: null});

function hidden(reason) {
  return Object.freeze({state: 'hidden', reason, messageKey: null});
}

function disabled(reason) {
  return Object.freeze({state: 'disabled', reason, messageKey: DENY_MESSAGE_KEY[reason] ?? null});
}

/**
 * A team-scoped gate with no `data-team`, and a `data-team` naming a team the organization read
 * never listed, both resolve to "unreadable" rather than to "fine". The page genuinely cannot
 * read a team it cannot name, so the refusal is true as well as safe. No warning is emitted
 * here: this function is on the pure side of the engine, and a `console` call would make the
 * whole role matrix untestable without capturing output.
 *
 * ONE CASE IS SPLIT OUT, because the sentence was false for it. `view-settings.js` emits
 * `data-team=""` when `selectedTeam()` is null, which happens for an organization that has NO
 * TEAMS AT ALL — a new organization, and the ordinary state before the first Create team. That
 * used to resolve to `TEAM_UNREADABLE` → "We cannot read this team's members right now.", which
 * names a team that does not exist and implies a transient failure the administrator should
 * wait out. `facts.teams` being empty is the whole of the distinction and it is available here,
 * so the engine stays pure and the answer becomes "there are no teams yet", which is true and
 * is a different instruction.
 */
function teamFact(teamId, facts) {
  if (teamId === null || teamId === undefined || teamId === '') {
    return hasAnyTeam(facts) ? UNREADABLE_TEAM : NO_TEAM;
  }
  return facts.teams?.[String(teamId)] ?? UNREADABLE_TEAM;
}

function hasAnyTeam(facts) {
  const teams = facts?.teams;
  return teams !== null && typeof teams === 'object' && Object.keys(teams).length > 0;
}

const UNREADABLE_TEAM = Object.freeze({readable: false, admin: false});

/** Not a team we failed to read — there is no team. `absent` is what tells the two apart. */
const NO_TEAM = Object.freeze({readable: false, admin: false, absent: true});

/**
 * THE DOM APPLIER. Walks `[data-requires]` under `root` (including `root` itself) and writes
 * the decision out. Deliberately thin: everything worth arguing about is in `decideGate`.
 *
 * Which "disabled" is written depends on the element, and the difference is an accessibility
 * one rather than a stylistic one (task.html, "GATE + REFUSAL CONTRACT"):
 *   button / wrapper       aria-disabled="true" — a `disabled` button is not focusable, so a
 *                          screen-reader user could never reach the reason we just wrote
 *                          next to it;
 *   input / textarea       readOnly — still focusable, still announced, and it cannot lose
 *                          typing the way an ignored-but-editable field would;
 *   select                 disabled — `readOnly` does not exist on a select, and a select the
 *                          user can change but the page ignores is the worst of both.
 * `.is-refused` goes on all three so the CSS paints one treatment.
 */
export function applyGates(root, facts) {
  const scope = root ?? (typeof document !== 'undefined' ? document : null);
  if (scope === null) return;
  const resolved = facts ?? readGateFacts();

  const nodes = [...scope.querySelectorAll('[data-requires]')];
  if (typeof scope.matches === 'function' && scope.matches('[data-requires]')) nodes.unshift(scope);

  for (const el of nodes) {
    const decision = decideGate(
      {requires: el.getAttribute('data-requires'), team: el.getAttribute('data-team')},
      resolved,
    );
    applyDecision(el, decision);
  }
}

function applyDecision(el, decision) {
  if (decision.state === 'hidden') {
    el.classList.add('hidden');
    releaseControl(el);
    clearRefusal(el, {source: 'gate'});
    el.removeAttribute('data-deny-reason');
    return;
  }

  el.classList.remove('hidden');

  if (decision.state === 'enabled') {
    releaseControl(el);
    el.removeAttribute('data-deny-reason');
    // Only gate-authored sentences are cleared. A server refusal written by an action a moment
    // ago is the more recent and more specific truth and must survive a re-gate.
    clearRefusal(el, {source: 'gate'});
    // A node whose OWN gate passes can still sit inside a GROUP whose gate did not.
    // `applyGates` walks in document order, so the group was refused first and this release
    // would undo the announcement the group just made on this control while `isRefused()` — and
    // the stylesheet — still treat it as refused. The ancestor is the more restrictive fact and
    // it wins, exactly as the click path already decides.
    if (el.parentElement?.closest?.('.is-refused') != null) refuseControl(el);
    return;
  }

  el.classList.add('is-refused');
  el.setAttribute('data-deny-reason', decision.reason);
  refuseControl(el);
  renderRefusal(el, {messageKey: decision.messageKey, reason: decision.reason, source: 'gate'});
}

/**
 * The controls a refused GROUP has to reach into.
 *
 * Deliberately only form controls. A refused group's headings, prose and — above all — its own
 * `.refusal-text` must stay in the accessibility tree exactly as they are: the sentence
 * explaining the refusal is the one thing that must never be marked unavailable.
 * `[contenteditable="true"]` is included for completeness only; the prototype's contenteditable
 * editor and both its `execCommand` sinks are deleted, and the description is a plain
 * `<textarea>` now.
 */
const REFUSABLE_DESCENDANTS = 'input, textarea, select, button, [contenteditable="true"]';

/**
 * Refuse one node — AND every form control inside it when the node is a group.
 *
 * THE RECURSION IS AN ACCESSIBILITY FIX, NOT TIDINESS. `data-requires` commonly sits on a
 * WRAPPER rather than on a control: `#labelLine` carries `data-requires="teams write"` on a
 * `<div>` (view-task.js) with `#inlineLabelInput` inside it. Marking only the wrapper
 * `aria-disabled` left that input announced as EDITABLE. Nothing was ever written — the
 * stylesheet's `pointer-events:none` stops a mouse (task.html, the `.is-refused` descendant
 * rule) and `isRefused()` stops both the delegated click and the Enter handler — but a keyboard
 * or screen-reader user could reach the field, type into it, and watch the typing be discarded
 * with no explanation offered. `pointer-events` is not an accessibility API, and announcing the
 * state is the whole of what a non-sighted user gets from a refusal.
 *
 * Which "disabled" is written still depends on the element, for the reason task.html's GATE +
 * REFUSAL CONTRACT gives: `aria-disabled` on buttons and wrappers (a `disabled` button is not
 * focusable, so the reason we just wrote beside it could never be reached), `readOnly` +
 * `aria-disabled` on inputs and textareas, `disabled` on selects (`readOnly` does not exist
 * there, and a select the user can change but the page ignores is the worst of both).
 */
function refuseControl(el) {
  refuseOne(el);
  if (typeof el.querySelectorAll !== 'function') return;
  for (const child of el.querySelectorAll(REFUSABLE_DESCENDANTS)) refuseOne(child);
}

function refuseOne(el) {
  const tag = el.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA') {
    el.readOnly = true;
    el.setAttribute('aria-disabled', 'true');
    return;
  }
  if (tag === 'SELECT') {
    el.disabled = true;
    return;
  }
  el.setAttribute('aria-disabled', 'true');
}

/**
 * The exact inverse, and it has to recurse for the same reason: a group released without its
 * children would leave every control inside it `readOnly` after the subscription that unlocked
 * it, with nothing on screen saying why.
 *
 * A DESCENDANT UNDER A REFUSAL OF ITS OWN IS LEFT ALONE. Views emit some controls refused in the
 * MARKUP — `rename-org` is the documented one (ruling C8.1), and the contract-only commercial
 * controls are the same shape. Those are not this gate's to undo: releasing a group must not
 * re-enable a control that was never refused by the group in the first place. The gated node
 * itself is always released in full, which is the behaviour that was there before the recursion
 * existed.
 *
 * THE MARKER IS ON THE WRAPPER, NOT ON THE CONTROL, which is why this walks ancestors instead of
 * reading the child's own attributes. `refusedGroup()` in the views puts `.is-refused` and
 * `data-deny-reason` on a `<div>` and leaves the button inside carrying `aria-disabled` alone,
 * and every markup refusal on these pages is that shape — so a check that read only the child
 * matched none of them. For an organization administrator the Organization section's `admin`
 * gate passes on every render, this loop reached the pencil beside the organization name, and
 * stripped the one attribute telling a screen-reader user it cannot be used. The refusal styling
 * and the sentence beside it both survived, so the page looked right and announced wrong.
 * `data-deny-reason` is still read off the child, for a control that does carry its own.
 */
function releaseControl(el) {
  releaseOne(el);
  if (typeof el.querySelectorAll !== 'function') return;
  for (const child of el.querySelectorAll(REFUSABLE_DESCENDANTS)) {
    if (child.closest('.is-refused') !== null || child.hasAttribute('data-deny-reason')) continue;
    releaseOne(child);
  }
}

function releaseOne(el) {
  el.classList.remove('is-refused');
  el.removeAttribute('aria-disabled');
  const tag = el.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA') el.readOnly = false;
  if (tag === 'SELECT') el.disabled = false;
}

/* ------------------------------------------------------------------ *
 * 6. Refusal rendering — ONE shared path (ruling C4)
 * ------------------------------------------------------------------ */

/**
 * Put a refusal sentence on a control. Used by BOTH the fork managed-gate refusals and the
 * commercial `/v1` outcome refusals, because there is exactly one rule for both:
 *
 *   RENDER THE SERVER'S OWN MESSAGE VERBATIM. NEVER PARAPHRASE A REFUSAL.
 *
 * A translated paraphrase of a capacity refusal can state a number the server would refuse
 * (the 409 body carries `seats_needed`, which is computed server-side and must not be
 * recomputed here), and a paraphrase of a managed-gate refusal drops the cause and the cure
 * that the server's sentence names. `t()` is used only when the server sent no sentence at all.
 *
 * `textContent`, never `innerHTML`: the message comes off the wire from two different
 * codebases and one of them is not this repository.
 *
 * @param {Element} el       the refused control or group
 * @param {{message?: string|null, messageKey?: string|null, messageParams?: object,
 *          reason?: string|null, source?: 'gate'|'server'}} refusal
 */
export function renderRefusal(el, refusal) {
  const node = refusalNodeFor(el);
  if (node === null) return null;

  node.textContent = refusalText(refusal);
  node.dataset.refusalSource = refusal?.source ?? 'server';
  if (refusal?.reason) node.dataset.refusalReason = refusal.reason;
  else delete node.dataset.refusalReason;
  return node;
}

/**
 * The same sentence a refusal would put on a control, as a plain string — for the toast and
 * the live region, which have no control to hang it on. Same rule: the server's own words win.
 */
export function refusalText(refusal) {
  const server = typeof refusal?.message === 'string' && refusal.message !== '' ? refusal.message : null;
  if (server !== null) return server;
  return refusal?.messageKey ? t(refusal.messageKey, refusal.messageParams) : '';
}

/**
 * Drop a refusal sentence. With `{source}` it drops only sentences of that source, which is
 * what lets a re-gate clear its own copy without erasing a server refusal from a write that
 * happened in between.
 */
export function clearRefusal(el, {source} = {}) {
  const node = existingRefusalNode(el);
  if (node === null) return;
  if (source !== undefined && node.dataset.refusalSource !== source) return;
  node.textContent = '';
  delete node.dataset.refusalReason;
  // `.refusal-text:empty{display:none}` does the hiding, so the node is left in place: a view
  // that pre-placed one in its markup keeps it, and removing it would change the grid.
}

/**
 * Where the sentence goes. `.refusal-text` is a BLOCK sibling — `grid-column:1/-1` in a
 * `.setting-row`, `flex-basis:100%` in a `.modal-foot` — so it cannot live inside a button.
 *
 *   1. a `.refusal-text` the view already placed inside the element, or
 *   2. one already placed next to it, or
 *   3. one created and inserted after it.
 */
function refusalNodeFor(el) {
  const existing = existingRefusalNode(el);
  if (existing !== null) return existing;
  const parent = el.parentElement;
  if (parent === null) return null;
  const node = el.ownerDocument.createElement('p');
  node.className = 'refusal-text';
  parent.insertBefore(node, el.nextSibling);
  return node;
}

function existingRefusalNode(el) {
  const own = el.querySelector?.(':scope > .refusal-text');
  if (own) return own;
  const next = el.nextElementSibling;
  if (next && next.classList.contains('refusal-text')) return next;
  return null;
}

/* ------------------------------------------------------------------ *
 * 6b. The commercial refusal vocabulary — the other half of bar 8
 * ------------------------------------------------------------------ */

/**
 * `outcome` value -> the sentence a person reads.
 *
 * THIS IS THE MIRROR OF `api.COMMERCIAL_OPS`, AND WITHOUT IT BAR 8 IS ONLY HALF DONE. The
 * descriptors over there read the service's full outcome vocabulary and classify each value as
 * affirmative or refused; the affirmative half already reaches the screen (the invite branches
 * on `body.outcome` one level below the guard). The refusal half did not: every 200-with-refusal
 * fell through to `one.error.requestFailed` — "That did not work. Nothing was changed." — which
 * is TRUE and USELESS. `not_invitable` distinguishes three actionable causes; `still_administrator`
 * names the exact next step; `below_users` says which number is in the way. All of that was read
 * and then discarded one function later.
 *
 * WHY A FLAT MAP RATHER THAN ONE PER OPERATION. `describeCommercialRefusal` is handed the result
 * alone, and the result carries no operation handle, so a per-operation table would need a second
 * argument threaded through every call site. It is not needed: every value below means the same
 * thing in every operation
 * that declares it. `not_invitable` is ONE definition on purpose — client-model-27c95232:1550-1558
 * says so in as many words ("ONE definition, on purpose … two hand-written copies of one rule is
 * how the two quietly diverge") — and it is the only value that appears in two unions
 * (`MemberInvitation.outcome` at client-service-27c95232:581 and `SeatAdmissionOutcome` at
 * client-model-27c95232:1500-1507). If a future union ever reuses a value for a different fact,
 * this map has to become per-operation and the call sites have to pass the descriptor; that is
 * written down here so the change is a decision rather than a surprise.
 *
 * WHAT IS DELIBERATELY ABSENT. `not_administrator` and `unknown_request` are declared members of
 * `TeamAccessDecisionResult.outcome` (client-service-27c95232:690-695) but can NEVER arrive in
 * a body: the handler converts them to a bare 403 and a bare 404 before it projects anything
 * (client-http-27c95232:3243-3250), so they arrive as `COMMERCIAL_REFUSAL.HTTP` and are handled
 * on that path. Listing them here as well would be two entries nothing can reach. Every
 * affirmative value —
 * `invited`, `already_member`, `admitted`, `removed`, `approved`, `declined`, `changed`,
 * `unchanged` — is absent for the opposite reason: it never reaches a refusal describer at all.
 *
 * FAIL-CLOSED RESIDUE, STATED. A value not in this map falls to `one.error.requestFailed`. That
 * is the same direction `readCommercialResult` already fails in, and it is the right one: a
 * value nobody has classified must not be given a sentence somebody guessed.
 */
export const COMMERCIAL_OUTCOME_MESSAGE_KEY = Object.freeze({
  /* MemberInvitation.outcome — client-service-27c95232:581, prose at :577-579.
     Also SeatAdmissionOutcome — client-model-27c95232:1506, prose at :1468-1474. */
  not_invitable: 'one.commercial.notInvitable',

  /* SeatAdmissionOutcome — client-model-27c95232:1500-1507. */
  invitation_expired: 'one.commercial.invitationExpired',
  invitation_revoked: 'one.commercial.invitationRevoked',
  no_invitation: 'one.commercial.noInvitation',
  // The hundred-seat PRODUCT ceiling, and deliberately NOT an upsell: model :1489-1493 says
  // Teams is the biggest edition and there is nothing larger to offer, so dressing this as
  // "upgrade" would offer something that does not exist.
  at_seat_ceiling: 'one.commercial.atSeatCeiling',

  /* MemberRemovalOutcome — client-model-27c95232:1541, prose at :1533-1539. */
  not_a_member: 'one.commercial.notAMember',
  still_administrator: 'one.commercial.stillAdministrator',

  /* TeamAccessDecisionResult.outcome — client-service-27c95232:690-695, prose at :682-687.
     The request is left OPEN so the same approval can be made again, which the sentence says. */
  not_admitted: 'one.commercial.notAdmitted',

  /* SeatPurchaseOutcome — client-model-27c95232:1153, prose at :1130-1147. */
  below_users: 'one.commercial.belowUsers',
  below_active_teams: 'one.commercial.belowActiveTeams',
});

/**
 * HTTP status -> the sentence a person reads, for a `/v1` refusal that arrived with NO body.
 *
 * A BARE STATUS IS THE NORMAL SHAPE HERE, NOT AN EDGE CASE. `bare(response, …)` writes a status
 * line with no content type at all (client-http-27c95232:728-731), so `readServerMessage`
 * returns null every time, and api.js's descriptors cite thirteen sites that answer that way —
 * `not_administrator` on invite, the join-request queue, decide, the whole subscription family's
 * "bare 403/402/409/503", erasure, and every `/v1` 401.
 *
 * These used to render as the literal string `HTTP 403`. The asymmetry that made it worse: where
 * the commercial service is NOT routed (CI, and any instance without the desktop app in front
 * of it) the
 * content-type check answers `not-json` and the user got the graceful "we could not reach the
 * subscription service" — so raw developer output appeared ONLY in the environment where the
 * call is real. No status code reaches a rendered string any more.
 *
 * EVERY SENTENCE HERE IS SCOPE-NEUTRAL, and that is a deliberate limit rather than laziness.
 * This function is handed the result alone and the result carries no operation handle, so 403
 * cannot say "you are not the organization administrator": that is true of invite, removal and
 * the join queue, and NOT of the account-scoped calls (erasure, the successor list), where a 403
 * means something else this page never saw. Naming a cause on a coin-flip is worse than naming
 * none. The same goes for 404, which is "the service has no such thing" on a live route and
 * "this route has not landed yet" on the four contract-only operations — one sentence covers
 * both honestly and claims neither.
 *
 * A CALL SITE THAT KNOWS MORE MAY SAY MORE. Every caller spreads this refusal object, so a view
 * with operation context can replace `messageKey` with a sharper key of its own. What it must
 * never do is fall back to the status.
 */
const COMMERCIAL_STATUS_MESSAGE_KEY = Object.freeze({
  // Every `/v1` call whose bearer the service does not accept.
  401: 'one.commercial.notAuthenticated',
  // The auto-renewal payment refusal (client-http-27c95232:2299-2367).
  402: 'one.commercial.paymentRequired',
  403: 'one.commercial.forbidden',
  404: 'one.commercial.notFound',
  409: 'one.commercial.conflict',
  500: 'one.commercial.unavailable',
  502: 'one.commercial.unavailable',
  503: 'one.commercial.unavailable',
  504: 'one.commercial.unavailable',
});

/**
 * Translate a commercial `/v1` result into a refusal. BAR 8: `readCommercialResult` has already
 * refused to trust `res.ok` alone; this only turns its machine reason into something a person
 * can read, preferring the service's own sentence.
 *
 * `not-json` is the CI shape and is reported as absence, not as a crash: the fork's static
 * handler answers an unrouted `/v1/...` with the SPA's index.html at HTTP 200, and CI starts no
 * commercial service at all.
 *
 * ORDER, and why the server's sentence still comes first: ruling C4 — render the server's own
 * message verbatim wherever we have one, never paraphrase a refusal. The outcome table below is
 * consulted only when the body carried no sentence, which for the routes read at 27c95232 is
 * every single time (the invite handler projects four fields and none of them is a message:
 * client-http-27c95232:2854-2884). That it is reached always today is a fact about the service
 * as it stands, not a licence to stop preferring its words if it starts sending them.
 *
 * FOUR SOURCES FOR ONE SENTENCE, in this order and no other: the service's own words; the
 * refusal `outcome`; the HTTP status; and the generic "nothing was changed", which is true of
 * every refusal by construction and is therefore the last resort rather than the first.
 */
export function describeCommercialRefusal(result) {
  const reason = result?.reason ?? null;
  // NETWORK belongs with these two, not with the generic failure. All three mean "nothing
  // answered for the subscription service": `not-json` is the fork's SPA shell standing in for a
  // route that does not exist, `unparsable` is that shell (or something like it) getting past the
  // content type, and `network` is fetch itself rejecting — connection refused, DNS, TLS. The
  // user-visible fact is the same in all three and it is not "your action failed": nothing was
  // ever attempted against the service, so `one.deny.commercial` is the true sentence and
  // `one.error.requestFailed` would not be.
  if (reason === api.COMMERCIAL_REFUSAL.NOT_JSON
    || reason === api.COMMERCIAL_REFUSAL.UNPARSABLE
    || reason === api.COMMERCIAL_REFUSAL.NETWORK) {
    return {message: null, messageKey: 'one.deny.commercial', reason: DENY.COMMERCIAL, source: 'server'};
  }
  const message = typeof result?.message === 'string' && result.message !== '' ? result.message : null;
  if (message !== null) {
    return {message, messageKey: null, reason: DENY.SERVER, source: 'server'};
  }
  if (reason === api.COMMERCIAL_REFUSAL.OUTCOME) {
    const key = commercialOutcomeMessageKey(result?.body);
    if (key !== null) {
      return {message: null, messageKey: key, reason: DENY.SERVER, source: 'server'};
    }
  }
  if (reason === api.COMMERCIAL_REFUSAL.HTTP) {
    const key = COMMERCIAL_STATUS_MESSAGE_KEY[result?.status];
    if (key !== undefined) {
      return {message: null, messageKey: key, reason: DENY.SERVER, source: 'server'};
    }
    // NO `messageParams`, and that is the whole point: the status number never reaches a rendered
    // string. It stays on `result.status` for a caller that wants it, and it goes to the console
    // so a support report still has something to quote — neither is a sentence a customer reads.
    console.warn(`[one/app] no sentence for commercial HTTP ${String(result?.status)}`);
  }
  return {message: null, messageKey: 'one.error.requestFailed', reason: DENY.SERVER, source: 'server'};
}

/**
 * The key for a refused body's `outcome`, or null when nothing has classified it.
 *
 * `invitation_outcome` is read FIRST for `not_admitted`, and that is the whole reason this is a
 * function rather than one lookup. `POST /v1/team-access-requests/decide` projects two fields —
 * `{outcome, invitation_outcome}` (client-http-27c95232:3261-3264) — and the handler's own
 * comment at :3257-3260 says the second is "the half that matters": an administrator told only
 * "not admitted" cannot tell "this address belongs to another organization" from the seat
 * ceiling. The nested value is a `SeatAdmissionOutcome`, so it resolves through the same table.
 * When it names something unclassified, the outer `not_admitted` sentence still stands.
 */
function commercialOutcomeMessageKey(body) {
  if (body === null || typeof body !== 'object') return null;
  const outcome = typeof body.outcome === 'string' ? body.outcome : null;
  if (outcome === 'not_admitted') {
    const nested = outcomeKey(body.invitation_outcome);
    if (nested !== null) return nested;
  }
  return outcomeKey(outcome);
}

/**
 * `hasOwnProperty`, not a bare index. The value is a string off the wire from a codebase that is
 * not this repository, and a bare `TABLE[value]` answers a function for `'constructor'`,
 * `'toString'` and `'valueOf'` — which would then be handed to `t()` as a message key. Reaching
 * an inherited property is not a vocabulary this file has read, and bar 7's discipline is the
 * same for a value as for a field name.
 */
function outcomeKey(value) {
  if (typeof value !== 'string') return null;
  return Object.prototype.hasOwnProperty.call(COMMERCIAL_OUTCOME_MESSAGE_KEY, value)
    ? COMMERCIAL_OUTCOME_MESSAGE_KEY[value]
    : null;
}

/**
 * HTTP status -> sentence for a FORK refusal that carried no body at all.
 *
 * Separate from the commercial table, and not a near-duplicate of it: the two services refuse
 * different things and 403 in particular means something else on each side. On `/api/v2` a bare
 * 403 is the managed gate — `managed: "service-managed"` answers 403 for everyone including an
 * instance administrator (route-classification.json) — while on `/v1` it is
 * `not_administrator`. Folding them into one sentence would state the wrong cause on whichever
 * side lost.
 *
 * The fork usually DOES send a sentence (`message ?? detail ?? title` across its three error
 * envelopes), so this table is the tail case. It exists because a raw HTTP status line was what a
 * user read when it did not. (The key that produced it is trimmed out of the page catalogue; it
 * survives in frontend/src/i18n/lang/en.json, which the guard allows the page trim to be a subset
 * of, and nothing on the page reaches it. It is deliberately not named as a quoted literal here —
 * the fork-guards sweep would then require a key we removed on purpose.)
 */
const FORK_STATUS_MESSAGE_KEY = Object.freeze({
  // Not "sign in again": the retry-once state machine has already tried that, and a 401 reaching
  // a rendered refusal means the replay was refused too.
  401: 'one.error.sessionExpired',
  403: 'one.deny.forkForbidden',
  // `managed: "disabled"` answers a bare 404 (BRIEF, "Managed-mode refusal shapes"), and so does
  // a task or comment somebody else deleted while this page held it. Both are "it is not there".
  404: 'one.deny.forkNotFound',
  409: 'one.deny.forkConflict',
  500: 'one.error.serverUnavailable',
  502: 'one.error.serverUnavailable',
  503: 'one.error.serverUnavailable',
  504: 'one.error.serverUnavailable',
});

/**
 * Translate a fork error into a refusal. `serverMessage` is `message ?? detail ?? title` across
 * the fork's three error envelopes (api.js `ForkError`), including the team-capacity 409 whose
 * body must be rendered as sent — that body carries a server-computed `seats_needed`, and a
 * paraphrase could state a number the server would refuse.
 */
export function describeForkError(err) {
  if (err instanceof api.SessionLostError) {
    return {message: null, messageKey: 'one.error.sessionExpired', reason: DENY.SERVER, source: 'server'};
  }
  if (err instanceof api.ForkError) {
    if (err.serverMessage !== null) {
      return {message: err.serverMessage, messageKey: null, reason: DENY.SERVER, source: 'server'};
    }
    const key = FORK_STATUS_MESSAGE_KEY[err.status];
    if (key === undefined) {
      console.warn(`[one/app] no sentence for fork HTTP ${String(err.status)}`);
    }
    return {
      message: null,
      messageKey: key ?? 'one.error.requestFailed',
      reason: DENY.SERVER,
      source: 'server',
    };
  }
  return {message: null, messageKey: 'one.error.requestFailed', reason: DENY.SERVER, source: 'server'};
}

/* ------------------------------------------------------------------ *
 * 7. The seat meter
 * ------------------------------------------------------------------ */

/**
 * The server rule, PURE and with the ratio as an argument so a test can pin both halves:
 * a new team costs `seatsPerTeam * teamCount` purchased seats, and NOTHING ELSE enters it.
 */
export function requiredSeatsForTeams(teamCount, seatsPerTeam = SEATS_PER_TEAM) {
  return seatsPerTeam * teamCount;
}

/**
 * Read the seat position out of the FORK organization endpoint. The commercial service is
 * never the source for this — the brief is explicit, and the fork model carries every number
 * the meter needs in one payload.
 *
 * Three things this deliberately does NOT do:
 *   - it never recomputes `can_create_team`. The server sends it precisely so a client renders
 *     the same answer the route enforces (pkg/models/brazn_organization.go:126-131);
 *   - it never treats a null `seats_purchased` as 0 or as unlimited. Null means "this instance
 *     cannot answer", and telling a customer to buy seats they may already own is worse than
 *     saying we do not know;
 *   - it never falls back with `||`. `seats_per_team || 3` reads a legitimate 0 as 3, which is
 *     the exact prototype bug at line 602.
 *
 * `meetsNextTeamRule` is for DISPLAY ONLY — the disabled state and its numbers come from
 * `can_create_team` and from the 409 body's `seats_needed`.
 *
 * THE RATIO IS THE SERVER'S OR IT IS UNKNOWN. There is no local fallback of any kind — not
 * `seats_per_team || 3` (the prototype's bug, where a legitimate 0 reads as 3) and not the
 * gentler `?? SEATS_PER_TEAM` either. api.js says it at `getOrganization`: a constant duplicated
 * either side of a boundary is checked by neither, and a page that filled the gap from its own
 * copy would state a requirement the server never sent, on the one number a customer is asked
 * to spend money against. When the field is absent `requiredForNextTeam` is null and the view
 * renders `organization.teams.capped.unknown` — "we cannot read how many seats you have bought"
 * — which is true. `SEATS_PER_TEAM` survives only as the value the drift warning compares
 * against and as the contract literal the tests pin.
 *
 * In practice the field is never absent from a 200: pkg/models/brazn_organization.go:200 sets
 * `SeatsPerTeam` unconditionally. The null path is for a payload that came back through
 * something else — which is precisely when guessing would be worst.
 */
export function readSeatMeter(org) {
  const source = org ?? null;
  const seatsPerTeam = intOrNull(source?.seats_per_team);
  if (seatsPerTeam !== null && seatsPerTeam !== SEATS_PER_TEAM) {
    // The one check that exists on either side of the boundary. A constant duplicated in Go and
    // in JS is checked by neither; this line is what turns that into a console warning instead
    // of a customer-visible wrong number.
    console.warn(`[one/app] seats_per_team drifted: server ${seatsPerTeam}, page ${SEATS_PER_TEAM}`);
  }

  const occupied = intOrNull(source?.seats_occupied);
  const purchased = intOrNull(source?.seats_purchased);
  const teamsUsed = intOrNull(source?.teams_used);
  const requiredForNextTeam = teamsUsed === null || seatsPerTeam === null
    ? null
    : requiredSeatsForTeams(teamsUsed + 1, seatsPerTeam);

  return {
    occupied,
    purchased,
    teamsUsed,
    teamsAllowed: intOrNull(source?.teams_allowed),
    seatsPerTeam,
    requiredForNextTeam,
    meetsNextTeamRule: purchased === null || requiredForNextTeam === null
      ? null
      : purchased >= requiredForNextTeam,
    canCreateTeam: source?.can_create_team === true,
    fillRatio: purchased === null || purchased <= 0 || occupied === null
      ? null
      : Math.min(1, Math.max(0, occupied / purchased)),
  };
}

function intOrNull(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

/* ------------------------------------------------------------------ *
 * 8. Formatters
 * ------------------------------------------------------------------ */

let dateTimeFormat = null;
let dateFormat = null;
let timeFormat = null;
let numberFormat = null;

/**
 * ONE `Intl.DateTimeFormat` family, built once from the negotiated locale plus the user's
 * `timezone` and `frontend_settings.time_format`.
 *
 * `hourCycle`, not `hour12: false`. `hour12: false` is specified to produce the h24 cycle in
 * several engines, which renders midnight as 24:00 — so the 24-hour preference is expressed as
 * `h23` and the 12-hour one as `h12`.
 *
 * An unknown IANA zone makes the constructor throw a RangeError, and a stale timezone string on
 * one account must not take the page down: the fallback is the browser's own zone.
 */
export function buildFormatters(locale, timezone, timeFormatPreference) {
  const hourCycle = timeFormatPreference === '12h' ? 'h12' : 'h23';
  const zone = typeof timezone === 'string' && timezone !== '' ? timezone : undefined;
  const make = (options, tz) => new Intl.DateTimeFormat(locale, {...options, timeZone: tz, hourCycle});
  const build = (tz) => {
    dateTimeFormat = make({dateStyle: 'medium', timeStyle: 'short'}, tz);
    dateFormat = make({dateStyle: 'medium'}, tz);
    timeFormat = make({timeStyle: 'short'}, tz);
  };
  try {
    build(zone);
  } catch {
    console.warn(`[one/app] unusable timezone ${String(timezone)}, using the browser's`);
    build(undefined);
  }
  numberFormat = new Intl.NumberFormat(locale);
}

/** The composed formatter, for a view that needs `formatToParts` or a one-off variant. */
export function getDateTimeFormat() {
  return dateTimeFormat;
}

export function formatDateTime(value) {
  return applyFormat(dateTimeFormat, value);
}

export function formatDate(value) {
  return applyFormat(dateFormat, value);
}

export function formatTime(value) {
  return applyFormat(timeFormat, value);
}

export function formatNumber(value) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '';
  return (numberFormat ?? new Intl.NumberFormat(currentLocale())).format(value);
}

/**
 * The fork sends the zero time as `0001-01-01T00:00:00Z` for "unset" rather than as null, and
 * a page that formatted it would print the year 1 as a due date. Empty string for anything
 * unset, so a caller can test it with one falsy check.
 */
function applyFormat(formatter, value) {
  if (value === null || value === undefined || value === '') return '';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '';
  return (formatter ?? new Intl.DateTimeFormat(currentLocale())).format(date);
}

/* ------------------------------------------------------------------ *
 * 9. Theme
 * ------------------------------------------------------------------ */

const darkMediaQuery = () => (typeof matchMedia === 'function' ? matchMedia('(prefers-color-scheme: dark)') : null);

/**
 * `frontend_settings.color_schema` -> the class on `<html>`. Values are the Vue app's:
 * `'auto' | 'light' | 'dark'` (frontend/src/models/userSettings.ts:26).
 *
 * THE CLASS ALWAYS BEATS THE OS, which is why 'auto' REMOVES BOTH classes rather than setting
 * one: task.html styles three states, and the `:root:not(.light)` guard inside its
 * `prefers-color-scheme` block is what lets an explicit light preference win on a dark OS. A
 * media query alone cannot express that, and setting a class for 'auto' would freeze the page
 * at whatever the OS said on the one morning it loaded.
 *
 * Anything unrecognised — including the camelCase key read (which returns undefined) — falls to
 * 'auto', not to 'light': following the OS is the least wrong answer when the preference is
 * unreadable.
 */
export function applyColorScheme(schema) {
  if (typeof document === 'undefined' || !document.documentElement) return;
  const root = document.documentElement;
  if (schema === 'dark' || schema === 'light') {
    root.classList.toggle('dark', schema === 'dark');
    root.classList.toggle('light', schema === 'light');
    return;
  }
  root.classList.remove('dark');
  root.classList.remove('light');
}

/* ------------------------------------------------------------------ *
 * 10. i18n hydration
 * ------------------------------------------------------------------ */

const I18N_ATTRIBUTES = Object.freeze([
  ['data-i18n', (el, value) => {el.textContent = value;}],
  ['data-i18n-aria', (el, value) => el.setAttribute('aria-label', value)],
  ['data-i18n-placeholder', (el, value) => el.setAttribute('placeholder', value)],
  ['data-i18n-alt', (el, value) => el.setAttribute('alt', value)],
  ['data-i18n-title', (el, value) => el.setAttribute('title', value)],
]);

/**
 * Replace every `data-i18n*` value under `root`.
 *
 * `<template>` CONTENT IS A SEPARATE DocumentFragment, so a page-wide
 * `document.querySelectorAll` never sees inside `#brandLogo` and the logo alt would silently
 * stay English with nothing reporting it (task.html says so at the template). `hydrateShell`
 * below reaches in explicitly; any other caller passing a fragment gets the same treatment
 * because this function takes the root as an argument rather than assuming `document`.
 */
export function hydrateI18n(root) {
  if (!root || typeof root.querySelectorAll !== 'function') return;
  for (const [attribute, apply] of I18N_ATTRIBUTES) {
    const nodes = [...root.querySelectorAll(`[${attribute}]`)];
    if (typeof root.matches === 'function' && root.matches(`[${attribute}]`)) nodes.unshift(root);
    for (const el of nodes) apply(el, t(el.getAttribute(attribute)));
  }
}

function hydrateShell() {
  hydrateI18n(document);
  const template = document.getElementById('brandLogo');
  if (template?.content) hydrateI18n(template.content);
}

/* ------------------------------------------------------------------ *
 * 11. Modal, toast and the live region
 * ------------------------------------------------------------------ */

export function openModal(html) {
  const root = document.getElementById('modalRoot');
  if (root === null) return null;
  root.innerHTML = html;
  hydrateI18n(root);
  applyGates(root, renderedFacts ?? readGateFacts());
  focusModal(root);
  return root.firstElementChild;
}

/**
 * Move focus into the dialog that was just opened.
 *
 * This closes a gap that predates round 1b: nothing focused a modal, so a keyboard or
 * screen-reader user was left with focus on the button behind the scrim, tabbing through a page
 * they could no longer see. Two modals focus their own field afterwards (view-task.js:1686,
 * :1702) and both still win — they run after this returns.
 *
 * It is also what makes PM item 1 reachable at all: `commitOnEnter` only fires for a press whose
 * target is inside `#modalRoot`, and that is the deliberate bound on it. Without focus landing
 * here, Enter in a modal would keep doing nothing.
 *
 * A FIELD, NEVER THE PRIMARY BUTTON. Focusing the confirm button would put the caret on the
 * commit the instant the dialog appeared; `commitOnEnter` already refuses auto-repeat, but
 * landing on "Send invitation" is still the wrong place to start reading a form. `.modal` itself
 * is the fallback, made focusable with `tabindex="-1"` — focusable programmatically, still out of
 * the tab order.
 *
 * Refused fields are skipped: a `readOnly` input is how `applyGates` renders a refused text field
 * (see `applyDecision`), and starting there would offer the user a box that ignores typing.
 * `preventScroll` because a modal is already in view and the page behind it must not jump.
 */
function focusModal(root) {
  const dialog = root.querySelector('.modal');
  if (dialog === null || typeof dialog.focus !== 'function') return;

  const field = [...dialog.querySelectorAll('input, textarea, select')]
    .find((el) => el.type !== 'hidden' && !el.disabled && !isRefused(el));

  const target = field ?? dialog;
  if (target === dialog && !dialog.hasAttribute('tabindex')) dialog.setAttribute('tabindex', '-1');
  try {
    target.focus({preventScroll: true});
  } catch {
    // A detached or unfocusable node is not worth a broken modal.
  }
}

export function closeModal() {
  const root = document.getElementById('modalRoot');
  if (root !== null) root.innerHTML = '';
}

let toastTimer = null;

/**
 * Every write result AND every failure is reported here, and mirrored into `#a11yLive`. Bar 8
 * makes failure reporting load-bearing, and the prototype's toast had no `aria-live` at all —
 * so a screen-reader user got no confirmation and, worse, no error.
 */
export function toast(message) {
  const root = document.getElementById('toastRoot');
  if (root === null || !message) return;
  root.innerHTML = '';
  const node = document.createElement('div');
  node.className = 'toast';
  node.textContent = message;
  root.appendChild(node);
  announce(message);
  if (toastTimer !== null) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {root.innerHTML = '';}, 2600);
}

export function announce(message) {
  const live = document.getElementById('a11yLive');
  if (live === null) return;
  // Re-writing an identical string does not re-announce in several screen readers, so the node
  // is blanked first. The blank is not itself announced: the region is polite, not assertive.
  live.textContent = '';
  live.textContent = String(message);
}

/* ------------------------------------------------------------------ *
 * 12. The delegated action registry
 * ------------------------------------------------------------------ */

const actions = new Map();
let listenersInstalled = false;

/**
 * Register delegated click handlers, keyed on `data-action` exactly as the prototype does
 * (one document listener, 102 hooks). The shape is kept because it is what makes wholesale
 * `innerHTML` re-rendering safe: no handler is ever bound to a node that a re-render replaces.
 *
 * Re-registering a name THROWS. Two views quietly claiming `confirm-remove` is a real bug and
 * the last one to load would win silently.
 *
 * @param {Record<string, (event: Event, el: Element) => (void|Promise<void>)>} map
 */
export function registerActions(map) {
  for (const [name, handler] of Object.entries(map ?? {})) {
    if (typeof handler !== 'function') throw new Error(`app.js: action ${name} is not a function`);
    if (actions.has(name)) throw new Error(`app.js: action ${name} is already registered`);
    actions.set(name, handler);
  }
}

/** The registered names, sorted. For a test that asserts the shipped set. */
export function actionNames() {
  return [...actions.keys()].sort();
}

/**
 * REFUSE TO ACT ON A DISABLED CONTROL. `aria-disabled` does not stop a click the way `disabled`
 * does, and `.is-refused` on a GROUP disables its children in CSS only — so the check walks
 * ancestors. Without this the one honest thing about a refused control (that pressing it does
 * nothing) would be true in the stylesheet and false in the handler.
 */
export function isRefused(el) {
  if (typeof el?.closest !== 'function') return true;
  return el.closest('.is-refused, [aria-disabled="true"], :disabled') !== null;
}

/**
 * Elements where Enter ALREADY means something, and where a second meaning would either destroy
 * the first or fire two actions from one press:
 *   TEXTAREA  Enter inserts a newline. The comment box's Shift+Enter rule is `view-task.js`'s and
 *             is deliberately untouched here.
 *   SELECT    Enter commits the highlighted option to the native picker.
 *   BUTTON    Enter activates THAT button natively. Committing the primary as well would run two
 *             handlers from one keypress — including Cancel, which would then also confirm.
 *   A         Enter follows the link.
 */
const ENTER_INERT_TAGS = Object.freeze(['TEXTAREA', 'SELECT', 'BUTTON', 'A']);

/**
 * PM ROUND 1B, ITEM 1 — the modal / single-line half. "In modals and single-line inputs, Enter
 * commits the primary action, exactly as clicking the primary button does."
 *
 * IT COMMITS BY CLICKING. `primary.click()` re-enters the one delegated click listener above, so
 * the keyboard and the mouse run the SAME `isRefused` check, the same handler, the same
 * `dispatch` and the same role-drift resync. Calling the handler directly would have been a
 * second path that could drift from the first, which is exactly what the finding is about.
 *
 * SCOPE: MODALS ONLY, and that is a decision rather than an omission.
 *   - Inside a modal there is one unambiguous primary action, so "the primary action" has a
 *     referent. On the page body there is none — the task view has a dozen controls and no
 *     primary — so a page-wide rule would have to guess which one Enter meant.
 *   - The single-line inputs on the body commit on Enter and need nothing here, but not all of
 *     them do it through `change`, and this note used to claim they did. An `<input type=text>`
 *     fires `change` when the user commits with Enter as well as on blur, and both view modules
 *     bind `change` (view-task.js `installListeners`, view-settings.js `installChangeListeners`),
 *     which covers most of them. The two whose commit is somewhere else — the inline label chip,
 *     and the task title, whose only writer is a capture-phase `blur` handler — carry their own
 *     Enter bindings in view-task.js's `keydown` and are left alone; this handler returns before
 *     reaching either, because neither is inside `#modalRoot`.
 *
 * `.btn.primary` IN `.modal-foot` ONLY, AND EXACTLY ONE OF THEM.
 *   - `.modal-foot`, because the modal BODY also holds `.btn.small.primary` — the per-row "Add"
 *     buttons in the member picker (view-settings.js:2088). A body-wide search would let Enter
 *     add whichever person happened to be first in the list.
 *   - Exactly one, or nothing happens. Two primaries in one foot is an ambiguity, and this file
 *     resolves ambiguity by refusing (see `decideGate`'s unknown-token branch).
 *   - `.btn.danger` IS DELIBERATELY NOT A PRIMARY HERE. Delete task, delete account and remove
 *     member all put a destructive confirm in that slot. A stray Enter — dismissing an
 *     autocomplete, or a keyboard that repeats — must not be able to delete a task, and those
 *     modals lose nothing: Enter simply does what it does today, which is nothing. Reported so
 *     the PM can overrule it; it is not a limitation that was missed.
 *
 * THE REFUSAL CHECK GUARDS THE KEY PATH, which is the explicit half of the finding: "the refusal
 * check that guards the click path must guard the key path too, or the keyboard becomes a way
 * past a gate." A refused primary returns WITHOUT `preventDefault`, so Enter is left exactly as
 * inert as it was, and silently — a click on a refused control is silent too, and the refusal
 * sentence is already rendered beside it by `renderRefusal`.
 *
 * THE FOUR EARLY GUARDS, each for a real case:
 *   isComposing / keyCode 229  Enter confirms an IME candidate. Two of the six launch languages
 *                              (zh-CN, ja-JP) type through one, and without this the first Enter
 *                              of every Chinese or Japanese word would submit the dialog.
 *   repeat                     a held Enter would otherwise open a modal and confirm it in one
 *                              press, since focus lands inside the modal (see `focusModal`).
 *   any modifier               Shift/Ctrl/Cmd/Alt+Enter are other gestures, not this one.
 *   defaultPrevented           a view module's own Enter binding has already claimed this press.
 */
function commitOnEnter(event) {
  if (event.defaultPrevented) return;
  if (event.isComposing === true || event.keyCode === 229) return;
  if (event.repeat === true) return;
  if (event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) return;

  const target = event.target;
  if (typeof target?.closest !== 'function') return;
  if (ENTER_INERT_TAGS.includes(target.tagName)) return;
  if (target.isContentEditable === true) return;

  // Only one modal is ever open — `openModal` replaces `#modalRoot`'s contents wholesale — so a
  // single query inside it is unambiguous without needing to find the `.modal` wrapper first.
  const root = target.closest('#modalRoot');
  if (root === null) return;
  const foot = root.querySelector('.modal-foot');
  if (foot === null) return;

  const primaries = [...foot.querySelectorAll('.btn.primary')];
  if (primaries.length !== 1) return;
  if (isRefused(primaries[0])) return;

  event.preventDefault();
  primaries[0].click();
}

function installListeners() {
  if (listenersInstalled || typeof document === 'undefined') return;
  listenersInstalled = true;

  document.addEventListener('click', (event) => {
    const target = event.target;
    if (typeof target?.closest !== 'function') return;

    // Only a click ON the scrim itself dismisses; a click that bubbled up from inside the
    // modal shares the same ancestor and must not.
    const scrim = target.closest('[data-modal-scrim]');
    if (scrim !== null && target === scrim) {
      closeModal();
      return;
    }

    for (const attribute of ATTRIBUTE_HOOKS) {
      const el = target.closest(`[${attribute}]`);
      if (el === null) continue;
      const handler = actions.get(attribute);
      if (handler === undefined) continue;
      if (isRefused(el)) return;
      dispatch(handler, event, el);
      return;
    }

    const el = target.closest('[data-action]');
    if (el === null) return;
    if (isRefused(el)) return;

    const name = el.getAttribute('data-action');
    const handler = actions.get(name);
    if (handler === undefined) {
      console.warn(`[one/app] no handler for data-action="${name}"`);
      return;
    }
    dispatch(handler, event, el);
  });

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      closeModal();
      document.getElementById('morePopover')?.remove();
      return;
    }
    if (event.key === 'Enter') commitOnEnter(event);
  });

  window.addEventListener('resize', () => {
    document.getElementById('morePopover')?.remove();
  });

  window.addEventListener('popstate', () => {
    state.route = parseRoute(location.search, document.body?.dataset?.defaultView);
    render();
  });
}

/**
 * Run a handler, then re-check the role. A refresh can land inside any handler and the edition
 * claim is re-read from the entitlement projection at every minting, so a subscription that
 * changed — Teams to Personal, or a write restriction applied — takes effect on the next token.
 * Doing the check here, once, is what keeps every view free of it (F2).
 */
async function dispatch(handler, event, el) {
  try {
    await handler(event, el);
  } catch (err) {
    // A lost session already has its own terminal surface via `onSessionLost`; a toast on top
    // of it would report the same fact twice and imply the action is worth retrying.
    if (err instanceof api.SessionLostError) return;
    console.error('[one/app] action failed', err);
    toast(refusalText(describeForkError(err)));
  } finally {
    syncRoleDrift();
  }
}

/**
 * The token's claims are the truth; the screen was drawn from an older copy of them. Rather
 * than swapping controls under the user's cursor, the page marks itself stale and offers a
 * reload — the same shape as the fork's own `markStale` (frontend/src/stores/organization.ts:106).
 */
function syncRoleDrift() {
  if (renderedFacts === null || state.stale) return;
  const now = readGateFacts();
  const changed = now.hasEdition !== renderedFacts.hasEdition
    || now.personalEdition !== renderedFacts.personalEdition
    || now.writeRestricted !== renderedFacts.writeRestricted;
  if (!changed) return;
  state.stale = true;
  render();
}

/* ------------------------------------------------------------------ *
 * 13. The view registry
 * ------------------------------------------------------------------ */

/**
 * @typedef {Object} ViewContext
 * @property {{taskId: number|null, view: string, tab: string}} route  already clamped
 * @property {GateFacts} facts
 */

/**
 * @typedef {Object} ViewModule
 * @property {(ctx: ViewContext) => string} render  the HTML for `#app`. Must EMIT gated nodes
 *   and let `applyGates` decide their fate — never omit a node because a gate is false
 *   (ruling C4).
 * @property {(root: Element, ctx: ViewContext) => void} [mount]  runs after insertion, before
 *   gates are applied. This is where select options, the seat-meter width and anything else
 *   that cannot be a template string get set.
 */

const views = new Map();

/**
 * Called by `view-task.js` / `view-settings.js` at import time. They import THIS module
 * statically; this module imports THEM dynamically, after it has finished evaluating, so there
 * is no static cycle to reason about.
 */
export function registerView(name, view) {
  if (!VIEWS.includes(name)) throw new Error(`app.js: unknown view ${name}`);
  if (typeof view?.render !== 'function') throw new Error(`app.js: view ${name} has no render()`);
  views.set(name, view);
}

async function loadViews() {
  // allSettled, not all: a view module that 404s must cost its own view and nothing else. This
  // is also what lets this file ship before either view module exists.
  const results = await Promise.allSettled([
    import('./view-task.js'),
    import('./view-settings.js'),
  ]);
  for (const result of results) {
    if (result.status === 'rejected') console.error('[one/app] a view module failed to load', result.reason);
  }
}

/* ------------------------------------------------------------------ *
 * 13b. The header identity block — avatar, name, subscription
 *
 * PM ROUND 1B, ITEM 3, AND THE ONE DELIBERATE EXCEPTION TO THIS FILE'S
 * "IT RENDERS NO VIEW MARKUP" RULE AT THE TOP.
 *
 * The finding: "the avatar circle sits next to the name and the subscription
 * line, vertically centred, as one block", and "in settings just like in
 * tasks". Two headers were drawing two different answers — `view-task.js` had
 * avatar + name + role + edition, `view-settings.js` had name + a combined
 * "{edition} · {role}" line and NO avatar at all — so "identical on both" is
 * not something either view module can deliver alone. Whichever one moved
 * first, the other would drift on the next edit.
 *
 * So the block is built ONCE here and ADOPTED into whichever header the view
 * drew (`mountIdentity`). This is chrome, not view content: it belongs to the
 * page, it is byte-identical on both documents, and neither view has anything
 * to say about it. The view modules keep rendering their own identity node as
 * a placeholder and this replaces it; once they are free to edit, each should
 * emit a bare `<div data-identity></div>` and delete its own copy — that is
 * the slot this looks for FIRST, precisely so that change needs nothing here.
 *
 * WHAT IS NOT CHANGED, because round 1 settled it: the subscription line is
 * the edition and it is ABSENT — not "ONE Teams", not blank — when the
 * `brazn_edition` claim is absent. `editionMessageKey` returns null there,
 * `data-requires="edition"` is in `GATES_THAT_HIDE`, and `applyGates` removes
 * the node. Printing an edition for a claimless session would state a
 * subscription the page never read (bar 7). See `editionMessageKey`.
 * ------------------------------------------------------------------ */

/** A user's display name, falling back to the username the fork always sends. */
export function personName(person) {
  const name = typeof person?.name === 'string' && person.name.trim() !== '' ? person.name.trim() : null;
  return name ?? person?.username ?? '';
}

/** Two letters at most, from the display name. The circle's fallback face. */
export function initials(person) {
  const words = String(personName(person)).trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return '?';
  const letters = words.length === 1 ? words[0].slice(0, 2) : words[0][0] + words[1][0];
  return letters.toUpperCase();
}

/**
 * EVERY KEY BELOW IS A LITERAL INSIDE ITS OWN `t()` CALL, and the two functions
 * exist only to make that true. Ruling C10 has the fork-guards step prove each
 * key exists by grepping `t('…')` literals out of this directory, and a key
 * reached through a variable — or interpolated into `data-i18n="${key}"` — is
 * invisible to it. `loadSurface()` below carries the same note for the same
 * reason: that was the one place on the page that used to disobey.
 */
function roleText(facts) {
  if (facts.orgAdmin) return t('one.role.administrator');
  return facts.personalEdition ? t('one.role.personalUser') : t('one.role.teamMember');
}

function editionText(facts) {
  const key = editionMessageKey(facts);
  if (key === null) return '';
  return key === 'one.edition.personal' ? t('one.edition.personal') : t('one.edition.teams');
}

/**
 * The two glyphs this file draws — the header's add-task plus, and the close cross every modal
 * head carries.
 *
 * `view-task.js` has an `ICON` table of fourteen and an `ic()` helper, and neither is reached
 * from here: that module is loaded DYNAMICALLY and is allowed to fail to load at all
 * (`loadViews` swallows a rejection into a console line), and the header has to keep drawing
 * when it does. Both paths are the same ones, copied rather than imported — two duplicated
 * short strings bought instead of a dependency the header cannot afford, which is the same
 * trade the whole of section 13b already made.
 *
 * `aria-hidden` and nothing else, exactly as `ic()` marks every SVG it emits: the button's own
 * `aria-label` is the accessible name, and a described glyph inside a labelled button is the
 * same thing read to somebody twice. Sized by the global `svg` rule in one.css.
 */
const PLUS_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>';
const CLOSE_ICON = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18"/></svg>';

/**
 * The block itself. Three lines beside one circle, then the add-task button,
 * vertically centred by `.one-identity` (one.css) rather than by anything here.
 *
 * The image is `alt=""` and `aria-hidden` ON PURPOSE and it costs no catalogue
 * key: the name it depicts is the very next node, so a described avatar would
 * make a screen reader read the same person twice. The initials underneath are
 * the same decoration and are hidden the same way.
 *
 * THE BUTTON IS LAST, AND THE ORDER IS LOAD-BEARING RATHER THAN TASTE. It is
 * gated, so `applyGates` inserts the refusal sentence as its NEXT SIBLING, and
 * `.refusal-text` is `flex-basis:100%` in a wrapping row — which puts the
 * sentence on its own line underneath. Put the button first instead and that
 * same full-width line lands BETWEEN the button and the avatar, splitting the
 * identity block in half for exactly the customer who is being refused. This is
 * the case one.css already documents for the project chip in the breadcrumb,
 * and `.one-identity` is added to the same `flex-wrap:wrap` list there.
 *
 * IT IS ON BOTH DOCUMENTS BECAUSE THIS BLOCK IS, which is the whole reason the
 * button lives here rather than in either view module. `mountIdentity` adopts
 * this markup into `.task-topbar` on task.html and `.settings-hero` on
 * settings.html, so the control cannot drift between the two the way the
 * avatar and the subscription line did before round 1b.
 */
export function identityBlock(facts = readGateFacts()) {
  const user = getUser();
  const edition = editionText(facts);
  return `<div class="one-identity" data-identity>
    <div class="one-identity-avatar" aria-hidden="true">${avatarFace(user)}</div>
    <div class="one-identity-meta">
      <strong>${escapeHtml(personName(user))}</strong>
      <span>${escapeHtml(roleText(facts))}</span>
      <small data-requires="edition">${escapeHtml(edition)}</small>
    </div>
    <button class="icon-btn" data-action="add-task" data-requires="write"
      aria-label="${escapeHtml(t('one.task.add.title'))}">${PLUS_ICON}</button>
  </div>`;
}

function avatarFace(user) {
  const url = headerAvatarUrl(user);
  if (url === null) return escapeHtml(initials(user));
  return `<img src="${escapeHtml(url)}" alt="">`;
}

/**
 * Put the block into the header the view just drew.
 *
 * The slot is looked for in this order, and the order is the migration path:
 *   [data-identity]        what a view SHOULD emit once it may be edited;
 *   .task-user-summary     what `view-task.js` emits today;
 *   .settings-role         what `view-settings.js` emits today.
 * If a header exists but holds none of them, the block is appended to it — so
 * a view that simply deletes its identity node still gets one. If there is no
 * header at all, nothing happens: this must never invent a header on the
 * blocking surfaces, which deliberately have none.
 *
 * `outerHTML` replacement rather than filling the existing node, because the
 * two nodes it replaces carry different classes with different CSS, and
 * inheriting either would reintroduce exactly the difference this removes.
 */
function mountIdentity(root, facts) {
  if (root === null || typeof root.querySelector !== 'function') return;
  const header = root.querySelector('.task-topbar, .settings-hero') ?? root.querySelector('header');
  if (header === null) return;

  // Kicked off here rather than in `boot()`: this is the only place that knows
  // a header is actually on screen, and it is a no-op once the bytes are in.
  ensureAvatar();

  const html = identityBlock(facts);
  const slot = header.querySelector('[data-identity], .task-user-summary, .settings-role');
  if (slot === null) header.insertAdjacentHTML('beforeend', html);
  else slot.outerHTML = html;
}

/* --- the avatar bytes, once, for both circles --------------------- *
 *
 * 44 px circle at 2x, so it is not soft on a retina display. The settings
 * card's own circle is 58 px (`.profile-avatar`) and asks for 116; the two
 * sizes are the only thing that differ between the two reads, which is why
 * `api.getAvatarBlob` takes the size rather than owning one.
 *
 * ONE HELPER, NOT TWO (PM item 2). `api.getAvatarBlob` is the shared request
 * and `api.getAvatarGeneration()` is the shared cache key, bumped inside
 * `api.saveAvatar` after both of its calls. That is what makes a stale face
 * impossible after an upload no matter which surface performed it: neither
 * circle keeps a private notion of "current". `view-settings.js` still has its
 * round-1 private copy of this logic and should be reduced to these two calls
 * — reported, not edited, because another agent holds that file.
 *
 * The OBJECT URL lifecycle stays here and out of api.js: only a renderer knows
 * when a URL has stopped being an `<img src>`, and revoking one that is still
 * on screen blanks the picture.
 */

const AVATAR_PIXEL_SIZE = 88;

/**
 * ONE SLOT PER PERSON, not one slot. This was a single pair of variables holding the signed-in
 * user's picture, which is why every other face on the page — a comment author's — could only
 * ever be initials. `username -> {key, url}`: `key` is the generation-stamped cache key CLAIMED
 * for that person (in flight or settled) and `url` is the object URL last produced for it, or
 * null meaning "there is no picture, draw the initials". The signed-in user is one entry in here
 * like anybody else, so an upload still invalidates it through the shared generation.
 */
const avatarByUsername = new Map();

function avatarCacheKey(user) {
  return `${user?.username ?? ''}|${api.getAvatarGeneration()}`;
}

/** The URL to paint for anyone, or null for "use the initials". Synchronous, for render. */
export function avatarUrlFor(user) {
  const entry = avatarByUsername.get(user?.username ?? '');
  return entry !== undefined && entry.key === avatarCacheKey(user) ? entry.url : null;
}

/** The header's circle. Unchanged in behaviour: it is the above, for the signed-in user. */
export function headerAvatarUrl(user) {
  return avatarUrlFor(user);
}

/**
 * Read one person's avatar once per key. Re-entrant by design — the callers run on every render
 * and this is what makes it a no-op after the first.
 *
 * Fire-and-forget on purpose: the picture is decorative, render is synchronous,
 * and a slow or refused avatar must never delay or fail the page around it.
 * `api.getAvatarBlob` resolves to null for every failure and never throws, so
 * there is no rejection path to handle here.
 *
 * `render()`, not `requestRender()`: the bytes landing say nothing about the
 * account, and `requestRender` would schedule an account re-read for it. It is also why the
 * `previous === next` test matters more now than it did with one face: most people have no
 * uploaded picture, both sides are null, and a page of comments must not re-render once per
 * author to learn that.
 */
export function ensureAvatarFor(user) {
  const username = user?.username ?? '';
  if (username === '') return;

  const key = avatarCacheKey(user);
  const entry = avatarByUsername.get(username);
  if (entry !== undefined && entry.key === key) return;
  // Claimed BEFORE the await. A second render during the read must not start a
  // second one. The PREVIOUS url is deliberately left in place until the new
  // bytes land, so an upload cross-fades instead of flashing the initials.
  avatarByUsername.set(username, {key, url: entry?.url ?? null});

  void api.getAvatarBlob(username, AVATAR_PIXEL_SIZE).then((blob) => {
    // The key moved while this was in flight — another upload, or another
    // account. Nothing is created, so nothing leaks.
    const current = avatarByUsername.get(username);
    if (current === undefined || current.key !== key) return;
    const next = blob === null ? null : URL.createObjectURL(blob);
    const previous = current.url;
    if (previous === next) return;
    avatarByUsername.set(username, {key, url: next});
    // Revoked only after nothing points at it any more, and only once.
    if (typeof previous === 'string') URL.revokeObjectURL(previous);
    render();
  });
}

function ensureAvatar() {
  ensureAvatarFor(getUser());
}

/* ------------------------------------------------------------------ *
 * 13c. Add a task — the header button's behaviour
 *
 * THE SECOND EXCEPTION TO "IT RENDERS NO VIEW MARKUP", declared here rather
 * than discovered later. It is the same exception as 13b and for the same
 * reason: the control sits in the shared identity block, so it exists on
 * task.html and settings.html alike, and neither view module can own behaviour
 * for a button on a page it does not render. `view-settings.js` has no notion
 * of a task at all.
 *
 * WHAT THE BUTTON CAN DO, ESTABLISHED BEFORE IT WAS BUILT. `api.createTask`
 * carries the route verification in full; the short version is that creating a
 * task inside an existing project is `ordinary` in route-classification.json,
 * so managed mode does not refuse it, while creating a PROJECT is
 * `protected-topology` and does. That is exactly why this asks which project
 * and never offers to make one.
 *
 * AND WHY IT HAS TO ASK. There is no default to fall back on: every account has
 * an Inbox and the server points `default_project_id` at it, but that column is
 * `json:"-"` (pkg/user/user.go:114) and reaches no client. Matching the title
 * "Inbox" instead would be a guess against a name customers can change and
 * duplicate, which is the reason pkg/models/brazn_topology.go gives for
 * refusing to identify an Inbox that way itself. So the destination is asked
 * for, from `GET /api/v2/projects`, the same source the move picker uses.
 * ------------------------------------------------------------------ */

/**
 * Normalise a collection body. `GET /api/v2/projects` answers with a bare array
 * today and the paginated shape is `{items}`; both are accepted so a server
 * change does not empty the picker in silence. Same three lines as
 * `view-task.js`'s `items()`, which this file cannot reach into.
 */
function collectionItems(payload) {
  if (Array.isArray(payload)) return payload;
  if (Array.isArray(payload?.items)) return payload.items;
  return [];
}

/**
 * Open the dialog. ASYNC ON PURPOSE, and the read happens BEFORE the modal is
 * drawn rather than after: a picker that appears empty and fills in a moment
 * later is one a fast reader commits against the wrong project, and there is no
 * loading state on this page to borrow. One `GET /api/v2/projects` is cheap.
 *
 * A FAILED OR EMPTY READ REFUSES IN THE MARKUP, exactly as `moveModal` does and
 * for the identical reason: every account has an Inbox, so an empty list means
 * the read failed rather than that there is nowhere to put a task. The refusal
 * is written into the markup instead of through `data-requires` because
 * `applyGates` calls `releaseControl` on a passing gate and would strip a
 * manually applied refusal straight back off.
 */
async function openAddTask() {
  let projects = [];
  try {
    projects = collectionItems(await api.listProjects({perPage: 100}));
  } catch (err) {
    if (err instanceof api.SessionLostError) throw err;
    console.error('[one/app] the projects read for a new task failed', err);
  }

  const options = projects.map((project) => `<option value="${escapeHtml(project?.id)}"
    >${escapeHtml(projectTitle(project))}</option>`).join('');

  const confirm = options === ''
    ? `<button class="btn primary is-refused" data-action="confirm-add-task" aria-disabled="true"
        >${escapeHtml(t('one.common.add'))}</button>
       <p class="refusal-text" data-refusal-source="server"
         >${escapeHtml(t('one.error.requestFailed'))}</p>`
    : `<button class="btn primary" data-action="confirm-add-task" data-requires="write"
        >${escapeHtml(t('one.common.add'))}</button>`;

  openModal(`<div class="modal-scrim" data-modal-scrim="true"><div class="modal">
    <div class="modal-head"><h3>${escapeHtml(t('one.task.add.title'))}</h3>
      <button class="icon-btn" data-action="modal-close"
        aria-label="${escapeHtml(t('misc.closeDialog'))}">${CLOSE_ICON}</button></div>
    <div class="modal-body">
      <label class="label">${escapeHtml(t('task.attributes.title'))}</label>
      <input class="input" id="newTaskTitle"
        aria-label="${escapeHtml(t('task.attributes.title'))}">
      <label class="label" style="margin-top:8px"
        >${escapeHtml(t('one.task.add.project'))}</label>
      <select class="select" id="newTaskProject"
        aria-label="${escapeHtml(t('one.task.add.project'))}">${options}</select>
    </div>
    <div class="modal-foot">
      <button class="btn" data-action="modal-close">${escapeHtml(t('misc.cancel'))}</button>
      ${confirm}
    </div>
  </div></div>`);
}

/**
 * Create it, then take the person to it.
 *
 * THE EMPTY TITLE IS CAUGHT HERE AND NOT LEFT TO THE SERVER, which is a
 * deliberate exception to "render the server's own sentence". The server does
 * refuse it — `minLength:"1"` on Task.Title (pkg/models/tasks.go:68) — but the
 * sentence Huma answers a schema violation with is "validation failed", which
 * tells a customer nothing about which box to fill in. Every OTHER refusal on
 * this path is still the server's own words, through `describeForkError`.
 *
 * NAVIGATION IS THE CONFIRMATION, so the toast is short. `navigate` re-renders,
 * and the task view loads the new task by id — which is also the only proof
 * that offers itself: the person lands on the thing they just made.
 */
async function confirmAddTask(event, el) {
  const root = document.getElementById('modalRoot');
  const title = String(root?.querySelector('#newTaskTitle')?.value ?? '').trim();
  if (title === '') {
    renderRefusal(el, {messageKey: 'one.task.add.titleRequired', source: 'server'});
    return;
  }

  const projectId = Number(root?.querySelector('#newTaskProject')?.value);
  if (!Number.isSafeInteger(projectId) || projectId <= 0) {
    renderRefusal(el, {messageKey: 'one.error.requestFailed', source: 'server'});
    return;
  }

  try {
    const task = await api.createTask(projectId, title);
    const id = typeof task?.id === 'number' ? task.id : null;
    closeModal();
    toast(t('one.toast.taskAdded'));
    // A created task with no id is not something this page can navigate to, and
    // guessing one would open somebody else's. The toast already said it worked.
    if (id !== null) showTask(id);
  } catch (err) {
    if (err instanceof api.SessionLostError) throw err;
    renderRefusal(el, describeForkError(err));
  }
}

/**
 * Go to a task — and CROSS TO THE TASK DOCUMENT when we are not already on it.
 *
 * `navigate()` alone is wrong from settings.html and the reason is written down
 * two hundred lines up, in `parseRoute`: the document chooses the view, `?view=`
 * only OVERRIDES it, and that override "is not what a link should carry".
 * Routing in place from the settings document lands the person on
 * `settings.html?task=41&view=task` — a task rendered inside the settings
 * document, on a web address whose filename says settings. It works, and it is
 * a URL nobody can hand to anyone, which is the exact fault splitting the two
 * documents was meant to remove.
 *
 * So the document is compared against the one the task needs. Already on it,
 * this stays a history push and nothing reloads — the common case, from the
 * task page. Anywhere else it is a real navigation to `task.html`, and the
 * address that results is the one somebody can paste to a colleague.
 *
 * The URL is built by `routeToSearch`, not by hand, so a later change to the
 * query grammar moves this with it.
 */
function showTask(taskId) {
  const route = {taskId, view: 'task', tab: SETTINGS_TABS[0]};
  if (document.body?.dataset?.defaultView === 'task') {
    navigate({view: 'task', taskId});
    return;
  }
  location.assign(`./task.html${routeToSearch(route)}`);
}

/* ------------------------------------------------------------------ *
 * 14. Render
 * ------------------------------------------------------------------ */

/**
 * Re-render the current view. Views call this after they change their own scratch state, and —
 * this is the part that matters — after every successful write.
 *
 * THE PARTIAL REFRESH AFTER AN EDIT, AND WHY IT WAS BROKEN. `render()` replaces `#app` wholesale,
 * so the redraw was never the problem: the DATA behind it was. `GET /api/v2/user` was read ONCE,
 * in `boot()`, and nothing re-read it — `reloadOrganization()` existed, `reloadUser()` did not. So
 * a view could save a display name, get a 200, call this function, and watch the page redraw the
 * SAME pre-write body it was drawn from before. The timezone select "jumping back" was the same
 * defect one field over, and it is why both view modules grew private copies of the account.
 *
 * `reloadUser()` below is the repair. This function is where it is TRIGGERED, because it is the
 * one signal both views already emit after a write and it costs neither of them a line:
 *
 *   - only on the settings view. Nothing on the task view can change the account, so the task
 *     page issues no extra request at all;
 *   - coalesced and throttled (`ACCOUNT_RESYNC_MIN_INTERVAL_MS`), so a burst of renders — the
 *     timezone list landing, the avatar bytes landing — is at most one read;
 *   - and it re-renders ONLY when the payload actually differs from the one on screen. That
 *     guard is not an optimisation: an unconditional second render would replace `#app` under a
 *     user who is mid-sentence in the comment box for no reason at all.
 *
 * A view that wants the refresh to be part of its own await chain — so its toast lands AFTER the
 * value it describes — calls `reloadUser()` directly and gets the same single in-flight promise.
 */
export function requestRender() {
  render();
  scheduleAccountResync();
}

function render() {
  const app = document.getElementById('app');
  if (app === null) return;

  if (state.failed || state.sessionEnded) {
    app.className = 'window settings-window';
    app.innerHTML = loadSurface();
    hydrateI18n(app);
    return;
  }

  const facts = readGateFacts();
  const route = resolveRoute(state.route, facts);
  // The clamp is WRITTEN BACK, not merely rendered. `navigate()` merges its patch over
  // `state.route` and re-serialises the whole of it, so keeping an unreachable `tab` here would
  // put `?tab=organization` back into the address bar on the next navigation of an account that
  // is not an administrator — a link that lands somewhere else again when it is pasted.
  state.route = route;
  renderedFacts = facts;

  app.className = `window ${route.view === 'settings' ? 'settings-window' : 'task-window'}`;

  const view = views.get(route.view);
  if (view === undefined) {
    app.innerHTML = loadSurface();
    hydrateI18n(app);
    return;
  }

  const ctx = {route, facts};
  app.innerHTML = pageNotices(route) + view.render(ctx);
  view.mount?.(app, ctx);
  // AFTER the view's mount and BEFORE hydration and gates, and all three positions matter.
  // After mount, so a view that rebuilds its own header cannot drop the block again. Before
  // hydrateI18n, so the logo `<img>`s the view cloned out of the shell template still get their
  // alt. Before applyGates, because the block's own subscription line carries
  // `data-requires="edition"` and must be removed by the same pass as everything else.
  mountIdentity(app, facts);
  hydrateI18n(app);
  // Gates last: a view's `mount` may have inserted per-team rows, and those carry the
  // `data-team` scope that only exists once the roster is on the page.
  applyGates(app, facts);
}

/**
 * The page-level notices, above whichever view is drawn. Both are `app.js`'s rather than a
 * view's because both describe the PAGE's state — the facts it was drawn from — and neither
 * belongs to the task detail or to one settings tab.
 */
function pageNotices(route) {
  return (state.stale ? staleNotice() : '')
    + (route.view === 'settings' && state.organizationError !== null ? organizationNotice() : '');
}

/**
 * THE ORGANIZATION READ FAILED, AND IT WAS NOT A 403.
 *
 * F3 draws exactly one distinction from that call, and until this notice existed the page drew
 * none: `loadOrganization` sets `state.organization = null` on a 403 AND on a 500, `readGateFacts`
 * derives `orgAdmin` from `state.organization !== null` alone, and `GATES_THAT_HIDE` then removes
 * the Organization and Team tabs. So an organization administrator who hit a transient 500 saw
 * the screen a demoted account sees: two tabs silently gone, no banner, no toast, no retry.
 * `getOrganizationError()` was exported for this and read by nothing.
 *
 * WHY A NOTICE AND NOT THE TABS. Ruling C4 reserves HIDE for "the whole surface is absent for
 * this user", and a 500 is not that — but the tabs cannot be drawn either: every control on them
 * renders out of the organization payload, and there is none. Rendering empty tabs would be a
 * second lie. What is true is "we could not read your subscription, nothing about it changed,
 * and it comes back on its own", which is `organization.unavailable.*` word for word. The 403
 * path is untouched and stays silent: `state.organizationError` is null there, so this never
 * fires for the ordinary answer.
 *
 * `organization.retry` reloads. `reloadOrganization()` would be the softer retry, but the failure
 * that reaches here is not necessarily confined to that one call — a full reload re-runs the
 * boot the page has exactly one of, and it is the same control the fatal surface offers.
 */
function organizationNotice() {
  return `<div class="load-surface"><div class="notice" data-notice="organization-unavailable">
    <strong data-i18n="organization.unavailable.title"></strong>
    <span data-i18n="organization.unavailable.text"></span>
    <p style="margin-top:10px"><button class="btn small" data-action="retry"
      data-i18n="organization.retry"></button></p>
  </div></div>`;
}

/**
 * The blocking surface: no session, a fatal load failure, or a view that would not load. It is
 * the prototype's `.live-surface` renamed — the demo/live duality it belonged to is deleted, so
 * "live" no longer distinguishes anything.
 *
 * NO DYNAMIC `t()` KEY. Ruling C10 requires it ("if a call is dynamic, make it non-dynamic") and
 * this was the one place on the page that disobeyed: `<h2 data-i18n="${titleKey}">` is invisible
 * to the fork-guards i18n sweep, which extracts quoted namespace-anchored LITERALS. Every value
 * `titleKey` could take happened to resolve, but a rename would have shipped a raw dotted path
 * onto the one screen with nothing else on it — precisely the silent failure the guard exists to
 * stop. Both keys are literals in this file now, and the guard can see both.
 *
 * The caller's old `loadSurface('one.error.loadFailed')` — the view-module-missing case — is now
 * `loadSurface()`, and it renders the identical sentence: the early return above means
 * `state.sessionEnded` is always false by the time that branch is reached.
 */
function loadSurface() {
  const detail = state.fatalMessage === null ? '' : escapeHtml(state.fatalMessage);
  const action = state.sessionEnded
    ? `<button class="btn primary" data-action="signin" data-i18n="user.auth.login"></button>`
    : `<button class="btn" data-action="retry" data-i18n="organization.retry"></button>`;
  const title = state.sessionEnded
    ? `<h2 data-i18n="one.error.sessionExpired"></h2>`
    : `<h2 data-i18n="one.error.loadFailed"></h2>`;
  return `<div class="load-surface"><div class="card">
    ${title}
    ${detail === '' ? '' : `<p class="refusal-text" data-refusal-source="server">${detail}</p>`}
    <p style="margin-top:14px">${action}</p>
  </div></div>`;
}

function staleNotice() {
  return `<div class="load-surface"><div class="notice">
    <strong data-i18n="organization.stale.title"></strong>
    <span data-i18n="organization.stale.text"></span>
    <p style="margin-top:10px"><button class="btn small" data-action="reload"
      data-i18n="organization.stale.action"></button></p>
  </div></div>`;
}

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[ch]));
}

/* ------------------------------------------------------------------ *
 * 15. Boot
 * ------------------------------------------------------------------ */

/**
 * The shell is hidden until the language resolves and the page renders ONCE (ruling C10).
 * Rendering before the negotiated catalogue is loaded produces an English flash and a second
 * hydration pass, and the second pass is where stale DOM state leaks through.
 */
function setShellVisible(visible) {
  const stage = document.querySelector('.stage');
  if (stage !== null) stage.classList.toggle('hidden', !visible);
}

/**
 * NO SESSION -> hand off to the fork's existing login route. Bar 4: do not build a login page,
 * a callback page, or touch auth.
 *
 * FINDING, recorded rather than papered over: the SPA's return-to mechanism cannot carry this
 * page. `saveLastVisited` stores a vue-router ROUTE NAME plus params
 * (frontend/src/helpers/saveLastVisited.ts:8) and the `#redirect=` bridge is resolved through
 * `router.resolve` (frontend/src/router/index.ts:601-603). `/one/task.html?task=…` is a static
 * file, not a router route, so both would resolve to the not-found route and send the user
 * somewhere worse than the app root. Writing a fake entry to make a return-to appear to work
 * would be inventing behaviour the router does not have, so the handoff is a plain top-level
 * navigation and the user lands in the app after signing in.
 *
 * THE HAND-OFF LOOP — the reason this is not one line.
 *
 * `/login` is a vue-router path. It is not a fork route (routes.go registers only POST /login)
 * and it is not a file in `dist/`, so on an instance with `brazn.restricteduionly` ON it falls
 * through to the static handler's not-found path, where `braznServeAppShell` redirects every
 * SPA path to this page (pkg/routes/static_brazn.go:82-86). The loop:
 *
 *   GET /one/task.html -> no session -> assign('/login') -> 302 /one/task.html -> boot ->
 *   no session -> assign('/login') -> ... -> ERR_TOO_MANY_REDIRECTS, and a signed-out user of a
 *   locked-down instance can never reach a sign-in form.
 *
 * THE REPAIR IS SERVER-SIDE and is not this file's to make: the authentication paths have to
 * keep reaching `serveIndexFile`, which is a change to the lockout (bar 4 forbids this page
 * building its own login, and the Go lockout is reviewed separately). It is written up in
 * docs/one-tasks-restricted-views.md so it cannot be lost between the two lanes.
 *
 * What this file owes the user meanwhile is that the loop is BOUNDED. Exactly one automatic
 * hand-off per browsing session; if the browser comes back here still without a session, the
 * page stops and renders its terminal sign-in surface, and the button on it hands off again
 * only because a person pressed it — one hop per press, never a chain. A visible surface with a
 * button is a dead end a user can act on and report; ERR_TOO_MANY_REDIRECTS is not.
 */
// Spelled with a brazn. prefix rather than a one. one, on purpose: the fork-guards i18n step
// sweeps every quoted namespace-anchored string out of this directory and checks it against the
// catalogue, so a storage key beginning with the one. namespace would be reported as a missing
// translation. It is a storage key, not a message key, and it must not read like one.
const LOGIN_HANDOFF_MARKER = 'brazn.one.login-handoff';

/**
 * The marker is `sessionStorage`, never `localStorage`: it is per-tab and it dies with the tab,
 * so it can never outlive the browsing session that created it and strand a later visit on the
 * terminal surface. Returns null when storage is unusable at all (private modes, storage
 * disabled by policy) — which is a third answer, not a false one.
 */
function readHandoffMarker() {
  try {
    return sessionStorage.getItem(LOGIN_HANDOFF_MARKER) !== null;
  } catch {
    return null;
  }
}

function writeHandoffMarker(present) {
  try {
    if (present) sessionStorage.setItem(LOGIN_HANDOFF_MARKER, '1');
    else sessionStorage.removeItem(LOGIN_HANDOFF_MARKER);
  } catch {
    // Unusable storage is handled by the redirectCount fallback in shouldHandOffToLogin.
  }
}

/**
 * PURE, so the whole decision is a table in a test rather than a browser navigation.
 *
 *   marker    the sessionStorage flag: true = we already sent this tab to /login, false = we
 *             have not, null = storage cannot answer.
 *   redirects `PerformanceNavigationTiming.redirectCount` for the current document.
 *   force     a person pressed Sign in. Always hands off: the loop is bounded by the user's
 *             own click at that point, and refusing would leave a button that does nothing.
 *
 * The marker is the primary signal because it is exact — written immediately before the
 * navigation, cleared the moment a session exists. `redirectCount` is consulted ONLY when
 * storage cannot answer, and it is deliberately the weaker signal: a `/tasks/123` deep link is
 * also redirected here under the lockout, so on its own it would refuse a first hand-off to
 * somebody who never had one.
 */
export function shouldHandOffToLogin({marker = null, redirects = 0, force = false} = {}) {
  if (force) return true;
  if (marker === true) return false;
  if (marker === false) return true;
  return !(typeof redirects === 'number' && redirects > 0);
}

function navigationRedirectCount() {
  try {
    const [entry] = performance.getEntriesByType('navigation');
    return typeof entry?.redirectCount === 'number' ? entry.redirectCount : 0;
  } catch {
    return 0;
  }
}

/** @returns {boolean} true when it navigated; false when it refused to re-enter the loop. */
function handOffToLogin({force = false} = {}) {
  const allowed = shouldHandOffToLogin({
    marker: readHandoffMarker(),
    redirects: navigationRedirectCount(),
    force,
  });
  if (!allowed) {
    console.error('[one/app] the /login hand-off returned here with no session; not redirecting again');
    return false;
  }
  writeHandoffMarker(true);
  location.assign(new URL('/login', location.origin).toString());
  return true;
}

/**
 * THE JOIN RETURN LEG (BRA-1439 Story 5). /one/join.html hands a signed-out invitation
 * recipient to the Vue app's sign-in pages, and signing in lands them in the APPLICATION — the
 * finding above explains why no return-to can carry a static page. So the join page records the
 * pending invitation under this key, and boot(), which every ONE page runs the moment a session
 * exists, sends the person back to the join page exactly once to finish accepting.
 *
 * `localStorage`, NOT sessionStorage, and that is a decision with a cost paid deliberately: the
 * set-a-password path crosses tabs (the reset link arrives by email and opens wherever the mail
 * client puts it), and a per-tab marker would strand exactly the people who most need the
 * return leg. What bounds the cost on a shared machine: the marker holds only the invitation id
 * (never the signup token, which stays in per-tab sessionStorage), it expires after an hour,
 * this hook REMOVES it before navigating — one automatic bounce per write — and the join page
 * consumes it on every terminal outcome, where the commercial service's own outcome vocabulary
 * (`no_invitation`, `not_invitable`) is the real guard against the wrong person accepting.
 */
// brazn. prefix, not one., for LOGIN_HANDOFF_MARKER's reason: a storage key must not read like
// an i18n key to the fork-guards sweep.
export const PENDING_JOIN_KEY = 'brazn.one.join-pending';

const PENDING_JOIN_MAX_AGE_MS = 60 * 60 * 1000;

/**
 * PURE, so the whole decision is a table in a test: the stored string (or null) and the clock
 * in, the invitation id to resume (or null) out. Malformed JSON, a missing id, a missing or
 * unreadable timestamp and a stale timestamp all answer null — a marker that cannot prove it is
 * fresh must not move the browser.
 */
export function pendingJoinRedirect(raw, now) {
  if (typeof raw !== 'string' || raw === '') return null;
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  const id = typeof parsed?.i === 'string' && parsed.i !== '' ? parsed.i : null;
  const at = typeof parsed?.at === 'number' && Number.isFinite(parsed.at) ? parsed.at : null;
  if (id === null || at === null) return null;
  if (typeof now !== 'number' || now < at || now - at > PENDING_JOIN_MAX_AGE_MS) return null;
  return id;
}

/** Read AND remove the marker — the removal is what makes the bounce one-shot. */
function consumePendingJoin() {
  let raw = null;
  try {
    raw = localStorage.getItem(PENDING_JOIN_KEY);
    if (raw !== null) localStorage.removeItem(PENDING_JOIN_KEY);
  } catch {
    return null;
  }
  return pendingJoinRedirect(raw, Date.now());
}

/**
 * The terminal sign-in surface. Reached when the hand-off refused to loop, and when the session
 * is lost after boot — both are "there is nothing to render and the user has to sign in", and
 * folding them into one function is what keeps the two paths from drifting apart.
 */
async function showSignInSurface() {
  state.sessionEnded = true;
  await ensureStrings();
  setShellVisible(true);
  render();
}

/**
 * The catalogue, for a path that never reached the ordinary boot ordering. The no-session and
 * fatal paths render BEFORE `GET /user` has resolved a language preference, and rendering them
 * with no catalogue loaded would put `one.error.sessionExpired` on screen as a raw key path —
 * the exact failure the i18n guard exists to stop, on the one screen with nothing else on it.
 * There is no preference to honour here, so it negotiates from `navigator.languages` alone.
 */
let stringsReady = false;

async function ensureStrings() {
  if (stringsReady) return;
  try {
    await initI18n(null, navigatorLanguages());
    stringsReady = true;
  } catch (err) {
    // A page of key paths is still better than a blank one, and t() falls back to the key path.
    console.error('[one/app] no string catalogue could be loaded', err);
  }
}

export async function boot() {
  if (typeof document === 'undefined') return;
  setShellVisible(false);
  installListeners();

  try {
    // 1. Refresh on load — v1, and only v1: the refresh cookie's Path is hardcoded to
    //    /api/v1/user/token/refresh, so a v2 refresh never receives it and always 401s.
    if (!await api.initSession()) {
      // F1: no shell is rendered first. A settings skeleton behind a redirect is a flash of a
      // page the user is not entitled to see. If the hand-off refuses to loop, the sign-in
      // surface IS the render — which is the one case where a shell has to appear.
      if (!handOffToLogin()) await showSignInSurface();
      return;
    }
    // A session exists, so the next expiry earns a fresh automatic hand-off: the marker is
    // about "we tried and came straight back", not about "we have ever tried".
    writeHandoffMarker(false);

    // The join return leg, before anything renders: a person who signed in to accept an
    // invitation is standing on the wrong page right now, and painting this one first would be
    // a flash of a surface they did not ask for. `consumePendingJoin` removed the marker, so
    // this navigation happens once per write, and the join page re-establishes the session from
    // the refresh cookie the way every page here does.
    const pendingJoinId = consumePendingJoin();
    if (pendingJoinId !== null) {
      location.replace(`${api.forkAppUrl('one/join.html')}?i=${encodeURIComponent(pendingJoinId)}`);
      return;
    }
    // After boot a lost session is a TERMINAL STATE with a visible hand-off, not a silent
    // redirect: a redirect mid-edit throws away whatever the user had typed with no warning.
    api.onSessionLost(() => {showSignInSurface();});

    // 2. The user and every preference, in one call — and 3. the palette before anything paints,
    //    so there is no light flash on a dark account. Both are `adoptAccount`, which is the SAME
    //    function every later re-read goes through (§15b): boot used to be the only place that
    //    turned this body into page state, which is precisely why nothing could refresh it.
    //
    //    The formatters are the one part boot still builds itself, and only because of ordering:
    //    they need the negotiated locale, and the preference that negotiates it is in this body.
    adoptAccount(await api.getCurrentUser(), {deriveFormatters: false});

    // 4. The language. Everything after this may call t().
    const locale = await initI18n(state.settings.language, navigatorLanguages());
    stringsReady = true;
    buildFormatters(locale, state.settings.timezone, state.frontendSettings.time_format);

    await loadOrganization();
    await loadTeams();

    state.route = parseRoute(location.search, document.body?.dataset?.defaultView);
    await loadViews();

    state.ready = true;
    hydrateShell();
    setShellVisible(true);
    render();
  } catch (err) {
    if (err instanceof api.SessionLostError) {
      if (!handOffToLogin()) await showSignInSurface();
      return;
    }
    console.error('[one/app] boot failed', err);
    state.failed = true;
    state.fatalMessage = describeForkError(err).message ?? null;
    // The failure may have happened before the catalogue loaded, and this surface is the whole
    // screen: rendering it as key paths would be the i18n failure on the one page that has
    // nothing else on it.
    await ensureStrings();
    setShellVisible(true);
    render();
  }
}

function navigatorLanguages() {
  return typeof navigator !== 'undefined' && Array.isArray(navigator.languages) ? navigator.languages : [];
}

/**
 * 403 IS THE ORDINARY ANSWER and api.js already folds it to null. Anything else — 500, a
 * network failure, an HTML error page — IS an error and is kept, because that one distinction
 * is the only thing this call tells the page beyond "administrator or not" (F3).
 *
 * A real error here is deliberately NOT fatal: the account tab does not depend on the
 * organization, and losing the whole page over a surface most users never see would be worse
 * than losing that surface.
 */
async function loadOrganization() {
  try {
    state.organization = await api.getOrganization();
    state.organizationError = null;
  } catch (err) {
    if (err instanceof api.SessionLostError) throw err;
    console.error('[one/app] organization read failed', err);
    state.organization = null;
    state.organizationError = err;
  }
}

/**
 * One read per team, in Promise.allSettled — NEVER Promise.all (ruling C11).
 *
 * `GET /api/v2/teams/{id}` 403s when the administrator is not a member of that team, because
 * `Team.CanRead` requires membership (pkg/models/teams_permissions.go:68-85). That is the
 * EXPECTED case for the commercially provisioned primary team, not an exception — so one 403
 * must degrade one roster to "unavailable" and must not blank the tab.
 *
 * `admin` comes from `members[].admin` for the acting user, never assumed from organization
 * administration: `Team.CanUpdate` is the team-admin bit, so an organization administrator with
 * no admin row on team X cannot rename X and the entitlement claim does not help.
 */
async function loadTeams() {
  state.teams.clear();
  const listed = Array.isArray(state.organization?.teams) ? state.organization.teams : [];
  if (listed.length === 0) return;

  const results = await Promise.allSettled(listed.map((entry) => api.getTeam(entry.team_id)));
  const me = currentUserId();

  listed.forEach((entry, index) => {
    const id = String(entry.team_id);
    const result = results[index];
    if (result.status !== 'fulfilled') {
      state.teams.set(id, {id, readable: false, admin: false, team: null, error: result.reason});
      return;
    }
    const team = result.value;
    const members = Array.isArray(team?.members) ? team.members : [];
    const mine = me === null ? undefined : members.find((member) => member?.id === me);
    state.teams.set(id, {id, readable: true, admin: mine?.admin === true, team, error: null});
  });
}

/** Re-read the organization and its rosters. The seat meter's only source (J6 step 3, F6). */
export async function reloadOrganization() {
  await loadOrganization();
  await loadTeams();
  render();
}

/* ------------------------------------------------------------------ *
 * 15b. The account read — the one thing that was loaded once and never again
 * ------------------------------------------------------------------ */

/**
 * A cheap stand-in for "is this the same account body we are already showing". `GET /api/v2/user`
 * is a Go struct marshalled by `encoding/json`, so its key order is stable across responses and a
 * string compare is a sound equality test for it. If two identical bodies ever did compare
 * different the only cost is one extra render, which is the safe direction: the guard exists to
 * suppress a pointless redraw, not to be authoritative about anything.
 */
let accountFingerprint = null;
/** The single in-flight read, in the same shape as api.js's refresh promise. */
let accountReloadInFlight = null;
let accountReadAt = 0;
/**
 * The floor between two account reads. Long enough that a burst of renders costs one request,
 * short enough that a save followed immediately by a render never waits for it — a save is
 * always more than this apart from the render that preceded it, because a round trip happened in
 * between.
 */
const ACCOUNT_RESYNC_MIN_INTERVAL_MS = 1500;

/**
 * Adopt a `GET /api/v2/user` body as the page's account state and RE-DERIVE EVERYTHING THAT HANGS
 * OFF IT. One function for the boot read and for every re-read, so the two cannot drift — which
 * they did: boot applied the colour scheme and built the formatters, and there was no other path,
 * so a timezone saved after boot left every date on the page formatted in the old zone until a
 * full reload.
 *
 * `deriveFormatters:false` is for the boot call ALONE, and only because of ordering: boot reads
 * the user BEFORE it negotiates the language (the preference it negotiates from is in this very
 * body), and `buildFormatters` takes the resolved locale. Boot therefore builds them itself one
 * step later. Every other caller gets them here.
 *
 * WHAT IT DELIBERATELY DOES NOT DO: re-negotiate the language. `initI18n` loads catalogues and
 * hydrates the shell once (ruling C10's boot ordering), and swapping catalogues under a rendered
 * page is a second hydration pass rather than a refresh. Nothing on either page writes
 * `settings.language`, so no control can reach that state; if one is ever added it needs a full
 * reload, not this function.
 *
 * @returns {boolean} whether the body differs from the one already on screen. False on the first
 *   adoption — there was nothing on screen to differ from.
 */
function adoptAccount(user, {deriveFormatters = true} = {}) {
  const settings = user?.settings ?? {};
  // `frontend_settings` is declared `any` and stored VERBATIM (pkg/models/user_settings.go:45),
  // so an account written by anything other than the Vue app can legitimately hold a string, an
  // array or null. Anything that is not a plain object reads as "no preferences", which lands on
  // auto / 24-hour rather than throwing on a property access.
  const frontend = settings.frontend_settings;
  const frontendSettings = frontend !== null && typeof frontend === 'object' && !Array.isArray(frontend)
    ? frontend
    : {};

  const fingerprint = fingerprintAccount(user);
  const changed = accountFingerprint !== null && accountFingerprint !== fingerprint;
  accountFingerprint = fingerprint;
  accountReadAt = Date.now();

  state.user = user ?? null;
  state.settings = settings;
  state.frontendSettings = frontendSettings;

  applyColorScheme(frontendSettings.color_schema);
  if (deriveFormatters) {
    buildFormatters(currentLocale(), settings.timezone, frontendSettings.time_format);
  }
  return changed;
}

function fingerprintAccount(user) {
  try {
    return JSON.stringify(user ?? null);
  } catch {
    // Unstringifiable is not "unchanged": a unique value makes the next comparison report a
    // change, which costs one render and never suppresses a real one.
    return `unstringifiable:${Date.now()}:${Math.random()}`;
  }
}

/**
 * RE-READ THE ACCOUNT. This is the missing half of "the section must reflect the new value" — the
 * counterpart of `reloadOrganization()` for the user half of `boot()`.
 *
 * Both views may call it directly and AWAIT it before they toast, which is the ordering that
 * matters: a success message that lands ahead of the value it describes is what made a stale
 * render look like a failed save. Concurrent callers share ONE request — the same single
 * in-flight promise shape api.js uses for the token refresh — so the view's own await and the
 * render-triggered resync can never become two reads.
 *
 * It renders only when the body actually changed (see `requestRender`).
 *
 * FAILURE POLICY. A failed re-read is NOT a failed write and must never be reported as one: the
 * write already landed, and saying otherwise invites the user to repeat an edit the server has
 * already taken. It is logged, the previous copy stands, and the page is exactly as stale as it
 * was a moment ago. `SessionLostError` is the one exception and is rethrown, because app.js owns
 * the terminal surface for it and swallowing it here would leave a dead page with no explanation.
 *
 * @returns {Promise<boolean>} whether anything changed.
 */
export function reloadUser() {
  if (accountReloadInFlight !== null) return accountReloadInFlight;
  // The chain is started from a microtask, and the release is on the OUTER promise, so the field
  // is always assigned before anything can clear it. Clearing it inside `readAccount`'s own
  // `finally` looked equivalent and is not: `api.getCurrentUser` is a plain function, so a throw
  // before its first await would run that `finally` synchronously — nulling the field a moment
  // BEFORE the line below set it — and the settled promise would stay pinned here for the life of
  // the page, with every later re-read resolving instantly against a body nobody re-fetched.
  accountReloadInFlight = Promise.resolve()
    .then(readAccount)
    .finally(() => {accountReloadInFlight = null;});
  return accountReloadInFlight;
}

async function readAccount() {
  try {
    const changed = adoptAccount(await api.getCurrentUser());
    if (changed) render();
    return changed;
  } catch (err) {
    if (err instanceof api.SessionLostError) throw err;
    console.error('[one/app] the account re-read failed', err);
    return false;
  }
}

/**
 * The render-driven trigger. Deliberately fire-and-forget and deliberately silent about a
 * `SessionLostError`: `api.onSessionLost` already draws the terminal surface for that, and a
 * background refresh must not be the thing that reports it twice.
 */
function scheduleAccountResync() {
  if (!state.ready || state.failed || state.sessionEnded) return;
  // Nothing on the task view can change the account, so it pays nothing for this.
  if (state.route.view !== 'settings') return;
  if (accountReloadInFlight !== null) return;
  const now = Date.now();
  if (now - accountReadAt < ACCOUNT_RESYNC_MIN_INTERVAL_MS) return;
  // The slot is claimed BEFORE the await, so a failed read cannot be retried on every render.
  accountReadAt = now;
  reloadUser().catch((err) => {
    if (err instanceof api.SessionLostError) return;
    console.error('[one/app] the account resync failed', err);
  });
}

/* ------------------------------------------------------------------ *
 * 16. The actions this file owns
 * ------------------------------------------------------------------ */

/**
 * TWO HOOKS WERE DROPPED HERE, and the deletion is recorded rather than silent.
 *
 * `return-signin` mirrored the prototype's "Return to sign in" (prototype line 1088), which
 * lived in the `state.accountDeleted` arm of the demo/live duality — an arm this page deletes
 * wholesale. Nothing emits `data-action="return-signin"` in the shell, in `view-task.js` or in
 * `view-settings.js`, and `signin` already does exactly the same thing for the one surface that
 * has the button. The orphaned `one.settings.accountDeleted.*` catalogue node went with it.
 *
 * `data-nav` was one of the three prototype ATTRIBUTE_HOOKS, and no view emits `data-nav=`
 * either: this page has no cross-view navigation control. The task view is reachable only by
 * deep link (ruling C19a), so there is no "back to the task" affordance to hook. A registered
 * handler for an attribute nothing writes is a hook a later reader assumes is load-bearing.
 * `data-settings-tab` and `data-resource` both have real emitters and both stay.
 */
registerActions({
  'modal-close': () => closeModal(),
  // `force`: a person pressed it. The automatic hand-off refuses to re-enter the /login loop,
  // but a button that declines to do the one thing it says would be worse than a bounce the
  // user can see and stop.
  signin: () => {handOffToLogin({force: true});},
  reload: () => location.reload(),
  // `retry` replaces the deleted `api-load`. A reload, not a re-run of `boot()`: boot is not
  // re-entrant (i18n.init, the formatters and the listeners are all one-shot) and making it so
  // would be more machinery than the one case it serves. It is also what the organization
  // notice offers, for the same reason.
  retry: () => location.reload(),

  // Section 13c. Registered HERE rather than in a view module because the button lives in the
  // shared identity block, so it is on both documents — and `view-settings.js` has no notion of
  // a task to own it with. `registerActions` throws on a duplicate name, so a view module that
  // later claims either of these fails loudly at load rather than silently winning.
  'add-task': () => openAddTask(),
  'confirm-add-task': (event, el) => confirmAddTask(event, el),

  'data-settings-tab': (event, el) => {
    const value = el.getAttribute('data-settings-tab');
    if (SETTINGS_TABS.includes(value)) navigate({view: 'settings', tab: value});
  },
});

/* ------------------------------------------------------------------ *
 * 17. Auto-boot
 * ------------------------------------------------------------------ */

/**
 * Boot only when a real shell is present. A unit test that imports this module for
 * `decideGate` / `parseRoute` / `readSeatMeter` has no `#app` in its document and therefore
 * gets no session refresh, no fetch and no render — which is what keeps this module as
 * import-time-pure as api.js.
 *
 * `queueMicrotask` rather than a bare call so the module finishes evaluating first: `boot()`
 * reaches `registerActions` above and the exports below it.
 */
if (typeof document !== 'undefined' && document.getElementById('app') !== null) {
  queueMicrotask(() => {boot();});
}
