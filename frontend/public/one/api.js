/**
 * ONE Tasks restricted views — data layer (BRA-1357 / BRA-1358).
 *
 * Plain ES module. No imports, no framework, no build step: Vite copies
 * `public/` verbatim, so anything imported here would have to be resolvable by
 * the browser. Nothing in `frontend/src/` is reachable from this file by design
 * — see `commercialV1Url` below and ruling C1.4.
 *
 * NO USER-FACING STRINGS LIVE HERE. Every message this module surfaces is
 * either the server's own sentence (rendered verbatim per ruling C4) or a
 * machine-readable reason code from one of the frozen enums below, which
 * `app.js` maps to a `t()` key. Thrown developer assertions carry a `code` and
 * must never be rendered.
 *
 * IMPORT-TIME PURITY: this module issues no request and touches no DOM/global
 * when it is loaded. `globalThis.fetch`, the page origin and the UUID source
 * are all resolved lazily on first use, and each can be replaced through
 * `configure()` so a stubbed-fetch unit test never has to patch a global.
 */

'use strict';

/* ------------------------------------------------------------------ *
 * 1. Injection points
 * ------------------------------------------------------------------ */

let injectedFetch = null;
let injectedOrigin = null;
let injectedRandomUUID = null;

/**
 * Replace the fetch implementation, the origin and/or the idempotency-key
 * source. Every field is optional; passing `null` restores the default.
 * Bar 9 makes stubbed-fetch unit tests the only automated evidence this page
 * can have, so this is the seam those tests attach to.
 */
export function configure({fetch: fetchImpl, origin, randomUUID} = {}) {
  if (fetchImpl !== undefined) injectedFetch = fetchImpl;
  if (origin !== undefined) injectedOrigin = origin;
  if (randomUUID !== undefined) injectedRandomUUID = randomUUID;
}

function rawFetch(url, init) {
  const impl = injectedFetch ?? globalThis.fetch;
  if (typeof impl !== 'function') throw assertion('no-fetch-implementation');
  // Called unbound so an injected stub keeps whatever `this` it wants; the
  // platform fetch is not bound to `window` in any engine we target.
  return impl(url, init);
}

function pageOrigin() {
  if (injectedOrigin !== null) return injectedOrigin;
  return globalThis.location.origin;
}

/**
 * THE FORK'S BASE, WHICH IS NOT THE ORIGIN ROOT.
 *
 * Resolved from the module's own URL rather than from `location.origin`. The two are the same
 * string while the application is served at the host root, which it is, and they stop being the
 * same the moment it is not — and neither CI nor the unit tests would show the difference,
 * because both serve from the root.
 *
 * So resolve against this module's own URL — it is `<base>/one/api.js`, so `../`
 * is the application base wherever it is mounted. This also survives the page
 * being split into task.html and settings.html, which a document-relative
 * version would not.
 */
function forkBase() {
  if (injectedOrigin !== null) return new URL('/', injectedOrigin);
  return new URL('../', import.meta.url);
}

/**
 * Idempotency keys for the three commercial routes whose contract names one.
 * The caller may always pass its own; this is only the default source.
 */
export function newIdempotencyKey() {
  const impl = injectedRandomUUID ?? globalThis.crypto?.randomUUID?.bind(globalThis.crypto);
  if (typeof impl !== 'function') throw assertion('no-uuid-source');
  return impl();
}

/* ------------------------------------------------------------------ *
 * 2. The three bases (ruling C18, bar 6)
 * ------------------------------------------------------------------ */

/**
 * TWO FORK BASES, not one (ruling C18).
 *
 * v1 exists here for exactly two reasons and must not spread:
 *   - the refresh cookie's Path is hardcoded to "/api/v1/user/token/refresh"
 *     (pkg/modules/auth/auth.go:60-72), so the browser never sends it to a v2
 *     refresh, which therefore always 401s;
 *   - the organization read model and organization team create/delete have no
 *     v2 equivalent (pkg/routes/routes.go:650-657).
 * Everything else is v2: `?format=markdown` and `X-Vikunja-Format` are
 * implemented only in pkg/routes/api/v2/richtext.go:31-49, and v1 would store
 * Markdown as literal text without erroring.
 */
export const FORK_V1_BASE = '/api/v1/';
export const FORK_V2_BASE = '/api/v2/';

/**
 * The commercial service is `/v1` with NO `/api` prefix (bar 6) — a different
 * codebase on the same host. It must be addressed ORIGIN-ROOTED: a relative
 * `/v1/...` re-bases onto the fork's `/api/v1` and silently becomes
 * `/api/v1/v1/...`.
 *
 * This is deliberately not imported from frontend/src/helpers/fetcher.ts:
 * `commercialV1Url()` there is on the unmerged PR #50, and ruling C1.4 forbids
 * importing from `src/` into this page at all.
 */
export function commercialV1Url(path) {
  return new URL(`/v1/${stripLeadingSlash(path)}`, pageOrigin()).toString();
}

export function forkV1Url(path) {
  return new URL(`${stripLeadingSlash(FORK_V1_BASE)}${stripLeadingSlash(path)}`, forkBase()).toString();
}

export function forkV2Url(path) {
  return new URL(`${stripLeadingSlash(FORK_V2_BASE)}${stripLeadingSlash(path)}`, forkBase()).toString();
}

function stripLeadingSlash(path) {
  return String(path).replace(/^\/+/, '');
}

function withQuery(url, params) {
  const u = new URL(url);
  for (const [key, value] of Object.entries(params ?? {})) {
    if (value === undefined || value === null || value === '') continue;
    // `expand` is repeatable (pkg/routes/api/v2/tasks.go:112-116); everything
    // else is scalar. append-per-element is the only shape that survives both.
    if (Array.isArray(value)) {
      for (const item of value) u.searchParams.append(key, String(item));
      continue;
    }
    u.searchParams.set(key, String(value));
  }
  return u.toString();
}

/* ------------------------------------------------------------------ *
 * 3. Errors
 * ------------------------------------------------------------------ */

/** A programming error in this page. Carries a code so it is never rendered. */
function assertion(code) {
  const err = new Error(`api.js assertion: ${code}`);
  err.name = 'ApiAssertionError';
  err.code = code;
  return err;
}

/**
 * A non-2xx answer from the fork.
 *
 * `serverMessage` reads `message ?? detail ?? title` because the fork emits
 * three different error envelopes and which one arrives is not predictable
 * from the status alone: managed-gate refusals are Echo middleware errors
 * (`{"message":...}`, pkg/routes/error_handler.go:37-40) even on a v2 route,
 * v2 handler errors are RFC 9457 problem+json (pkg/routes/api/v2/errors.go:119-149),
 * and the team-capacity 409 is a bare struct with its own `message`
 * (pkg/routes/api/v1/brazn_organization.go:167). Render `serverMessage`
 * verbatim (ruling C4) — never paraphrase a refusal.
 */
export class ForkError extends Error {
  constructor(status, body, url) {
    super(`fork ${status} ${url}`);
    this.name = 'ForkError';
    this.status = status;
    this.body = body ?? null;
    this.url = url;
    this.details = readServerErrorDetails(body);
    this.serverMessage = composeServerMessage(readServerMessage(body), this.details);
    this.code = body && typeof body.code === 'number' ? body.code : null;
  }
}

/** The session is gone and cannot be recovered. `app.js` hands off to login. */
export class SessionLostError extends Error {
  constructor() {
    super('session lost');
    this.name = 'SessionLostError';
  }
}

function readServerMessage(body) {
  if (body === null || typeof body !== 'object') return null;
  for (const key of ['message', 'detail', 'title']) {
    if (typeof body[key] === 'string' && body[key] !== '') return body[key];
  }
  return null;
}

/**
 * THE HALF OF AN RFC 9457 BODY THAT USED TO BE THROWN AWAY.
 *
 * Huma answers a request-schema failure with HTTP 422 and the CONSTANT string
 * `"validation failed"` in `detail`; everything that says WHICH field and WHY
 * lives in a sibling `errors` array of `{message, location, value}`
 * (huma v2.39.0 `huma.ErrorDetail`, surfaced by this fork through
 * `invalidFieldDetails` at pkg/routes/api/v2/errors.go:100-113, which builds the
 * same shape for a govalidator failure with `location` = "body.<field>").
 *
 * Reading only `detail` therefore renders one unactionable sentence for every
 * possible bad field — which is exactly what the Task Details page showed for
 * every write, with the one word naming the cause sitting unread in the body.
 * Nothing outside this file could see it: `ForkError` exposed `body`, but
 * `app.js`'s `describeForkError` reads `serverMessage` and nothing else.
 *
 * Returns [] rather than null so callers can iterate without a guard.
 */
function readServerErrorDetails(body) {
  if (body === null || typeof body !== 'object' || !Array.isArray(body.errors)) return [];
  const details = [];
  for (const entry of body.errors) {
    if (entry === null || typeof entry !== 'object') continue;
    const message = typeof entry.message === 'string' && entry.message !== '' ? entry.message : null;
    const location = typeof entry.location === 'string' && entry.location !== '' ? entry.location : null;
    if (message === null && location === null) continue;
    details.push({location, message});
  }
  return details;
}

/**
 * The server's sentence, plus the server's own per-field sentences after it.
 *
 * THIS IS NOT A PARAPHRASE AND RULING C4 STILL HOLDS. Every word here came off
 * the wire; the only thing this function contributes is punctuation. It exists
 * because `serverMessage` is the ONLY channel `app.js` renders (app.js:1086-1088),
 * so a detail that is not folded into it cannot reach a person today. `details`
 * is also exposed on the error untouched, so a caller that wants to render the
 * fields as their own list — the better surface, and the one described in the
 * report accompanying this change — can do that without re-parsing the body.
 *
 * Capped at four entries: a merged-resource PATCH can fail on many fields at
 * once and a toast is not a log. The full array is always on `err.details`.
 */
function composeServerMessage(message, details) {
  if (details.length === 0) return message;
  const parts = details.slice(0, 4).map((detail) => {
    if (detail.location === null) return detail.message;
    if (detail.message === null) return detail.location;
    return `${detail.location}: ${detail.message}`;
  });
  const suffix = parts.join('; ');
  return message === null ? suffix : `${message} (${suffix})`;
}

/* ------------------------------------------------------------------ *
 * 4. Session
 * ------------------------------------------------------------------ */

/**
 * The access token lives here and only here. Never localStorage: the TTL is
 * 600 s, the refresh cookie is HttpOnly, and a token in storage outlives the
 * tab that earned it.
 */
let accessToken = null;
let sessionLost = false;
let refreshInFlight = null;
const sessionLostListeners = new Set();

export function getToken() {
  return accessToken;
}

export function setToken(token) {
  accessToken = typeof token === 'string' && token !== '' ? token : null;
}

export function hasSession() {
  return accessToken !== null && !sessionLost;
}

export function isSessionLost() {
  return sessionLost;
}

/**
 * Observe the terminal no-session state. `app.js` uses this to hand off to the
 * fork's existing login route (bar 4 — do not build a login page here).
 *
 * A listener registered after the state was already reached is invoked
 * immediately: `initSession()` can fail before `app.js` has finished wiring,
 * and a handoff that depends on subscription order would be lost for exactly
 * the users who need it.
 */
export function onSessionLost(listener) {
  if (typeof listener !== 'function') throw assertion('session-listener-not-a-function');
  if (sessionLost) {
    listener();
    return () => {};
  }
  sessionLostListeners.add(listener);
  return () => sessionLostListeners.delete(listener);
}

function markSessionLost() {
  if (sessionLost) return;
  sessionLost = true;
  accessToken = null;
  for (const listener of sessionLostListeners) listener();
}

/** Test seam: drop all session state. Listeners are kept. */
export function resetSession() {
  accessToken = null;
  sessionLost = false;
  refreshInFlight = null;
}

/**
 * A SINGLE in-flight refresh shared by every caller.
 *
 * Two concurrent 401s must not mint two tokens: the refresh rotates the cookie
 * (pkg/routes/api/v1/login.go:143-150), so the second rotation would invalidate
 * the first caller's freshly issued token and produce a logout that looks
 * random. Resolves to the new token, or `null` once the state is terminal.
 */
export function refreshSession() {
  if (sessionLost) return Promise.resolve(null);
  if (refreshInFlight === null) {
    refreshInFlight = performRefresh().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

async function performRefresh() {
  // v1, and only v1 — the cookie Path makes a v2 refresh a guaranteed 401.
  // No bearer: the browser attaches the HttpOnly refresh cookie itself
  // (pkg/modules/auth/auth.go:56), which is the whole reason
  // `credentials: 'same-origin'` is never `omit` (bar 3).
  let res;
  try {
    res = await rawFetch(forkV1Url('user/token/refresh'), {
      method: 'POST',
      credentials: 'same-origin',
      headers: {Accept: 'application/json'},
    });
  } catch {
    // A transport failure is not proof the session is gone, but this page has
    // nothing to render without one, so it takes the same terminal path.
    markSessionLost();
    return null;
  }
  if (!res.ok) {
    markSessionLost();
    return null;
  }
  const body = await readJsonOrNull(res);
  const token = body && typeof body.token === 'string' ? body.token : null;
  if (token === null) {
    markSessionLost();
    return null;
  }
  setToken(token);
  return token;
}

/**
 * Refresh on load. Call this once from `app.js` before anything else — it is
 * what turns the HttpOnly cookie into the bearer every other call needs.
 * Returns true when a session was established.
 */
export async function initSession() {
  const token = await refreshSession();
  return token !== null;
}

function authInit(init) {
  const headers = {...(init.headers ?? {})};
  if (accessToken !== null) headers.Authorization = `Bearer ${accessToken}`;
  if (headers.Accept === undefined) headers.Accept = 'application/json';
  return {...init, headers, credentials: 'same-origin'};
}

/**
 * 401 -> refresh -> retry ONCE -> terminal.
 *
 * A second 401 carrying a token minted seconds earlier is not a token problem,
 * so retrying again would loop against a server that has already answered.
 *
 * The request is replayed with the same `init.body`. Every body this module
 * sends is a string or a FormData, both replayable; a stream body would be
 * consumed by the first attempt and is deliberately unsupported.
 */
async function authedFetch(url, init = {}) {
  if (sessionLost) throw new SessionLostError();

  let res = await rawFetch(url, authInit(init));
  if (res.status !== 401) return res;

  const token = await refreshSession();
  if (token === null) throw new SessionLostError();

  res = await rawFetch(url, authInit(init));
  if (res.status === 401) {
    markSessionLost();
    throw new SessionLostError();
  }
  return res;
}

/* ------------------------------------------------------------------ *
 * 5. JWT claims (ruling C1)
 * ------------------------------------------------------------------ */

/**
 * Mirrors entitlement.EditionPersonal (pkg/modules/brazn/entitlement/entitlement.go:48).
 *
 * Not imported from anywhere. frontend/src/composables/useManagedCapabilities.ts:26
 * is this page's sibling copy of the same constant and carries the same note
 * about the Go original; this file is a third copy for the same reason it is a
 * second one — the value travels as a plain string in the JWT and there is no
 * module boundary between server and page to import across.
 *
 * `teams-cloud` and `community` are deliberately NOT mirrored. The rule is
 * "personal-cloud, or permissive" (useManagedCapabilities.ts:62-66): a page
 * that whitelisted `teams-cloud` instead would render the restricted surface
 * for every future edition string, which is the failure that costs a customer
 * access rather than the one that costs a wasted click.
 */
export const PERSONAL_EDITION = 'personal-cloud';

const EDITION_CLAIM = 'brazn_edition';
const WRITE_RESTRICTED_CLAIM = 'brazn_write_restricted';

/**
 * Decode a JWT payload. The signature is NOT verified and must not be: this is
 * a hint for what to draw, never enforcement. The server's managed gate is the
 * real refusal (useManagedCapabilities.ts:49-57), and every control this
 * informs still issues its request and renders whatever comes back.
 *
 * Returns null for anything unparsable, which then reads as "claims absent" —
 * the permissive case for the edition, per ruling C1.1.
 */
export function parseJwt(token) {
  if (typeof token !== 'string') return null;
  const segment = token.split('.')[1];
  if (segment === undefined || segment === '') return null;
  try {
    const base64 = segment.replace(/-/g, '+').replace(/_/g, '/');
    // JWT segments are unpadded base64url. THIS PADDING IS NOT LOAD-BEARING and
    // the comment that used to claim it was ("atob rejects a bad length in some
    // engines") was wrong. WHATWG forgiving-base64 fails on exactly one
    // remainder, `length % 4 === 1`, which valid base64 can never produce; the
    // remainders an unpadded segment actually yields, 2 and 3, decode in every
    // conformant engine including Node's `atob`. Deleting this line changes no
    // result here, so no test can be written that it makes red — said plainly
    // because CLAUDE.md §4 makes an untraceable mutation sentence worse than
    // none. It stays as cheap normalisation, not as a guard.
    const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4);
    // Byte-for-byte the decode in frontend/src/stores/auth.ts:376-379. Both
    // claims read below are ASCII, so the absence of a UTF-8 step there is not
    // a divergence worth introducing here.
    const payload = JSON.parse(globalThis.atob(padded));
    return payload !== null && typeof payload === 'object' ? payload : null;
  } catch {
    return null;
  }
}

function sessionClaims() {
  return parseJwt(accessToken);
}

/**
 * The edition claim, or null when absent or empty.
 * Empty-string is folded to null to match frontend/src/stores/auth.ts:216-218.
 */
export function getEdition() {
  const claims = sessionClaims();
  if (claims === null) return null;
  const edition = claims[EDITION_CLAIM];
  return typeof edition === 'string' && edition !== '' ? edition : null;
}

/** True only for `personal-cloud`. Anything else, INCLUDING ABSENT, is false. */
export function isPersonalEdition() {
  return getEdition() === PERSONAL_EDITION;
}

/**
 * Whether an edition claim is present at all — the gate behind
 * `data-requires="edition"` (ruling C4). Distinct from `isPersonalEdition()`:
 * absence is permissive for capability purposes but still means there is no
 * edition to name on the edition line.
 */
export function hasEditionClaim() {
  return getEdition() !== null;
}

/**
 * `brazn_write_restricted === true` -> restricted; absent or false -> full.
 *
 * ABSENCE IS THE PERMITTING CASE here, the opposite of most claims and
 * deliberate: the claim is stamped only when true, so every token minted
 * before it existed would otherwise be read as write-blocked
 * (pkg/modules/auth/auth.go:186-196).
 */
export function isWriteRestricted() {
  const claims = sessionClaims();
  return claims !== null && claims[WRITE_RESTRICTED_CLAIM] === true;
}

/* ------------------------------------------------------------------ *
 * 6. The commercial guard (ruling C14, bar 8)
 * ------------------------------------------------------------------ */

/**
 * THE COMMERCIAL OUTCOME VOCABULARY IS PER-OPERATION. There is no single
 * affirmative value, and in particular there is no `'success'`: searching the
 * commercial service at the verified commit (`origin/master` @ `27c95232`) for
 * `outcome: "success"` returns nothing. An earlier `COMMERCIAL_OK = 'success'`
 * constant here was a guess, and it made EVERY commercial control render its
 * refusal path on a genuine success — invisibly, because CI never reaches `/v1`
 * at all (bar 9).
 *
 * Every value below is read from the service's own declared result type and
 * cited as `percy-service-27c95232.ts:<line>`. An uncited value is a guess and
 * bar 7 forbids guesses, so anything this file cannot cite is treated as a
 * refusal — the direction that costs a wasted click rather than a fake success.
 *
 * TWO FALSE FRIENDS, both rejected here on purpose:
 *
 *   * `outcome: "succeeded" | "failed"` at percy-service-27c95232.ts:778 is
 *     `interface PaymentReport` — what a payment provider said about one payment
 *     attempt. It is an internal type and never an HTTP response field.
 *   * The `outcome:` key inside every `log({...})` call in the service is an
 *     AUDIT-LOG word, not a response field. `"revoked"` (:4780), `"transferred"`
 *     (:4332), `"seat_withdrawn"` (:4031) and `"held_by_another"` (:4138) are all
 *     log words whose operations return result types carrying no `outcome` at
 *     all, and adopting them would have re-created the same defect one level
 *     down. Only a value that appears in a DECLARED RESULT INTERFACE, or in a
 *     literal the service assigns to one, is adopted below.
 *
 * The presence of the field is itself per-operation. Most of this surface —
 * every reader, and the whole subscription family — answers with data and no
 * `outcome` whatsoever, so a guard that demanded one would refuse all of them.
 * Each descriptor therefore declares which of the two shapes it expects, and a
 * body arriving in the other shape is a refusal either way.
 */

/** The body MUST carry `outcome`, and it must be one of `affirmative`. */
const OUTCOME_REQUIRED = 'required';

/**
 * The body carries NO `outcome` — the operation's declared result type has no
 * such field, so the route has nothing to project one from. An `outcome`
 * appearing anyway is a vocabulary this file has not read, and is refused.
 */
const OUTCOME_ABSENT = 'absent';

/**
 * @param {string} shape           OUTCOME_REQUIRED or OUTCOME_ABSENT.
 * @param {string[]} [affirmative] The cited affirmative values for this operation.
 * @param {object} [flags]
 * @param {boolean} [flags.noContent]    A documented success is 204 with no body.
 * @param {boolean} [flags.contractOnly] §16: no HTTP handler exists at the
 *   verified commit. It is declared HERE rather than left implicit because the
 *   two things that answer such a call today are indistinguishable from real
 *   refusals to a caller reading `status` alone: a bare 404 where the commercial
 *   service IS routed (percy-http-27c95232.ts:2905 for revoke, :3335 for the
 *   other three), and the SPA's index.html at 200 where it is not. A UI that
 *   renders `status` verbatim therefore tells an administrator "404" for a
 *   feature that has simply not shipped. This flag is what lets `app.js` word
 *   that case as "not available yet"; api.js itself renders nothing (see the
 *   module header) and refuses both shapes either way.
 */
function commercialOp(shape, affirmative = [], {noContent = false, contractOnly = false} = {}) {
  return Object.freeze({
    shape,
    affirmative: Object.freeze(affirmative),
    noContent,
    contractOnly,
  });
}

/**
 * One descriptor per commercial call this page can make. `readCommercialResult`
 * takes the descriptor, so the affirmative set can never drift away from the
 * operation it belongs to.
 */
export const COMMERCIAL_OPS = Object.freeze({
  /**
   * POST /v1/organizations/invitations.
   * Body: `{outcome, invited_user_id, invitation, seat_notice}`
   * (percy-http-27c95232.ts:2854-2884, projecting `MemberInvitation`).
   *
   * The union is DECLARED, all three values in one place:
   * percy-service-27c95232.ts:581 — `"invited" | "already_member" | "not_invitable"`.
   *
   *   * `invited` — affirmative. Constructed at :4665.
   *   * `already_member` — AFFIRMATIVE, and this is the judgement call the
   *     finding names. Constructed at :4596. The declaration's own prose at
   *     :575-577 says the invitee "holds a seat here already, so nothing was
   *     offered and nothing was sent — the coherent second result AC1 asks for
   *     rather than an error". The administrator's goal state (this person is in
   *     the organization) holds, so refusing it would show a red error for a
   *     roster that is already correct. It is NOT identical to `invited`, and
   *     THE CALLER MUST STILL BRANCH ON `body.outcome`, and this file cannot
   *     make it: `ok:true` here means "the administrator's goal state holds",
   *     NOT "an invitation was sent". Toasting "invitation sent" on `ok` alone
   *     reports a message that was never sent and offers a withdrawal for an
   *     invitation that does not exist — a fake success one level below the
   *     guard, which is the direction bar 8 exists to prevent. `invited` gets
   *     the sent wording and a pending row; `already_member` gets its own
   *     sentence and NO pending row, and arrives with `invitation: null`
   *     (percy-service-27c95232.ts:583) because nothing was recorded.
   *   * `not_invitable` — refusal. Constructed at :4556; :577-579 says the
   *     address belongs to a Personal customer, to another organization, or to
   *     an erased account. Nothing was sent and nothing will be.
   *
   * `not_administrator` never arrives here: it is a bare 403 with no body
   * (percy-http-27c95232.ts:2850-2853), caught by the `res.ok` check.
   */
  INVITE_MEMBER: commercialOp(OUTCOME_REQUIRED, ['invited', 'already_member']),

  /**
   * POST /v1/organizations/invitations/accept.
   * Body: `{outcome, organization_id}` (percy-http-27c95232.ts:2896-2899,
   * projecting `InvitationAcceptance`).
   *
   * `InvitationAcceptance.outcome` is typed `SeatAdmissionOutcome`
   * (percy-service-27c95232.ts:640), and that alias is declared in full at
   * percy-model-27c95232.ts:1500-1507:
   *
   *     "admitted" | "already_member" | "invitation_expired"
   *     | "invitation_revoked" | "no_invitation" | "not_invitable"
   *     | "at_seat_ceiling"
   *
   * The two affirmatives below are the whole affirmative half; the remaining
   * five are refusals, which is what this descriptor already assumed by failing
   * closed — so completing the union confirmed the set rather than changing it.
   * `at_seat_ceiling` is the product-ceiling refusal :5186 alludes to, renamed
   * deliberately (:1495-1498) because the old name read as "the seats ran out"
   * for a condition ninety-seven seats away from most organizations.
   * What the service commits to in writing:
   *
   *   * `admitted` — affirmative. `acceptInvitation` branches on
   *     `admission.outcome === "admitted"` at :4716 to open the seat proration
   *     and deliver the entitlement; that is the seated case by construction.
   *   * `already_member` — affirmative. :5185 treats it alongside `admitted` as
   *     a non-refusal, and :5178-5180 states why: "they hold a seat here
   *     already, which is the outcome the approval was asking for."
   *   * `no_invitation` — refusal, constructed at :4690.
   *
   * Every other member of the union is a refusal, and each still fails closed
   * on its own account rather than because the set is complete — a value added
   * to `SeatAdmissionOutcome` later refuses here until somebody classifies it.
   */
  ACCEPT_INVITATION: commercialOp(OUTCOME_REQUIRED, ['admitted', 'already_member']),

  /**
   * POST /v1/organizations/members/removal.
   * Body: `{outcome, organization_id, member_user_id}`
   * (percy-http-27c95232.ts:2947-2951, projecting `MemberRemovalResult`).
   *
   * `MemberRemovalResult.outcome` is typed `MemberRemovalOutcome`
   * (percy-service-27c95232.ts:713), declared in full at
   * percy-model-27c95232.ts:1541 as
   * `"removed" | "not_a_member" | "still_administrator"`. Only `removed` is
   * affirmative; `still_administrator` is called "a REFUSAL and the one
   * judgement call in this ticket" at :1533, because removing the sole
   * administrator would leave the organization with no route back.
   * The one value the service names is `"removed"`: it branches
   * `removal.outcome !== "removed"` at :6014 to return early without cutting
   * access, and :6066 says the answer to a refused projection "is to call again
   * — which `removeMember` answers `removed` for".
   *
   * `"seat_withdrawn"` (:4031) and `"held_by_another"` (:4138) are NOT members
   * of this union. Both are log words inside `registerOrganization`, an
   * unrelated operation; the sample table in FINDING-OUTCOME.md lists them under
   * this operation and is wrong, which is exactly why that finding said not to
   * extrapolate from it.
   *
   * Everything other than `removed` fails closed.
   */
  REMOVE_ORGANIZATION_MEMBER: commercialOp(OUTCOME_REQUIRED, ['removed']),

  /**
   * GET /v1/team-access-requests.
   * Body: `{requests: [{request_id, requester_email, message, team_id,
   * requested_at, verified_at}]}` (percy-http-27c95232.ts:3170-3197).
   * A list projection with no `outcome` field. `not_administrator` is a bare 403
   * (:3166-3169), so `res.ok` carries that refusal.
   */
  LIST_TEAM_ACCESS_REQUESTS: commercialOp(OUTCOME_ABSENT),

  /**
   * POST /v1/team-access-requests/decide.
   * Body: `{outcome, invitation_outcome}` (percy-http-27c95232.ts:3261-3264).
   *
   * The union is DECLARED at percy-service-27c95232.ts:690-695 —
   * `"approved" | "declined" | "not_administrator" | "unknown_request" | "not_admitted"`.
   *
   *   * `approved` — affirmative (:5217, :5222).
   *   * `declined` — AFFIRMATIVE, and the second judgement call. Constructed at
   *     :5121/:5130. A decline is the administrator's decision carried out, not
   *     a refusal of their request: the button they pressed said Decline and the
   *     request is now resolved `declined` in the store. Rendering it red would
   *     tell an administrator their own decision failed.
   *   * `not_admitted` — refusal (:5171, :5190). The approval did not seat
   *     anybody; :682-687 says the request is deliberately LEFT OPEN so the same
   *     approval can be made again. `invitation_outcome` carries why, and the
   *     caller should render it.
   *   * `not_administrator` → bare 403 and `unknown_request` → bare 404
   *     (percy-http-27c95232.ts:3243-3250), so neither reaches this check.
   */
  DECIDE_TEAM_ACCESS_REQUEST: commercialOp(OUTCOME_REQUIRED, ['approved', 'declined']),

  /**
   * POST /v1/team-access-requests/confirm — 204 with no body at all
   * (percy-http-27c95232.ts:3306-3308); an unusable handle is a bare 404.
   *
   * THIS ROUTE IS NOT REACHABLE FROM THIS PAGE. Its block checks
   * `sameSecret(offered, serviceToken)` (percy-http-27c95232.ts:3278) — the
   * percy.works relay's shared service credential — so a user bearer is 401
   * unconditionally. The descriptor is honest about the wire shape rather than
   * pretending the call can work.
   *
   * THE EXPORT IS KEPT, AND THAT IS NOW A DECISION RATHER THAN AN OPEN
   * RECOMMENDATION. An earlier note here deferred to "the report"; the report
   * was written, the recommendation was not taken, and a comment pointing at an
   * answer nobody acted on reads as an unfinished thought. The reason to keep it
   * is that `docs/one-tasks-restricted-views.md` inventories all eighteen `/v1`
   * routes and this descriptor is what keeps the inventory's claim about this
   * one checkable against the source. Nothing on the page calls it, and nothing
   * should.
   */
  CONFIRM_TEAM_ACCESS_REQUEST: commercialOp(OUTCOME_ABSENT, [], {noContent: true}),

  /**
   * POST /v1/subscription/cancellation.
   * Body: `{user_id, cancelled_at, access_ends_at}` — all three fields of
   * `CancellationResult`, named one by one (percy-http-27c95232.ts:2474-2478).
   * `CancellationResult` is declared at percy-service-27c95232.ts:740-746 and
   * has NO `outcome` field. Every refusal on this route is a bare status
   * (404/409/403 at :2405-2455), so `res.ok` is the whole guard.
   */
  CANCEL_SUBSCRIPTION: commercialOp(OUTCOME_ABSENT),

  /**
   * POST /v1/subscription/auto-renewal.
   * Body: `{auto_renewal: true}` (percy-http-27c95232.ts:2375). The service
   * method answers a bare subscription handle — `startAutoRenewal(userId):
   * Promise<string>` at percy-service-27c95232.ts:1681 — which the route
   * deliberately drops (:2368-2372). No `outcome` exists anywhere on this path.
   * The five refusals are bare 403/402/409/503 (:2299-2367).
   */
  SET_SUBSCRIPTION_AUTO_RENEWAL: commercialOp(OUTCOME_ABSENT),

  /**
   * POST /v1/subscription/renewal-consent.
   * Body: `{renewal_consent_at}` — ONE field (percy-http-27c95232.ts:2270).
   * `recordRenewalConsent(userId): Promise<AccountRecord>`
   * (percy-service-27c95232.ts:1667) — an account row, which carries no
   * `outcome`. The one refusal is a bare 404 (:2259-2262).
   */
  GIVE_RENEWAL_CONSENT: commercialOp(OUTCOME_ABSENT),

  /**
   * POST /v1/checkout/resume.
   * Body: `{user_id, payment}` (percy-http-27c95232.ts:2211 and :2229). No
   * `outcome`; `no_open_charge` is a log word beside a bare 409 (:2196, :2224).
   *
   * THIS ROUTE IS NOT REACHABLE FROM THIS PAGE either: it checks
   * `sameSecret(offered, serviceToken)` at percy-http-27c95232.ts:2172, so a
   * user bearer is 401 unconditionally. Kept for the same settled reason as
   * `CONFIRM_TEAM_ACCESS_REQUEST` above, and called by nothing.
   */
  RESUME_CHECKOUT: commercialOp(OUTCOME_ABSENT),

  /**
   * GET /v1/entitlements.
   * Body: the `Entitlements` projection, written straight out
   * (percy-http-27c95232.ts:2803). `projectEntitlements` composes it at
   * percy-service-27c95232.ts:1992-2015 — `edition`, `locale`, `seats`,
   * `limits`, `footer`, `referral` — and there is no `outcome` among them. The
   * `outcome: "ok"` at :7019 is the log line beside it.
   */
  GET_ENTITLEMENTS: commercialOp(OUTCOME_ABSENT),

  /**
   * GET /v1/account/successor-candidates.
   * Body: `{candidates: [{user_id}]}` (percy-http-27c95232.ts:2986-2988), from
   * `listSuccessorCandidates`, which answers an account array. No `outcome`, and
   * an empty list is an ordinary 200 rather than a refusal
   * (percy-http-27c95232.ts:2976-2984) — so `ok:true` with zero candidates is a
   * real answer the caller must handle as "no choice has to be offered".
   */
  LIST_SUCCESSOR_CANDIDATES: commercialOp(OUTCOME_ABSENT),

  /**
   * POST /v1/account/erasure — **204 NO CONTENT, and no body at all**
   * (percy-http-27c95232.ts:3071-3076: "the absence of a body is deliberate").
   * `eraseAccount` answers the redacted account record
   * (percy-service-27c95232.ts:1726) and the route deliberately projects none of
   * it. The three caller-fixable refusals are bare 404/409 (:3025-3067).
   *
   * `noContent` is what stops a SUCCESSFUL erasure being reported as a failure:
   * a 204 carries no `Content-Type`, so the JSON check below would otherwise
   * refuse it as `not-json` — the CI-shape reason — on the one operation that
   * cannot be retried to find out.
   */
  ERASE_ACCOUNT: commercialOp(OUTCOME_ABSENT, [], {noContent: true}),

  /* --- contract only: no HTTP handler exists yet ------------------- *
   *
   * These four have no route at the verified commit, so their RESPONSE BODIES
   * are not yet decided by anything. What can be read is the service method each
   * one wraps, and that is what these descriptors are built from — the shape a
   * handler would have to project.
   *
   * Until a handler lands, what actually answers depends on whether the
   * commercial service is routed at this origin, and the guard refuses either
   * way: routed, all four are a bare 404 (revoke via the invitations prefix
   * block at percy-http-27c95232.ts:2819 falling to :2905, the other three via
   * the listener's final :3335) and `res.ok` carries it; unrouted — CI, bar 9 —
   * the fork's static handler answers the SPA's index.html at 200 and the
   * content-type check carries it. §16 sets this out in full.
   */

  /**
   * POST /v1/organizations/invitations/revoke.
   * `revokeMemberInvitation` (percy-service-27c95232.ts:4762-4785) answers
   * `Promise<OrganizationInvitationRecord | null>` — an invitation row, with NO
   * `outcome` field. Null means "you do not administer this organization", which
   * the sibling routes answer as a bare 403.
   *
   * **The `"revoked"` at :4780 is a LOG word, not a result field**, and
   * FINDING-OUTCOME.md's sample table lists it as revoke's outcome value. It is
   * not adopted: there is no declared union to put it in. If the handler that
   * eventually lands does project an `outcome`, this descriptor refuses it and
   * the value must be read from that handler and added here. That is the
   * fail-closed residue, and it is reported rather than papered over.
   */
  REVOKE_INVITATION: commercialOp(OUTCOME_ABSENT, [], {contractOnly: true}),

  /**
   * GET /v1/organizations/seats/quote.
   * `quoteSeatIncrease` answers `SeatIncreaseQuote | null`
   * (percy-service-27c95232.ts:5350-5378), declared at :895-912 as
   * `{organization_id, seats, seats_after, proration}`. No `outcome`, and
   * `proration: null` is explicitly "a perfectly ordinary answer and never an
   * error" (:906). Null from the method is the non-administrator case.
   */
  QUOTE_SEATS: commercialOp(OUTCOME_ABSENT, [], {contractOnly: true}),

  /**
   * POST /v1/organizations/seats.
   * `changeOrganizationSeats` answers `SeatPurchaseResult | null`
   * (percy-service-27c95232.ts:5455-5501), declared at :865-885 with
   * `outcome: SeatPurchaseOutcome` (:867). The alias is declared in full at
   * percy-model-27c95232.ts:1153:
   *
   *     "changed" | "unchanged" | "below_users" | "below_active_teams"
   *
   *   * `changed` — affirmative. The service branches
   *     `change.outcome === "changed"` at :5490 to open the pro-rated charge,
   *     which is the seat actually moving.
   *   * `unchanged` — AFFIRMATIVE, and the model says so in as many words at
   *     percy-model-27c95232.ts:1149-1151: "a request that named the quantity
   *     already held. Not a refusal and not a no-op to hide". Nothing is
   *     written, so `updated_at` does not move. Treating it as a refusal — which
   *     this descriptor did while `model.ts` was unextracted — shows an
   *     administrator a failure for asking for what they already have.
   *   * `below_users` and `below_active_teams` — refusals (:1130-1147). Both
   *     decline a decrease into territory no source has a rule for, and both
   *     report the numbers they refused against.
   *
   * Out-of-range quantities raise rather than answering an outcome (:5467-5472),
   * so they arrive as a non-2xx.
   */
  PURCHASE_SEATS: commercialOp(OUTCOME_REQUIRED, ['changed', 'unchanged'], {contractOnly: true}),

  /**
   * POST /v1/organizations/admin-transfer.
   * `transferOrganizationAdministration` answers `AdminTransferResult | null`
   * (percy-service-27c95232.ts:4299-4390). `AdminTransferResult` is declared at
   * :541-547 as `{organization_id, from_user_id, to_user_id}` — NO `outcome`.
   * `"transferred"` (:4332) and `"not_applicable"` (:4323) are the log words on
   * either side of it, and `not_applicable` corresponds to the null result a
   * handler would answer as a bare status.
   */
  TRANSFER_ADMINISTRATOR: commercialOp(OUTCOME_ABSENT, [], {contractOnly: true}),

  /**
   * The default when a caller names no operation: require an `outcome` and
   * recognise nothing, so an unnamed commercial call can only ever refuse. A new
   * commercial call that forgets its descriptor fails visibly and closed rather
   * than inheriting somebody else's vocabulary.
   */
  UNKNOWN: commercialOp(OUTCOME_REQUIRED),
});

/** Machine reason codes. `app.js` maps these to `t()` keys. */
export const COMMERCIAL_REFUSAL = Object.freeze({
  HTTP: 'http',
  NOT_JSON: 'not-json',
  UNPARSABLE: 'unparsable',
  OUTCOME: 'outcome',
  /**
   * The request never produced a Response at all — `fetch` itself rejected
   * (connection refused, DNS, TLS, an aborted navigation). Distinct from
   * `NOT_JSON`, which is a real 200 carrying the SPA's shell: this one means
   * nothing answered.
   *
   * It exists so a transport failure stays INSIDE the refusal path. Before it,
   * the rejection propagated out of `commercialRequest` as an exception, so
   * `describeCommercialRefusal` was never reached — and on the two controls
   * that read before they draw (`listSuccessorCandidates()` ahead of the
   * transfer and the delete-account modals) the modal never opened at all, so
   * pressing Continue produced nothing visible.
   */
  NETWORK: 'network',
});

/**
 * The ONE guard every `/v1` call goes through. Takes the Response and the
 * operation's descriptor, and requires ALL THREE of (ruling C14):
 *
 *   1. res.ok;
 *   2. a JSON content type AND a successful parse;
 *   3. an `outcome` of the shape THIS operation declares — an affirmative value
 *      from its own cited set, or no `outcome` at all where its result type has
 *      none.
 *
 * The content-type check is load-bearing, not defensive tidiness: the fork's
 * static handler answers an UNROUTED `/v1/...` with the SPA's index.html at
 * HTTP 200. That is exactly the shape CI produces, because CI starts no
 * commercial service — so a guard that skipped this check would report every
 * commercial control as working in precisely the environment that cannot run
 * one (bar 9). It is UNCHANGED, and the 204 branch above it cannot weaken it:
 * the SPA shell is served at 200, never at 204, and only the two operations
 * whose success is documented as bodiless opt into that branch.
 *
 * Fails closed on everything else, including an `outcome` value we do not
 * recognise and an `outcome` appearing on an operation whose result type has
 * none. Returns a plain object rather than throwing, because a refusal is an
 * ordinary rendered outcome for these controls, not an exception.
 *
 * WHAT `outcome` IS FOR, AND WHY IT IS ON THE RESULT RATHER THAN LEFT IN THE BODY.
 * `reason` says which of the five REFUSAL MECHANISMS fired; it cannot say which
 * refusal the SERVICE named, because every `outcome`-shaped refusal shares the
 * single code `COMMERCIAL_REFUSAL.OUTCOME`. So a caller reading `reason` alone
 * can only ever render one sentence for every declined invitation, and the
 * service went to the trouble of distinguishing three actionable causes —
 * `not_invitable` is a Personal customer, somebody in another organization, or
 * an erased account (percy-service-27c95232.ts:577-579). Lifting the value out
 * makes "WHICH refusal happened" a first-class fact instead of one buried at
 * `result.body.outcome`, which is also the path a bare-status refusal has no
 * body for.
 *
 * It is populated on BOTH halves and on every branch:
 *
 *   * affirmative — `invited` vs `already_member` are two different sentences
 *     and two different roster effects (see `COMMERCIAL_OPS.INVITE_MEMBER`), so
 *     a caller must branch on this even when `ok` is true;
 *   * refusal — `not_invitable`, `no_invitation`, `below_users`, … name what to
 *     say;
 *   * null — the body carried no `outcome` string at all. That is the ORDINARY
 *     case for every `OUTCOME_ABSENT` operation and for every bare-status
 *     refusal, which is most of this surface, so null must read as "the service
 *     named nothing" and never as "the service said no".
 *
 * @param {Response} res
 * @param {{shape: string, affirmative: readonly string[], noContent: boolean, contractOnly: boolean}} [op]
 *   One of `COMMERCIAL_OPS`. Omitted means `COMMERCIAL_OPS.UNKNOWN`, which
 *   recognises nothing.
 * @returns {{ok: boolean, status: number, body: unknown, message: string|null,
 *   outcome: string|null, reason: string|null}}
 */
export async function readCommercialResult(res, op = COMMERCIAL_OPS.UNKNOWN) {
  const base = {status: res.status, body: null, message: null, outcome: null, reason: null};

  if (!res.ok) {
    const body = await readJsonOrNull(res);
    return {
      ...base,
      ok: false,
      body,
      message: readServerMessage(body),
      outcome: readOutcome(body),
      reason: COMMERCIAL_REFUSAL.HTTP,
    };
  }

  // A documented bodiless success, and only for an operation that declares one.
  // `POST /v1/account/erasure` answers 204 with nothing at all
  // (percy-http-27c95232.ts:3076); read through the JSON check below it would
  // refuse a completed account erasure as `not-json` — the CI-absence reason —
  // on the one call a user cannot retry to find out what really happened.
  if (res.status === 204 && op.noContent) {
    return {status: 204, ok: true, body: null, message: null, outcome: null, reason: null};
  }

  const contentType = res.headers.get('content-type') ?? '';
  if (!/^application\/(?:[\w.+-]+\+)?json\b/i.test(contentType.trim())) {
    return {...base, ok: false, reason: COMMERCIAL_REFUSAL.NOT_JSON};
  }

  let body;
  try {
    body = await res.json();
  } catch {
    return {...base, ok: false, reason: COMMERCIAL_REFUSAL.UNPARSABLE};
  }

  if (body === null || typeof body !== 'object') {
    return {...base, ok: false, body, reason: COMMERCIAL_REFUSAL.UNPARSABLE};
  }

  const message = readServerMessage(body);
  const outcome = readOutcome(body);
  // The commercial service is the authority on what went wrong; every refusal
  // below surfaces its own sentence when it sent one (ruling C4) rather than a
  // page-authored paraphrase — and now its own outcome word when it sent one of
  // those instead, which on this surface is the commoner of the two.
  const refused = {...base, ok: false, body, message, outcome, reason: COMMERCIAL_REFUSAL.OUTCOME};

  if (op.shape === OUTCOME_ABSENT) {
    // This operation's result type carries no `outcome`. One arriving is a
    // vocabulary nothing in this file has read, so it is refused rather than
    // ignored: a route that grew an outcome grew it to report something.
    return body.outcome === undefined
      ? {status: res.status, ok: true, body, message, outcome: null, reason: null}
      : refused;
  }

  if (typeof body.outcome !== 'string' || !op.affirmative.includes(body.outcome)) {
    return refused;
  }

  return {status: res.status, ok: true, body, message, outcome, reason: null};
}

/**
 * `body.outcome` when it is a non-empty string, else null.
 *
 * Deliberately does NOT distinguish an absent key from a non-string one: both
 * mean "no outcome was named", and the guard above has already refused the
 * non-string case on its own account.
 */
function readOutcome(body) {
  if (body === null || typeof body !== 'object') return null;
  return typeof body.outcome === 'string' && body.outcome !== '' ? body.outcome : null;
}

/* ------------------------------------------------------------------ *
 * 6b. The declared payloads (bar 8's other half)
 * ------------------------------------------------------------------ *
 *
 * `readCommercialResult` answers WHETHER the operation was affirmative and, now,
 * WHICH word the service used. Four routes also compose a PAYLOAD they exist to
 * deliver — the invite, the seat quote, the successor list and the team-access
 * decision — and reading one correctly is a separate problem from reading the
 * verdict: the fields are nested, several of them are legitimately null, and the
 * null in each case means something specific that is not "an error".
 *
 * These readers exist so the meaning lives here beside the citation instead of
 * being re-derived at each call site. Two things they deliberately do NOT do:
 *
 *   * THEY NAME NO FIELD THIS REPOSITORY CANNOT READ (bar 7). `proration` is
 *     typed `SeatProration`, which percy-service-27c95232.ts:115-125 imports
 *     from `./billing.ts` — a file that is NOT among the three extracted at
 *     27c95232. Its members are therefore unknown, and a reader that reached
 *     inside it would be guessing at the one shape on this surface that carries
 *     money. It is passed through whole, and what IS readable about it — that
 *     null means "this costs nothing now" — is stated below.
 *   * They do not re-report the outcome. That is `result.outcome`, one value in
 *     one place, so a caller cannot end up branching on two copies that a later
 *     edit lets drift apart.
 */

/** A declared numeric field, or null when it did not arrive as a number. */
function numberOrNull(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

/**
 * A declared string field, or null. Ids, statuses and ISO timestamps all arrive
 * as strings here — every id on this service is one (`isId`,
 * percy-http-27c95232.ts:1448) — so a non-string in any of these slots is a
 * shape this file has not read and is dropped rather than rendered.
 */
function stringOrNull(value) {
  return typeof value === 'string' && value !== '' ? value : null;
}

function objectOrNull(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value : null;
}

/**
 * The seat position an administrator is committing to, off an INVITE result.
 *
 * `SeatNotice` is declared at percy-service-27c95232.ts:619-636 and projected
 * verbatim by the handler (percy-http-27c95232.ts:2871-2883, which calls it
 * "an administrator being told what they are about to commit their organization
 * to, which is the whole of BRA-1075"). This is a LIVE route, so this payload
 * arrives today.
 *
 *   * `seats` — seats purchased today (:620-621).
 *   * `users` — people holding one today (:622-623).
 *   * `seats_after` — the purchase if this invitation is accepted and nothing
 *     else changes. **EQUAL TO `seats` WHEN THERE IS ALREADY ROOM** (:625-629),
 *     and that equality is the whole test for "this costs nothing" versus "this
 *     buys a seat". Comparing the two is the correct render; subtracting them
 *     and showing a delta of 0 says the same thing worse.
 *   * `proration` — what that would cost for the remainder of the period, or
 *     null when it costs nothing, because there is room already or no period has
 *     been invoiced yet (:630-635). Opaque here, see the section header.
 *
 * The whole notice is null when the service could not read a seat utilisation
 * (percy-service-27c95232.ts:4441-4443), and on both non-`invited` outcomes
 * (:4563, :4601) — nothing was offered, so there is nothing to commit to.
 *
 * ADVISORY, AND THE SERVICE SAYS SO IN AS MANY WORDS (:606-617): it is read at
 * invitation time and the seat is taken at acceptance time, so two invitations
 * outstanding at once will each say they add one. Render it as what this
 * invitation would do, never as a running total.
 *
 * @returns {{seats: number|null, users: number|null, seats_after: number|null,
 *   proration: object|null}|null}
 */
export function readSeatNotice(result) {
  const notice = objectOrNull(result?.body?.seat_notice);
  if (notice === null) return null;
  return {
    seats: numberOrNull(notice.seats),
    users: numberOrNull(notice.users),
    seats_after: numberOrNull(notice.seats_after),
    proration: objectOrNull(notice.proration),
  };
}

/**
 * The invitation row an invite result recorded, or null when it recorded none.
 *
 * THE HANDLE IS NESTED AND THERE IS NO TOP-LEVEL COPY. The handler projects
 * three fields of the record and not the record (percy-http-27c95232.ts:2863-2870):
 * `{invitation_id, status, expires_at}`. A caller reading `body.invitation_id`
 * or `body.id` gets undefined and can never offer a withdrawal.
 *
 * NULL IS THE HONEST "NOTHING TO WITHDRAW" CASE, not a failure: it is the shape
 * `already_member` arrives in, because nothing was recorded
 * (percy-service-27c95232.ts:583, :4601). `already_member` is affirmative — see
 * `COMMERCIAL_OPS.INVITE_MEMBER` — so `result.ok` is true and this is still
 * null, and those two facts together are exactly "the roster is already right".
 *
 * `status` is `InvitationStatus`, declared in full at percy-model-27c95232.ts:1302
 * as `"pending" | "accepted" | "revoked"` — THREE values and no `expired`,
 * because an expired invitation is a `pending` row whose `expires_at` has passed
 * (:1295-1301). A caller wanting to show "expired" must compare `expires_at`
 * against now; there is no status to match on, and there deliberately never
 * will be.
 *
 * @returns {{invitation_id: string|null, status: string|null,
 *   expires_at: string|null}|null}
 */
export function readInvitationRecord(result) {
  const invitation = objectOrNull(result?.body?.invitation);
  if (invitation === null) return null;
  return {
    invitation_id: stringOrNull(invitation.invitation_id),
    status: stringOrNull(invitation.status),
    expires_at: stringOrNull(invitation.expires_at),
  };
}

/** The invitee's opaque commercial id off an invite result (percy-http:2856-2858). */
export function readInvitedUserId(result) {
  return stringOrNull(result?.body?.invited_user_id);
}

/**
 * The seat quote, off `GET /v1/organizations/seats/quote`. CONTRACT ONLY.
 *
 * `SeatIncreaseQuote` is declared at percy-service-27c95232.ts:895-912 as
 * `{organization_id, seats, seats_after, proration}` and its doc at :887-893
 * says it "exists so an administrator can be shown the figure at the moment they
 * are deciding". **THERE IS NO `message` FIELD ON IT.** Reading one yields
 * undefined on every real reply, so a surface that preferred `body.message` and
 * fell back to its own sentence would show the fallback always — and would then
 * put a confirm button under it that commits a pro-rated charge with no figure
 * anywhere on screen.
 *
 * **`proration: null` IS A PERFECTLY ORDINARY ANSWER AND NEVER AN ERROR**
 * (:901-909, in those words). It means there is nothing to charge: no period has
 * been invoiced yet — an organization inside its trial — the current period has
 * already run out, or the change adds no billed seat. The correct rendering is
 * "this costs nothing now", NOT a missing-data state and NOT a blocked button.
 *
 * Returns null when the result was not affirmative, so "no quote" and "a quote
 * of nothing" stay two different values rather than collapsing into one.
 *
 * @returns {{organization_id: string|null, seats: number|null,
 *   seats_after: number|null, proration: object|null}|null}
 */
export function readSeatQuote(result) {
  if (result?.ok !== true) return null;
  const body = objectOrNull(result.body);
  if (body === null) return null;
  return {
    organization_id: stringOrNull(body.organization_id),
    seats: numberOrNull(body.seats),
    seats_after: numberOrNull(body.seats_after),
    proration: objectOrNull(body.proration),
  };
}

/**
 * The successor candidates, off `GET /v1/account/successor-candidates`.
 *
 * **`null` AND `[]` ARE TWO DIFFERENT ANSWERS AND THIS IS THE WHOLE REASON THIS
 * READER EXISTS.** `null` is "the service did not answer" — a refusal, a
 * transport failure, the SPA shell in CI. `[]` is an ORDINARY 200
 * (percy-http-27c95232.ts:2976-2984): the service returns an empty list for a
 * sole-member administrator, for a non-administrator and for an account it does
 * not hold alike, "because the question is 'must I offer a choice' and the true
 * answer to all three is no".
 *
 * A caller that collapses them disables its own control for the sole-member
 * administrator — who may still erase, since `POST /v1/account/erasure` skips
 * the successor block entirely when the list is empty (:3033-3039). An empty
 * list means CALL `eraseAccount()` WITH NO BODY, not "you cannot proceed".
 *
 * **THE IDS ARE OPAQUE AND THIS PAGE CANNOT RESOLVE THEM TO NAMES. Read this
 * before writing a picker.** The projection is
 * `candidates.map((account) => ({user_id: account.user_id}))` (:2986-2988) and
 * :2968-2974 explains that `AccountRecord` holds no name and no mailbox.
 *
 * An earlier version of this comment told callers to "join each `user_id`
 * against the already-loaded fork roster (`organization.members[]`)". **THAT WAS
 * WRONG AND IT IS CORRECTED HERE**, because a caller took it at its word and
 * shipped a join that misses on every row:
 *
 *   * `user_id` here is the COMMERCIAL service's own account id, an opaque
 *     string. `entitlement.go:193-195` says the Subject holds ids "in the
 *     commercial service's own identifiers rather than this instance's row ids".
 *   * `OrganizationMember.UserID` is `int64` and is assigned `u.ID`
 *     (pkg/models/brazn_organization.go:69, :478) — the FORK's local row id.
 *   * Nothing carries the other one across. The commercial id lives on
 *     `Subject.UserID` inside the signed entitlement envelope, and
 *     `EntitlementProjection.Envelope` is `json:"-"`
 *     (pkg/models/brazn_managed_mode.go:138), so no fork response a browser can
 *     read contains it. The two session claims are `brazn_edition` and
 *     `brazn_write_restricted` and neither is an id (auth.go:183, :197).
 *
 * Stringifying both sides does not fix this — the types were never the problem,
 * the namespaces are. Worse, `opaqueID` admits a bare numeric string
 * (`^[A-Za-z0-9_-]{1,64}$`, entitlement.go:191), so a coincidental collision
 * would label the WRONG PERSON on an irreversible handover.
 *
 * So: until the fork surfaces the commercial id on `OrganizationMember` — a fork
 * change, outside bar 1, to be REPORTED and not built here — a picker must show
 * these ids as the opaque handles they are and say so, rather than claiming a
 * resolution it cannot perform.
 *
 * @returns {string[]|null}
 */
export function readSuccessorCandidates(result) {
  if (result?.ok !== true) return null;
  const candidates = result.body?.candidates;
  if (!Array.isArray(candidates)) return null;
  return candidates
    .map((candidate) => stringOrNull(candidate?.user_id))
    .filter((userId) => userId !== null);
}

/**
 * `invitation_outcome` off `POST /v1/team-access-requests/decide`.
 *
 * The body is `{outcome, invitation_outcome}` (percy-http-27c95232.ts:3261-3264).
 * When `outcome` is `not_admitted` — a refusal — the approval seated nobody and
 * the request is deliberately LEFT OPEN so the same approval can be made again
 * (percy-service-27c95232.ts:682-687); `invitation_outcome` is the field
 * carrying WHY, and it is a `SeatAdmissionOutcome`, declared in full at
 * percy-model-27c95232.ts:1500-1507.
 *
 * Read alongside `result.outcome`, never instead of it: on `approved` and
 * `declined` this says how the seat moved, and on `not_admitted` it is the only
 * thing that can word the refusal.
 *
 * @returns {string|null}
 */
export function readInvitationOutcome(result) {
  return stringOrNull(result?.body?.invitation_outcome);
}

/**
 * The commercial seam's own fetch. DELIBERATELY NOT `authedFetch`.
 *
 * A `/v1` 401 IS NOT EVIDENCE THE FORK SESSION EXPIRED. The commercial service
 * authenticates on its own authority and answers a bare 401 on every route when
 * `authenticator.authenticate(offered)` returns null (percy-http-27c95232.ts:2826-2828,
 * :2918-2920, :2999-3002, :3219-3222) — which happens for an account the
 * commercial service simply does not hold. Two of its routes go further and 401
 * a user bearer UNCONDITIONALLY, because they demand the percy.works relay's
 * shared service credential rather than a user token (:2172 checkout/resume,
 * :3278 team-access-requests/confirm).
 *
 * Routed through `authedFetch`, every one of those became `markSessionLost()`
 * plus a thrown `SessionLostError` on the second attempt: an administrator with
 * a perfectly good fork session was logged out by the first Organization action
 * they took, and — because `dispatch` swallows `SessionLostError` by design —
 * with no message at all. A 401 here is an ordinary `COMMERCIAL_REFUSAL.HTTP`
 * carrying status 401, and the caller renders it like any other refusal.
 *
 * ONE refresh and ONE replay are still attempted, and only that. The access TTL
 * is 600 s and the commercial service validates the same bearer, so an aged-out
 * token is a real and cheap-to-fix cause of a first 401. What is removed is the
 * TERMINAL step: whatever the replay answers, including a second 401, is
 * returned rather than made final.
 *
 * The one case that still throws `SessionLostError` is a refresh that itself
 * fails — `performRefresh` has then been refused by the FORK's own
 * `/api/v1/user/token/refresh`, which is a genuine session loss no matter which
 * call discovered it.
 */
async function commercialFetch(url, init) {
  if (sessionLost) throw new SessionLostError();

  const res = await rawFetch(url, authInit(init));
  if (res.status !== 401) return res;

  const token = await refreshSession();
  if (token === null) throw new SessionLostError();

  return rawFetch(url, authInit(init));
}

// Takes an absolute URL, not a path: building it once at the call site keeps
// `commercialV1Url` applied exactly once, so a query-bearing GET cannot end up
// re-prefixed into /v1/v1/....
//
// The try/catch is what makes `/v1` calls resolve to a refusal rather than
// reject. `rawFetch` rejects on a transport failure, and a rejection here
// escapes past `readCommercialResult` into a caller that has no `try` — see
// COMMERCIAL_REFUSAL.NETWORK. A `SessionLostError` is re-thrown unchanged
// because `app.js` owns the terminal surface for it, and an ApiAssertionError
// is a programming error in this page that must never be rendered as a service
// refusal.
async function commercialRequest(url, init, op) {
  let res;
  try {
    res = await commercialFetch(url, init);
  } catch (err) {
    if (err instanceof SessionLostError) throw err;
    if (err !== null && typeof err === 'object' && err.name === 'ApiAssertionError') throw err;
    return {
      status: 0, ok: false, body: null, message: null, outcome: null,
      reason: COMMERCIAL_REFUSAL.NETWORK,
    };
  }
  return readCommercialResult(res, op);
}

// `op` is required, not defaulted, at these two seams: a new commercial call
// that forgets its descriptor should be a visibly wrong call rather than one
// that silently inherits a vocabulary belonging to another operation.
function commercialGet(path, op, query) {
  return commercialRequest(withQuery(commercialV1Url(path), query), {method: 'GET'}, op);
}

function commercialPost(path, op, payload) {
  return commercialRequest(commercialV1Url(path), {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(payload ?? {}),
  }, op);
}

/* ------------------------------------------------------------------ *
 * 7. Fork request helpers
 * ------------------------------------------------------------------ */

async function readJsonOrNull(res) {
  if (res.status === 204) return null;
  const contentType = res.headers.get('content-type') ?? '';
  if (!/json/i.test(contentType)) return null;
  try {
    return await res.json();
  } catch {
    return null;
  }
}

/** Resolve to the parsed body, or throw a ForkError carrying the server's own. */
async function expectOk(res, url) {
  if (res.ok) return readJsonOrNull(res);
  throw new ForkError(res.status, await readJsonOrNull(res), url);
}

async function forkGet(url) {
  return expectOk(await authedFetch(url, {method: 'GET'}), url);
}

async function forkSend(method, url, payload) {
  const init = {method};
  if (payload !== undefined) {
    init.headers = {'Content-Type': 'application/json'};
    init.body = JSON.stringify(payload);
  }
  return expectOk(await authedFetch(url, init), url);
}

async function forkUpload(method, url, formData) {
  // No Content-Type: the multipart boundary is generated by the runtime and
  // setting the header by hand strips it, which the server reads as a body
  // with no parts at all.
  return expectOk(await authedFetch(url, {method, body: formData}), url);
}

async function forkBlob(method, url, payload) {
  const init = {method, headers: {Accept: '*/*'}};
  if (payload !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(payload);
  }
  const res = await authedFetch(url, init);
  if (!res.ok) throw new ForkError(res.status, await readJsonOrNull(res), url);
  return res.blob();
}

/* ------------------------------------------------------------------ *
 * 8. Fork — user and settings
 * ------------------------------------------------------------------ */

/** GET /api/v2/user — the user plus `settings`, which carries every preference. */
export function getCurrentUser() {
  return forkGet(forkV2Url('user'));
}

/**
 * GET /api/v2/user/settings/avatar/provider.
 * The only way to read it: `avatar_provider` is `json:"-"` on user.User
 * (pkg/user/user.go:102), so GET /api/v2/user does not carry it.
 */
export function getAvatarProvider() {
  return forkGet(forkV2Url('user/settings/avatar/provider'));
}

/**
 * GET /api/v2/user/timezones, sorted here.
 *
 * The list is deliberately unsorted server-side and the operation description
 * says to sort it client-side (pkg/routes/api/v2/user_settings.go:118-119).
 * Plain `.sort()` rather than `localeCompare`: IANA identifiers are ASCII and
 * the order must not change with the negotiated language, or the same list
 * reorders itself between two of this page's six locales.
 */
export async function listTimezones() {
  const zones = await forkGet(forkV2Url('user/timezones'));
  return Array.isArray(zones) ? [...zones].sort() : [];
}

/**
 * PUT /api/v2/user/settings/general — GET, merge, then write.
 *
 * PUT REPLACES, and destructively. UpdateUserGeneralSettings assigns every
 * field unconditionally including `u.FrontendSettings`
 * (pkg/models/user_settings.go:104-116) and calls UpdateUser with
 * forceOverride = true, which makes an empty `name` overwrite the stored one
 * (pkg/user/user.go:695-697). Sending a bare patch therefore blanks the display
 * name and nulls every preference the page did not mention.
 *
 * Preference keys are snake_case and the nesting is real: the wire path is
 * `frontend_settings.color_schema` / `frontend_settings.time_format`, because
 * objectToSnakeCase recurses (frontend/src/helpers/case.ts:74-77). Reading
 * camelCase returns undefined and falls back to light / 24-hour in silence.
 *
 * @param {object} patch          top-level settings fields (name, language, timezone, …)
 * @param {object} [frontendPatch] frontend_settings fields (color_schema, time_format, …)
 */
export async function saveGeneralSettings(patch, frontendPatch) {
  const user = await getCurrentUser();
  const current = user?.settings ?? {};
  const merged = {
    ...current,
    ...patch,
    frontend_settings: {
      ...(current.frontend_settings ?? {}),
      ...(frontendPatch ?? {}),
    },
  };
  // readOnly on models.UserGeneralSettings; sending it back is noise at best.
  delete merged.extra_settings_links;
  return forkSend('PUT', forkV2Url('user/settings/general'), merged);
}

/**
 * PUT /api/v2/user/settings/avatar — multipart, field name `avatar`, one file
 * (pkg/routes/api/v2/avatar_upload.go:43-45). No SVG and no WebP: no decoder is
 * registered for either.
 */
export function uploadAvatar(file) {
  const form = new FormData();
  form.append('avatar', file);
  return forkUpload('PUT', forkV2Url('user/settings/avatar'), form);
}

/** PUT /api/v2/user/settings/avatar/provider. */
export function setAvatarProvider(provider) {
  return forkSend('PUT', forkV2Url('user/settings/avatar/provider'), {avatar_provider: provider});
}

/**
 * The avatar is two calls: upload, then set the provider to `upload`.
 *
 * Kept as two per the brief, but NOT for the reason the brief gives, and no
 * test may assert the second is required (ruling C12). StoreUploadedAvatar
 * already sets AvatarProvider = "upload" before storing
 * (pkg/modules/avatar/avatar.go:163) and `avatar_provider` is in
 * baseUserUpdateColumns (pkg/user/user.go:642), so the upload alone persists
 * it today. The second call is idempotent, costs one request on a rare action,
 * and keeps this page correct if that column list ever changes upstream.
 */
export async function saveAvatar(file) {
  const uploaded = await uploadAvatar(file);
  const provider = await setAvatarProvider('upload');
  // BUMPED HERE, AFTER BOTH CALLS, AND NOWHERE ELSE. See `getAvatarGeneration`.
  avatarGeneration += 1;
  return {uploaded, provider};
}

/* --- reading the avatar back (round 1b, PM item 3) ----------------- */

/**
 * THE STALE-AVATAR COUNTER, AND WHY IT LIVES IN THIS FILE.
 *
 * Round 1 fixed the settings circle by keeping a private `avatarVersion` inside
 * `view-settings.js` and bumping it in that file's own save handler. That works
 * for exactly one renderer. The header block `app.js` now draws shows the SAME
 * image, so a second private counter would be a second thing to remember to
 * bump — and the first upload made through any future third surface would leave
 * one of them behind, showing the old face next to the new one on the same
 * screen. `saveAvatar` above is the ONE place an upload completes, so the
 * counter belongs beside it and every reader keys its cache on this number.
 *
 * It is a generation, not a URL and not a cache: this module holds no blob and
 * revokes no object URL. Whoever renders owns that lifecycle (app.js
 * `ensureAvatar` does it for the header), because only the renderer knows when
 * a URL has stopped being an `<img src>`.
 *
 * Starts at 0 and only ever increases. A reader that has never seen an upload
 * therefore has a stable key and issues exactly one request.
 */
let avatarGeneration = 0;

/** The current generation. Key any avatar cache on `username + this`. */
export function getAvatarGeneration() {
  return avatarGeneration;
}

/**
 * GET /api/v2/avatar/{username}?size= — the image BYTES, as a Blob, or null.
 *
 * This is the export `view-settings.js` asked for in round 1 ("Requested in the
 * report as `getAvatarBlob()`, after which this function becomes a one-line
 * call"). Its private `readAvatarObjectUrl` / `avatarFetch` pair is reproduced
 * here verbatim in behaviour so that adopting it is a deletion, not a change.
 *
 * `size` IS THE ONLY QUERY PARAMETER THE ROUTE DECLARES, alongside `username`
 * (pkg/routes/api/v2/avatar.go:37-40). A `?v=` cache-buster is therefore NOT
 * used: hanging an undeclared parameter off a Huma-validated route bets the fix
 * on a validator's leniency. `cache: 'reload'` is the cache-buster instead — it
 * bypasses the HTTP cache for this one request, needs nothing from the server,
 * and cannot be rejected by it.
 *
 * NOT `authedFetch`, AND THE DIFFERENCE IS DELIBERATE. `authedFetch` calls
 * `markSessionLost()` on a second 401, which puts the whole page on its
 * terminal sign-in surface. An avatar is decorative; it must never be the
 * request that ends someone's session. So the 401 path is spelled out here:
 * `refreshSession()` — api.js's SINGLE IN-FLIGHT promise, so this waits on the
 * same refresh every other call waits on and cannot start a second one — then
 * ONE retry, then give up quietly.
 *
 * NO `outcome` CHECK AND NO JSON CHECK. This is a fork route returning image
 * bytes; bar 8 is about the commercial `/v1` service. A non-2xx here means "no
 * picture", and every caller already has initials on screen behind it.
 *
 * Returns null — never throws — for: no username, a non-2xx, a zero-byte body,
 * a lost refresh, or a network failure. One falsy answer, one fallback path.
 *
 * @param {string} username
 * @param {number} size  the pixel size to ask the server to render
 * @returns {Promise<Blob|null>}
 */
export async function getAvatarBlob(username, size) {
  const name = String(username ?? '');
  if (name === '') return null;
  const pixels = Number.isFinite(Number(size)) && Number(size) > 0 ? Math.round(Number(size)) : 0;
  const url = forkV2Url(`avatar/${encodeURIComponent(name)}${pixels > 0 ? `?size=${pixels}` : ''}`);
  try {
    let res = await avatarFetch(url, getToken());
    if (res.status === 401) {
      const token = await refreshSession();
      if (token === null) return null;
      res = await avatarFetch(url, token);
    }
    if (!res.ok) return null;
    const blob = await res.blob();
    return blob.size === 0 ? null : blob;
  } catch (err) {
    console.error('[one/api] avatar read failed', err);
    return null;
  }
}

/**
 * `rawFetch` is not used: it is the instrumented path every AUTHED call shares,
 * and this request deliberately opts out of the shared 401 handling above it.
 * `credentials: 'same-origin'` is still non-negotiable (BRIEF, "Session").
 */
function avatarFetch(url, token) {
  const headers = {Accept: 'image/*'};
  if (typeof token === 'string' && token !== '') headers.Authorization = `Bearer ${token}`;
  return fetch(url, {method: 'GET', headers, credentials: 'same-origin', cache: 'reload'});
}

/* --- credentials, export and deletion (ruling C3) ----------------- */

/** PUT /api/v2/user/settings/email. Visible under managed mode — commit f203aae6. */
export function changeEmail(newEmail, password) {
  return forkSend('PUT', forkV2Url('user/settings/email'), {new_email: newEmail, password});
}

/** POST /api/v2/user/password. Invalidates every other session on success. */
export function changePassword(oldPassword, newPassword) {
  return forkSend('POST', forkV2Url('user/password'), {
    old_password: oldPassword,
    new_password: newPassword,
  });
}

/** POST /api/v2/user/export/request. `password` is ignored for external-auth users. */
export function requestExport(password) {
  return forkSend('POST', forkV2Url('user/export/request'), {password});
}

/**
 * POST /api/v2/user/export/download. A POST, not a GET, because the password
 * travels in the body; the response is the zip itself, so this returns a Blob.
 */
export function downloadExport(password) {
  return forkBlob('POST', forkV2Url('user/export/download'), {password});
}

/**
 * POST /api/v2/user/deletion/request.
 *
 * EXPECTED TO 403 FOR EVERY ACCOUNT: the route is `protected-topology` /
 * `managed:"service-managed"` (route-classification.json:408), which refuses
 * everyone including an instance admin. It is wired anyway because this page is
 * refusal-driven rather than capability-driven — the browser cannot learn the
 * instance is managed (brief, "What is knowingly unavailable"), so the honest
 * shape is to issue the request and render the refusal.
 *
 * The delete-account control does NOT call this. It calls `eraseAccount()`
 * (POST /v1/account/erasure). The trap the brief names: `/api/v1/info` still
 * advertises `enableuserdeletion` because the config default is true, so the
 * flag cannot be used to decide.
 */
export function requestAccountDeletion(password) {
  return forkSend('POST', forkV2Url('user/deletion/request'), {password});
}

/**
 * POST /api/v2/user/deletion/cancel. `ordinary`, `write:"deletion"`
 * (route-classification.json:406) — this one genuinely works locally, which is
 * why "Cancel scheduled deletion" stays a fork call while the deletion itself
 * links out.
 */
export function cancelAccountDeletion(password) {
  return forkSend('POST', forkV2Url('user/deletion/cancel'), {password});
}

/** GET /api/v1/info — unauthenticated, and the one call that sends no bearer. */
export async function getInfo() {
  const url = forkV1Url('info');
  const res = await rawFetch(url, {method: 'GET', credentials: 'same-origin', headers: {Accept: 'application/json'}});
  return expectOk(res, url);
}

/**
 * The fork's own app root, NOT an /api/v1 or /api/v2 path — for building the
 * Google OAuth redirect_uri (`{base}auth/openid/{provider}`), which is the
 * VUE APP'S route, registered verbatim with Google and never this page's own.
 * `forkV1Url`/`forkV2Url` both hardcode an API prefix this path must not have.
 */
export function forkAppUrl(path) {
  return new URL(stripLeadingSlash(path), forkBase()).toString();
}

/**
 * The Google authorization URL for starting an OIDC round trip, built the
 * same way the Vue app's own `redirectToProvider.ts` builds it — same four
 * query parameters, same redirect_uri shape. Kept here rather than in
 * view-settings.js per this file's own rule: nothing outside api.js builds a
 * URL, and that includes a third-party one, not only the fork's own prefixes.
 *
 * `provider` is one entry of GET /api/v1/info's `auth.openid_connect.providers`
 * (`{key, auth_url, client_id, scope}`); `state` is the caller's own opaque
 * value, round-tripped by the provider and verified by whoever consumes it.
 *
 * THIS FILE HAS NO FUNCTION THAT CALLS THE RESULTING ROUTE
 * (`POST /api/v2/user/settings/connect/openid/{provider}`), and that is not
 * an omission. Google's redirect_uri always lands on the Vue app's own
 * `frontend/src/views/user/OpenIdAuth.vue`, a separate bundle this page
 * cannot import into or be imported from (bar 1) — that page makes the actual
 * connect call, using the session it re-establishes there. This function's
 * whole job ends at handing the browser off to Google.
 */
export function buildOpenIdAuthorizeUrl(provider, state) {
  const redirectUri = forkAppUrl(`auth/openid/${provider.key}`);
  const url = new URL(provider.auth_url);
  url.searchParams.set('client_id', provider.client_id);
  url.searchParams.set('redirect_uri', redirectUri);
  url.searchParams.set('response_type', 'code');
  url.searchParams.set('scope', provider.scope);
  url.searchParams.set('state', state);
  return url.toString();
}

/* ------------------------------------------------------------------ *
 * 9. Fork — task
 * ------------------------------------------------------------------ */

/** Values of `expand` on the task read (pkg/routes/api/v2/tasks.go:112-116). */
export const TASK_EXPAND = Object.freeze([
  'subtasks', 'buckets', 'reactions', 'comments', 'comment_count', 'time_entries_count', 'is_unread',
]);

/**
 * GET /api/v2/tasks/{id}?format=markdown — read ONCE (ruling C13).
 *
 * `format=markdown` is the opt-in; HTML is the default and `format=html` is not
 * a documented value on this operation. There is no description-only endpoint,
 * and issuing two full reads invites them to disagree with each other, so the
 * page takes one read and keeps it.
 */
export function getTask(taskId, {expand} = {}) {
  const url = withQuery(forkV2Url(`tasks/${encodeURIComponent(taskId)}`), {
    format: 'markdown',
    expand,
  });
  return forkGet(url);
}

/**
 * The markdown exchange header. Module-private ON PURPOSE — see the two
 * functions below. Nothing outside this file can name it, and nothing inside
 * this file attaches it except `updateTaskDescription`.
 */
const MARKDOWN_HEADER = 'X-Vikunja-Format';

/**
 * THE TWO PROPERTIES EVERY TASK PATCH MUST EXCISE, AND WHY THIS IS NOT US BEING
 * CLEVER. THIS IS THE FIX FOR "validation failed" ON EVERY CONTROL.
 *
 * `PATCH /api/v2/tasks/{id}` is not a hand-written handler. It is synthesised by
 * Huma's AutoPatch, which does GET -> RFC 7386 merge -> PUT inside the one
 * request (pkg/routes/api/v2/huma.go:163-181). So the body the server validates
 * is NOT the body we send: it is OUR PATCH MERGED OVER THE WHOLE TASK AS THE GET
 * RETURNED IT, and every property of that merged document is validated against
 * the PUT's schema. AutoPatch's internal GET is issued against the bare path with
 * the query string dropped (which is the whole reason
 * pkg/routes/api/v2/richtext.go:71-89 exists), so no `expand` reaches it and the
 * read shape it produces is the same one every time.
 *
 * TWO PROPERTIES OF THAT READ SHAPE CANNOT PASS THE WRITE SCHEMA. Both are
 * `readOnly`, which does NOT exempt them: Huma's read-only exemption applies only
 * to the *required* check, and a present read-only property has its VALUE
 * validated like any other (huma v2.39.0 validate.go, `handleMapString`).
 *
 *   1. `reactions` is ALWAYS `null` and the schema forbids null.
 *      `Reactions ReactionMap` carries `json:"reactions"` with NO `omitempty`
 *      (pkg/models/tasks.go:160) and is only populated for `?expand=reactions`
 *      (pkg/models/tasks.go:805-810) — which the internal GET cannot ask for.
 *      `ReactionMap` is `map[string][]*user.User` (pkg/models/reaction.go:64);
 *      Huma marks Go SLICES nullable by default (`DefaultArrayNullable = true`)
 *      and MAPS not at all. Verified on the running instance's own
 *      `/api/v2/schemas/TaskReadOneBody.json` — the exact schema this request is
 *      validated against — where `reminders`, `assignees`, `labels` and
 *      `attachments` all read `"type":["array","null"]` and `reactions` reads a
 *      bare `"type":"object"`. `null` against that hits
 *      `default: res.Add(path, v, MsgExpectedObject)`.
 *
 *   2. `subscription` is a string on the wire and an integer in the schema.
 *      `Subscription.EntityType` is a Go `int` whose `MarshalJSON` emits
 *      `"task"` / `"project"` (pkg/models/subscription.go:60-68), but no
 *      `huma.SchemaProvider` overrides the generated schema, so
 *      `/api/v2/schemas/Subscription.json` publishes
 *      `"entity":{"format":"int64","type":"integer"}`. The property is
 *      `omitempty`, so this only fires when the task read carries a subscription
 *      — which is not rare: `ReadOne` fills it from `GetSubscriptionForUser`
 *      (pkg/models/tasks.go:2101-2106) and that INHERITS the project's
 *      subscription when the task has none of its own
 *      (pkg/models/subscription.go:224-227). The page's own subscribe toggle
 *      guarantees it for any task the user has ever subscribed to.
 *
 * The inner PUT's 422 is copied out to the client verbatim, and Huma's `detail`
 * for a schema failure is the CONSTANT string "validation failed" — which is
 * exactly what appeared on the title, the done box, priority, the three dates,
 * the repeat builder, percent done, the favourite star, reminders, the move
 * picker and the description: every control that writes through this function.
 *
 * DELETING THEM FROM THE MERGED DOCUMENT IS THE WHOLE FIX. Under RFC 7386 a
 * `null` in the patch REMOVES the member from the target, and an absent property
 * is not validated at all (nothing on this schema is required —
 * `cfg.FieldsOptionalByDefault` at pkg/routes/api/v2/huma.go:79, and the live
 * schema carries no `required` array). Removal is exact here because this fork
 * does not enable Huma's opt-in merge-patch nullability extension — no
 * `MergePatchNullabilityExtension` is registered anywhere in pkg/ — so nulls keep
 * their plain RFC 7386 meaning.
 *
 * Deleting is also the honest operation. Both fields are read-only and neither is
 * ours to write: `Reactions` and `Subscription` are both `xorm:"-"` and neither
 * appears in the columns `updateSingleTask` writes
 * (pkg/models/tasks.go:1159-1174), so removing them changes nothing but whether
 * the request is accepted. Sending `{}` instead would work for `reactions` (the
 * stored value is null, so the merge replaces it) but NOT for `subscription`:
 * RFC 7386 RECURSES when both sides are objects, so an empty object would leave
 * the offending `"entity":"task"` exactly where it was.
 *
 * THIS IS A WORKAROUND FOR TWO SERVER DEFECTS AND SHOULD BE DELETED WHEN THEY ARE
 * FIXED. The repairs are one tag each — `omitempty` or `nullable:"true"` on
 * `Task.Reactions` (and `TaskComment.Reactions`, which has the same shape), and a
 * schema override or string type for `Subscription.EntityType`. Both are model
 * changes, which bar 1 puts outside this page's remit. Until they land,
 * `PATCH /api/v2/tasks/{id}` is unusable for EVERY client, not just this one.
 */
const PATCH_EXCISED_TASK_FIELDS = Object.freeze({reactions: null, subscription: null});

async function patchTaskInternal(taskId, patch, extraHeaders) {
  const url = forkV2Url(`tasks/${encodeURIComponent(taskId)}`);
  const res = await authedFetch(url, {
    method: 'PATCH',
    // `application/merge-patch+json` rather than `application/json`. Both reach
    // AutoPatch's merge branch, but only this one is a declared media type of
    // the synthesised operation, so it is the one that stays correct if the
    // undeclared `application/json` alias is ever dropped. It is also what the
    // fork's own second client sends (veans/internal/client/client.go:54) and
    // what every AutoPatch test in pkg/webtests/ exercises.
    headers: {'Content-Type': 'application/merge-patch+json', ...extraHeaders},
    // Caller last: a patch may one day legitimately restate an excised field, and
    // it must win. Nothing today does.
    body: JSON.stringify({...PATCH_EXCISED_TASK_FIELDS, ...patch}),
  });
  return expectOk(res, url);
}

/**
 * PATCH /api/v2/tasks/{id} with `X-Vikunja-Format: markdown`.
 *
 * THE ONLY FUNCTION IN THIS MODULE THAT ATTACHES THAT HEADER, and the reason
 * the header constant is private and `patchTask` takes no headers argument.
 *
 * AutoPatch is GET -> merge -> PUT inside one request, and it strips the query
 * string (pkg/routes/api/v2/richtext.go:38-49, 71-89), so the header is the
 * only channel that survives the re-dispatch — and it applies to EVERY
 * rich-text field in the merged resource, not just the one being edited. A
 * non-description PATCH carrying it therefore round-trips the untouched stored
 * HTML through HTMLToMarkdown and back through MarkdownToHTMLWithMentions, a
 * conversion the API's own description calls lossy (richtext.go:65-69:
 * constructs Markdown cannot express, such as underline, are dropped). The
 * corruption is silent — no error, just a description that quietly degrades on
 * an edit that never touched it.
 */
export function updateTaskDescription(taskId, descriptionMarkdown) {
  return patchTaskInternal(taskId, {description: descriptionMarkdown}, {[MARKDOWN_HEADER]: 'markdown'});
}

/**
 * PATCH /api/v2/tasks/{id} for every other field: title, done, due_date,
 * priority, percent_done, hex_color, …
 *
 * Rejects a `description` key rather than sending one. Without the header the
 * server treats the body as HTML and would store the user's Markdown as
 * literal text — the same silent corruption from the other direction, and the
 * assertion is what makes "the header is on exactly one PATCH" true by
 * construction instead of by convention.
 *
 * `done_at` is server-set and readOnly (pkg/models/tasks.go:74) — never send
 * it. So are assignees, labels, attachments, related_tasks, reactions,
 * identifier, index, position, created and updated; each has its own endpoint.
 *
 * ACCESS-EXPANDING, `managed:"task-move"` (route-classification.json:363).
 * EVERY task PATCH passes the task-move rule, not only an explicit move, so a
 * refusal on an ordinary edit is an expected answer under Personal edition and
 * must be surfaced, not swallowed. In practice decideTaskMove returns nil when
 * the body names no destination project (pkg/routes/managed_rules_core.go:91-110),
 * so a `{done:true}` patch passes — but that is the rule's behaviour, not a
 * guarantee this page may bake in.
 */
export function patchTask(taskId, patch) {
  if (patch !== null && typeof patch === 'object' && 'description' in patch) {
    throw assertion('description-patch-must-use-updateTaskDescription');
  }
  return patchTaskInternal(taskId, patch, {});
}

/** DELETE /api/v2/tasks/{id} (ruling C3, route-classification.json:362). */
export function deleteTask(taskId) {
  return forkSend('DELETE', forkV2Url(`tasks/${encodeURIComponent(taskId)}`));
}

/**
 * POST /api/v2/tasks/{id}/duplicate (ruling C3, :368).
 * Note the v1 -> v2 method flip: v1 duplicate is a PUT.
 */
export function duplicateTask(taskId) {
  return forkSend('POST', forkV2Url(`tasks/${encodeURIComponent(taskId)}/duplicate`));
}

/**
 * POST /api/v2/projects/{project}/tasks — the only way this page creates a task.
 *
 * VERIFIED BEFORE WIRING, the same way `listProjects` below was and for the same reason (ruling
 * C3, which forbids inventing a route). Two separate facts had to hold, and a control offering
 * to do something the server refuses is worse than no control:
 *
 *   1. THE ROUTE EXISTS. `tasks-create`, registered at pkg/routes/api/v2/tasks.go:76-83. Note
 *      the v1 -> v2 method flip this file keeps meeting: v1 create is a PUT.
 *   2. MANAGED MODE DOES NOT REFUSE IT. route-classification.json classifies
 *      `POST /api/v2/projects/:project/tasks` as `ordinary`, and an ordinary route carries no
 *      `managed` rule, so `RequireManagedPolicy` has nothing to look up and lets it through.
 *      The distinction is the whole of that file's first class: creating a PROJECT is
 *      `protected-topology` and is refused, because a project can touch the Inbox and the
 *      Public root the product guarantees. Creating a TASK INSIDE a project it already has is
 *      ordinary task work, which managed mode was never meant to stop.
 *
 * THE UNPAID CASE IS THE OTHER HALF, AND IT IS THE SERVER'S TO DECIDE. The same row carries no
 * `write` marker, and absence there means refuse — so a subject whose entitlement says
 * `write_access: settings_only` is refused this by the server, exactly as they are refused every
 * other task write. `data-requires="write"` on the button in app.js MIRRORS that refusal so the
 * page can say why before the round trip; it never replaces it, and the request is still issued
 * and still answered by the gate whenever one is made.
 *
 * THE TITLE IS THE WHOLE BODY, deliberately. `project_id` comes from the URL and OVERWRITES
 * anything a body sends (tasks.go:178), and the creator is the authenticated user. Sending a
 * fuller task would be this page inventing defaults — a due date, a priority, a bucket — that
 * nobody asked it to hold an opinion about.
 */
export function createTask(projectId, title) {
  return forkSend('POST', forkV2Url(`projects/${encodeURIComponent(projectId)}/tasks`), {title});
}

/**
 * The move picker's data source: GET /api/v2/projects
 * (pkg/routes/api/v2/projects.go:41-45).
 *
 * Ruling C3 asks this to be verified before wiring and forbids inventing one.
 * route-classification.json cannot answer it — that file holds 265 rows and not
 * one is a GET, because it classifies only mutating routes — so the check is
 * against the registration site, where the operation exists. The control is
 * wired, not disabled.
 */
export function listProjects({page, perPage, q, isArchived} = {}) {
  return forkGet(withQuery(forkV2Url('projects'), {
    page, per_page: perPage, q, is_archived: isArchived,
  }));
}

/**
 * The relation picker's data source: GET /api/v2/tasks?q=…
 * (tasks-list, pkg/routes/api/v2/task_collection.go:135-139) — flat, paginated,
 * across every project the caller can see. Verified the same way as
 * `listProjects` above.
 */
export function searchTasks(q, {page, perPage} = {}) {
  return forkGet(withQuery(forkV2Url('tasks'), {q, page, per_page: perPage}));
}

/* --- relations and subscription (ruling C3) ----------------------- */

/**
 * The wire values of `relation_kind` (pkg/models/task_relation.go:89). Wire
 * values, never labels — `app.js` looks each one up in the catalogue (C10).
 */
export const RELATION_KINDS = Object.freeze([
  'subtask', 'parenttask', 'related', 'duplicateof', 'duplicates',
  'blocking', 'blocked', 'precedes', 'follows', 'copiedfrom', 'copiedto',
]);

/** POST /api/v2/tasks/{task}/relations (:380). The inverse relation is created server-side. */
export function addRelation(taskId, otherTaskId, relationKind) {
  return forkSend('POST', forkV2Url(`tasks/${encodeURIComponent(taskId)}/relations`), {
    other_task_id: otherTaskId,
    relation_kind: relationKind,
  });
}

/** DELETE /api/v2/tasks/{task}/relations/{relationKind}/{otherTask} (:381). */
export function removeRelation(taskId, relationKind, otherTaskId) {
  return forkSend('DELETE', forkV2Url(
    `tasks/${encodeURIComponent(taskId)}/relations/${encodeURIComponent(relationKind)}/${encodeURIComponent(otherTaskId)}`,
  ));
}

/**
 * POST / DELETE /api/v2/subscriptions/{entity}/{entityID} (:360-361).
 *
 * This is the route behind the subscribe toggle. SPEC-BACKEND §0 read the
 * `/v1/subscriptions` rows as an unrelated feature and waved them away; ruling
 * C3 overturns that. Note the v1 -> v2 method flip: subscribe is PUT on v1.
 */
export const SUBSCRIPTION_ENTITIES = Object.freeze(['project', 'task']);

export function subscribe(entity, entityId) {
  return forkSend('POST', forkV2Url(`subscriptions/${encodeURIComponent(entity)}/${encodeURIComponent(entityId)}`));
}

export function unsubscribe(entity, entityId) {
  return forkSend('DELETE', forkV2Url(`subscriptions/${encodeURIComponent(entity)}/${encodeURIComponent(entityId)}`));
}

/* ------------------------------------------------------------------ *
 * 10. Fork — comments
 * ------------------------------------------------------------------ */

/** GET /api/v2/tasks/{task}/comments?format=markdown&order_by=asc */
export function listComments(taskId, {orderBy = 'asc', page, perPage} = {}) {
  return forkGet(withQuery(forkV2Url(`tasks/${encodeURIComponent(taskId)}/comments`), {
    format: 'markdown',
    order_by: orderBy,
    page,
    per_page: perPage,
  }));
}

/** POST /api/v2/tasks/{task}/comments?format=markdown — task from the URL, author from the JWT. */
export function createComment(taskId, comment) {
  return forkSend(
    'POST',
    withQuery(forkV2Url(`tasks/${encodeURIComponent(taskId)}/comments`), {format: 'markdown'}),
    {comment},
  );
}

/**
 * PUT /api/v2/tasks/{task}/comments/{id}?format=markdown.
 *
 * PUT, not PATCH, and the query rather than the header — deliberately.
 * SPEC-BACKEND row 13 proposed a PATCH carrying `X-Vikunja-Format`, but ruling
 * C6 requires that header to be absent from EVERY PATCH but the description
 * one, and PATCH here is only AutoPatch's synthesis anyway: the sole registered
 * update operation on this resource is `task-comments-update`, a PUT
 * (pkg/routes/api/v2/task_comments.go:76-80). Going straight to the PUT skips
 * the re-dispatch entirely, so the `format` query survives and no header is
 * needed. `comment` is the only writable field, so a full replace loses
 * nothing.
 */
export function updateComment(taskId, commentId, comment) {
  return forkSend(
    'PUT',
    withQuery(
      forkV2Url(`tasks/${encodeURIComponent(taskId)}/comments/${encodeURIComponent(commentId)}`),
      {format: 'markdown'},
    ),
    {comment},
  );
}

/**
 * DELETE /api/v2/tasks/{task}/comments/{id}.
 *
 * Only the author may update or delete. `max_permission` on the comment read
 * reports the PARENT TASK's permission and therefore over-states what may be
 * done here (pkg/routes/api/v2/task_comments.go:120-125) — gate the control on
 * author identity, never on that field.
 */
export function deleteComment(taskId, commentId) {
  return forkSend('DELETE', forkV2Url(
    `tasks/${encodeURIComponent(taskId)}/comments/${encodeURIComponent(commentId)}`,
  ));
}

/* ------------------------------------------------------------------ *
 * 11. Fork — labels
 * ------------------------------------------------------------------ */

/** GET /api/v2/labels?q= — every label the caller can use. */
export function listLabels({q, page, perPage} = {}) {
  return forkGet(withQuery(forkV2Url('labels'), {q, page, per_page: perPage}));
}

/** POST /api/v2/labels (ruling C3, :311) — the create-if-absent half of the inline picker. */
export function createLabel(title, hexColor) {
  const payload = {title};
  if (hexColor !== undefined) payload.hex_color = hexColor;
  return forkSend('POST', forkV2Url('labels'), payload);
}

/** GET /api/v2/tasks/{id}/labels */
export function listTaskLabels(taskId) {
  return forkGet(forkV2Url(`tasks/${encodeURIComponent(taskId)}/labels`));
}

/** POST /api/v2/tasks/{id}/labels — the body is `{label_id}` (pkg/models/label_task.go:39). */
export function addTaskLabel(taskId, labelId) {
  return forkSend('POST', forkV2Url(`tasks/${encodeURIComponent(taskId)}/labels`), {label_id: labelId});
}

/** DELETE /api/v2/tasks/{id}/labels/{label} — `{label}` is the numeric label id. */
export function removeTaskLabel(taskId, labelId) {
  return forkSend('DELETE', forkV2Url(
    `tasks/${encodeURIComponent(taskId)}/labels/${encodeURIComponent(labelId)}`,
  ));
}

/* ------------------------------------------------------------------ *
 * 12. Fork — attachments
 * ------------------------------------------------------------------ */

/**
 * GET /api/v2/tasks/{task}/attachments.
 *
 * Returns null — not an error — on 404. The whole resource is absent when
 * `service.enabletaskattachments` is false: RegisterTaskAttachmentRoutes
 * returns early (pkg/routes/api/v2/task_attachments.go:55-58) and all four
 * attachment routes answer 404. That is a config gate, not managed mode, and
 * the page degrades rather than reporting a failure.
 */
export async function listAttachments(taskId) {
  try {
    return await forkGet(forkV2Url(`tasks/${encodeURIComponent(taskId)}/attachments`));
  } catch (err) {
    if (err instanceof ForkError && err.status === 404) return null;
    throw err;
  }
}

/**
 * POST /api/v2/tasks/{task}/attachments — multipart, field `files`, repeatable.
 *
 * PARTIAL SUCCESS IS THE DESIGNED BEHAVIOUR: a file that fails is listed in
 * `errors` while the request still returns 201
 * (pkg/routes/api/v2/task_attachments.go:72). `res.ok` is a lie here for the
 * same reason bar 8 says it is on the commercial service, so this resolves the
 * envelope untouched and the caller branches on `errors.length`.
 */
export function uploadAttachments(taskId, files) {
  const form = new FormData();
  for (const file of files) form.append('files', file);
  return forkUpload('POST', forkV2Url(`tasks/${encodeURIComponent(taskId)}/attachments`), form);
}

/** GET /api/v2/tasks/{task}/attachments/{id} — raw bytes, real mime in Content-Type. */
export function downloadAttachment(taskId, attachmentId, {previewSize} = {}) {
  return forkBlob('GET', withQuery(
    forkV2Url(`tasks/${encodeURIComponent(taskId)}/attachments/${encodeURIComponent(attachmentId)}`),
    {preview_size: previewSize},
  ));
}

/** DELETE /api/v2/tasks/{task}/attachments/{id} */
export function deleteAttachment(taskId, attachmentId) {
  return forkSend('DELETE', forkV2Url(
    `tasks/${encodeURIComponent(taskId)}/attachments/${encodeURIComponent(attachmentId)}`,
  ));
}

/* ------------------------------------------------------------------ *
 * 13. Fork — assignees
 * ------------------------------------------------------------------ */

/** GET /api/v2/tasks/{id}/assignees */
export function listAssignees(taskId, {q} = {}) {
  return forkGet(withQuery(forkV2Url(`tasks/${encodeURIComponent(taskId)}/assignees`), {q}));
}

/** POST /api/v2/tasks/{id}/assignees — `{user_id}` (pkg/models/task_assignees.go:35). */
export function addAssignee(taskId, userId) {
  return forkSend('POST', forkV2Url(`tasks/${encodeURIComponent(taskId)}/assignees`), {user_id: userId});
}

/**
 * DELETE /api/v2/tasks/{id}/assignees/{user} — {user} IS A NUMERIC USER ID.
 *
 * The sharpest trap on this surface. The identical `{user}` path segment means
 * a numeric id here (pkg/routes/api/v2/task_assignees.go:107-108, `path:"user"`
 * int64) and a USERNAME on the team-member routes (pkg/models/teams.go:82,
 * `path:"user"` string). Applying the brief's "member routes take a username"
 * correction uniformly across every `{user}` segment breaks this call.
 */
export function removeAssignee(taskId, userId) {
  return forkSend('DELETE', forkV2Url(
    `tasks/${encodeURIComponent(taskId)}/assignees/${encodeURIComponent(userId)}`,
  ));
}

/**
 * GET /api/v2/projects/{project}/users/search?q= — the assignee picker.
 *
 * Preferred over GET /api/v2/users: an assignee must have access to the task's
 * project and this returns exactly that set, whereas the generic user search
 * only matches users who made themselves discoverable and never returns email.
 */
export function searchProjectUsers(projectId, q) {
  return forkGet(withQuery(forkV2Url(`projects/${encodeURIComponent(projectId)}/users/search`), {q}));
}

/* ------------------------------------------------------------------ *
 * 14. Fork — organization and teams
 * ------------------------------------------------------------------ */

/**
 * GET /api/v1/brazn/organization — v1 only; there is no v2 equivalent.
 *
 * RESOLVES TO NULL ON 403, WHICH IS NOT AN ERROR. 403 is the ordinary answer
 * for a non-administrator, and it is also the answer a genuine administrator
 * gets when a second active projection claims organization_admin
 * (ErrOrganizationAdministratorAmbiguous, pkg/models/brazn_organization.go:168-181).
 * The refusal string is flat on purpose — a reply distinguishing those cases
 * would answer questions about an organization the caller is not in — so the
 * page cannot tell them apart and must not try.
 *
 * Ruling C1.5: this null is what hides the Organization and Team tabs. Tab
 * visibility is gated on this read returning 200, never on the edition.
 *
 * Every number the seat meter needs is in this one payload — seats_occupied,
 * seats_purchased, teams_used, teams_allowed, can_create_team, seats_per_team.
 * `seats_purchased` and `teams_allowed` may be null TOGETHER, meaning "this
 * instance cannot answer"; null is neither zero nor unlimited, and the page
 * must not tell a customer to buy seats they may already own. Gate the add-team
 * control on `can_create_team`, which the server sends precisely so a client
 * renders the same answer the route enforces — do not recompute it, and never
 * fall back to a local `seats_per_team || 3` (pkg/models/brazn_organization.go:126-131:
 * a constant duplicated either side of a boundary is checked by neither).
 */
export async function getOrganization() {
  try {
    return await forkGet(forkV1Url('brazn/organization'));
  } catch (err) {
    if (err instanceof ForkError && err.status === 403) return null;
    throw err;
  }
}

/** GET /api/v2/teams */
export function listTeams({page, perPage, q, includePublic} = {}) {
  return forkGet(withQuery(forkV2Url('teams'), {
    page, per_page: perPage, q, include_public: includePublic,
  }));
}

/**
 * GET /api/v2/teams/{id} — the team plus `members[]`.
 *
 * CAN 403 LEGITIMATELY: Team.CanRead requires membership, and the organization
 * administrator is commonly not a member of the commercially provisioned
 * primary team. Callers must use Promise.allSettled, never Promise.all — one
 * 403 must not blank the whole Team-management tab (ruling C11); degrade that
 * one row to disabled with a reason instead.
 */
export function getTeam(teamId) {
  return forkGet(forkV2Url(`teams/${encodeURIComponent(teamId)}`));
}

/**
 * PUT /api/v1/brazn/organization/teams — `{name}` and nothing else.
 *
 * THE ONLY WORKING TEAM-CREATE ROUTE. PUT /api/v1/teams and POST /api/v2/teams
 * are both `managed:"service-managed"` (route-classification.json:257, :383)
 * and 403 for everyone, instance admins included.
 *
 * The 409 body must be rendered VERBATIM (ruling C4). It is
 * BraznOrganizationTeamCapacityResponse
 * (pkg/routes/api/v1/brazn_organization.go:64-76):
 *   {message, seats_purchased, teams_used, seats_per_team, seats_needed}
 * `seats_purchased` and `seats_needed` are *int and are NULL TOGETHER when the
 * instance cannot read a seat count, and `message` then changes to a different
 * sentence. Render `seats_needed` as sent — do not compute it. The server rule
 * is `seats_purchased >= 3 * (teams_used + 1)` and it IGNORES member count
 * entirely (pkg/models/brazn_organization.go:265-270).
 */
export function createOrganizationTeam(name) {
  return forkSend('PUT', forkV1Url('brazn/organization/teams'), {name});
}

/**
 * DELETE /api/v1/brazn/organization/teams/{team}.
 *
 * 409 for the primary team, 404 for a team this organization does not have —
 * which is also the answer for another organization's team, deliberately
 * indistinguishable (pkg/models/brazn_organization.go:302-305). Drive the
 * presence of the removal control off `teams[].primary`, which the server
 * carries for exactly this purpose, never off array position.
 */
export function deleteOrganizationTeam(teamId) {
  return forkSend('DELETE', forkV1Url(`brazn/organization/teams/${encodeURIComponent(teamId)}`));
}

/** PATCH /api/v2/teams/{id} — write 1 of the rename pair. */
export function renameTeam(teamId, name) {
  return forkSend('PATCH', forkV2Url(`teams/${encodeURIComponent(teamId)}`), {name});
}

/**
 * PATCH /api/v2/projects/{id} — write 2 of the rename pair, on the team's root
 * project (`organization.teams[].project_id`).
 *
 * PATCH with `title` alone, never PUT. PATCH is the one method where an omitted
 * `parent_project_id` means "leave it alone"; on a full update an omitted
 * parent is read as an explicit 0 (requestedParentID,
 * pkg/routes/managed_rules_teams.go:280-292), and a full update would also
 * blank every other project field.
 */
export function renameTeamRootProject(projectId, title) {
  return forkSend('PATCH', forkV2Url(`projects/${encodeURIComponent(projectId)}`), {title});
}

/**
 * Renaming a team is TWO writes, and both must be issued.
 *
 * CreateOrganizationTeam sets the team name and its root project's title from
 * the same string and links them nowhere (pkg/models/brazn_organization.go:330-341),
 * so neither write updates the other and one alone drifts the two apart
 * permanently.
 *
 * Sequential rather than concurrent: if the team rename is refused there is no
 * reason to have already renamed the project. Resolves to both results so the
 * caller can tell which half landed.
 */
export async function renameTeamEverywhere(teamId, projectId, name) {
  const team = await renameTeam(teamId, name);
  const project = await renameTeamRootProject(projectId, name);
  return {team, project};
}

/**
 * POST /api/v2/teams/{team}/members — `{username, admin}`, a USERNAME.
 *
 * `access-expanding` / `managed:"teams-only"` (route-classification.json:387),
 * NOT service-managed: the Teams edition passes. Under Personal the control is
 * disabled with a reason (ruling C8.3).
 */
export function addTeamMember(teamId, username, admin = false) {
  return forkSend('POST', forkV2Url(`teams/${encodeURIComponent(teamId)}/members`), {username, admin});
}

/**
 * DELETE /api/v2/teams/{team}/members/{username} — TEAM-SCOPED removal, and a
 * USERNAME in the path (pkg/routes/api/v2/team_members.go:82-84).
 *
 * This is NOT the same operation as POST /v1/organizations/members/removal,
 * which releases the seat and removes the person from the organization. The
 * prototype ships only this one — its modal says the person remains part of the
 * organization — and the prototype is the scope bar (bar 10, ruling C8.2).
 */
export function removeTeamMember(teamId, username) {
  return forkSend('DELETE', forkV2Url(
    `teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(username)}`,
  ));
}

/**
 * POST /api/v2/teams/{team}/members/{username}/admin — the body is IGNORED
 * (pkg/routes/api/v2/team_members.go:57); the route toggles. Username, not id.
 */
export function toggleTeamMemberAdmin(teamId, username) {
  return forkSend('POST', forkV2Url(
    `teams/${encodeURIComponent(teamId)}/members/${encodeURIComponent(username)}/admin`,
  ));
}

/* ------------------------------------------------------------------ *
 * 15. Commercial /v1 — live today
 * ------------------------------------------------------------------ *
 *
 * Every function below resolves to a `readCommercialResult`-shaped object and
 * NEVER throws on a refusal — including a `/v1` 401, a 403, a body whose
 * `outcome` is not affirmative, the SPA's index.html at 200, and a transport
 * failure. THE ONE EXCEPTION, stated because the earlier absolute claim was
 * false and callers took it at its word: a `SessionLostError` is thrown when
 * the FORK's own `/api/v1/user/token/refresh` refuses, which is a genuine
 * session loss no matter which call discovered it. `app.js`'s `dispatch` and
 * the two view modules already catch that class and defer to the terminal
 * surface, so no per-call `try` is needed for it — see `commercialFetch`.
 *
 * NONE of them is verifiable from this repository (bar 6) or in CI (bar 9): CI
 * starts no commercial service, so a green run must never be cited as evidence
 * that any of these works.
 *
 * EVERY IDENTIFIER ON THIS SERVICE IS A STRING, never a number: `isId`
 * (percy-http-27c95232.ts:1448) requires `typeof value === "string"`, and every
 * grammar here — `organization_id`, `member_user_id`, `invitation_id`,
 * `request_id`, `successor_user_id`, `to_user_id`, `team_id` — is validated
 * through it. These ids come from the commercial service's own identity
 * provider and are NOT the fork's numeric user ids. Sending a number is a bare
 * 400, so a caller that has one must not `Number()` a candidate id on its way
 * back in.
 *
 * WHERE THE BRIEF DID NOT STATE A BODY, THE BODY IS A PASS-THROUGH. api.js
 * invents no field names (bar 7, ruling C17) — an unexpected field is exactly
 * what a strict validator rejects with 200-plus-a-failure-`outcome`, the
 * hardest failure on this surface to debug.
 */

/**
 * POST /v1/organizations/invitations — invite by email.
 *
 * THE GRAMMAR IS CLOSED AND `idempotency_key` IS REQUIRED. `parseInvite`
 * (percy-http-27c95232.ts:1596-1609) allowlists exactly
 * `["email", "idempotency_key", "organization_id", "team_id"]` and then demands
 * `UUID_PATTERN.test(idempotency_key)` at :1602 — BEFORE the optional `team_id`
 * branch, so the key is unconditional. A null parse is a bare 400 at :2833-2834.
 * A body of `{organization_id, email}` therefore cannot parse: every invitation
 * this page sent was a guaranteed 400, and nothing in CI could see it (bar 9).
 * The key is defaulted here, the same seam `purchaseSeats` already defaults its
 * own at, so no caller can forget it.
 *
 * ONE KEY PER USER ACTION, not per attempt: `commercialFetch` replays the same
 * `init.body` on its single retry, so a refreshed retry converges on the
 * original invitation instead of sending a second one. That is what the key is
 * for — `inviteMember` runs under `runOnce`.
 *
 * `team_id` IS NOT SENT (ruling C17), AND THE REASON IS SCOPE, NOT ABSENCE.
 * An earlier version of this comment said the field was invented. That was
 * wrong and is corrected here, because on this project a citation is the
 * contract (bar 7): `parseInvite` allowlists `team_id` at :1598, `isId`s it at
 * :1607, and :1603-1606 documents that absent means the organization's primary
 * team. The field is real. It is not sent because the prototype has no team
 * picker and the prototype is the scope bar (bar 10), so no control on this
 * page can produce a value for it. The assertion stays as the mechanism that
 * keeps that true and keeps SPEC-BACKEND's negative test writable; if a team
 * picker is ever added, remove the assertion and send `team_id` as a string
 * (`MemberInvitationRequest.team_id?: string`, percy-service-27c95232.ts:566).
 *
 * THE REPLY CARRIES FOUR FIELDS AND THE PAGE OWES THE ADMINISTRATOR THREE OF
 * THEM: `{outcome, invited_user_id, invitation: {invitation_id, status,
 * expires_at} | null, seat_notice}` (percy-http-27c95232.ts:2854-2884). Read
 * them through §6b — `result.outcome`, `readInvitationRecord()`,
 * `readSeatNotice()`, `readInvitedUserId()` — each of which carries the meaning
 * of its own null. `seat_notice` in particular is not decoration: :2871-2883
 * calls it "an administrator being told what they are about to commit their
 * organization to, which is the whole of BRA-1075", and it is the one place an
 * amount crosses this boundary on purpose.
 */
export function inviteOrganizationMember(body, idempotencyKey) {
  if (body !== null && typeof body === 'object' && 'team_id' in body) {
    throw assertion('invitation-body-must-not-carry-team_id');
  }
  // The assertion runs first, and the key is minted lazily rather than in a
  // default parameter, so a body carrying `team_id` is refused even on a page
  // with no UUID source at all.
  return commercialPost('organizations/invitations', COMMERCIAL_OPS.INVITE_MEMBER, {
    idempotency_key: idempotencyKey ?? newIdempotencyKey(),
    // Spread last so an explicit key in the body wins over the default.
    ...body,
  });
}

/**
 * POST /v1/organizations/invitations/accept
 *
 * Body: `{invitation_id}` AND NOTHING ELSE, and NO idempotency key —
 * `parseAcceptInvitation` allowlists the single field (percy-http-27c95232.ts:1618-1624).
 * Do not copy the invite's key onto this call: `keysWithin` would refuse the
 * whole body and the route would answer 400. `invitation_id` is a string
 * (`isId`, :1448) like every id on this service.
 *
 * `already_member` is affirmative here as well as on the invite — see both
 * descriptors. A caller that renders "you have joined" must still read
 * `body.outcome` to tell a fresh admission from a seat already held.
 */
export function acceptOrganizationInvitation(body) {
  return commercialPost('organizations/invitations/accept', COMMERCIAL_OPS.ACCEPT_INVITATION, body);
}

/**
 * POST /v1/organizations/members/removal — organization-level removal, which
 * releases the seat.
 *
 * AVAILABLE BUT DELIBERATELY NOT SURFACED (ruling C8.2): the prototype has no
 * organization-level removal, only the team-scoped `removeTeamMember`. Kept
 * implemented so the two operations stay visibly distinct and nobody collapses
 * them into one control later.
 *
 * Body: `{organization_id, member_user_id}`, both strings, and NO idempotency
 * key — `parseRemoval` allowlists exactly those two (percy-http-27c95232.ts:1644-1650).
 * The removal grammar is closed the same way the invite's is, so adding a key
 * here to match the invite would turn every call into a 400.
 */
export function removeOrganizationMember(body) {
  return commercialPost('organizations/members/removal', COMMERCIAL_OPS.REMOVE_ORGANIZATION_MEMBER, body);
}

/**
 * GET /v1/team-access-requests — the administrator's join-request queue.
 * Answers `{requests: [...]}` and no `outcome`; a non-administrator is a 403.
 */
export function listTeamAccessRequests(query) {
  return commercialGet('team-access-requests', COMMERCIAL_OPS.LIST_TEAM_ACCESS_REQUESTS, query);
}

/**
 * POST /v1/team-access-requests/decide — approve or decline.
 *
 * BOTH decisions are affirmative: `declined` is the administrator's own choice
 * carried out. A caller must read `body.outcome` to word what it shows, and
 * `body.invitation_outcome` to explain a `not_admitted` refusal.
 */
export function decideTeamAccessRequest(body) {
  return commercialPost('team-access-requests/decide', COMMERCIAL_OPS.DECIDE_TEAM_ACCESS_REQUEST, body);
}

/**
 * POST /v1/team-access-requests/confirm
 *
 * UNREACHABLE WITH A USER BEARER: the route demands the relay's shared service
 * credential (percy-http-27c95232.ts:3278), so this page is answered 401 every
 * time. Kept only so the operation stays visible; nothing in the page calls it.
 */
export function confirmTeamAccessRequest(body) {
  return commercialPost('team-access-requests/confirm', COMMERCIAL_OPS.CONFIRM_TEAM_ACCESS_REQUEST, body);
}

/**
 * POST /v1/subscription/cancellation — answers dates, never an `outcome`.
 *
 * `idempotency_key` IS REQUIRED AND IS THE ONLY PERMITTED FIELD:
 * `parseCancellation` allowlists `["idempotency_key"]` and demands a UUID
 * (percy-http-27c95232.ts:1692-1698), so the pass-through this used to be could
 * only ever have produced a 400 — the same defect class as the invite, latent
 * because no control calls this yet. `user_id` is gone from the grammar on
 * purpose (:1679-1684): the subject is the resolved bearer.
 *
 * Defaulted here rather than left to the caller for the reason the invite gives:
 * a customer who double-clicks must record one cancellation, not two.
 *
 * THERE IS NO `body` PARAMETER, AND ITS REMOVAL IS THE POINT. This used to take
 * one and spread it over the key, so any field a caller passed was a guaranteed
 * 400 against a grammar that allows exactly one name — the same defect already
 * fixed on the invite, and latent here only because nothing calls this yet. The
 * absence of the parameter is what makes the grammar closed by construction
 * rather than by a comment, exactly as `transferAdministrator` has no
 * `from_user_id` parameter.
 */
export function cancelSubscription(idempotencyKey) {
  return commercialPost('subscription/cancellation', COMMERCIAL_OPS.CANCEL_SUBSCRIPTION, {
    idempotency_key: idempotencyKey ?? newIdempotencyKey(),
  });
}

/**
 * POST /v1/subscription/auto-renewal — answers `{auto_renewal: true}`.
 *
 * THE ONLY VALID BODY IS `{}`, SO THERE IS NO PARAMETER. `parseSubjectless`
 * (percy-http-27c95232.ts:1723-1726) checks `keysWithin(body, NO_FIELDS)` — an
 * empty allowed set — so ANY field refuses the whole body and the route answers
 * 400. A parameter documented as "must be left unset" is one an unlucky caller
 * sets; removing it is the same construction `cancelSubscription` above now
 * uses. `commercialPost` sends `{}` when no payload is passed, which is the
 * correct call — `{}` and not "no body at all", because `readJson` answers 400
 * for an empty stream (:1719-1721).
 */
export function setSubscriptionAutoRenewal() {
  return commercialPost('subscription/auto-renewal', COMMERCIAL_OPS.SET_SUBSCRIPTION_AUTO_RENEWAL);
}

/**
 * POST /v1/subscription/renewal-consent — answers `{renewal_consent_at}`.
 * Same closed empty grammar as auto-renewal above: `parseSubjectless`, so the
 * only valid body is `{}` and there is likewise no parameter to get it wrong
 * with.
 */
export function giveRenewalConsent() {
  return commercialPost('subscription/renewal-consent', COMMERCIAL_OPS.GIVE_RENEWAL_CONSENT);
}

/**
 * POST /v1/checkout/resume
 *
 * UNREACHABLE WITH A USER BEARER, exactly like the confirm route above: it
 * checks the shared service credential (percy-http-27c95232.ts:2172), so this
 * page is answered 401 every time. Since `commercialFetch` no longer treats a
 * `/v1` 401 as a fork-session loss, wiring this by accident now costs a visible
 * refusal rather than logging the user out.
 *
 * Body: `{user_id}` and nothing else, with NO idempotency key — the operation
 * creates nothing, so a key would imply there was something to replay
 * (percy-http-27c95232.ts:1452-1466).
 */
export function resumeCheckout(body) {
  return commercialPost('checkout/resume', COMMERCIAL_OPS.RESUME_CHECKOUT, body);
}

/** GET /v1/entitlements — the entitlement projection; no `outcome`. */
export function getEntitlements() {
  return commercialGet('entitlements', COMMERCIAL_OPS.GET_ENTITLEMENTS);
}

/**
 * GET /v1/account/successor-candidates — the admin-successor picker.
 *
 * An EMPTY list is an ordinary 200, not a refusal: it means no choice has to be
 * offered (percy-http-27c95232.ts:2976-2984). `ok` and `candidates.length === 0`
 * are two different facts and the caller must not collapse them — an empty list
 * is a sole-member administrator or a non-administrator, and BOTH may still
 * erase (see `eraseAccount`). A control that disables itself on an empty list
 * makes the sole-member administrator's erasure impossible.
 *
 * `{candidates: [{user_id}]}` — THE ID AND NOTHING ELSE, deliberately
 * (percy-http-27c95232.ts:2986-2988).
 *
 * READ THE LIST THROUGH `readSuccessorCandidates()`, WHICH CARRIES THE WHOLE
 * WARNING. In short, and because the previous version of this comment got it
 * wrong in a way a caller then shipped: these ids are the COMMERCIAL service's
 * own, and the fork roster's `user_id` is a local `int64` row id
 * (pkg/models/brazn_organization.go:69), so the two cannot be joined and no fork
 * response a browser can read carries the commercial id at all. The service's
 * own line — "a picker resolves display names from the fork" (:2968-2974) —
 * describes a capability the fork does not yet expose, not one this page has.
 */
export function listSuccessorCandidates() {
  return commercialGet('account/successor-candidates', COMMERCIAL_OPS.LIST_SUCCESSOR_CANDIDATES);
}

/**
 * POST /v1/account/erasure — THE delete-account path.
 *
 * The fork's own POST /api/v2/user/deletion/request is `service-managed` and
 * 403s for everyone (route-classification.json:408), so this replaces it.
 * `POST /api/v2/user/deletion/cancel` still works locally and stays a fork
 * call — see `cancelAccountDeletion`.
 *
 * SUCCESS IS 204 WITH NO BODY (percy-http-27c95232.ts:3071-3076), so
 * `result.body` is null on the happy path and a caller must not read anything
 * off it.
 *
 * Body: `{successor_user_id}` or nothing, and NO idempotency key —
 * `parseErasure` allowlists the single field (percy-http-27c95232.ts:1750-1759).
 *
 * "NOBODY" IS A LAWFUL AND COMMON ANSWER, not a missing input. An explicit
 * `null` and an omitted field mean the same thing (:1740-1748: "'nobody' is a
 * real, common and lawful answer — it is what the sole-member administrator
 * has"), and the handler SKIPS the successor-eligibility block entirely when
 * the candidate list is empty (:3033-3039), because an empty list is a
 * sole-member administrator or a non-administrator and `eraseAccount` ignores
 * the successor for both. So a caller must NOT collapse "no candidates" into
 * "cannot erase": `listSuccessorCandidates()` answering `ok:true` with zero
 * candidates means no choice has to be offered, and the correct next step is to
 * call `eraseAccount()` with no body at all.
 */
export function eraseAccount(body) {
  return commercialPost('account/erasure', COMMERCIAL_OPS.ERASE_ACCOUNT, body);
}

/* ------------------------------------------------------------------ *
 * 16. Commercial /v1 — contract only
 * ------------------------------------------------------------------ *
 *
 * These routes are not exposed yet. Implemented against the documented
 * contract, which is not the same as implementing the routes (bar 1) — the
 * calls work the moment the route lands. Their bodies ARE documented, so unlike
 * §15 they name their fields.
 *
 * WHAT ANSWERS THEM TODAY IS TWO DIFFERENT THINGS, and the guard refuses both.
 * The earlier blanket claim that all four "answer the SPA's index.html at 200"
 * was only half right:
 *
 *   * WHERE THE COMMERCIAL SERVICE IS ROUTED, its listener answers. Revoke is
 *     the odd one out: `POST /v1/organizations/invitations/revoke` IS claimed,
 *     by the prefix block at percy-http-27c95232.ts:2819
 *     (`path.startsWith("/v1/organizations/invitations")`), matches neither
 *     inner path, and falls to `bare(response, 404)` at :2905. Seats, the seat
 *     quote and admin-transfer are claimed by nothing and reach the listener's
 *     final `bare(response, 404)` at :3335. `bare` writes a status line with no
 *     content-type (:728-731), so all four are `COMMERCIAL_REFUSAL.HTTP`.
 *   * WHERE IT IS NOT — CI, and any instance with no commercial routing — the
 *     fork's static handler answers `/v1/...` with the SPA's index.html at HTTP
 *     200, and the guard's content-type check turns that into `NOT_JSON`. That
 *     check is the one that matters for bar 9: it is why CI cannot report these
 *     as working.
 *
 * BOTH SHAPES ARE INDISTINGUISHABLE FROM A REAL REFUSAL TO A CALLER READING
 * `status` ALONE, which is why all four descriptors carry `contractOnly: true`.
 * A 404 here does not mean "we could not find your organization"; it means the
 * feature has not shipped, and only the descriptor knows the difference. The
 * wording is `app.js`'s to choose — no user-facing string lives in this file —
 * but the fact it needs is declared here.
 */

/**
 * POST /v1/organizations/invitations/revoke
 *
 * The service method answers an invitation RECORD and no `outcome`, so this
 * expects none. If the handler that lands projects one, the value must be read
 * from that handler and added to `COMMERCIAL_OPS.REVOKE_INVITATION` — until then
 * an `outcome` arriving here is refused, which is the fail-closed residue.
 *
 * `invitationId` COMES FROM `inviteResult.body.invitation.invitation_id`, NESTED
 * (percy-http-27c95232.ts:2863-2870). There is no top-level `invitation_id` and
 * no `id` on the invite reply, so a caller reading either gets `null` and can
 * never offer this control. Both ids are strings (`isId`, :1448).
 */
export function revokeOrganizationInvitation(organizationId, invitationId) {
  return commercialPost('organizations/invitations/revoke', COMMERCIAL_OPS.REVOKE_INVITATION, {
    organization_id: organizationId,
    invitation_id: invitationId,
  });
}

/**
 * GET /v1/organizations/seats/quote?organization_id=&seats= — no charge.
 *
 * Answers `{organization_id, seats, seats_after, proration}`. READ IT THROUGH
 * `readSeatQuote()`, which states both traps: there is NO `message` field on
 * this body, and `proration: null` is an ordinary answer meaning the change
 * costs nothing now (percy-service-27c95232.ts:901-909) rather than a figure
 * that failed to arrive.
 */
export function quoteSeats(organizationId, seats) {
  return commercialGet('organizations/seats/quote', COMMERCIAL_OPS.QUOTE_SEATS, {
    organization_id: organizationId,
    seats,
  });
}

/** POST /v1/organizations/seats — change purchased seats. `changed` is the one affirmative. */
export function purchaseSeats(organizationId, seats, idempotencyKey = newIdempotencyKey()) {
  return commercialPost('organizations/seats', COMMERCIAL_OPS.PURCHASE_SEATS, {
    organization_id: organizationId,
    seats,
    idempotency_key: idempotencyKey,
  });
}

/**
 * POST /v1/organizations/admin-transfer.
 *
 * `from_user_id` IS THE RESOLVED BEARER AND NEVER A BODY FIELD. There is no
 * parameter for it here on purpose: a caller cannot pass one, so it cannot be
 * sent. It is declared on the RESULT (`AdminTransferResult.from_user_id`,
 * percy-service-27c95232.ts:543) and on the service-layer request the handler
 * composes (`AdminTransferRequest`, :531-538) — never on the wire body.
 *
 * `toUserId` MUST BE A STRING. `AdminTransferRequest.to_user_id` is declared
 * `string` at percy-service-27c95232.ts:535, and every id grammar on this
 * service goes through `isId`, which requires `typeof value === "string"`
 * (percy-http-27c95232.ts:1448). It is an opaque commercial-service account id,
 * not the fork's numeric user id, and it round-trips: the value comes from
 * `GET /v1/account/successor-candidates`, which projects `{user_id}` and
 * nothing else (percy-http-27c95232.ts:2986-2988). Passing the string the
 * picker was populated with is correct; converting it to a number is what would
 * be wrong.
 */
export function transferAdministrator(organizationId, toUserId, idempotencyKey = newIdempotencyKey()) {
  return commercialPost('organizations/admin-transfer', COMMERCIAL_OPS.TRANSFER_ADMINISTRATOR, {
    organization_id: organizationId,
    to_user_id: toUserId,
    idempotency_key: idempotencyKey,
  });
}

/*
 * POST /v1/organizations/rename IS DELIBERATELY NOT IMPLEMENTED.
 *
 * It is the one control with no route anywhere: no commercial route, no
 * service method, and models.Organization has no Name field. Ruling C8.1 keeps
 * the field rendered but DISABLED with a reason, and SPEC-BACKEND §5's negative
 * test asserts it issues no request. Exporting a function here would make that
 * test unwritable, so the absence of this export is the mechanism, not an
 * oversight. Do not add one until the route lands.
 */
