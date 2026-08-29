/**
 * join.js — the invitation acceptance page (BRA-1439 Story 5).
 *
 * The invitation email links to `/one/join.html?i=<invitation_id>#signup_token=<token>` on this
 * host (the commercial half of BRA-1439 points `invitationLinkBase` here). This page's whole job
 * is the last mile: land the recipient, get them a session THROUGH THE FORK'S EXISTING SIGN-IN
 * FLOW, accept the invitation through `POST /v1/organizations/invitations/accept`, and put them
 * in the application.
 *
 * WHAT THIS FILE MAY DO — the same bars as the other view modules, restated because this page is
 * its own document with its own spine. It renders markup and calls api.js; it defines no route
 * and builds no URL itself (every address comes from an api.js export, bar 6); every commercial
 * result goes through `api.readCommercialResult`'s guard inside the api.js call and is worded
 * through app.js's shared describers (bar 8 — importing app.js is safe here because app.js is
 * import-time pure and its `boot()` self-schedules only on a document carrying `#app`, which
 * this page deliberately does not).
 *
 * IT BUILDS NO SIGN-IN FORM (bar 4). A visitor without a session is handed off to the Vue app's
 * own pages — `/login` for a password or Google, `/get-password-reset` to set a password on the
 * account the commercial service already provisioned for them. A second credential surface is a
 * second thing to keep correct, and the lockout serves those routes for exactly this reason
 * (pkg/routes/static_brazn.go, restrictedUIAuthPaths).
 *
 * THE FRAGMENT IS A SECURITY PROPERTY, NOT A STYLE. `#signup_token` rides the URL fragment
 * because no browser transmits a fragment to any server, so the token cannot land in an access
 * log, a proxy log or a Referer header. This page keeps that property: the token is read from
 * the fragment, stored for this tab only, and stripped from the address bar before anything
 * else happens — the same discipline, and THE SAME sessionStorage KEY, as the Vue helper
 * (frontend/src/helpers/signupToken.ts, STORAGE_KEY 'signupToken'), so the Vue app's own
 * register and Google flows find it after the hand-off without either bundle importing the
 * other (bar 1). Grep for the literal in that file before ever renaming it here.
 *
 * THE RETURN LEG IS APP.JS'S. Signing in lands the person in the application, not back here —
 * the SPA's return-to mechanism cannot carry a static page (app.js explains this at its /login
 * hand-off). So before handing off, this page records the pending invitation under
 * `PENDING_JOIN_KEY` (localStorage, because the set-a-password path crosses tabs through an
 * email), and app.js's boot — which every ONE page runs — sends a freshly signed-in session
 * back here exactly once. This page CONSUMES the marker on every terminal outcome, so the
 * bounce cannot loop.
 */

'use strict';

import * as api from './api.js';
import {t, init as initI18n} from './i18n.js';
import {PENDING_JOIN_KEY, describeCommercialRefusal, refusalText} from './app.js';

/* ------------------------------------------------------------------ *
 * 1. Local primitives (each view module carries its own — bar 1)
 * ------------------------------------------------------------------ */

function esc(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function tx(key, params) {
  return esc(t(key, params));
}

// Byte-for-byte frontend/src/helpers/signupToken.ts STORAGE_KEY — see the file
// header for why the two files share the literal and cannot share a constant.
// Hyphen-free and namespace-free on purpose: it is the Vue app's own key, and
// it must not read like an i18n key to the fork-guards sweep (it does not — no
// dot).
const SIGNUP_TOKEN_STORAGE_KEY = 'signupToken';
const SIGNUP_TOKEN_FRAGMENT_KEY = 'signup_token';

/* ------------------------------------------------------------------ *
 * 2. Pure parsing — exported for the unit tests (bar 9: stubbed-fetch
 *    and pure-function tests are the only automated evidence this page
 *    can have; /v1 is not reachable from CI)
 * ------------------------------------------------------------------ */

/**
 * The invitation id out of a query string, or null. Only `i` is read; the id
 * is opaque commercial-service text (`^[A-Za-z0-9_-]{1,64}$` is its shape over
 * there), and this page does not validate it beyond non-emptiness — a wrong id
 * is the service's refusal to make, not this page's guess.
 */
export function invitationIdFromSearch(search) {
  const value = new URLSearchParams(String(search ?? '').replace(/^\?/, '')).get('i');
  return value === null || value === '' ? null : value;
}

/**
 * The signup token out of a URL fragment, or null. Pure; storage is the
 * caller's.
 *
 * A QUERY-SHAPED STRING IS REFUSED, not parsed: URLSearchParams strips a
 * leading `?` itself, so without this guard a caller handing over
 * `location.search` would find a token there and quietly legitimise moving it
 * into the query — where every access log, proxy log and Referer header sees
 * it. The fragment placement is the security property; the parser enforces it
 * rather than trusting every future caller to.
 */
export function signupTokenFromHash(hash) {
  const raw = String(hash ?? '');
  if (raw.startsWith('?')) return null;
  const fragment = raw.replace(/^#/, '');
  if (fragment === '') return null;
  const token = new URLSearchParams(fragment).get(SIGNUP_TOKEN_FRAGMENT_KEY);
  return token === null || token === '' ? null : token;
}

/**
 * Which of the acceptance's two affirmative outcomes happened. Both are
 * non-refusals and they are NOT the same sentence (api.js ACCEPT_INVITATION):
 * `admitted` seated the person just now; `already_member` means they held a
 * seat all along and nothing changed. A caller that has `result.ok === true`
 * must still branch here — rendering "you have joined" for a seat that was
 * always theirs is the fake-success direction bar 8 exists to prevent.
 */
export function acceptedOutcome(result) {
  return result?.outcome === 'already_member' ? 'already-member' : 'accepted';
}

/* ------------------------------------------------------------------ *
 * 3. Surfaces — pure markup from one state object, exported for tests
 * ------------------------------------------------------------------ */

/**
 * One function, five surfaces: `missing-link`, `choices` (no session yet),
 * `accepting`, `done` (with `outcome`), and `refused` (with `sentence`).
 * Every control is a `data-action` button; this page owns its own delegated
 * click listener because app.js's is installed only by its boot.
 */
export function joinSurface(state) {
  const kind = state?.kind ?? 'missing-link';

  if (kind === 'missing-link') {
    return `<div class="settings-section"><div class="card settings-card wide">
      <h2>${tx('one.join.title')}</h2>
      <p class="card-sub">${tx('one.join.missingLink')}</p>
    </div></div>`;
  }

  if (kind === 'choices') {
    // BRA-1469 Done-when #2: every control must lead somewhere that can finish.
    // No credential form here (must-not). Create account → Vue /register, which
    // keeps the sessionStorage signup token join.js already captured.
    const hasToken = state?.hasSignupToken === true;
    const explain = hasToken ? tx('one.join.explainWithToken') : tx('one.join.explain');
    return `<div class="settings-section"><div class="card settings-card wide">
      <h2>${tx('one.join.title')}</h2>
      <p class="card-sub">${tx('one.join.lead')}</p>
      <p class="help">${explain}</p>
      <div class="profile-actions" style="flex-wrap:wrap;gap:8px">
        ${hasToken
          ? `<button class="btn primary" data-action="join-create-account">${tx('one.join.createAccount')}</button>
        <button class="btn" data-action="join-signin">${tx('one.join.signIn')}</button>`
          : `<button class="btn primary" data-action="join-signin">${tx('one.join.signIn')}</button>
        <button class="btn" data-action="join-create-account">${tx('one.join.createAccount')}</button>`}
        <button class="btn" data-action="join-set-password">${tx('one.join.setPassword')}</button>
      </div>
      <p class="help">${tx('one.join.setPasswordHint')}</p>
    </div></div>`;
  }

  if (kind === 'accepting') {
    return `<div class="settings-section"><div class="card settings-card wide">
      <h2>${tx('one.join.title')}</h2>
      <p class="card-sub">${tx('one.join.accepting')}</p>
    </div></div>`;
  }

  if (kind === 'done') {
    const sentence = state.outcome === 'already-member'
      ? tx('one.join.alreadyMember')
      : tx('one.join.accepted');
    return `<div class="settings-section"><div class="card settings-card wide">
      <h2>${tx('one.join.title')}</h2>
      <p class="card-sub">${sentence}</p>
      <div class="profile-actions">
        <button class="btn primary" data-action="join-open-app">${tx('one.join.openApp')}</button>
      </div>
    </div></div>`;
  }

  // `refused`. The sentence is the service's own words or the shared
  // describers' key, already resolved by the caller — never a status code.
  return `<div class="settings-section"><div class="card settings-card wide">
    <h2>${tx('one.join.title')}</h2>
    <p class="card-sub">${tx('one.join.refusedLead')}</p>
    <p class="help">${esc(state?.sentence ?? '')}</p>
    <div class="profile-actions" style="flex-wrap:wrap;gap:8px">
      <button class="btn" data-action="join-retry">${tx('organization.retry')}</button>
      <button class="btn" data-action="join-open-app">${tx('one.join.openApp')}</button>
    </div>
  </div></div>`;
}

/* ------------------------------------------------------------------ *
 * 4. The impure spine
 * ------------------------------------------------------------------ */

function render(state) {
  const root = document.getElementById('join');
  if (root === null) return;
  root.innerHTML = joinSurface(state);
  document.querySelector('.stage')?.classList.remove('hidden');
}

/**
 * Capture the fragment token for this tab, then strip it from the address bar
 * and history — but ONLY once it is stored: with storage unusable (private
 * modes, policy), the fragment is left in place, because it is the only copy
 * and it still travels intact through a same-tab hand-off to the Vue pages.
 */
function captureSignupToken() {
  const token = signupTokenFromHash(location.hash);
  if (token === null) return;
  try {
    sessionStorage.setItem(SIGNUP_TOKEN_STORAGE_KEY, token);
    history.replaceState(history.state, '', location.pathname + location.search);
  } catch {
    // Storage refused; the token stays in the fragment on purpose.
  }
}

/** Remember the invitation for app.js's one-shot return leg. Failing is survivable: */
/** the person can still open the email link again after signing in. */
function writePendingJoin(invitationId) {
  try {
    localStorage.setItem(PENDING_JOIN_KEY, JSON.stringify({i: invitationId, at: Date.now()}));
  } catch {
    console.warn('[one/join] could not remember the invitation for after sign-in');
  }
}

function clearPendingJoin() {
  try {
    localStorage.removeItem(PENDING_JOIN_KEY);
  } catch {
    // Nothing to do; the marker expires on its own (app.js pendingJoinRedirect).
  }
}

async function accept(invitationId) {
  render({kind: 'accepting'});

  let result;
  try {
    result = await api.acceptOrganizationInvitation({invitation_id: invitationId});
  } catch (err) {
    if (err instanceof api.SessionLostError) {
      // The session died between the refresh and the call: back to the
      // choices, with the pending marker intact for the round trip.
      writePendingJoin(invitationId);
      render({kind: 'choices'});
      return;
    }
    throw err;
  }

  // A terminal answer either way, so the return-leg marker has done its job:
  // consuming it here is what bounds app.js's bounce at one.
  clearPendingJoin();

  if (!result.ok) {
    render({kind: 'refused', sentence: refusalText(describeCommercialRefusal(result))});
    return;
  }

  // The token is spent conceptually once the person is seated; keeping it
  // would only offer a stale value to the next flow on a shared machine.
  try {
    sessionStorage.removeItem(SIGNUP_TOKEN_STORAGE_KEY);
  } catch {
    // Best effort; it dies with the tab regardless.
  }
  render({kind: 'done', outcome: acceptedOutcome(result)});
}

function installClickListener(invitationId) {
  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null) return;
    const action = el.getAttribute('data-action');

    // Every hand-off is person-initiated — one hop per press, never a chain —
    // which is what keeps this page out of the redirect-loop territory app.js
    // has to manage for its automatic hand-off.
    if (action === 'join-signin') {
      if (invitationId !== null) writePendingJoin(invitationId);
      location.assign(api.forkAppUrl('login'));
    } else if (action === 'join-create-account') {
      // Token is already in sessionStorage (captureSignupToken). /register's
      // beforeEnter reads getSignupToken via readSignupTokenFromFragment and
      // keeps the form when a token is present (BRA-1444 / accountCreationRedirect).
      if (invitationId !== null) writePendingJoin(invitationId);
      location.assign(api.forkAppUrl('register'));
    } else if (action === 'join-set-password') {
      if (invitationId !== null) writePendingJoin(invitationId);
      location.assign(api.forkAppUrl('get-password-reset'));
    } else if (action === 'join-open-app') {
      // The root: under the restricted-UI lockout it lands on the ONE pages,
      // and without it on the full application. Both are "inside".
      location.assign(api.forkAppUrl(''));
    } else if (action === 'join-retry') {
      location.reload();
    }
  });
}

export async function boot() {
  if (typeof document === 'undefined') return;

  captureSignupToken();
  const invitationId = invitationIdFromSearch(location.search);
  installClickListener(invitationId);

  // No user preference exists before a session, so negotiate from the browser
  // alone — the same rule as app.js's ensureStrings. A failed catalogue still
  // renders: t() falls back to the key path, which beats a blank page.
  try {
    await initI18n(null, typeof navigator !== 'undefined' ? navigator.languages : []);
  } catch (err) {
    console.error('[one/join] no string catalogue could be loaded', err);
  }

  if (invitationId === null) {
    render({kind: 'missing-link'});
    return;
  }

  if (!await api.initSession()) {
    writePendingJoin(invitationId);
    let hasSignupToken = false;
    try {
      hasSignupToken = Boolean(sessionStorage.getItem(SIGNUP_TOKEN_STORAGE_KEY));
    } catch {
      hasSignupToken = Boolean(signupTokenFromHash(location.hash));
    }
    render({kind: 'choices', hasSignupToken});
    return;
  }

  await accept(invitationId);
}

/* Boot only on the real page. A unit test importing the pure functions has no
 * `#join` in its document and gets no fetch, no storage and no render. */
if (typeof document !== 'undefined' && document.getElementById('join') !== null) {
  queueMicrotask(() => {boot();});
}
