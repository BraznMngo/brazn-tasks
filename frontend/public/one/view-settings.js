/**
 * view-settings.js — the settings view (BRA-1358): Account, Organization, Team management.
 *
 * Ported from the prototype's `settingsPage()` / `accountSettings()` / `organizationSettings()` /
 * `teamManagement()` (prototype-pristine.html:1086-1133) and its fifteen settings modals
 * (:1150-1230). The prototype is the scope bar (bar 10): every control it draws is here, and
 * nothing it leaves out has been added. Every `isLive(` branch collapsed to its live arm, so the
 * demo fixtures, the role switcher and `state.accountDeleted` (the demo arm of
 * confirm-delete-account, :1405 — the live arm at :1404 opens the "Deletion requested" modal
 * instead) are gone with them.
 *
 * WHAT THIS FILE MAY DO. It renders markup and calls api.js. It defines no route, no service
 * method and no model field (bar 1), and it never builds a URL itself — every path below comes
 * from an api.js export, which is what keeps the /api/v1, /api/v2 and /v1 prefixes in one place
 * (bar 6, ruling C16).
 *
 * ONE DECLARED EXCEPTION, and it is declared rather than quiet: `readAvatarObjectUrl` (§1c) issues
 * its own `fetch` for `GET /api/v2/avatar/{username}`, because api.js exports the avatar UPLOAD and
 * the provider set but nothing that returns the image bytes, and this file may not add one to it.
 * The BASE still comes from api.js (`forkV2Url`), so the prefix decision bar 6 is about is still
 * made in one place, and the 401 path defers to api.js's own `refreshSession()` rather than
 * carrying a second copy of the refresh policy. The report asks for `api.getAvatarBlob()`, after
 * which the exception disappears.
 *
 * GATES ARE DECLARED, NEVER DECIDED HERE (ruling C4). Every gated node is emitted unconditionally
 * with `data-requires`; app.js resolves it and either hides it or leaves it visible, disabled and
 * carrying a reason. The one exception is a control refused by a fact no gate token can express —
 * an absent route, or `can_create_team:false` — which is emitted already refused and WITHOUT
 * `data-requires`, because `applyGates` releases any node whose gate passes and would hand the
 * control back (app.js `releaseControl`).
 *
 * BAR 8 IS THE REASON EVERY COMMERCIAL PATH LOOKS OVERBUILT. `/v1` answers 200 with the failure in
 * the body, and on an instance with no commercial service the fork's static handler answers with
 * the SPA's index.html at 200 as well. `api.readCommercialResult` (ruling C14) is therefore the
 * only thing allowed to decide, and no toast claims "sent", "revoked" or "transferred" before its
 * `ok` is true. Seat numbers are re-read from the FORK organization endpoint afterwards, never
 * taken from the commercial response.
 */

import * as api from './api.js';
import {t} from './i18n.js';
import {
  DENY,
  closeModal,
  currentUserId,
  describeCommercialRefusal,
  describeForkError,
  editionMessageKey,
  getOrganization,
  getSettings,
  getTeamState,
  getUser,
  getViewState,
  openModal,
  readSeatMeter,
  refusalText,
  registerActions,
  registerView,
  reloadOrganization,
  renderRefusal,
  requestRender,
  setViewState,
  toast,
} from './app.js';

/* ------------------------------------------------------------------ *
 * 1. Local primitives
 * ------------------------------------------------------------------ */

const NS = 'settings';

/**
 * The six icons these three tabs use, lifted verbatim from the prototype's `ICON` map
 * (prototype-pristine.html:512-539). They live here rather than in app.js because app.d.ts is the
 * view modules' contract and exports no icon helper: a view owns its own glyphs.
 */
const ICON = Object.freeze({
  close: '<path d="M6 6l12 12M18 6 6 18"/>',
  edit: '<path d="m4 16-.8 4 4-.8L18 8.4 15.6 6z"/><path d="m14.5 7.2 2.3 2.3"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  download: '<path d="M12 3v12M8 11l4 4 4-4"/><path d="M5 20h14"/>',
  image: '<rect x="4" y="4" width="16" height="16" rx="2"/><circle cx="9" cy="9" r="1.5"/><path d="m5 17 4-4 3 3 2-2 5 5"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
});

/** `aria-hidden` on every glyph, exactly as the prototype's `ic()` does (:541). */
function ic(name) {
  return `<svg viewBox="0 0 24 24" aria-hidden="true">${ICON[name]}</svg>`;
}

function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[ch]));
}

/**
 * A translated string, escaped for insertion into markup. Every user-facing string in this file
 * goes through here or through `t()` — there are no English literals (brief, i18n).
 */
function tx(key, params) {
  return esc(t(key, params));
}

/** ` · ` as a catalogue value, because the separator is not the same glyph in every language. */
function joinParts(parts) {
  return parts.filter(Boolean).join(t('one.common.separator'));
}

/**
 * The refused-in-markup state: a control the SERVER or the ROUTE TABLE refuses, not a gate.
 * Carries no `data-requires` on purpose — `applyGates` would release it (see the file header).
 * `isRefused` in app.js walks ancestors, so putting this on a group blocks its buttons too; the
 * button inside still gets its own `aria-disabled`, because a wrapper cannot announce anything.
 */
function refusedGroup(reason, extraClass = '') {
  const classes = extraClass === '' ? 'is-refused' : `${extraClass} is-refused`;
  return `class="${esc(classes)}" data-deny-reason="${esc(reason)}"`;
}

/**
 * The sentence that goes with it. `data-refusal-source="server"` matters: `applyGates` clears
 * only `gate`-sourced sentences on a re-render, so this survives one (app.js `clearRefusal`).
 */
function refusalNote(text, {notice = false} = {}) {
  const className = notice ? 'notice refusal-text' : 'refusal-text';
  return `<p class="${className}" data-refusal-source="server">${esc(text)}</p>`;
}

function displayName(user) {
  const name = typeof user?.name === 'string' && user.name.trim() !== '' ? user.name.trim() : null;
  return name ?? user?.username ?? '';
}

/** Two letters at most, from the display name — the prototype's `initials()` (:597). */
function initials(user) {
  const source = displayName(user);
  const words = String(source).trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return '?';
  const letters = words.length === 1 ? words[0].slice(0, 2) : words[0][0] + words[1][0];
  return letters.toUpperCase();
}

function scratch() {
  return getViewState(NS);
}

/* ------------------------------------------------------------------ *
 * 1b. The account overlay — "the section must reflect the new value"
 *
 * `app.js` reads `GET /api/v2/user` ONCE, in `boot()` (app.js:1838), and exports no way to re-read
 * it: `reloadOrganization()` exists, `reloadUser()` does not. `requestRender()` therefore redraws
 * the account tab from the SAME `state.user` it was drawn from before the write, which is why the
 * timezone select "jumps back" — the value reached the server, the page then re-rendered the stale
 * one over it and `fillTimezones` re-`selected` that (PM finding 4). Display name behaved the same
 * way; nobody noticed because the modal closes over it.
 *
 * Until app.js grows that reload (requested in the report — this file may not edit it), the view
 * keeps its OWN fresh copy in its scratch state and every account-tab read goes through the two
 * accessors below, which prefer it and fall back to app.js's boot copy. Nothing else on the page
 * reads them, so nothing else can drift: the overlay is per-view scratch, exactly what
 * `getViewState` is for.
 * ------------------------------------------------------------------ */

/** The user the account tab draws: the post-write re-read when there is one, else boot's copy. */
function accountUser() {
  const fresh = scratch().user;
  return fresh === undefined || fresh === null ? getUser() : fresh;
}

/** `settings` from the same body, with the same preference. Keys are snake_case on the wire. */
function accountSettings() {
  const fresh = scratch().settings;
  return fresh === undefined || fresh === null ? getSettings() : fresh;
}

/**
 * Re-read the user after a successful account write. AWAIT IT BEFORE THE TOAST: a success message
 * that lands ahead of the value it describes is the shape that made finding 4 look like a failed
 * save rather than a stale render.
 *
 * A failed re-read is NOT reported as a failed write — the write succeeded, and saying otherwise
 * would invite the user to repeat an edit that already landed. It is logged, the overlay keeps the
 * value it had, and the next render is simply as stale as it was before. A lost session is the one
 * exception and is rethrown, because app.js owns the terminal surface for it.
 */
async function refreshAccount() {
  try {
    const user = await api.getCurrentUser();
    setViewState(NS, {user: user ?? null, settings: user?.settings ?? null});
  } catch (err) {
    if (err instanceof api.SessionLostError) throw err;
    console.error('[one/settings] account re-read failed', err);
  }
}

/**
 * S5. THE ADDRESS IS NOT ON THE WIRE, and that is a server fact rather than a bug in this file.
 *
 * `GET /api/v2/user` embeds `user.User`, whose `Email` is `json:"email,omitempty"`
 * (pkg/user/user.go:96) — but the handler reads the row through
 * `models.GetUserOrLinkShareUser` → `user.GetUserByID` → `getUser(s, u, false)`, and that last
 * argument is what blanks it: `if !withEmail { userOut.Email = "" }` (pkg/user/user.go:332-334).
 * `omitempty` then drops the key entirely. The v1 `/user` handler is the same code
 * (pkg/routes/api/v1/user_show.go:64) and the JWT carries no address either
 * (pkg/modules/auth/auth.go:244-249), so NO endpoint this page may call answers with it.
 *
 * This reads the field anyway rather than hard-coding the absence: `GetUserWithEmail` is one
 * argument away in the same function, so the day the fork passes `true` there this line starts
 * showing the address with no change here. Until then the sentence beside it says why it is blank,
 * which is the honest render — an empty row is what the PM reported as "does not show the current
 * email address", and it looked broken because nothing on screen said it was not.
 */
function accountEmail() {
  const email = accountUser()?.email;
  return typeof email === 'string' ? email.trim() : '';
}

/* ------------------------------------------------------------------ *
 * 1c. The avatar image
 *
 * `GET /api/v2/avatar/{username}?size=` is a real route (pkg/routes/api/v2/avatar.go:52) and is
 * AUTHENTICATED like every other one (its own Description says so, :50) — it is not in
 * `unauthenticatedAPIPaths` (pkg/routes/routes.go:349-379). So a bare `<img src>` cannot load it:
 * an `<img>` sends no `Authorization` header and the page's bearer lives in a module variable, not
 * a cookie. It has to be fetched and turned into an object URL, which is what the Vue app does for
 * the same route (frontend/src/models/user.ts:29, `avatarService.getBlobUrl`).
 *
 * That is why both views drew initials and said so. The initials fallback stays — it is what shows
 * before the bytes arrive and whenever they do not — but the circle now shows the real avatar,
 * which is what makes an upload visible (PM finding 1).
 * ------------------------------------------------------------------ */

/** 58 px circle (one.css `.profile-avatar`) at 2×, so it is not soft on a retina display. */
const AVATAR_SIZE = 116;

/**
 * Identity of the image currently wanted: whose it is, and which upload generation.
 * `avatarVersion` is bumped by `saveAvatar` AFTER BOTH CALLS, and that bump is the whole
 * cache-busting mechanism — a new key forces a re-read, and the re-read produces a new object URL,
 * so the `<img src>` changes and the browser cannot paint the old bytes.
 */
function avatarKey(user) {
  return `${user?.username ?? ''}|${scratch().avatarVersion ?? 0}`;
}

/**
 * Read the avatar once per key. Re-entrant by design: `mount` runs on every render and this is
 * what makes it a no-op after the first.
 *
 * The read is fire-and-forget on purpose. It is decorative, `mount` is synchronous, and a slow or
 * refused avatar must never delay or fail the tab around it.
 */
function ensureAvatar() {
  const user = accountUser();
  const username = user?.username ?? '';
  if (username === '') return;

  const key = avatarKey(user);
  if (scratch().avatarKey === key) return;
  // Written BEFORE the await: a second render during the fetch must not start a second one.
  setViewState(NS, {avatarKey: key});

  readAvatarObjectUrl(username).then((url) => {
    // The key moved while this was in flight — another upload, or another account. The bytes are
    // for a picture nobody is asking for any more, so the URL is released rather than shown.
    if (scratch().avatarKey !== key) {
      if (url !== null) URL.revokeObjectURL(url);
      return;
    }
    const previous = scratch().avatarUrl ?? null;
    if (previous === url) return;
    setViewState(NS, {avatarUrl: url});
    // Revoked only after the state no longer points at it, and only once: revoking a URL that is
    // still an `<img src>` blanks the picture that is currently on screen.
    if (typeof previous === 'string') URL.revokeObjectURL(previous);
    requestRender();
  });
}

/**
 * The one request this view issues itself, and the deviation is recorded rather than quiet.
 *
 * The PREFIX still comes from `api.js` (`forkV2Url`), which is the whole point of the file
 * header's "never builds a URL itself" rule — bar 6 and ruling C16 are about which of the three
 * bases a path is hung off, and that decision is still made in one place. What is NOT in api.js is
 * a binary avatar read; it exports the upload and the provider set but nothing that returns the
 * image, and this file may not add one to it. Requested in the report as `getAvatarBlob()`, after
 * which this function becomes a one-line call.
 *
 * `cache: 'reload'` IS THE CACHE-BUSTER, in place of a `?v=` parameter. The parameter would be the
 * usual trick, but `avatarGet`'s input declares `username` and `size` and nothing else
 * (pkg/routes/api/v2/avatar.go:37-40) — hanging an undeclared parameter off a Huma-validated route
 * to defeat a browser cache is betting the fix on a validator's leniency. `cache: 'reload'`
 * bypasses the HTTP cache for this request outright, needs nothing from the server, and cannot be
 * rejected by it.
 *
 * The 401 path mirrors `api.js`'s own `authedFetch` (api.js:337-352) rather than reinventing it:
 * `refreshSession()` is api.js's SINGLE IN-FLIGHT refresh promise, so a stale token here waits on
 * the same refresh every other call waits on and cannot start a second one. One retry, then give
 * up — a second 401 on a token minted seconds ago is not a token problem. Nothing here marks the
 * session lost: an avatar is not the request that should end someone's session.
 */
async function readAvatarObjectUrl(username) {
  const url = api.forkV2Url(`avatar/${encodeURIComponent(username)}?size=${AVATAR_SIZE}`);
  try {
    let res = await avatarFetch(url, api.getToken());
    if (res.status === 401) {
      const token = await api.refreshSession();
      if (token === null) return null;
      res = await avatarFetch(url, token);
    }
    // No `outcome` check and no JSON check: this is a fork route returning image bytes, not a
    // commercial one (bar 8 is about `/v1`). A non-2xx simply means "no picture", and the
    // initials fallback is already on screen.
    if (!res.ok) return null;
    const blob = await res.blob();
    return blob.size === 0 ? null : URL.createObjectURL(blob);
  } catch (err) {
    console.error('[one/settings] avatar read failed', err);
    return null;
  }
}

function avatarFetch(url, token) {
  const headers = {Accept: 'image/*'};
  if (typeof token === 'string' && token !== '') headers.Authorization = `Bearer ${token}`;
  return fetch(url, {method: 'GET', headers, credentials: 'same-origin', cache: 'reload'});
}

/* ------------------------------------------------------------------ *
 * 2. Organization-shaped reads
 *
 * All of these read the FORK organization payload that app.js already loaded. The commercial
 * service is never the source for a seat number or a roster (brief, "Mis-wired calls").
 * ------------------------------------------------------------------ */

function organizationTeams() {
  const teams = getOrganization()?.teams;
  return Array.isArray(teams) ? teams : [];
}

function organizationMembers() {
  const members = getOrganization()?.members;
  return Array.isArray(members) ? members : [];
}

/**
 * The team the Team-management tab is pointed at. Defaults to the payload's own `primary` team
 * and falls back to the first listed — `primary` is server-carried precisely so a client never
 * infers "the first one" (brief, §1.4).
 */
function selectedTeam() {
  const teams = organizationTeams();
  if (teams.length === 0) return null;
  const wanted = scratch().selectedTeamId;
  const chosen = teams.find((team) => String(team.team_id) === String(wanted));
  return chosen ?? teams.find((team) => team.primary === true) ?? teams[0];
}

/** The roster `GET /api/v2/teams/{id}` returned, or `[]` when that read was refused (ruling C11). */
function teamMembers(teamId) {
  const members = getTeamState(teamId)?.team?.members;
  return Array.isArray(members) ? members : [];
}

/** A 403 on the team read is the EXPECTED case for the commercially provisioned primary team. */
function isTeamMember(teamId) {
  const me = currentUserId();
  if (me === null) return false;
  return teamMembers(teamId).some((member) => member?.id === me);
}

/** `administrator` is never null in a 200 (brief, §1.4), but a bad payload must not throw. */
function organizationAdministratorId() {
  const administrator = getOrganization()?.administrator;
  return administrator?.user_id ?? administrator?.id ?? null;
}

function isOrganizationAdministrator(member) {
  const adminId = organizationAdministratorId();
  return adminId !== null && member?.id != null && String(member.id) === String(adminId);
}

/* ------------------------------------------------------------------ *
 * 3. The view
 * ------------------------------------------------------------------ */

function render(ctx) {
  return hero() + nav(ctx) + `<div class="settings-body">${tabBody(ctx)}</div>`;
}

/**
 * The hero. `#app` already carries `settings-window` (app.js `render`), so this must not wrap
 * itself in it again the way the prototype's `settingsPage()` did (:1089).
 *
 * The logo pair is the shell's `<template>`; reading its `innerHTML` serialises both `<img>`s, and
 * app.js hydrates their `data-i18n-alt` once they are ordinary nodes inside `#app`.
 */
function hero() {
  const user = accountUser();
  const logo = document.getElementById('brandLogo')?.innerHTML ?? '';
  return `<header class="settings-hero">${logo}<div>
    <div class="settings-title">${tx('one.brand.settingsPage')}</div>
  </div><div class="settings-role">
    <strong>${esc(displayName(user))}</strong>
    <span data-requires="edition">${esc(editionLine())}</span>
  </div></header>`;
}

/**
 * "ONE Team Edition · Administrator". The wire strings (`personal-cloud`, `teams-cloud`) are
 * identifiers and are never displayed — the mapping is a lookup (ruling C10). The whole line is
 * gated `edition`, so U — which is every CI session — renders no edition at all rather than
 * defaulting to one (S9/T13).
 */
function editionLine() {
  const edition = editionLabel();
  if (edition === null) return '';
  return t('one.edition.withRole', {edition, role: roleLabel()});
}

/**
 * Every key below is written out as a LITERAL inside its own `t()` call rather than passed through
 * as a variable. Ruling C10 has the fork-guards step prove each key exists by grepping `t('…')`
 * literals, and a key reached through a variable is invisible to it — a missing one would then
 * degrade at runtime with nothing reporting it, which is the silent failure §4 warns about.
 */
function editionLabel() {
  const key = editionMessageKey();
  if (key === null) return null;
  return key === 'one.edition.personal' ? t('one.edition.personal') : t('one.edition.teams');
}

function roleLabel() {
  if (getOrganization() !== null) return t('one.role.administrator');
  return editionMessageKey() === 'one.edition.personal'
    ? t('one.role.personalUser')
    : t('one.role.teamMember');
}

/**
 * The tab strip. `settingsNav()` returned '' when only one tab existed (:1094), so a non-
 * administrator saw no nav bar at all; gating the `<nav>` itself on `admin` reproduces exactly
 * that, and is the case ruling C4 names as a legitimate HIDE — the whole surface is absent for
 * this user, and 403 on the organization read is the ORDINARY answer, not an error (F3).
 */
function nav(ctx) {
  // Resolved here, not carried as keys: the guard greps `t('…')` literals (ruling C10).
  const tabs = [
    ['account', t('navigation.settings'), ''],
    ['organization', t('organization.title'), ' data-requires="admin"'],
    ['team', t('one.org.teamManagement'), ' data-requires="admin"'],
  ];
  const buttons = tabs.map(([id, label, gate]) => `<button data-settings-tab="${id}"
    class="${ctx.route.tab === id ? 'on' : ''}"${gate}>${esc(label)}</button>`).join('');
  return `<nav class="settings-nav" data-requires="admin">${buttons}</nav>`;
}

function tabBody(ctx) {
  if (ctx.route.tab === 'organization') return organizationTab();
  if (ctx.route.tab === 'team') return teamTab();
  return accountTab();
}

/* ------------------------------------------------------------------ *
 * 4. Account tab (S1-S14)
 * ------------------------------------------------------------------ */

function accountTab() {
  return `<section class="settings-section">
    <div class="settings-heading"><div><h2>${tx('user.settings.title')}</h2></div></div>
    <div class="settings-grid">
      ${profileCard()}
      ${otherCard()}
      ${subscriptionCard()}
      ${dataCard()}
      ${dangerZone()}
    </div>
  </section>`;
}

/**
 * S4/S5/S6/S7. Email and Password are VISIBLE under managed mode (brief; commit f203aae6) and
 * survive the write-restricted overlay because their routes are marked `write:"credentials"` —
 * so neither carries the `write` gate that avatar and display name do.
 *
 * THE AVATAR CIRCLE SHOWS THE REAL AVATAR (PM finding 1). `§1c` reads
 * `GET /api/v2/avatar/{username}` authenticated and hands back an object URL; the initials are the
 * fallback for before it arrives and for whenever it does not. The `<img>` is sized inline because
 * one.css gives `.profile-avatar` no `img` rule and this file may not edit that stylesheet —
 * `.task-user-avatar img` (one.css:323) is the shape being reproduced. `border-radius:50%` on the
 * image itself rather than `overflow:hidden` on the circle, for the same reason.
 *
 * The email row renders `accountEmail()`, which is empty on every instance today for a reason that
 * is not this file's (see `accountEmail`), so the row says why instead of rendering nothing.
 */
function profileCard() {
  const user = accountUser();
  const email = accountEmail();
  const avatarUrl = scratch().avatarKey === avatarKey(user) ? scratch().avatarUrl ?? null : null;
  const face = typeof avatarUrl === 'string' && avatarUrl !== ''
    ? `<img src="${esc(avatarUrl)}" alt="" aria-hidden="true"
        style="inline-size:100%;block-size:100%;object-fit:cover;border-radius:50%;display:block">`
    : esc(initials(user));
  return `<div class="card settings-card">
    <div class="card-title">${tx('user.settings.sections.personalInformation')}</div>
    <div class="profile-line" style="margin-top:16px">
      <div class="profile-avatar" id="profileAvatar">${face}</div>
      <div>
        <strong style="font-size:13px">${esc(displayName(user))}</strong>
        <div class="help">${tx('one.common.atUsername', {username: user?.username ?? ''})}</div>
        <div class="profile-actions">
          <button class="btn small" data-action="avatar" data-requires="write">
            ${ic('image')} ${tx('user.settings.avatar.uploadAvatar')}</button>
          <button class="btn small" data-action="edit-profile" data-requires="write">
            ${ic('edit')} ${tx('one.settings.editName')}</button>
        </div>
      </div>
    </div>
    <div class="setting-row">
      <div>
        <div class="setting-name">${tx('user.auth.email')}</div>
        <div class="setting-desc" id="accountEmailValue">${
          email === '' ? tx('one.settings.emailUnavailable') : esc(email)}</div>
      </div>
      <button class="btn small" data-action="change-email">${tx('one.common.change')}</button>
    </div>
    <div class="setting-row">
      <div><div class="setting-name">${tx('user.auth.password')}</div></div>
      <button class="btn small" data-action="change-password">${tx('one.common.change')}</button>
    </div>
  </div>`;
}

/**
 * S8/S9. The timezone list is fetched once and sorted in api.js (the route documents itself as
 * unsorted). Until it arrives the select holds the stored zone alone, so it never shows a value
 * the account does not have.
 *
 * THE ZONE IS READ FROM THE OVERLAY, NOT FROM BOOT (PM finding 4). `getSettings()` is the copy
 * `boot()` took and never refreshes, so after a successful save this markup re-emitted the OLD
 * zone as the only `selected` option and `fillTimezones` then re-`selected` it in the rebuilt
 * list — the value the user chose was gone from the screen while being correct on the server. §1b
 * has the mechanism; `saveTimezone` awaits the re-read before it toasts.
 *
 * The subscription row that used to sit here has moved to `subscriptionCard()` (PM finding 6): the
 * PM asked for a subscription SECTION, and a badge in the "Other" card is not one.
 */
function otherCard() {
  const timezone = accountSettings()?.timezone ?? '';
  return `<div class="card settings-card">
    <div class="card-title">${tx('one.settings.other')}</div>
    <div class="setting-row">
      <div>
        <div class="setting-name">${tx('user.settings.general.timezone')}</div>
        <div class="setting-desc">${tx('one.settings.timezoneHelp')}</div>
      </div>
      <select class="select setting-control" id="timezone" data-requires="write">
        <option selected>${esc(timezone)}</option>
      </select>
    </div>
  </div>`;
}

/**
 * PM FINDING 6 — the subscription section, and it is deliberately one fact wide.
 *
 * The edition comes from the `brazn_edition` JWT claim through app.js's `editionMessageKey()`
 * (ruling C1): `personal-cloud` is the only defined constant and anything else, INCLUDING ABSENT,
 * is the Teams shape. The wire strings are identifiers and are never displayed — the mapping is a
 * lookup, and both branches are written as `t()` LITERALS so the fork-guards key sweep can see
 * them (ruling C10), exactly as `editionLabel()` does.
 *
 * NO GATE ON THIS CARD. `data-requires="edition"` is one of `GATES_THAT_HIDE` (app.js:93), so
 * gating it would delete the section the PM asked to exist for every session with no claim — which
 * is every CI run and every unentitled account. The card is emitted unconditionally and the
 * no-claim case states itself; that is the same choice `membersCard` makes for an organization
 * with no team, and for the same reason (an absent fact and an unreadable one are different).
 *
 * NOTHING ELSE IS SHOWN, and the omissions are omissions rather than gaps to fill. A renewal date,
 * a seat total, a price or an auto-renewal state would each have to come from a `/v1` body whose
 * shape is not among the extracted commercial sources — `GET /v1/entitlements` exists and api.js
 * exports it, but no field of its response is documented at the verified commit, and bar 7's
 * discipline covers response fields as much as routes. They are listed in the report instead. A
 * restricted view showing the edition truthfully beats a billing dashboard showing a guess.
 */
function subscriptionCard() {
  const edition = subscriptionLabel();
  return `<div class="card settings-card wide">
    <div class="card-title">${tx('one.settings.subscription')}</div>
    <div class="setting-row">
      <div>
        <div class="setting-name" id="subscriptionEdition">${
          edition === null ? tx('one.subscription.unknown') : esc(edition)}</div>
        <div class="setting-desc">${tx('one.subscription.help')}</div>
      </div>
    </div>
  </div>`;
}

/**
 * The subscription wording, which is NOT the header line's wording: the hero says what edition the
 * session is running under ("ONE Team Edition · Administrator") and this says what the account is
 * subscribed to ("ONE Teams Subscription", the PM's own example). Two sentences about one claim,
 * so they get two keys rather than one key used in two voices.
 */
function subscriptionLabel() {
  const key = editionMessageKey();
  if (key === null) return null;
  return key === 'one.edition.personal'
    ? t('one.subscription.personal')
    : t('one.subscription.teams');
}

/**
 * S10/S11. ONE tile, not two: the Import card is deleted whole. `csv/detect` and `csv/preview` are
 * `ordinary` and SUCCEED, and only `csv/migrate` is `managed:"disabled"` — two working steps
 * followed by a bare 404 is the worst outcome, so the card goes rather than the button (brief).
 */
function dataCard() {
  return `<div class="card settings-card wide">
    <div class="card-title">${tx('one.settings.dataTitle')}</div>
    <div class="data-actions">
      <div class="data-tile">
        <h4>${tx('one.settings.export.title')}</h4>
        <p>${tx('one.settings.export.text')}</p>
        <button class="btn small" data-action="export-data">
          ${ic('download')} ${tx('one.settings.export.action')}</button>
      </div>
    </div>
  </div>`;
}

/**
 * S12. Survives the write-restricted overlay (`write:"deletion"`).
 *
 * PM FINDING 5 — "Cancel scheduled deletion" IS GONE, control, modal and handler. What it did, for
 * the record and for whoever reinstates it: it opened a one-field modal asking for the current
 * password and posted it to `POST /api/v2/user/deletion/cancel`, which clears a deletion that had
 * already been scheduled against the account and re-enables it; the server's own message was
 * toasted verbatim because `DeletionScheduledAt` is `json:"-"` (pkg/user/user.go:122) and the
 * browser therefore cannot know whether a deletion was pending, which is exactly why the control
 * was unconditional and why the PM did not recognise it — it was offered to every account,
 * including the overwhelming majority with nothing scheduled.
 *
 * `api.cancelAccountDeletion()` is UNTOUCHED in api.js and the route still works, so reinstating
 * this is a markup row plus the two handlers and nothing more. If billing turns out to need it,
 * the honest shape is to render it only when a scheduled deletion is readable — which needs
 * `DeletionScheduledAt` on the user body, a fork change, not a change here.
 */
function dangerZone() {
  return `<div class="card settings-card wide danger-zone">
    <div class="setting-row">
      <div>
        <div class="setting-name">${tx('one.settings.deleteAccount.title')}</div>
        <div class="setting-desc">${tx('user.deletion.text1')}</div>
      </div>
      <button class="btn small danger" data-action="delete-account">
        ${tx('one.settings.deleteAccount.title')}</button>
    </div>
  </div>`;
}

/* ------------------------------------------------------------------ *
 * 5. Organization tab (O1-O5)
 * ------------------------------------------------------------------ */

function organizationTab() {
  const teams = organizationTeams();
  const organization = getOrganization();
  return `<section class="settings-section" data-requires="admin">
    <div class="settings-heading"><div><h2>${tx('organization.title')}</h2></div></div>
    <div class="settings-grid"><div class="card settings-card wide">
      <div class="org-identity-row">
        <div class="org-identity">
          ${teams.length > 1 ? teamListBlock(teams) : teamIdentityBlock(teams[0] ?? null)}
          ${organizationIdentityBlock(organization)}
        </div>
        <span class="role-badge admin">${tx('organization.members.roleAdministrator')}</span>
      </div>
      <div class="card-title">${tx('one.org.yourProjects')}</div>
      <div class="org-map">
        <div class="org-tile">
          <div class="k">${tx('one.org.tile.privateKind')}</div>
          <div class="v">${tx('one.org.tile.privateName')}</div>
          <div class="d">${tx('one.org.tile.privateDesc')}</div>
        </div>
        ${teams.map(teamTile).join('')}
        <div class="org-tile">
          <div class="k">${tx('one.org.tile.publicKind')}</div>
          <div class="v">${tx('one.org.tile.publicName')}</div>
          <div class="d">${tx('one.org.tile.publicDesc')}</div>
        </div>
      </div>
    </div></div>
  </section>`;
}

/**
 * O1/O2. `models.Organization` HAS NO NAME FIELD — `id` is an identifier, and it is what the
 * prototype's name slot can honestly show.
 *
 * The pencil stays, refused, with its reason (ruling C8.1 — "read-only until
 * POST /v1/organizations/rename lands", not "remove"). It is the only control on the page with no
 * route anywhere: no commercial route, no service method, no model field. api.js deliberately
 * exports no rename function and NO HANDLER IS REGISTERED for `rename-org` below, so the negative
 * test — the field renders and issues no request — holds by construction rather than by care.
 */
function organizationIdentityBlock(organization) {
  return `<div ${refusedGroup(DENY.NO_ROUTE, 'org-identity-item')} style="flex-wrap:wrap">
    <div class="meta">
      <span>${tx('organization.general.name')}</span>
      <strong id="orgNameValue">${esc(organization?.id ?? '')}</strong>
    </div>
    <button class="mini-edit" data-action="rename-org" aria-disabled="true"
      aria-label="${tx('one.org.renameOrgAria')}">${ic('edit')}</button>
    ${refusalNote(t('one.deny.renameOrg'))}
  </div>`;
}

/** O3/O4, single-team shape (:1114). */
function teamIdentityBlock(team) {
  if (team === null) return '';
  return `<div class="org-identity-item">
    <div class="meta">
      <span>${tx('one.org.teamLabel')}</span>
      <strong>${esc(team.name ?? '')}</strong>
    </div>
    ${renameTeamButton(team)}
  </div>`;
}

/** O3/O4, multi-team shape (:1113). The membership badge is real — see §2.5 of SPEC-ROLES. */
function teamListBlock(teams) {
  const lines = teams.map((team) => `<div class="org-team-line">
    <div class="team-meta">
      <span>${tx('one.org.teamLabel')}</span>
      <strong>${esc(team.name ?? '')}</strong>
    </div>
    ${isTeamMember(team.team_id)
      ? `<span class="role-badge admin">${tx('team.attributes.member')}</span>`
      : '<span></span>'}
    ${renameTeamButton(team)}
  </div>`).join('');
  return `<div class="org-team-list">${lines}</div>`;
}

/**
 * Renaming is gated on the acting user's `admin` bit IN THAT TEAM, not on organization
 * administration: `Team.CanUpdate` is `IsAdmin` → `team_members.admin = true`
 * (pkg/models/teams_permissions.go:34-64). An organization administrator with no admin row on
 * team X cannot rename X, and the entitlement claim does not help — which is the difference
 * between a Rename button that works and one that 403s after the user has typed a name.
 */
function renameTeamButton(team) {
  return `<button class="mini-edit" data-action="rename-team"
    data-team="${esc(team.team_id)}" data-requires="team-admin write"
    aria-label="${tx('one.org.renameTeamAria', {team: team.name ?? ''})}">${ic('edit')}</button>`;
}

function teamTile(team) {
  const membership = isTeamMember(team.team_id) ? ` ${t('one.org.tile.youAreMember')}` : '';
  return `<div class="org-tile">
    <div class="k">${tx('one.org.teamLabel')}</div>
    <div class="v">${esc(team.name ?? '')}</div>
    <div class="d">${tx('one.org.tile.teamDesc')}${esc(membership)}</div>
  </div>`;
}

/* ------------------------------------------------------------------ *
 * 6. Team management tab (M1-M15)
 * ------------------------------------------------------------------ */

function teamTab() {
  const team = selectedTeam();
  // Read once: `readSeatMeter` warns when `seats_per_team` has drifted from the page's copy of the
  // constant, and three reads per render would print that warning three times.
  const meter = readSeatMeter(getOrganization());
  return `<section class="settings-section" data-requires="admin">
    <div class="settings-heading" style="flex-wrap:wrap;gap:12px">
      <div style="min-width:0">
        <h2>${tx('one.org.teamManagement')}</h2>
        <p>${tx('one.org.teamManagementSubtitle')}</p>
      </div>
      ${addTeamButton(meter)}
    </div>
    <div class="settings-grid">
      ${seatsCard(meter)}
      ${administratorCard()}
      ${membersCard(team)}
      ${pendingInvitationsCard()}
    </div>
  </section>`;
}

/**
 * M5. `can_create_team` is the SERVER'S OWN DECISION and is rendered, never recomputed
 * (pkg/models/brazn_organization.go:126-131). When it is false the button is refused in markup
 * rather than gated, because no gate token can express it and a gate that passes would release
 * the control (file header).
 *
 * `seats_purchased: null` is neither 0 nor unlimited — it is "this instance cannot answer" — so
 * the refusal names that case with its own sentence instead of quoting a seat count.
 */
function addTeamButton(meter) {
  if (meter.canCreateTeam) {
    return `<button class="btn small primary team-action-btn" data-action="add-team"
      data-requires="admin write">${ic('plus')} ${tx('organization.teams.create')}</button>`;
  }
  const sentence = meter.purchased === null || meter.requiredForNextTeam === null
    ? t('organization.teams.capped.unknown')
    : `${t('organization.teams.capped.title')} ${t('organization.teams.capped.text', {seats: meter.requiredForNextTeam})}`;
  return `<div ${refusedGroup(DENY.SERVER)} style="display:flex;flex-wrap:wrap;justify-content:flex-end">
    <button class="btn small primary team-action-btn" data-action="add-team" aria-disabled="true">
      ${ic('plus')} ${tx('organization.teams.create')}</button>
    ${refusalNote(sentence)}
  </div>`;
}

/**
 * M1/M2. Every number here comes from the fork organization endpoint — the brief is explicit that
 * the commercial service is not the source for the seat meter.
 *
 * The requirement line is the NEXT team's cost: `seats_per_team × (teams_used + 1)`, and member
 * count is not in it. The prototype's `requiredSeats()` (:603) folded members and pending
 * invitations into it and is deleted rather than adapted.
 */
function seatsCard(meter) {
  const pending = scratch().pendingInvites?.length ?? 0;
  const help = joinParts([
    meter.requiredForNextTeam === null ? '' : t('one.org.seats.required', {count: meter.requiredForNextTeam}),
    meter.occupied === null ? '' : t('one.org.seats.members', {count: meter.occupied}),
    pending === 0 ? '' : t('one.org.seats.pending', {count: pending}),
    meter.teamsUsed === null ? '' : t('one.org.seats.teams', {count: meter.teamsUsed}),
  ]);
  const subtitle = meter.purchased === null
    ? t('organization.teams.capped.unknown')
    : t('one.org.seats.inSubscription', {count: meter.purchased});

  return `<div class="card settings-card">
    <div class="card-title">${tx('organization.seats.title')}</div>
    <div class="card-sub">${esc(subtitle)}</div>
    <div class="seat-meter"><span id="seatMeterFill"></span></div>
    <div class="help">${esc(help)}</div>
    <div class="profile-actions">
      ${meter.purchased === null
        ? `<div ${refusedGroup(DENY.SERVER)} style="display:flex;flex-wrap:wrap">
             <button class="btn small" data-action="add-seat" aria-disabled="true">${
               tx('organization.seats.add')}</button>
             ${refusalNote(t('organization.teams.capped.unknown'))}
           </div>`
        : `<button class="btn small" data-action="add-seat" data-requires="admin write">
             ${tx('organization.seats.add')}</button>`}
    </div>
  </div>`;
}

/** M3/M4. `administrator` comes from the fork read; the transfer itself is commercial. */
function administratorCard() {
  const administrator = getOrganization()?.administrator ?? {};
  return `<div class="card settings-card">
    <div class="card-title">${tx('organization.administration.current')}</div>
    <div class="card-sub">${tx('one.org.administratorText')}</div>
    <div class="profile-line" style="margin-top:14px">
      <div class="avatar">${esc(initials(administrator))}</div>
      <div>
        <strong style="font-size:12px">${esc(displayName(administrator))}</strong>
        <div class="help">${esc(administrator.email ?? '')}</div>
      </div>
    </div>
    <div class="profile-actions">
      <button class="btn small" data-action="transfer-admin" data-requires="admin">
        ${tx('organization.administration.transfer.action')}</button>
    </div>
  </div>`;
}

/**
 * M6/M7/M8. The roster is per-team and can be legitimately unreadable: `Team.CanRead` requires
 * membership and the administrator is commonly not a member of the commercially provisioned
 * primary team (ruling C11). An unreadable roster renders as an explicit unavailable state — the
 * prototype rendered an empty list, which is a fake fact ("this team has nobody in it").
 */
function membersCard(team) {
  const teams = organizationTeams();
  const teamId = team === null ? '' : String(team.team_id);
  const state = team === null ? null : getTeamState(teamId);
  const selector = teams.length > 1
    // The floor lives in `.team-selector` as `min-inline-size:210px` (task.html); repeating it here
    // as a physical `min-width` duplicated it in the property family the page converted away from.
    ? `<select class="select team-selector" id="teamSelector">${teams.map((entry) =>
        `<option value="${esc(entry.team_id)}"${String(entry.team_id) === teamId ? ' selected' : ''}>${esc(entry.name ?? '')}</option>`).join('')}</select>`
    : '';

  const rows = state?.readable
    ? (teamMembers(teamId).length === 0
      ? `<div class="empty-state">${tx('one.org.membersEmpty')}</div>`
      : teamMembers(teamId).map((member) => memberRow(member, teamId)).join(''))
    : '';

  return `<div class="card settings-card wide">
    <div class="section-head" style="flex-wrap:wrap">
      <div>
        <div class="card-title">${tx('team.edit.members')}</div>
        <div class="card-sub">${tx('one.org.membersSubtitle')}</div>
      </div>
      <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        ${selector}
        <button class="btn small primary team-action-btn" data-action="invite"
          data-requires="admin">${ic('plus')} ${tx('one.org.addMember')}</button>
      </div>
    </div>
    ${teamId === ''
      // AN ABSENT TEAM AND AN UNREADABLE ONE ARE DIFFERENT FACTS. `decideGate` resolves a `team`
      // token with an empty `data-team` to `UNREADABLE_TEAM` (app.js `teamFact`), which is the
      // right answer for a team we could not READ and the wrong one for an organization that
      // simply has none — it printed "We cannot read this team's members right now" about a team
      // that does not exist, and offered a retry for a read that was never attempted. The gate is
      // therefore not declared here at all; the true sentence is stated directly and it names the
      // cure, which is the Create a team button already in this tab's heading.
      ? `<div class="member-list">${refusalNote(t('one.deny.noTeams'), {notice: true})}</div>`
      : `<div class="member-list" data-requires="team" data-team="${esc(teamId)}">${rows}</div>`}
  </div>`;
}

/**
 * The Remove control is TEAM-SCOPED — the modal says so and the route is
 * `DELETE /api/v2/teams/{team}/members/{username}`. The organization-level removal
 * (`POST /v1/organizations/members/removal`) is a different operation with no control on this
 * page (ruling C8.2). The organization administrator's own row carries no Remove, as in the
 * prototype (:1133).
 */
function memberRow(member, teamId) {
  const isAdministrator = isOrganizationAdministrator(member);
  const me = currentUserId();
  const suffix = me !== null && member?.id === me ? ` ${t('one.common.you')}` : '';
  return `<div class="member-row">
    <div class="avatar">${esc(initials(member))}</div>
    <div class="member-meta">
      <strong>${esc(displayName(member))}${esc(suffix)}</strong>
      <span>${esc(member?.email ?? '')}</span>
    </div>
    <div class="member-actions" style="flex-wrap:wrap">
      <span class="role-badge ${isAdministrator ? 'admin' : ''}">${isAdministrator
        ? tx('organization.members.roleAdministrator')
        : tx('organization.members.roleMember')}</span>
      ${isAdministrator ? '' : `<button class="btn small ghost" data-action="remove-member"
        data-username="${esc(member?.username ?? '')}" data-team="${esc(teamId)}"
        data-requires="team-admin write">${tx('organization.teams.remove')}</button>`}
    </div>
  </div>`;
}

/**
 * M13. THERE IS NO LIST ROUTE. The `/v1` inventory has create, accept and (contract-only) revoke;
 * `GET /v1/organizations/invitations` does not exist and inventing one is barred (bar 7). So the
 * card renders a cannot-list state rather than the prototype's "No pending invitations for this
 * team" (:1131) — claiming zero asserts a fact the page never read, which is the same defect
 * bar 8 names.
 *
 * Rows created in THIS session are appended optimistically and are the only rows that can exist.
 *
 * `one.org.seatReserved` IS NOT EMITTED ON THESE ROWS, and its absence is the fix rather than an
 * omission. A pending invitation reserves nothing: "Pending invitations deliberately reserve no
 * seat (and under this model they need not), so two invitations outstanding at once will each say
 * they add one" (percy-service-27c95232.ts:610-613). The seat is taken at ACCEPTANCE, under the
 * organization lock, by `admitInvitedMember`. Printing "Seat reserved" beside a row asserted a
 * commercial fact the service explicitly denies — the same fake-success direction bar 8 guards,
 * one level below the guard. "Invitation pending" is the whole of what this page read.
 */
function pendingInvitationsCard() {
  const invites = scratch().pendingInvites ?? [];
  const rows = invites.map((invite, index) => `<div class="member-row">
    <div class="avatar">?</div>
    <div class="member-meta">
      <strong>${esc(invite.email)}</strong>
      <span>${tx('one.org.invitePending')}</span>
    </div>
    <div class="member-actions" style="flex-wrap:wrap">
      ${invite.invitationId
        ? `<button class="btn small ghost" data-action="revoke-invite" data-index="${index}"
             data-requires="admin write">${tx('one.org.revoke')}</button>`
        : ''}
    </div>
  </div>`).join('');

  return `<div class="card settings-card wide">
    <div class="card-title">${tx('one.org.pendingInvitations')}</div>
    ${refusalNote(t('one.deny.noRoute'), {notice: true})}
    ${seatNoticeLine()}
    <div class="member-list" style="margin-top:12px">${rows}</div>
  </div>`;
}

/**
 * The seat position the commercial service composed on the last successful invite, as it composed
 * it. This is the ONE place a `/v1` number is rendered, and it is not the seat meter: the meter's
 * source is the fork organization endpoint and stays that way (brief, "Mis-wired calls"). What
 * this line says is narrower and is what `SeatNotice` is for — the position at the moment that
 * invitation was sent, which the service itself calls "ADVISORY, like the eligibility check beside
 * it, and for the same reason" (percy-service-27c95232.ts:606-617).
 *
 * Only the two counts that are true in the PRESENT tense are rendered: `seats` is "Seats PURCHASED
 * today" (:620-621) and `users` is "People holding one today" (:622-623). `seats_after` and
 * `proration` are held back for the reasons `confirm-invite` sets out; both need catalogue keys
 * this page does not have, and approximating them with the keys it does have would state a future
 * purchase as a present one.
 *
 * THAT IT ECHOES THE SEATS CARD IS THE POINT, NOT A DUPLICATION BUG. The seats card counts the
 * fork's entitlement projections; this counts what the commercial service itself holds. In the
 * ordinary case the two agree and the line reads as confirmation. When they DISAGREE the
 * projection has lagged the purchase, and an administrator who has just been told an invitation
 * was sent is exactly the person who needs to see it — silently preferring one number would hide
 * the only symptom of that lag this page can show. Neither number moves the meter: the meter's
 * source is unchanged.
 */
function seatNoticeLine() {
  const notice = scratch().seatNotice;
  if (notice === null || typeof notice !== 'object') return '';
  const parts = joinParts([
    Number.isInteger(notice.seats) ? t('one.org.seats.inSubscription', {count: notice.seats}) : '',
    Number.isInteger(notice.users) ? t('one.org.seats.members', {count: notice.users}) : '',
  ]);
  return parts === '' ? '' : `<div class="help">${esc(parts)}</div>`;
}

/* ------------------------------------------------------------------ *
 * 7. Mount — everything that cannot be a template string
 * ------------------------------------------------------------------ */

function mount(root, ctx) {
  installChangeListeners();

  if (ctx.route.tab === 'account') {
    ensureTimezones();
    fillTimezones(root);
    // After the select, not before: `ensureAvatar` may call `requestRender()` when the bytes land,
    // and the render it triggers re-enters this function from the top.
    ensureAvatar();
    return;
  }

  if (ctx.route.tab === 'team') {
    const meter = readSeatMeter(getOrganization());
    const fill = root.querySelector('#seatMeterFill');
    // A null denominator gets a zero-width bar, never a full one: "we cannot read how many seats
    // you bought" must not paint as "every seat is taken".
    if (fill !== null) fill.style.inlineSize = `${Math.round((meter.fillRatio ?? 0) * 100)}%`;
  }
}

function ensureTimezones() {
  const state = scratch();
  if (state.timezonesStatus !== undefined) return;
  setViewState(NS, {timezonesStatus: 'loading'});
  api.listTimezones().then((zones) => {
    setViewState(NS, {timezones: zones, timezonesStatus: 'ready'});
    requestRender();
  }).catch((err) => {
    // Not fatal: the stored zone still shows, and the select simply cannot offer alternatives.
    console.error('[one/settings] timezone list failed', err);
    setViewState(NS, {timezonesStatus: 'failed'});
  });
}

/**
 * `accountSettings()`, NOT `getSettings()` — this is the second half of PM finding 4. The list is
 * rebuilt on every render, so whichever zone this function calls `current` is the one the select
 * ends up showing; reading boot's copy is what put the pre-save value back under the user's cursor
 * every time and made a saved zone look like a rejected one.
 */
function fillTimezones(root) {
  const select = root.querySelector('#timezone');
  const zones = scratch().timezones;
  if (select === null || !Array.isArray(zones) || zones.length === 0) return;
  const current = accountSettings()?.timezone ?? '';
  // A stored zone the server no longer lists is kept as an option rather than dropped: without it
  // the select would silently display someone else's first zone as if it were this account's.
  const options = current !== '' && !zones.includes(current) ? [current, ...zones] : zones;
  select.innerHTML = options.map((zone) =>
    `<option${zone === current ? ' selected' : ''}>${esc(zone)}</option>`).join('');
}

/**
 * `change` is not delegated by app.js — its registry is click-only — and the prototype keyed every
 * change handler on element id from one document listener (:1488-1517). Same shape here, and
 * installed ONCE: `#avatarInput` lives in the shell and is never replaced, so binding it inside
 * `mount` would stack a listener per render.
 */
let changeListenersInstalled = false;

function installChangeListeners() {
  if (changeListenersInstalled || typeof document === 'undefined') return;
  changeListenersInstalled = true;

  document.addEventListener('change', (event) => {
    const target = event.target;
    if (target === null || target === undefined) return;

    if (target.id === 'timezone') {
      saveTimezone(target.value);
      return;
    }
    if (target.id === 'teamSelector') {
      setViewState(NS, {selectedTeamId: target.value});
      requestRender();
      return;
    }
    if (target.id === 'deleteSuccessor') {
      // The prototype's :1489 — the confirm button stays disabled until a successor is chosen.
      const button = document.getElementById('confirmDeleteAccountBtn');
      if (button !== null) button.disabled = target.value === '';
      return;
    }
    if (target.id === 'newAdmin') {
      // The same guard on the other irreversible handover. Both selects open on an empty
      // placeholder, so neither confirm button can fire against a successor nobody chose.
      const button = document.getElementById('confirmTransferBtn');
      if (button !== null) button.disabled = target.value === '';
      return;
    }
    if (target.id === 'avatarInput') {
      const file = target.files?.[0];
      target.value = '';
      if (file !== undefined) saveAvatar(file);
    }
  });
}

/* ------------------------------------------------------------------ *
 * 8. Writes — account
 * ------------------------------------------------------------------ */

/**
 * S8. `PUT /api/v2/user/settings/general` REPLACES, destructively; api.js does the GET, the merge
 * and the write in one call so no caller can forget the read half.
 */
async function saveTimezone(timezone) {
  // Its own try/catch: this runs from the raw `change` listener below, not through app.js's
  // `dispatch`, so nothing else would catch the rejection or report the refusal.
  try {
    await api.saveGeneralSettings({timezone});
    // THE RE-READ IS THE FIX, and it is awaited BEFORE the toast and before the render (PM
    // finding 4). Without it the render below redrew boot's zone over the one just saved.
    await refreshAccount();
    toast(t('user.settings.general.savedSuccess'));
    requestRender();
    // NOT re-derived here, and stated rather than hidden: `buildFormatters` is app.js's and is
    // called once in `boot()`, so every date on the task view keeps formatting in the OLD zone
    // until the page is reloaded. The settings tab itself formats no dates, so nothing on this
    // screen is wrong — but the page as a whole is briefly inconsistent, and app.js is where that
    // is fixable. Requested in the report.
  } catch (err) {
    console.error('[one/settings] timezone save failed', err);
    toast(refusalText(describeForkError(err)));
  }
}

/**
 * S4 / PM FINDING 1. Two calls — upload, then set the provider — kept per the brief. Ruling C12
 * forbids a test asserting the second is REQUIRED (the upload alone already persists the provider
 * today); it is idempotent, costs one request on a rare action, and keeps this page correct if
 * `baseUserUpdateColumns` ever changes upstream.
 *
 * THE SEQUENCE WAS ALREADY CORRECT AND IS VERIFIED HERE: `api.saveAvatar` awaits
 * `PUT /api/v2/user/settings/avatar` and only then `PUT /api/v2/user/settings/avatar/provider`
 * (api.js:1470-1474), and this `await` covers BOTH — so everything below runs after the second
 * call, never after the first. What was missing was anything to refresh: the circle drew initials
 * and no avatar was ever read, so there was nothing for a successful upload to change.
 *
 * `avatarVersion` is bumped FIRST. It is the identity of the image wanted (`avatarKey`), so the
 * bump is what makes the next `ensureAvatar` treat the picture on screen as the wrong one and
 * re-read it — and the re-read goes out with `cache: 'reload'`, so the browser cannot answer it
 * with the bytes it already has for that URL, which is the cache the PM identified.
 */
async function saveAvatar(file) {
  try {
    await api.saveAvatar(file);
  } catch (err) {
    console.error('[one/settings] avatar save failed', err);
    toast(refusalText(describeForkError(err)));
    return;
  }
  setViewState(NS, {avatarVersion: (scratch().avatarVersion ?? 0) + 1});
  // Caught rather than propagated: this runs from the raw `change` listener, so a rethrown
  // SessionLostError becomes an unhandled rejection. app.js already draws the terminal surface
  // from its own `onSessionLost` subscription, so there is nothing for this path to add.
  try {
    await refreshAccount();
  } catch (err) {
    console.error('[one/settings] account re-read after avatar upload failed', err);
    return;
  }
  toast(t('user.settings.avatar.setSuccess'));
  requestRender();
}

/* ------------------------------------------------------------------ *
 * 9. Modals
 * ------------------------------------------------------------------ */

/**
 * The prototype's `modal(title, body, foot)` factory (:629), with one difference: the title
 * arrives ALREADY TRANSLATED. Taking a key here would put every modal title behind a variable and
 * out of reach of the key guard, which greps for `t('…')` literals (ruling C10).
 */
function modal(title, body, foot = '') {
  return openModal(`<div class="modal-scrim" data-modal-scrim="true"><div class="modal">
    <div class="modal-head">
      <h3>${esc(title)}</h3>
      <button class="icon-btn" data-action="modal-close"
        aria-label="${tx('misc.closeDialog')}">${ic('close')}</button>
    </div>
    <div class="modal-body">${body}</div>
    ${foot === '' ? '' : `<div class="modal-foot" style="flex-wrap:wrap">${foot}</div>`}
  </div></div>`);
}

function footCancel() {
  return `<button class="btn" data-action="modal-close">${tx('misc.cancel')}</button>`;
}

function fieldValue(id) {
  return String(document.getElementById(id)?.value ?? '').trim();
}

/**
 * NEVER trimmed. A password may legitimately begin or end with a space, and trimming one silently
 * turns a correct credential into a wrong one — which surfaces as an unexplainable 412 from the
 * server rather than as a bug here.
 */
function secretValue(id) {
  return String(document.getElementById(id)?.value ?? '');
}

/*
 * NO LOCAL COMMERCIAL-REFUSAL TABLE LIVES HERE, and that is a decision rather than an omission.
 *
 * Every `/v1` refusal in this file goes through `app.js`'s `describeCommercialRefusal`, which now
 * owns three tables: the service's own sentence first (ruling C4), then
 * `COMMERCIAL_OUTCOME_MESSAGE_KEY` for a refused `outcome` — including `not_invitable`, the only
 * refusal `POST /v1/organizations/invitations` can return — and then
 * `COMMERCIAL_STATUS_MESSAGE_KEY` for the bare statuses `bare()` writes with no body at all
 * (percy-http-27c95232.ts:728-731). Nothing here needs to know which operation was refused: a
 * bare 403 is `not_administrator` on every route that sends one, and a bare 404 is "the
 * subscription service does not have that", which is true both for a handle it does not know and
 * for a §16 route that has not landed — a distinction the status alone cannot support, so the
 * shared sentence deliberately does not claim it.
 *
 * A second copy of that policy in this view is how the two would drift, and the one thing a view
 * could add — per-operation context — is precisely the thing the shared table is careful not to
 * assert. So the call sites below pass the result straight through.
 */

/**
 * Put a refusal on the modal's footer so the numbers and the sentence stay on screen with the
 * form still filled in — the 409 case depends on it (F5).
 */
function refuseModal(refusal) {
  const foot = document.querySelector('#modalRoot .modal-foot');
  if (foot === null) {
    toast(refusalText(refusal));
    return;
  }
  let node = foot.querySelector(':scope > .refusal-text');
  if (node === null) {
    node = document.createElement('p');
    node.className = 'refusal-text';
    foot.appendChild(node);
  }
  renderRefusal(node, {...refusal, source: 'server'});
}

/**
 * THE FAILURE PATH FOR EVERY MODAL WRITE ON THIS TAB, fork and commercial alike.
 *
 * Without it a refused write fell through to app.js's `dispatch`, which toasts and nothing more —
 * so a wrong current password on Update email or Update password left the user staring at a modal
 * still full of their own typing with the only explanation already faded away. Ruling C4 is
 * explicit that a refusal belongs ON the control with the server's own sentence; a toast alone is
 * the "missing affordance reads as a bug" case it was written against.
 *
 * The modal is NOT closed and the sentence is NOT paraphrased. The toast is kept alongside it
 * because `toast()` is the only path into `#a11yLive` — a refusal a screen reader never hears is
 * the same defect one screen. `view-task.js` `reportModalFailure` is the same shape for the same
 * reason; these two are the page's one modal-failure convention.
 *
 * A lost session is rethrown, never rendered: app.js owns a terminal surface for it and a second
 * report of the same fact reads as retryable.
 */
function reportModalFailure(err) {
  if (err instanceof api.SessionLostError) throw err;
  console.error('[one/settings] modal write failed', err);
  const refusal = describeForkError(err);
  refuseModal(refusal);
  toast(refusalText(refusal));
}

/* ------------------------------------------------------------------ *
 * 10. Actions
 * ------------------------------------------------------------------ */

registerActions({
  /* --- account ---------------------------------------------------- */

  avatar: () => {
    document.getElementById('avatarInput')?.click();
  },

  'edit-profile': () => {
    modal(t('one.settings.editProfile'), `<label class="label">${tx('user.settings.general.newName')}</label>
      <input class="input" id="profileName" value="${esc(displayName(accountUser()))}">`,
      `${footCancel()}<button class="btn primary" data-action="save-profile">${tx('misc.save')}</button>`);
  },

  'save-profile': async () => {
    try {
      await api.saveGeneralSettings({name: fieldValue('profileName')});
      // Awaited before the toast, so the card behind the closing modal already carries the new
      // name. `requestRender()` alone redrew boot's copy — the same defect as finding 4, one
      // field over, and invisible only because the modal covers the card while it happens.
      await refreshAccount();
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('user.settings.general.savedSuccess'));
    requestRender();
  },

  /**
   * PM FINDING 3 — the modal was wrong in three ways and is rebuilt.
   *
   * WAS: one field labelled "New email address", PREFILLED, and a current-password field.
   * NOW: the old address, read-only, above an EMPTY new-address field, and the password field
   * kept — because the route requires it (see `save-email`).
   *
   * WHY THE OLD FIELD PREFILLED WITH THE USER'S NAME. Nothing in this file put it there:
   * `getUser()?.email` is `undefined` on every instance (see `accountEmail`), so the value
   * attribute rendered empty. What filled it was the BROWSER. A bare `<input type="email">`
   * immediately above `<input type="password" autocomplete="current-password">` is the exact
   * shape of a sign-in form, so password managers and Chrome's own autofill treated it as the
   * username slot and wrote the stored account identity into it. Two attributes break that, and
   * they work as a pair: the read-only field now claims `autocomplete="username"`, which gives the
   * manager the designated slot it was looking for AND one it will not write to (browsers do not
   * autofill a readonly input), while the new address claims `autocomplete="off"` so it is no
   * longer the best candidate for that role. The password field keeps `current-password`, which is
   * correct and is what lets a manager offer the right secret.
   *
   * The read-only field is a real, focusable `<input>` rather than static text on purpose: the
   * PM's requirement is that the user can SEE what they are changing, and an input is what a
   * screen reader announces alongside its label. `readonly` (not `disabled`) keeps it in the
   * accessibility tree and selectable, so the address can still be copied.
   */
  'change-email': () => {
    const current = accountEmail();
    modal(t('user.settings.updateEmailTitle'), `
      <label class="label" for="currentEmail">${tx('one.settings.currentEmail')}</label>
      <input class="input" id="currentEmail" type="email" readonly aria-readonly="true"
        autocomplete="username" value="${esc(current)}">
      ${current === '' ? `<div class="help">${tx('one.settings.emailUnavailable')}</div>` : ''}
      <label class="label" for="newEmail">${tx('user.settings.updateEmailNew')}</label>
      <input class="input" id="newEmail" type="email" autocomplete="off">
      <label class="label" for="emailPassword">${tx('user.settings.currentPassword')}</label>
      <input class="input" id="emailPassword" type="password" autocomplete="current-password">`,
      `${footCancel()}<button class="btn primary" data-action="save-email">${tx('one.settings.updateEmail')}</button>`);
  },

  /**
   * A WRONG CURRENT PASSWORD IS A 412 HERE, and it is the single most likely failure on this tab.
   * It must land next to the field that caused it, not in a toast that fades off a modal the user
   * is still looking at.
   *
   * THE PASSWORD FIELD STAYS BECAUSE THE ROUTE REQUIRES IT, checked rather than assumed:
   * `PUT /api/v2/user/settings/email` binds `{new_email, password}`
   * (pkg/routes/api/v2/user_settings.go:191-195) and hands both to
   * `user.ChangeUserEmail(ctx, s, doer, in.Body.Password, in.Body.NewEmail)` (:209), whose first
   * act is `CheckPasswordForOwnAccount(ctx, s, u, password)` (pkg/user/update_email.go:43). An
   * empty password is a refusal, not an omission. `new_email` additionally carries
   * `valid:"email,length(0|250),required"`, which v2 answers as a 422 (see
   * pkg/webtests/huma_user_settings_test.go:103-104) — which is why an empty new address is
   * stopped here rather than sent.
   */
  'save-email': async () => {
    const next = fieldValue('newEmail');
    // Not sent empty: the server would answer 422 for a field the user simply has not filled in,
    // and `required` is a validator message, not an explanation.
    if (next === '') return;
    try {
      await api.changeEmail(next, secretValue('emailPassword'));
      // The address will NOT come back changed today — no route returns it (see `accountEmail`) —
      // but `status` moves to email-confirmation-required when the mailer is on
      // (pkg/user/update_email.go:82), so the account is re-read rather than assumed unchanged.
      await refreshAccount();
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('user.settings.updateEmailSuccess'));
    requestRender();
  },

  'change-password': () => {
    modal(t('user.settings.newPasswordTitle'), `
      <label class="label">${tx('user.settings.currentPassword')}</label>
      <input class="input" id="pwCurrent" type="password" autocomplete="current-password">
      <label class="label">${tx('user.settings.newPassword')}</label>
      <input class="input" id="pwNew" type="password" autocomplete="new-password">`,
      `${footCancel()}<button class="btn primary" data-action="save-password">${tx('one.settings.updatePassword')}</button>`);
  },

  'save-password': async () => {
    try {
      await api.changePassword(secretValue('pwCurrent'), secretValue('pwNew'));
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    // Every other session is invalidated by this route; this one keeps its access token.
    toast(t('user.settings.passwordUpdateSuccess'));
  },

  /**
   * S10. The prototype read `GET /api/v2/user/export` first to decide whether "Download current"
   * was enabled (:1219). api.js exposes no export-status read, and bar 7 forbids adding one here,
   * so the modal makes NO readiness claim and lets the download report the server's own answer.
   * `one.settings.export.ready` / `.notReady` are consequently unused — recorded, not silently
   * dropped.
   */
  'export-data': () => {
    modal(t('one.settings.export.title'), `
      <div class="card-sub">${tx('one.settings.export.modalText')}</div>
      <label class="label">${tx('user.settings.currentPassword')}</label>
      <input class="input" id="exportPassword" type="password" autocomplete="current-password">`,
      `${footCancel()}
       <button class="btn" data-action="download-export">${tx('one.settings.export.download')}</button>
       <button class="btn primary" data-action="request-export">${tx('one.settings.export.request')}</button>`);
  },

  'request-export': async () => {
    try {
      await api.requestExport(secretValue('exportPassword'));
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('user.export.success'));
  },

  /**
   * The link is APPENDED before it is clicked and removed afterwards. A programmatic click on a
   * detached `<a download>` is ignored outright by some engines, which would have made this button
   * do nothing at all while still toasting "The export was downloaded" — a fake success with no
   * server involved.
   */
  'download-export': async () => {
    let blob;
    try {
      blob = await api.downloadExport(secretValue('exportPassword'));
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'one-tasks-export.zip';
    link.hidden = true;
    document.body.appendChild(link);
    link.click();
    link.remove();
    // Revoked on the next turn, not inline: revoking the URL in the same task cancels the download
    // in some browsers, and the prototype's inline revoke (:1409) is the shape that bug wears.
    setTimeout(() => {URL.revokeObjectURL(url);}, 0);
    closeModal();
    toast(t('one.toast.exportDownloaded'));
  },

  'delete-account': () => {
    modal(t('user.deletion.title'), `<div class="notice">
      <strong>${tx('one.settings.deleteAccount.confirmText')}</strong>${tx('user.deletion.text1')}
      </div><div class="help">${tx('misc.cannotBeUndone')}</div>`,
      `${footCancel()}<button class="btn danger" data-action="delete-account-second">${tx('one.common.continue')}</button>`);
  },

  /**
   * S13. Only the organization administrator must hand over first. The successor list is
   * commercial (`GET /v1/account/successor-candidates`); when it cannot be read — which is exactly
   * what CI looks like — the confirm button is refused with that reason rather than sending a
   * guessed `to_user_id`.
   *
   * THREE ANSWERS, NOT TWO, and collapsing the last two is what trapped the sole-member
   * administrator behind a permanently disabled button:
   *
   *   * not an administrator            → no handover exists; confirm straight away.
   *   * `ok` with candidates            → offer the picker; the transfer runs first.
   *   * `ok` with an EMPTY list         → **also confirm straight away.** An empty list is a real
   *     answer, not a refusal: `listSuccessorCandidates` "returns [] for a missing account, a
   *     non-administrator and an organization of one alike, because the question is 'must I offer
   *     a choice' and the true answer to all three is no" (percy-http-27c95232.ts:2979-2984), and
   *     `parseErasure` names the case in as many words — "'nobody' is a real, common and lawful
   *     answer — it is what the sole-member administrator has" (:1745-1746). api.js says the same
   *     at its `listSuccessorCandidates` export: `ok` and `candidates.length === 0` are two
   *     different facts and must not be collapsed.
   *   * `!ok`                           → the picker with the refusal on it, confirm disabled.
   *
   * `deleteTransfer` carries the decision to `confirm-delete-account`, which must not re-derive it
   * from an empty `<select>`: an absent picker and an unanswered one look identical in the DOM.
   */
  'delete-account-second': async () => {
    if (getOrganization() === null) {
      renderErasureConfirmModal();
      return;
    }

    const result = await api.listSuccessorCandidates();
    const candidates = result.ok ? readCandidates(result.body) : [];

    if (result.ok && candidates.length === 0) {
      renderErasureConfirmModal();
      return;
    }

    setViewState(NS, {deleteTransfer: 'required'});
    const options = candidates.map((candidate) =>
      `<option value="${esc(candidate.id)}">${esc(candidate.label)}</option>`).join('');

    modal(t('one.settings.deleteAccount.confirmTitle'), `<div class="notice">
      <strong>${tx('one.settings.deleteAccount.successorTitle')}</strong>${
        tx('one.settings.deleteAccount.successorText')}</div>
      <label class="label">${tx('one.org.newAdministrator')}</label>
      <select class="select" id="deleteSuccessor">
        <option value="">${tx('one.org.selectMember')}</option>${options}</select>`,
      `${footCancel()}<button class="btn danger" id="confirmDeleteAccountBtn"
        data-action="confirm-delete-account" disabled>${tx('one.settings.deleteAccount.title')}</button>`);

    if (!result.ok) refuseModal(describeCommercialRefusal(result));
  },

  /**
   * J11. Transfer first, and STOP on a failed transfer: an organization with no administrator is
   * the state `OrganizationFor` refuses to serve, and every organization surface would 403
   * afterwards.
   *
   * `POST /v1/account/erasure` is sent with NO BODY. Its body is not documented anywhere
   * (SPEC-BACKEND §"commercial /v1": "not stated"), and ruling C17 makes inventing a field the
   * same defect as inventing a route — so the modal also collects no password it could not send.
   */
  'confirm-delete-account': async () => {
    const organization = getOrganization();
    // `deleteTransfer === 'none'` is the sole-member administrator and the non-administrator
    // alike: there is nobody to hand over to, which is a lawful state and not a missing step.
    //
    // The test is `!== 'none'` and NOT `=== 'required'`, so an unset flag still demands the
    // handover. Failing the other way would erase an administrator's account without transferring
    // the role — the state `OrganizationFor` refuses to serve, and irreversible.
    if (organization !== null && scratch().deleteTransfer !== 'none') {
      const successor = fieldValue('deleteSuccessor');
      if (successor === '') return;
      // A STRING, and deliberately so: `to_user_id` is declared `string`
      // (percy-service-27c95232.ts:537) and `isId` rejects anything that is not one
      // (percy-http-27c95232.ts:1448-1450), so coercing it to a number would be a 400.
      const transfer = await api.transferAdministrator(organization.id, successor);
      if (!transfer.ok) {
        refuseModal(describeCommercialRefusal(transfer));
        return;
      }
    }

    // `POST /v1/account/erasure` is LIVE, and its 404 is a real answer about a real account —
    // "eraseAccount raises for a missing account, because 'destroy this' has no honest answer when
    // there is nothing there" (percy-http-27c95232.ts:2981-2984). `one.commercial.notFound` says
    // exactly that and claims nothing about a missing route, which is why one shared sentence
    // serves both this call and the §16 ones.
    const erasure = await api.eraseAccount();
    if (!erasure.ok) {
      refuseModal(describeCommercialRefusal(erasure));
      return;
    }

    modal(t('one.settings.deleteAccount.requestedTitle'), `<div class="notice">
      <strong>${tx('one.settings.deleteAccount.requestedTitle')}</strong>${
        tx('one.settings.deleteAccount.requestedText')}</div>`,
      `<button class="btn primary" data-action="modal-close">${tx('misc.close')}</button>`);
    toast(t('user.deletion.requestSuccess'));
  },

  /*
   * S14 IS DELETED — `cancel-deletion` and `confirm-cancel-deletion` are both gone (PM finding 5).
   * `dangerZone()` records what they did and what reinstating them would take; the route and
   * `api.cancelAccountDeletion()` are both untouched. No handler is registered, so the delegated
   * listener has nothing to dispatch even if a stale `data-action="cancel-deletion"` survived a
   * cache — which is the same "true by construction" shape `rename-org` relies on below.
   */

  /* --- organization ------------------------------------------------ */

  /*
   * `rename-org` HAS NO HANDLER AND MUST NOT GET ONE (ruling C8.1). The control is emitted,
   * visible and refused, so `isRefused` blocks the click before dispatch; api.js exports no
   * rename function for it to call. Both halves together are what make "the field issues no
   * request" true by construction.
   */

  'rename-team': (event, el) => {
    const teamId = el.getAttribute('data-team');
    const team = organizationTeams().find((entry) => String(entry.team_id) === String(teamId));
    if (team === undefined) return;
    setViewState(NS, {rename: {teamId: String(team.team_id), projectId: team.project_id, teamDone: false}});
    renderRenameTeamModal(team.name ?? '');
  },

  /**
   * J7. TWO WRITES: the team and its root project. One alone drifts, because creation set both
   * names from the same string and links them nowhere.
   *
   * They are issued separately rather than through `api.renameTeamEverywhere` for one reason: that
   * helper cannot report WHICH half failed, and a half-rename reported as "Team updated" is a fake
   * success. On a failed second write the modal stays open with the server's own sentence and
   * retries only the project write.
   */
  'save-team': async () => {
    const rename = scratch().rename;
    if (rename === undefined || rename === null) return;
    const name = fieldValue('teamName');
    if (name === '') return;

    try {
      if (!rename.teamDone) {
        await api.renameTeam(rename.teamId, name);
        setViewState(NS, {rename: {...rename, teamDone: true}});
      }
      await api.renameTeamRootProject(rename.projectId, name);
    } catch (err) {
      if (err instanceof api.SessionLostError) throw err;
      // Re-opened with the typed name intact: unsaved form state is never thrown away to make a
      // retry simpler (F2). `rename.teamDone` decides whether the retry re-issues write 1.
      renderRenameTeamModal(name);
      refuseModal(describeForkError(err));
      return;
    }

    setViewState(NS, {rename: null});
    closeModal();
    toast(t('team.edit.success'));
    await reloadOrganization();
  },

  /* --- team management --------------------------------------------- */

  'add-team': () => {
    modal(t('organization.teams.create'), `
      <label class="label">${tx('team.attributes.name')}</label>
      <input class="input" id="newTeamName" placeholder="${tx('one.org.teamNameExample')}">
      <div id="addTeamError"></div>`,
      `${footCancel()}<button class="btn primary" data-action="confirm-add-team">${
        tx('organization.teams.create')}</button>`);
  },

  /**
   * M5/F5. `PUT /api/v1/brazn/organization/teams` is the ONLY working create route — the v1 and v2
   * team routes are `service-managed` and 403 for everyone, instance admins included.
   *
   * The 409 body is rendered VERBATIM: `message` as returned and its four numbers as returned.
   * `describeForkError` already prefers `serverMessage`, so nothing here paraphrases it, nothing
   * recomputes `seats_needed`, and the modal stays open so the numbers are still on screen when
   * the user goes to buy seats.
   */
  'confirm-add-team': async () => {
    const name = fieldValue('newTeamName');
    if (name === '') return;
    try {
      const team = await api.createOrganizationTeam(name);
      closeModal();
      toast(t('team.create.success'));
      if (team?.id != null) setViewState(NS, {selectedTeamId: String(team.id)});
      await reloadOrganization();
    } catch (err) {
      // A lost session has its own terminal surface; app.js's dispatch swallows it deliberately.
      if (err instanceof api.SessionLostError) throw err;
      // Handled HERE and not rethrown: rethrowing would add a toast saying the same thing as the
      // sentence now sitting in the modal, and the modal is where the numbers have to stay (F5).
      const refusal = describeForkError(err);
      const slot = document.getElementById('addTeamError');
      if (slot !== null) renderRefusal(slot, {...refusal, source: 'server'});
      else refuseModal(refusal);
    }
  },

  /**
   * M2. Quote first, then purchase. Both routes are contract-only and answer today with the SPA's
   * index.html at HTTP 200 — which `readCommercialResult` refuses as `not-json` rather than
   * treating as success (ruling C14, bar 8), so the purchase never fires behind a quote that did
   * not arrive. Where the commercial service IS routed but does not yet serve these, the 404
   * reaches `one.commercial.notFound` through app.js's status table rather than a status code.
   */
  'add-seat': async (event, el) => {
    const organization = getOrganization();
    const meter = readSeatMeter(organization);
    if (organization === null || meter.purchased === null) return;
    const seats = meter.purchased + 1;

    const quote = await api.quoteSeats(organization.id, seats);
    if (!quote.ok) {
      renderRefusal(el, {...describeCommercialRefusal(quote), source: 'server'});
      return;
    }

    // THE QUOTE HAS NO `message` FIELD, and reading one was inventing a response field — the
    // mirror of ruling C17's request-field discipline. `SeatIncreaseQuote` is
    // `{organization_id, seats, seats_after, proration}` and nothing else
    // (percy-service-27c95232.ts:895-912); api.js:2019-2021 already said so in its own words.
    //
    // `seats_after` is the SERVER'S echo of what the purchase becomes (:899-900) and is what
    // `confirm-seats` now sends, rather than the number this page computed a moment earlier:
    // `SeatPurchaseRequest.seats` is "ABSOLUTE, never a delta, and bounded 3-100" (:854-855), so
    // the server is the authority on the figure the administrator is about to agree to.
    //
    // `proration` IS DELIBERATELY NOT RENDERED, which is a gap and is recorded as one rather than
    // filled with a guess — and it is emphatically not read as a failure signal: `null` is an
    // ordinary answer meaning this costs nothing now, "a perfectly ordinary answer and never an
    // error" (:906-909), so no branch here treats it as one. But `SeatProration` is declared in the
    // service's `billing.ts` (percy-service-27c95232.ts:122 imports it from there), which is not
    // among the extracted sources, so its field names are unknown and bar 7 forbids inventing them
    // to put an amount on screen. The figure is reported as the remaining half of BRA-1075.
    const body = quote.body ?? {};
    const seatsAfter = Number.isInteger(body.seats_after) ? body.seats_after : seats;
    setViewState(NS, {seatPurchase: {seats: seatsAfter}});
    modal(t('organization.seats.title'),
      `<div class="notice">${tx('one.org.seats.inSubscription', {count: seatsAfter})}</div>`,
      `${footCancel()}<button class="btn primary" data-action="confirm-seats">${
        tx('organization.seats.add')}</button>`);
  },

  'confirm-seats': async () => {
    const organization = getOrganization();
    const purchase = scratch().seatPurchase;
    if (organization === null || purchase === undefined || purchase === null) return;

    const result = await api.purchaseSeats(organization.id, purchase.seats);
    if (!result.ok) {
      refuseModal(describeCommercialRefusal(result));
      return;
    }
    closeModal();
    toast(t('one.toast.seatAdded'));
    // The meter's source is the FORK endpoint, never the commercial response (brief).
    await reloadOrganization();
  },

  /**
   * M4. `from_user_id` is the resolved bearer and is NEVER a body field —
   * `api.transferAdministrator` has no parameter for it, so it cannot be sent.
   *
   * THREE ANSWERS, NOT TWO — the same distinction `delete-account-second` documents, on the same
   * commercial read, and this handler was still collapsing the last two. `ok` with an empty
   * candidate list is an ORDINARY answer and not a refusal: `listSuccessorCandidates` "returns []
   * for a missing account, a non-administrator and an organization of one alike, because the
   * question is 'must I offer a choice' and the true answer to all three is no"
   * (percy-http-27c95232.ts:2979-2984). Rendering it as `commercial-unavailable` behind a dead
   * button told the sole-member administrator that the subscription service could not be reached,
   * which is a machine reason that is simply false — the service answered, and its answer was
   * "there is nobody to hand over to". That one gets its own shape: the fact, and a Close.
   *
   * THE PICKER OPENS UNANSWERED, AND CONFIRM STARTS DISABLED. This modal's own copy is
   * `organization.administration.transfer.text` — "You lose every administrative control the
   * moment it completes and cannot take the role back yourself" — so a `<select>` whose first
   * option is already a real person, sitting under an enabled Transfer, hands the organization to
   * whoever `readCandidates` happened to order first. The sibling irreversible path
   * (`delete-account-second`) has always emitted the empty placeholder and the disabled button,
   * and `#newAdmin` now takes the same two guards, released by the same kind of `change` listener.
   */
  'transfer-admin': async () => {
    const result = await api.listSuccessorCandidates();
    const candidates = result.ok ? readCandidates(result.body) : [];

    if (result.ok && candidates.length === 0) {
      // `one.org.noOrganizationMembers` ("No organization members available") is a STAND-IN and is
      // reported as one: the precise sentence is "there is nobody to hand over to", and it has no
      // key yet. This one is true of the successor list, which is what the modal is asking about,
      // and it is not a refusal — which is the property that matters and the one the old markup
      // got wrong. No Cancel/Confirm pair, because there is nothing to confirm.
      modal(t('organization.administration.transfer.action'), `<div class="notice">${
        tx('organization.administration.transfer.text')}</div>
        <div class="empty-state">${tx('one.org.noOrganizationMembers')}</div>`,
        `<button class="btn" data-action="modal-close">${tx('misc.close')}</button>`);
      return;
    }

    const options = candidates.map((candidate) =>
      `<option value="${esc(candidate.id)}">${esc(candidate.label)}</option>`).join('');

    modal(t('organization.administration.transfer.action'), `<div class="notice">${
      tx('organization.administration.transfer.text')}</div>
      <label class="label">${tx('one.org.newAdministrator')}</label>
      <select class="select" id="newAdmin">
        <option value="">${tx('one.org.selectMember')}</option>${options}</select>`,
      `${footCancel()}<button class="btn primary" id="confirmTransferBtn"
        data-action="confirm-transfer" disabled>${
        tx('organization.administration.transfer.action')}</button>`);

    // A real `disabled`, not `aria-disabled`: the sentence lives in the footer as its own
    // paragraph, so it is reachable whether or not the button is focusable, and an
    // `aria-disabled` button still dispatches its click. On `!ok` the select holds the
    // placeholder alone, so the listener below can never enable it — which is the state the old
    // markup was trying to express, without asserting a reason it had not read.
    if (!result.ok) refuseModal(describeCommercialRefusal(result));
  },

  'confirm-transfer': async () => {
    const organization = getOrganization();
    const successor = fieldValue('newAdmin');
    if (organization === null || successor === '') return;

    const result = await api.transferAdministrator(organization.id, successor);
    if (!result.ok) {
      refuseModal(describeCommercialRefusal(result));
      return;
    }
    closeModal();
    toast(t('one.toast.adminTransferSubmitted'));
    await reloadOrganization();
  },

  invite: () => {
    setViewState(NS, {memberAddMode: 'new'});
    renderInviteModal();
  },

  'member-mode': (event, el) => {
    const mode = el.getAttribute('data-mode');
    setViewState(NS, {memberAddMode: mode === 'existing' ? 'existing' : 'new'});
    renderInviteModal();
  },

  /**
   * M10. `POST /v1/organizations/invitations` with `{organization_id, email}`.
   *
   * NO `team_id`. The field is not invented — `parseInvite` allowlists it and the handler forwards
   * it (percy-http-27c95232.ts:1598 and :2841), and absent means "the organization's primary team"
   * (:1603-1606). It is left off because THE PROTOTYPE HAS NO TEAM PICKER and the prototype is the
   * scope bar (bar 10); ruling C17's discipline then keeps a field nobody chose out of the body.
   * api.js asserts on it so it cannot come back by accident.
   *
   * BAR 8 IS LOAD-BEARING TWICE HERE. `readCommercialResult` decides `ok`, and then the AFFIRMATIVE
   * SET ITSELF HAS TWO MEMBERS: `invited` and `already_member` are both non-refusals
   * (percy-service-27c95232.ts:581, and api.js's `INVITE_MEMBER` descriptor), and they are not the
   * same event. The declaration's own prose at :575-577 says `already_member` means the invitee
   * "holds a seat here already, so nothing was offered and nothing was sent". Toasting "Invitation
   * sent" for it, and appending a pending row for an invitation that does not exist, is a fake
   * success one level below the guard — exactly the direction bar 8 protects, and invisible in CI
   * because CI never reaches `/v1` (bar 9).
   */
  'confirm-invite': async () => {
    const organization = getOrganization();
    const email = fieldValue('inviteEmail');
    if (organization === null || email === '') return;

    const result = await api.inviteOrganizationMember({organization_id: organization.id, email});
    if (!result.ok) {
      // BOTH HALVES OF THE VOCABULARY REACH A SENTENCE HERE. A bare 403 is the service saying "you
      // do not administer this organization" in as many words (percy-http-27c95232.ts:2844-2852),
      // and a 200 carrying `outcome: "not_invitable"` — the only refusal this route can return
      // (percy-service-27c95232.ts:581) — resolves through app.js's outcome table to the sentence
      // naming its three causes. Neither is a status code on screen any more.
      refuseModal(describeCommercialRefusal(result));
      return;
    }

    // Only now — after the BODY said so, not after a 200 (bar 8).
    if (String(result.body?.outcome ?? '') !== 'invited') {
      // `already_member`: the roster is already in the state the administrator wanted, so this is
      // not an error and not a refusal — but nothing was sent, so there is no invitation to list
      // and nothing to revoke. No pending row, and its own sentence.
      closeModal();
      toast(t('one.toast.alreadyMember'));
      await reloadOrganization();
      return;
    }

    // NESTED, not top-level: the projection is `{outcome, invited_user_id, invitation:{
    // invitation_id, status, expires_at}, seat_notice}` (percy-http-27c95232.ts:2854-2884). There
    // is no top-level `invitation_id` and no `id`, so reading either one made `invitationId` null
    // on every successful invite and M14's Revoke button could never render.
    // `invitation` is null when nothing was recorded (percy-service-27c95232.ts:583) — that is the
    // honest no-Revoke case, and the only one.
    const invitationId = result.body?.invitation?.invitation_id ?? null;
    const invites = [...(scratch().pendingInvites ?? []), {email, invitationId}];
    // `seat_notice` IS THE ADMINISTRATOR'S ANSWER AND WAS BEING DROPPED ON THE FLOOR. The handler
    // passes it through "as the service composed it" and its own comment calls it "an
    // administrator being told what they are about to commit their organization to, which is the
    // whole of BRA-1075" (percy-http-27c95232.ts:2871-2883). It is declared
    // `{seats, users, seats_after, proration}` (percy-service-27c95232.ts:619-636) — four
    // numbers, no provider handle, no customer reference — and it rides the ADMINISTRATOR's
    // reply, never the invitation mail (:598-600).
    //
    // Read here and rendered by `pendingInvitationsCard`. Only `seats` and `users` are shown, and
    // the restraint is deliberate on both sides: `seats_after` is a FUTURE purchase and stating it
    // in the present tense would be the fake-success shape bar 8 exists for, while `proration`'s
    // own type lives in the service's `billing.ts` and is not among the extracted sources, so its
    // fields cannot be named without inventing them (bar 7). Both are reported as needing their
    // own catalogue keys rather than being approximated with the ones this page happens to have.
    const seatNotice = result.body?.seat_notice ?? null;
    setViewState(NS, {pendingInvites: invites, seatNotice});
    closeModal();
    toast(t('one.toast.invitationSent'));
    await reloadOrganization();
  },

  /**
   * M14. `invitation_id` can only come from the create response, so a row without one carries no
   * Revoke button at all: a Revoke that cannot name its invitation is a button that lies.
   */
  'revoke-invite': async (event, el) => {
    const organization = getOrganization();
    const index = Number(el.getAttribute('data-index'));
    const invites = [...(scratch().pendingInvites ?? [])];
    const invite = invites[index];
    if (organization === null || invite === undefined || !invite.invitationId) return;

    const result = await api.revokeOrganizationInvitation(organization.id, invite.invitationId);
    if (!result.ok) {
      renderRefusal(el, {...describeCommercialRefusal(result), source: 'server'});
      return;
    }
    invites.splice(index, 1);
    setViewState(NS, {pendingInvites: invites});
    toast(t('one.toast.invitationRevoked'));
    await reloadOrganization();
  },

  /**
   * M11/J9. The roster is already loaded, so adding an organization member needs no search call.
   * `POST /api/v2/teams/{team}/members` is `teams-only`, not service-managed
   * (route-classification.json:387), so it works for the users who see it (ruling C8.3) — hence
   * the `teams` gate rather than a removal.
   */
  'add-existing-member': async (event, el) => {
    const username = el.getAttribute('data-username');
    const teamId = el.getAttribute('data-team');
    if (!username || !teamId) return;
    try {
      await api.addTeamMember(teamId, username);
    } catch (err) {
      if (err instanceof api.SessionLostError) throw err;
      console.error('[one/settings] add team member failed', err);
      // On the ROW, not in the footer: this modal lists many people and a footer sentence would
      // not say which one was refused. The modal is not re-rendered afterwards, so it survives.
      const refusal = describeForkError(err);
      renderRefusal(el, {...refusal, source: 'server'});
      toast(refusalText(refusal));
      return;
    }
    toast(t('team.edit.userAddedSuccess'));
    await reloadOrganization();
    renderInviteModal();
  },

  /*
   * `search-existing-member` HAS NO HANDLER. The control is kept (ruling C8.3) but refused: the
   * lookup it needs is `GET /api/v2/users?q=`, api.js exports no global user search, and bar 7
   * forbids building a fork URL here to reach one. The organization-members picker in the same
   * modal is the working half and is fully wired.
   */

  'remove-member': (event, el) => {
    const username = el.getAttribute('data-username');
    const teamId = el.getAttribute('data-team');
    const team = organizationTeams().find((entry) => String(entry.team_id) === String(teamId));
    const member = teamMembers(teamId).find((entry) => entry?.username === username);
    const title = t('one.org.removeMemberTitle', {
      name: displayName(member ?? {username}),
      team: team?.name ?? '',
    });
    modal(title, `<div class="notice">
      <strong>${tx('one.org.removeMemberLead')}</strong>${tx('one.org.removeMemberText')}</div>`,
      `${footCancel()}<button class="btn danger" data-action="confirm-remove"
        data-username="${esc(username)}" data-team="${esc(teamId)}">${tx('one.org.removeFromTeam')}</button>`);
  },

  /**
   * M8/J10. TEAM-SCOPED, and a USERNAME in the path — not a numeric id. The modal's promise (the
   * person remains in the organization) is exactly what this route does; the organization-level
   * removal is a different operation and this page does not surface it (ruling C8.2).
   */
  'confirm-remove': async (event, el) => {
    const username = el.getAttribute('data-username');
    const teamId = el.getAttribute('data-team');
    if (!username || !teamId) return;
    try {
      await api.removeTeamMember(teamId, username);
    } catch (err) {
      reportModalFailure(err);
      return;
    }
    closeModal();
    toast(t('team.edit.deleteUser.success'));
    await reloadOrganization();
  },
});

/* ------------------------------------------------------------------ *
 * 11. Modal bodies that re-render themselves
 * ------------------------------------------------------------------ */

/**
 * The erasure confirmation with NO successor step — the shape for everyone who has nobody to hand
 * over to: the non-administrator, and the sole-member administrator whose candidate list is a
 * lawful empty one. `deleteTransfer: 'none'` is what tells `confirm-delete-account` to skip the
 * transfer instead of returning early on an unanswered picker that was never drawn.
 */
function renderErasureConfirmModal() {
  setViewState(NS, {deleteTransfer: 'none'});
  modal(t('one.settings.deleteAccount.confirmTitle'), `<div class="notice">
    <strong>${tx('one.settings.deleteAccount.confirmText')}</strong>${tx('user.deletion.text1')}</div>`,
    `${footCancel()}<button class="btn danger" data-action="confirm-delete-account">${
      tx('one.settings.deleteAccount.title')}</button>`);
}

function renderRenameTeamModal(name) {
  // After a successful team write only the root-project write is left, so the button says so (J7).
  const label = scratch().rename?.teamDone === true ? tx('organization.retry') : tx('misc.save');
  modal(t('team.attributes.name'), `
    <label class="label">${tx('team.attributes.name')}</label>
    <input class="input" id="teamName" value="${esc(name)}">`,
    `${footCancel()}<button class="btn primary" data-action="save-team">${label}</button>`);
}

/**
 * The two-tab Add-member modal (:1150-1161), rebuilt on every tab switch as the prototype does.
 *
 * DELIBERATE COPY CHANGE, reported rather than smuggled: the prototype's seat notice reads "The
 * invitation reserves one seat immediately for {team}" (`one.org.inviteSeatNotice`, :1154). This
 * modal has no team picker (bar 10 — the prototype has none either), so the invite this page sends
 * carries no `team_id` and the service reads that as the organization's PRIMARY team, which is not
 * necessarily the team the Team-management tab is pointed at (percy-http-27c95232.ts:1603-1606).
 * Naming a team in the sentence would therefore be a promise the request does not make. The
 * organization-scoped seat sentence is the true one.
 */
function renderInviteModal() {
  const team = selectedTeam();
  const teamId = team === null ? '' : String(team.team_id);
  const mode = scratch().memberAddMode === 'existing' ? 'existing' : 'new';
  // NO `min-width:0` ON THESE TWO. task.html records it as a deliberate deviation from SPEC-UI
  // §6.3 rule 4 ("that is the one thing that would let a nowrap label overflow its box, so it is
  // deliberately not applied"), and `.member-add-tabs button` is `flex:1 1 auto` + `white-space:
  // nowrap` — a floor of 0 lets each tab shrink below its own min-content and the German labels
  // then overflow rather than wrap. An inline style would have beaten the stylesheet on
  // specificity and silently reversed a decision recorded in another file.
  const tabs = `<div class="member-add-tabs" style="flex-wrap:wrap">
    <button class="${mode === 'new' ? 'on' : ''}" data-action="member-mode" data-mode="new"
      >${tx('one.org.inviteNewUser')}</button>
    <button class="${mode === 'existing' ? 'on' : ''}" data-action="member-mode" data-mode="existing"
      >${tx('one.org.addExisting')}</button>
  </div>`;

  if (mode === 'new') {
    modal(t('one.org.addMember'), `${tabs}
      <label class="label">${tx('user.auth.email')}</label>
      <input class="input" id="inviteEmail" type="email" placeholder="${tx('one.org.inviteEmailPlaceholder')}">
      <div class="help">${tx('organization.seats.explanation')}</div>`,
      `${footCancel()}<button class="btn primary" data-action="confirm-invite">${
        tx('one.org.sendInvitation')}</button>`);
    return;
  }

  const roster = teamMembers(teamId);
  const inTeam = new Set(roster.map((member) => member?.username));
  const members = organizationMembers();
  const picker = members.length === 0
    ? `<div class="empty-state">${tx('one.org.noOrganizationMembers')}</div>`
    : members.map((member) => memberPickerRow(member, teamId, inTeam.has(member?.username))).join('');

  modal(t('one.org.addMember'), `${tabs}
    <div>
      <label class="label">${tx('one.org.organizationMembers')}</label>
      <div class="member-picker">${picker}</div>
    </div>
    <div ${refusedGroup(DENY.NO_ROUTE)}>
      <label class="label">${tx('one.org.findExternal')}</label>
      <div class="member-search-row">
        <input class="input" id="existingMemberSearch" readonly aria-disabled="true"
          placeholder="${tx('one.org.searchPlaceholder')}">
        <button class="btn" data-action="search-existing-member" aria-disabled="true">${
          tx('one.common.search')}</button>
      </div>
      <div class="info-label">${ic('info')}<span>${tx('one.org.findExternalHint')}</span></div>
      ${refusalNote(t('one.deny.noRoute'))}
    </div>`,
    `<button class="btn" data-action="modal-close">${tx('misc.close')}</button>`);
}

/**
 * `teamId === ''` — an organization with no team at all — refuses the Add button in MARKUP rather
 * than through the `team-admin` gate, for `membersCard`'s reason: an empty `data-team` resolves to
 * `UNREADABLE_TEAM` and would tell the administrator their roster could not be read, when the
 * truth is that there is no team to add anybody to. Refused in markup carries no `data-requires`
 * on purpose — `applyGates` releases any node whose gate passes (file header).
 */
function memberPickerRow(member, teamId, alreadyInTeam) {
  const addButton = teamId === ''
    // A `<div>`, not a `<span>`: `refusalNote` emits a `<p>`, and a `<p>` inside a `<span>` is
    // reparented by the parser — the sentence would land outside the group `isRefused` walks.
    ? `<div ${refusedGroup(DENY.NO_ROUTE)}>
        <button class="btn small primary" data-action="add-existing-member" aria-disabled="true"
          >${tx('one.common.add')}</button>
        ${refusalNote(t('one.deny.noTeams'))}
      </div>`
    : `<button class="btn small primary" data-action="add-existing-member"
        data-username="${esc(member?.username ?? '')}" data-team="${esc(teamId)}"
        data-requires="teams team-admin write">${tx('one.common.add')}</button>`;
  return `<div class="member-picker-row">
    <div class="avatar">${esc(initials(member))}</div>
    <div class="member-picker-meta">
      <strong>${esc(displayName(member))}</strong>
      <span>${esc(joinParts([
        t('one.common.atUsername', {username: member?.username ?? ''}),
        member?.email ?? '',
      ]))}</span>
    </div>
    ${alreadyInTeam ? `<span class="role-badge">${tx('one.org.inTeam')}</span>` : addButton}
  </div>`;
}

/**
 * The successor candidates, as the service sends them. The list is read tolerantly and every
 * unusable row is dropped rather than guessed at. Reading a response defensively is not the same
 * as inventing a request field (ruling C17): nothing here is sent.
 *
 * THE FORK-ROSTER JOIN IS GONE, AND REMOVING IT IS THE FIX. It read:
 *
 *     members.find((row) => String(row?.id ?? row?.user_id ?? '') === String(id))
 *
 * and its comment blamed a type difference — string on the commercial side, number on the fork's
 * rows — which is true and is not the problem. The two values are from DIFFERENT ID SPACES, so
 * stringifying both cannot make them meet:
 *
 *   * `GET /v1/account/successor-candidates` projects the COMMERCIAL account id
 *     (percy-http-27c95232.ts:2986-2988), and percy-service-27c95232.ts:522 says of the sibling
 *     field "A commercial id, never the fork's."
 *   * `Organization.Members[].user_id` is `u.ID`, this instance's own row id
 *     (pkg/models/brazn_organization.go:478 — `UserID: u.ID`, `json:"user_id"`, an int64). The
 *     `OrganizationMember` struct has no `id` field at all, so the `row?.id` half of that
 *     expression was always `undefined` and every comparison fell through to the fork row id.
 *
 * So the join missed on every row and each label already fell back to `String(id)` — while the
 * code claimed a resolution it could not perform. Worse in the tail: `opaqueID` admits a bare
 * numeric string (`^[A-Za-z0-9_-]{1,64}$`, pkg/modules/auth/entitlement.go:191), so a coincidental
 * collision would have put the WRONG PERSON'S NAME against a stranger's id — on the two most
 * irreversible flows this page has, administrator handover and pre-erasure handover.
 *
 * A wrong name is worse than an opaque one here, so the id is what is shown. THE REAL FIX IS NOT
 * IN THIS FILE and is not available inside bar 1: nothing the browser holds maps a commercial
 * account id to a fork row. `Subject.UserID` — the fork's own copy of the commercial id — is never
 * put on a member row (pkg/modules/auth/entitlement.go:193-195 states the distinction outright),
 * and surfacing it on `OrganizationMember` is a fork change. Reported, not built.
 */
function readCandidates(body) {
  // `{candidates: [{user_id}]}` is the documented shape; the two array fallbacks cost nothing and
  // keep a shape change from emptying the picker silently.
  const list = Array.isArray(body) ? body
    : Array.isArray(body?.candidates) ? body.candidates
    : Array.isArray(body?.items) ? body.items
    : [];
  return list.map((entry) => {
    const id = entry?.user_id ?? entry?.id ?? null;
    if (id === null || id === undefined || id === '') return null;
    // `user_id` AND NOTHING ELSE is projected (percy-http-27c95232.ts:2986-2988) — `AccountRecord`
    // carries no name and no mailbox, which is also why erasure genuinely destroys an address
    // rather than leaving a copy there. There is nothing else on the row to label it with.
    return {id, label: String(id)};
  }).filter(Boolean);
}

/* ------------------------------------------------------------------ *
 * 12. Registration
 * ------------------------------------------------------------------ */

registerView('settings', {render, mount});
