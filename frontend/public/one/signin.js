/**
 * signin.js — the sign-in page (BRA-1475).
 *
 * SIGNING IN IS ONE THING. WHERE SOMEBODY LANDS AFTERWARDS IS THREE THINGS, and
 * keeping that distinction is the whole design of this file:
 *
 *   1. an ordinary sign-in lands on the ONE Tasks settings page;
 *   2. a sign-in carrying a destination returns the person to it;
 *   3. a sign-in the DESKTOP APPLICATION asked for goes back to the desktop
 *      application with its code.
 *
 * The third is not a separate document and must never become one. A ONE desktop
 * application opens `/oauth/authorize` in a browser with five parameters in the
 * address; once a session exists this page posts them with that session,
 * receives a one-time code, and sends the browser to the application's own
 * address with the code and any state attached. Somebody who is ALREADY signed
 * in when the application opens that address passes straight through without
 * seeing a form, which is what happens today and must keep happening.
 *
 * THE COST OF GETTING THE THIRD WRONG IS INVISIBLE FROM HERE. No check on this
 * site observes a desktop application connecting; the evidence is a real
 * application against a real deployment. So the exchange below is taken field
 * for field from the page it replaces (`frontend/src/views/user/OAuthAuthorize.vue`)
 * rather than re-derived, including `state` being omitted from the return
 * address when the client sent none.
 *
 * WHAT THIS PAGE MUST NOT CARRY, and each of these is a bullet of the ticket's
 * own "what the fix must not be": a second registration form; a remember-me
 * control nothing honours (`long_token` is therefore never sent); any link into
 * the old application. There is also exactly one sign-in operation in this
 * product — `api.signIn` — and the invitation page calls that same function
 * after it has created an account, so there is no second way in.
 */

'use strict';

import * as api from './api.js';
import {t} from './i18n.js';
import {
  bannerBlock,
  brandBlock,
  esc,
  forkErrorSentence,
  goToPage,
  googleMark,
  installAuthLanguage,
  installPasswordReveal,
  loadStrings,
  pageUrl,
  passwordField,
  renderAuth,
  showError,
  tx,
} from './auth-shell.js';

/* ------------------------------------------------------------------ *
 * 1. The server's answers this page must tell apart
 * ------------------------------------------------------------------ */

/**
 * Two replies that are NOT plain failures, and treating either as one locks out
 * people who could have got in. Both arrive as HTTP 412 with a code in the body
 * (`pkg/user/error.go`), which is why `ForkError` carries `.code`.
 *
 *   1017 — this account has a second factor and no passcode arrived. It is a
 *          FOLLOW-UP: ask for the passcode and submit again. Rendering it as
 *          "wrong password" would tell somebody with correct credentials that
 *          their credentials are wrong.
 *   1012 — the address on this account has never been confirmed. It has its own
 *          sentence and its own way out, because the person cannot fix it by
 *          typing more carefully.
 */
const CODE_TOTP_REQUIRED = 1017;
const CODE_EMAIL_NOT_CONFIRMED = 1012;

/* ------------------------------------------------------------------ *
 * 2. Pure parsing
 * ------------------------------------------------------------------ */

/**
 * THE FIVE PARAMETERS A DESKTOP APPLICATION PUTS IN THE ADDRESS, or null when
 * this is not a desktop authorization at all.
 *
 * All five are required and a missing one is not a partial request to repair:
 * the exchange would be refused by the server anyway, and guessing a default
 * for `code_challenge_method` would hand a desktop client a code it cannot
 * redeem. `state` is separate — it is optional by the OAuth specification, is
 * echoed back untouched, and is the client's own value.
 */
export function desktopAuthorizationFrom(search) {
  const params = new URLSearchParams(String(search ?? '').replace(/^\?/, ''));
  const required = ['response_type', 'client_id', 'redirect_uri', 'code_challenge', 'code_challenge_method'];
  const request = {};
  for (const name of required) {
    const value = params.get(name);
    if (value === null || value === '') return null;
    request[name] = value;
  }
  request.state = params.get('state') ?? '';
  return request;
}

/**
 * The destination this arrival wants to be returned to, read from the FRAGMENT.
 *
 * THE FRAGMENT RATHER THAN THE QUERY IS A SECURITY PROPERTY, not a style: no
 * browser transmits a fragment to any server, so a destination carrying a
 * desktop application's own parameters never reaches an access log, a proxy log
 * or a Referer header. The Vue application it replaces used the same prefix
 * (`frontend/src/constants/redirectHash.ts`, REDIRECT_HASH_PREFIX) for the same
 * reason, so a link already written by anything works here unchanged.
 *
 * A query-shaped string is refused rather than parsed, for the reason
 * `signupTokenFromHash` refuses one: `URLSearchParams` strips a leading `?`
 * itself, so without this guard a caller handing over `location.search` would
 * find a destination there and quietly legitimise moving it into the query.
 */
export function destinationFromHash(hash) {
  const raw = String(hash ?? '');
  if (raw.startsWith('?')) return null;
  const fragment = raw.replace(/^#/, '');
  if (fragment === '') return null;
  const value = new URLSearchParams(fragment).get('redirect');
  return value === null || value === '' ? null : value;
}

/**
 * The destination, ONLY IF IT IS SOMEWHERE WE ARE WILLING TO SEND SOMEBODY.
 *
 * A page that navigates to whatever a stranger put in the address is an open
 * redirect, and a sign-in page is the most valuable place in a product to have
 * one: a link that signs somebody in and then lands them on a copy of this page
 * is a credible way to ask for their password a second time.
 *
 * So the allowed set is: this origin, plus the origins the SERVER published in
 * `GET /api/v1/info` — `brazn_checkout_url` and `brazn_account_url`. Those two
 * are configuration an operator set, not anything a visitor can influence, and
 * they are what makes "a first sign-in after registering lands on the download
 * page" expressible without this page holding a hardcoded address for the
 * website. An instance that published neither allows only its own origin, which
 * is the correct answer for a self-hosted one.
 *
 * `new URL(raw, origin)` resolves a relative destination against this origin,
 * so `/one/settings.html` is allowed and `//evil.example` — which is
 * protocol-relative and therefore a DIFFERENT origin, not a path — is not.
 *
 * @param {string|null} raw
 * @param {string} origin
 * @param {string[]} publishedUrls  brazn_checkout_url and brazn_account_url, either possibly ''
 */
export function allowedDestination(raw, origin, publishedUrls = []) {
  if (raw === null || raw === undefined || raw === '') return null;
  let target;
  try {
    target = new URL(String(raw), origin);
  } catch {
    return null;
  }
  if (target.protocol !== 'http:' && target.protocol !== 'https:') return null;

  const allowed = new Set([new URL(origin).origin]);
  for (const published of publishedUrls) {
    if (typeof published !== 'string' || published === '') continue;
    try {
      allowed.add(new URL(published).origin);
    } catch {
      // A misconfigured value allows nothing extra, which is the safe direction.
    }
  }
  return allowed.has(target.origin) ? target.toString() : null;
}

/** The provider list from `GET /api/v1/info`, or an empty list when the instance offers none. */
export function openIdProviders(info) {
  const providers = info?.auth?.openid_connect?.providers;
  if (info?.auth?.openid_connect?.enabled !== true || !Array.isArray(providers)) return [];
  return providers.filter(p => p !== null && typeof p === 'object' && typeof p.key === 'string' && p.key !== '');
}

/* ------------------------------------------------------------------ *
 * 3. The surface
 * ------------------------------------------------------------------ */

/**
 * One function, three surfaces: the form, the working state, and the state a
 * person sees while the desktop exchange happens. Pure markup from one state
 * object.
 *
 * The form is the ticket's list and nothing else: one field for a username OR
 * an email address (both are accepted — `resolveLoginUser` falls through to a
 * lookup that takes either — so offering two fields would invent a distinction
 * the server does not make), one field for a password, one button, a link to
 * the forgotten-password page, the account-creation link with its exact
 * sentence, a Google button, and one place errors appear.
 */
export function signInSurface(state) {
  if (state.phase === 'authorizing') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.auth.signIn.title')}</h1>
        <p>${tx('one.auth.signIn.returningToApp')}</p>
      </div>`;
  }

  const providers = state.providers ?? [];
  const google = providers.length === 0 ? '' : `
    ${providers.map(p => `<button type="button" class="auth-alt" data-action="signin-openid" data-provider="${esc(p.key)}">
      ${googleMark()}${tx('one.auth.signIn.withProvider', {provider: p.name ?? p.key})}
    </button>`).join('')}
    <p class="auth-or"><span>${tx('one.auth.signIn.or')}</span></p>`;

  // The totp field appears only after the server has asked for it, so an
  // account without a second factor never sees a box it must leave empty.
  const totp = state.needsTotp !== true ? '' : `
    <div class="auth-field">
      <label for="totp">${tx('one.auth.signIn.totp')}</label>
      <p class="auth-rule">${tx('one.auth.signIn.totpHint')}</p>
      <input id="totp" name="totp" type="text" inputmode="numeric" autocomplete="one-time-code" required>
    </div>`;

  // The account-creation link is rendered only when this instance published
  // somewhere to send people. An instance that creates its own accounts
  // publishes nothing, and a link to a page that cannot exist is worse than no
  // link — which is the same rule accountCreationRedirect states on the other
  // side of this handoff.
  const createAccount = state.checkoutUrl === null ? '' :
    `<a href="${esc(state.checkoutUrl)}" data-action="signin-create-account">${tx('one.auth.signIn.noAccount')}</a>`;

  return `${brandBlock()}
    <h1 class="auth-title">${tx('one.auth.signIn.title')}</h1>
    ${bannerBlock()}
    ${google}
    <form class="auth-form" id="signInForm" novalidate>
      <div class="auth-field">
        <label for="username">${tx('one.auth.signIn.username')}</label>
        <input id="username" name="username" type="text" autocomplete="username"
          autocapitalize="none" spellcheck="false" value="${esc(state.username ?? '')}" required>
      </div>
      ${passwordField('password', 'one.auth.signIn.password',
        'name="password" autocomplete="current-password" required')}
      ${totp}
      <button type="submit" class="auth-submit" ${state.phase === 'working' ? 'disabled' : ''}>
        ${state.phase === 'working' ? tx('one.auth.signIn.working') : tx('one.auth.signIn.submit')}
      </button>
    </form>
    <div class="auth-links">
      <a href="${esc(state.passwordUrl)}" data-action="signin-forgot">${tx('one.auth.signIn.forgotPassword')}</a>
      ${createAccount}
    </div>`;
}

/* ------------------------------------------------------------------ *
 * 4. The impure spine
 * ------------------------------------------------------------------ */

const state = {
  phase: 'form',
  needsTotp: false,
  username: '',
  providers: [],
  checkoutUrl: null,
  accountUrl: null,
  passwordUrl: '',
};

function render() {
  renderAuth(signInSurface(state));
  const first = document.getElementById(state.needsTotp ? 'totp' : 'username');
  if (first instanceof HTMLElement) first.focus();
}

/**
 * Where this person goes once they hold a session, and the order is the whole
 * rule.
 *
 * The desktop exchange is FIRST because it is the only destination that is not
 * a page: a desktop application waiting on a code must not be handed a settings
 * page instead, and a person who was already signed in when the application
 * opened that address never sees this page at all.
 */
async function landAfterSignIn() {
  const desktop = desktopAuthorizationFrom(location.search);
  if (desktop !== null) {
    await returnToDesktopApplication(desktop);
    return;
  }

  const destination = allowedDestination(
    destinationFromHash(location.hash),
    location.origin,
    [state.checkoutUrl ?? '', state.accountUrl ?? ''],
  );
  if (destination !== null) {
    // Trial→paid conversion (BRA-1442): the website sent us back with
    // `#redirect=…/checkout?convert=1`. Mint a claim under this session and
    // append it before leaving — without a claim the website would bounce the
    // person through login again (an infinite loop). A mint failure lands on
    // settings instead of that loop.
    const withClaim = await withConversionClaimIfNeeded(destination);
    if (withClaim === null) {
      goToPage('settings');
      return;
    }
    location.assign(withClaim);
    return;
  }

  goToPage('settings');
}

/**
 * When `destination` is a convert checkout URL, mint a claim and append
 * `&claim=`. Otherwise return the destination unchanged. `null` means mint
 * failed — callers must not send the person back to convert without a claim.
 *
 * Exported for tests (BRA-1442).
 *
 * @param {string} destination
 * @returns {Promise<string|null>}
 */
export async function withConversionClaimIfNeeded(destination) {
  let target;
  try {
    target = new URL(destination);
  } catch {
    return destination;
  }
  if (target.searchParams.get('convert') !== '1') return destination;

  const result = await api.issueTrialConversionClaim();
  const claim = result.ok && result.body !== null && typeof result.body === 'object'
    ? result.body.claim
    : null;
  if (typeof claim !== 'string' || claim.trim() === '') return null;
  target.searchParams.set('claim', claim.trim());
  return target.toString();
}

/**
 * The desktop application's code, and the hand back.
 *
 * A failure here CANNOT be silent and cannot land the person on a settings page
 * as though nothing happened: the application that sent them is still waiting,
 * and a person who sees a working product would never know to try again. So the
 * refusal is rendered on this page, with the server's own sentence.
 */
async function returnToDesktopApplication(request) {
  state.phase = 'authorizing';
  render();

  let answer;
  try {
    answer = await api.authorizeDesktopClient(request);
  } catch (err) {
    state.phase = 'form';
    render();
    showError(err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.auth.signIn.desktopFailed')
      : t('one.auth.signIn.desktopFailed'));
    return;
  }

  const code = typeof answer?.code === 'string' ? answer.code : '';
  const returnTo = typeof answer?.redirect_uri === 'string' ? answer.redirect_uri : '';
  if (code === '' || returnTo === '') {
    state.phase = 'form';
    render();
    showError(t('one.auth.signIn.desktopFailed'));
    return;
  }

  // The application's own address, which is a custom scheme rather than a web
  // address, so it is built with URL and handed to the browser unchanged.
  //
  // ONLY THE SERVER'S ECHOED `state`, AND ONLY WHEN IT SENT ONE. This used to
  // fall back to the client's own value when the server echoed none, which is
  // arguably the better reading of the OAuth specification and is the wrong
  // change to make here: criterion 22 is that a real desktop application gets
  // its access exactly as it does TODAY, and a strict client receiving a
  // parameter the page this replaces never sent is a risk with no upside. The
  // old page attached `state` on a truthy echoed value and on nothing else
  // (frontend/src/views/user/OAuthAuthorize.vue), so this does the same.
  const url = new URL(returnTo);
  url.searchParams.set('code', code);
  if (answer?.state) url.searchParams.set('state', String(answer.state));
  location.assign(url.toString());
}

async function submitSignIn(form) {
  const username = String(new FormData(form).get('username') ?? '').trim();
  const password = String(new FormData(form).get('password') ?? '');
  const totpPasscode = String(new FormData(form).get('totp') ?? '').trim();

  state.username = username;
  if (username === '' || password === '') {
    showError(t('one.auth.signIn.missingCredentials'));
    return;
  }

  state.phase = 'working';
  render();
  showError(null);

  try {
    await api.signIn({username, password, totpPasscode});
  } catch (err) {
    state.phase = 'form';

    if (err instanceof api.ForkError && err.code === CODE_TOTP_REQUIRED) {
      // A FOLLOW-UP, NOT A REJECTION. The credentials were accepted; what is
      // missing is the second factor. `needsTotp` stays true for the rest of
      // this page's life so a mistyped passcode does not hide the box again.
      state.needsTotp = true;
      render();
      showError(t(totpPasscode === ''
        ? 'one.auth.signIn.totpRequired'
        : 'one.auth.signIn.totpWrong'));
      return;
    }

    if (err instanceof api.ForkError && err.code === CODE_EMAIL_NOT_CONFIRMED) {
      render();
      showError(t('one.auth.signIn.emailNotConfirmed'));
      return;
    }

    render();
    showError(err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.auth.signIn.failed')
      : t('one.auth.signIn.failed'));
    return;
  }

  await landAfterSignIn();
}

/**
 * Hand the browser to the identity provider.
 *
 * The return address is `{base}auth/openid/{provider}`, which is what is
 * registered with Google and is now OUR document — the address is unchanged, so
 * nothing in the Google console moves. `api.buildOpenIdAuthorizeUrl` is the one
 * place that address is built, on both legs.
 *
 * The `state` value is this page's own opaque marker, kept for this tab so the
 * return leg can prove the round trip is the one that started here.
 */
function startOpenIdSignIn(provider) {
  let opaque;
  try {
    opaque = api.newIdempotencyKey();
  } catch {
    opaque = String(Date.now());
  }
  try {
    sessionStorage.setItem(OIDC_STATE_KEY, opaque);

    // WHAT MUST SURVIVE THE TRIP TO THE PROVIDER, and it is two different
    // things depending on how the person got here. Storage is the only bridge
    // that survives, because the browser leaves this origin entirely: the
    // fragment does not travel, and neither does the query.
    //
    // A DESKTOP APPLICATION'S REQUEST IS THE ONE THAT BREAKS SILENTLY. It puts
    // its five parameters in the QUERY of /oauth/authorize, and this document
    // is served in place at that address rather than redirected to, so there is
    // no `#redirect=` fragment holding them the way there was on the page this
    // replaces (frontend/src/router/index.ts, getAuthForRoute, whose own
    // comment says that hash is the only bridge that survives a provider round
    // trip). Without the line below, a person who presses Continue with Google
    // signs in, lands on the settings page, and the application that sent them
    // waits forever — and no check on this side can see it happen.
    //
    // The whole address is kept rather than the five parameters, so the return
    // leg replays the arrival exactly and `landAfterSignIn` makes the same
    // decision it would have made without the detour.
    const desktop = desktopAuthorizationFrom(location.search);
    const destination = desktop !== null
      ? location.pathname + location.search
      : destinationFromHash(location.hash);
    if (destination !== null) sessionStorage.setItem(OIDC_DESTINATION_KEY, destination);
  } catch {
    // Storage refused — a private window, or a policy that forbids it.
    //
    // FOR AN ORDINARY SIGN-IN this costs only the check below: the round trip
    // still works and the person still lands in the product.
    //
    // FOR A DESKTOP APPLICATION IT DOES NOT, and that is worth stating rather
    // than leaving somebody to find it. The arrival address is the only thing
    // that survives leaving this origin for the provider, and storage is the
    // only place to keep it. With nothing stored, the return leg finds no
    // address, the person lands in the product on the web, and the application
    // waits forever with nothing anywhere reporting it.
    //
    // There is no second bridge to fall back on, which is why this is accepted
    // rather than repaired. Do not read the sentence above as covering this
    // case; it was written before the desktop hand-off lived here.
  }
  location.assign(api.buildOpenIdAuthorizeUrl(provider, opaque));
}

// Per-tab, and per-tab is the point: a marker that outlived the tab would be
// offered by the next round trip on a shared machine. Namespaced with a
// `brazn.` prefix rather than a `one.` one so it cannot read like an i18n key
// to the fork-guards sweep, which is the same reason LOGIN_HANDOFF_MARKER is
// spelled that way in app.js.
const OIDC_STATE_KEY = 'brazn.one.oidc-state';
const OIDC_DESTINATION_KEY = 'brazn.one.oidc-destination';

/**
 * The return leg from the identity provider, which lands on THIS document at
 * `/auth/openid/{provider}`.
 *
 * The two refusals the ticket names — an address with no account, and an
 * account that does not use Google — are Go string literals in
 * `pkg/modules/auth/openid/openid.go` and arrive as the server's own sentence.
 * They are rendered verbatim rather than mapped to a catalogue key, because
 * this page cannot tell the two apart: the server answers with a sentence and
 * no code.
 */
async function completeOpenIdReturn(providerKey) {
  const params = new URLSearchParams(location.search.replace(/^\?/, ''));
  const code = params.get('code') ?? '';
  const returned = params.get('state') ?? '';
  const providerError = params.get('error');

  state.phase = 'form';

  if (providerError !== null && providerError !== '') {
    render();
    showError(params.get('message') ?? t('one.auth.signIn.providerFailed'));
    return;
  }
  if (code === '') {
    render();
    showError(t('one.auth.signIn.providerFailed'));
    return;
  }

  let expected = null;
  try {
    expected = sessionStorage.getItem(OIDC_STATE_KEY);
    sessionStorage.removeItem(OIDC_STATE_KEY);
  } catch {
    expected = null;
  }
  // A round trip this tab did not start is refused. `expected === null` means
  // storage could not answer rather than that the value was wrong — a private
  // mode, or a return that landed in a different tab — and refusing that case
  // would strand people whose browser simply does not keep the value.
  if (expected !== null && expected !== returned) {
    render();
    showError(t('one.auth.signIn.providerFailed'));
    return;
  }

  try {
    await api.completeOpenIdSignIn(providerKey, {
      code,
      redirectUrl: api.forkAppUrl(`auth/openid/${providerKey}`),
    });
  } catch (err) {
    render();
    showError(err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.auth.signIn.providerFailed')
      : t('one.auth.signIn.providerFailed'));
    return;
  }

  // Whatever was carried into the provider round trip comes back out of storage,
  // because neither the fragment nor the query survived leaving this origin.
  // For a desktop application this is the address it opened, five parameters
  // and all: replaying it lands back on this document with a session, and
  // `landAfterSignIn` then makes the same decision it would have made had the
  // person never needed to sign in.
  let stored = null;
  try {
    stored = sessionStorage.getItem(OIDC_DESTINATION_KEY);
    sessionStorage.removeItem(OIDC_DESTINATION_KEY);
  } catch {
    stored = null;
  }
  const destination = allowedDestination(stored, location.origin, [state.checkoutUrl ?? '', state.accountUrl ?? '']);
  if (destination !== null) {
    const withClaim = await withConversionClaimIfNeeded(destination);
    if (withClaim === null) {
      goToPage('settings');
      return;
    }
    location.assign(withClaim);
    return;
  }
  await landAfterSignIn();
}

/** The provider key out of `/auth/openid/{provider}`, or null when this is not that address. */
export function openIdProviderFromPath(pathname) {
  const match = /^\/auth\/openid\/([^/]+)\/?$/.exec(String(pathname ?? ''));
  return match === null ? null : decodeURIComponent(match[1]);
}

function installListeners() {
  // One delegated listener drives every reveal control on this page; it is installed
  // once and survives every re-render.
  installPasswordReveal();

  document.addEventListener('submit', (event) => {
    if (!(event.target instanceof HTMLFormElement) || event.target.id !== 'signInForm') return;
    event.preventDefault();
    submitSignIn(event.target);
  });

  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null) return;
    if (el.getAttribute('data-action') !== 'signin-openid') return;
    event.preventDefault();
    const key = el.getAttribute('data-provider');
    const provider = state.providers.find(p => p.key === key);
    if (provider !== undefined) startOpenIdSignIn(provider);
  });
}

export async function boot() {
  if (typeof document === 'undefined') return;

  installListeners();
  installAuthLanguage(render);
  await loadStrings();

  // The addresses to link to are built once, through pages.js, so this page
  // cannot name a document that does not exist.
  state.passwordUrl = pageUrl('password');

  // `GET /api/v1/info` is unauthenticated and is where this instance publishes
  // its identity providers and where accounts are created. A failure is
  // survivable: the password form still works, and the two things that go
  // missing are a Google button and a link.
  try {
    const info = await api.getInfo();
    state.providers = openIdProviders(info);
    state.checkoutUrl = typeof info?.brazn_checkout_url === 'string' && info.brazn_checkout_url !== ''
      ? info.brazn_checkout_url : null;
    state.accountUrl = typeof info?.brazn_account_url === 'string' && info.brazn_account_url !== ''
      ? info.brazn_account_url : null;
  } catch (err) {
    console.warn('[one/signin] could not read this instance information', err);
  }

  const provider = openIdProviderFromPath(location.pathname);
  if (provider !== null) {
    await completeOpenIdReturn(provider);
    return;
  }

  // A SESSION THAT ALREADY EXISTS SKIPS THE FORM. This is what makes the
  // desktop application's second case work: somebody already signed in when the
  // application opens /oauth/authorize passes straight through, which is what
  // happens today. It matters on the other two addresses too — a person who is
  // signed in and lands on /login should not be asked to sign in again.
  if (await api.initSession()) {
    await landAfterSignIn();
    return;
  }

  render();
}

/* Boot only on the real page. A test importing the pure functions has no
 * `#auth` in its document and gets no fetch, no storage and no render. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
