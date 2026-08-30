/**
 * join.js — the invitation page (BRA-1475, a rewrite in place of BRA-1439 Story 5).
 *
 * WHAT WAS WRONG WITH THE PAGE THIS REPLACES, because the shape of this file is
 * the answer to it. The old page accepted the invitation the moment it loaded,
 * using whoever happened to be signed in, without asking. For an administrator
 * clicking an invitation they had just sent — the most common arrival — that
 * always failed, because the seat was not theirs. For a person with no session
 * it offered three buttons, and every one of them led into the old Vue
 * application, where none could finish: the task server's registration route
 * refuses everybody, and setting a password on an account nobody had made is
 * not a way in.
 *
 * SO THIS PAGE DOES NOTHING ON ITS OWN. On load it does exactly two things: it
 * reads whether anybody is signed in, and it asks the paid-account service what
 * organisation and team the handle in the address names. Neither consumes
 * anything, neither spends the token, and neither takes a seat. Everything else
 * happens when the person presses the one button.
 *
 * THE TWELVE-STEP JOURNEY, and which steps are this file's:
 *
 *   1-2  the administrator invites, the mail arrives, the person presses its
 *        button                                            (elsewhere, already live)
 *   3    this page opens and does nothing on its own                    HERE
 *   4    it asks for the organisation and team names                    HERE
 *   5-6  one screen: a heading, one sentence, three fields, one button;
 *        the address is filled in and cannot be changed                 HERE
 *   7    the username, password and token go to the paid-account service HERE
 *   8-10 the service checks the token, creates the account through the
 *        private channel, spends the token, takes the seat, admits the
 *        member and puts them on the task server's team    (the service's half)
 *   11   the person is signed in                                        HERE
 *   12   they land in the product with the team's lists visible          HERE
 *
 * STEP 11 CALLS THE SAME OPERATION THE SIGN-IN PAGE CALLS. `api.signIn` is the
 * only sign-in operation in this product, which is the ticket's "do not build a
 * second way to sign in". And there is NO SIGN-IN FORM HERE: a password form
 * that CREATES the new account is wanted; a second place to sign in with
 * existing credentials is not.
 *
 * EVERY WAY THIS FAILS ENDS ON THE GENERAL ERROR PAGE. An expired token, a
 * withdrawn invitation, a full seat ceiling and an address that already has an
 * account are all refusals the person cannot act on from a form, so they are
 * told what happened and what to do next rather than being left looking at a
 * button that will not work.
 */

'use strict';

import * as api from './api.js';
import {t} from './i18n.js';
import {
  bannerBlock,
  brandBlock,
  esc,
  goToPage,
  loadStrings,
  renderAuth,
  sendToErrorPage,
  showError,
  tx,
} from './auth-shell.js';

/* ------------------------------------------------------------------ *
 * 1. The fragment discipline
 * ------------------------------------------------------------------ */

// Byte-for-byte frontend/src/helpers/signupToken.ts STORAGE_KEY. The two files
// are separate bundles that may not import each other, so there is no shared
// constant and no test that would catch a drift — grep for the literal there
// before renaming it here. Hyphen-free and namespace-free on purpose: it is the
// Vue application's own key, and it must not read like an i18n key to the
// fork-guards sweep.
const SIGNUP_TOKEN_STORAGE_KEY = 'signupToken';
const SIGNUP_TOKEN_FRAGMENT_KEY = 'signup_token';

/* ------------------------------------------------------------------ *
 * 2. Pure parsing
 * ------------------------------------------------------------------ */

/**
 * The invitation handle out of a query string, or null. Only `i` is read; the
 * handle is opaque text belonging to the paid-account service, and this page
 * does not validate it beyond non-emptiness — a wrong handle is that service's
 * refusal to make, not this page's guess.
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
 * Which refusal the service named, translated into one word the general error
 * page recognises.
 *
 * IT FAILS CLOSED. An outcome this file has not read becomes the general
 * refusal rather than being rendered as its own raw word, so a vocabulary the
 * service grows later tells the reader something true and vague instead of
 * something meaningless and specific.
 */
export function refusalReason(result) {
  switch (result?.outcome) {
    case 'invitation_expired': return 'invitation-expired';
    case 'invitation_revoked': return 'invitation-revoked';
    case 'no_invitation': return 'invitation-unknown';
    case 'at_seat_ceiling': return 'seats-full';
    case 'account_exists': return 'account-exists';
    case 'not_invitable': return 'not-invitable';
    default: return 'invitation-failed';
  }
}

/* ------------------------------------------------------------------ *
 * 3. The two screens
 * ------------------------------------------------------------------ */

/**
 * THE INVITATION SCREEN: a heading, one sentence, three fields, one button.
 *
 * The body sentence is the ticket's, to be used as written: "You have been
 * invited to join the {teamName} team of {organizationName} for ONE Personal
 * Assistant." It lives in the catalogue with those two placeholders so it can be
 * translated, and both values are escaped because they are names somebody typed
 * into another system.
 *
 * THE ADDRESS IS FILLED IN AND LOCKED, because the token was issued for that
 * address and no other. It is `readonly` rather than `disabled`: a disabled
 * field is out of the tab order and unreadable to some assistive technology, so
 * a person would be told nothing about the one field on the form they cannot
 * change.
 *
 * The empty error box is not a sixth element on the screen — it is `:empty` and
 * therefore invisible, and it exists so a person who presses the button with a
 * field blank is told so rather than watching nothing happen. Every refusal the
 * SERVICE makes leaves this page for the general error page instead.
 */
export function invitationSurface(state) {
  return `${brandBlock()}
    <h1 class="auth-title">${tx('one.join.title')}</h1>
    <p class="auth-lead">${tx('one.join.lead', {
      teamName: state.teamName ?? '',
      organizationName: state.organizationName ?? '',
    })}</p>
    ${bannerBlock()}
    <form class="auth-form" id="joinForm" novalidate>
      <div class="auth-field">
        <label for="email">${tx('one.join.email')}</label>
        <input id="email" name="email" type="email" value="${esc(state.invitedEmail ?? '')}"
          readonly aria-describedby="emailLocked" autocomplete="email">
        <p class="auth-note" id="emailLocked">${tx('one.join.emailLocked')}</p>
      </div>
      <div class="auth-field">
        <label for="username">${tx('one.join.username')}</label>
        <input id="username" name="username" type="text" autocomplete="username"
          autocapitalize="none" spellcheck="false" value="${esc(state.username ?? '')}" required>
      </div>
      <div class="auth-field">
        <label for="password">${tx('one.join.password')}</label>
        <p class="auth-rule">${tx('one.join.passwordRule')}</p>
        <input id="password" name="password" type="password" autocomplete="new-password"
          minlength="8" required>
      </div>
      <button type="submit" class="auth-submit" ${state.phase === 'working' ? 'disabled' : ''}>
        ${state.phase === 'working' ? tx('one.join.working') : tx('one.join.submit')}
      </button>
    </form>`;
}

/**
 * THE ONE OTHER SCREEN: somebody else is signed in on this browser.
 *
 * This is what an administrator sees when they click an invitation they have
 * just sent, which is the most common arrival of all. Both sentences are the
 * ticket's, to be used as written, and the invitation survives signing out —
 * nothing here consumes it, so pressing the button and coming back lands on the
 * form above.
 */
export function signedInElsewhereSurface() {
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.join.otherAccount.title')}</h1>
      <p>${tx('one.join.otherAccount.body')}</p>
      ${bannerBlock()}
      <button type="button" class="auth-submit" data-action="join-logout">${tx('one.join.otherAccount.logout')}</button>
    </div>`;
}

/** The link carried no handle at all — there is nothing to look up and nothing to join. */
export function missingLinkSurface() {
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.join.missingLink.title')}</h1>
      <p>${tx('one.join.missingLink.body')}</p>
    </div>`;
}

/* ------------------------------------------------------------------ *
 * 4. The impure spine
 * ------------------------------------------------------------------ */

const state = {
  phase: 'reading',
  invitationId: null,
  signupToken: null,
  organizationName: null,
  teamName: null,
  invitedEmail: null,
  username: '',
};

/**
 * Capture the fragment token for this tab, then strip it from the address bar
 * and history — but ONLY once it is stored: with storage unusable (private
 * modes, policy), the fragment is left in place, because it is the only copy
 * this page has.
 */
function captureSignupToken() {
  const fromHash = signupTokenFromHash(location.hash);
  if (fromHash !== null) {
    try {
      sessionStorage.setItem(SIGNUP_TOKEN_STORAGE_KEY, fromHash);
      history.replaceState(history.state, '', location.pathname + location.search);
    } catch {
      // Storage refused; the token stays in the fragment on purpose.
    }
    return fromHash;
  }
  try {
    const stored = sessionStorage.getItem(SIGNUP_TOKEN_STORAGE_KEY);
    return stored === null || stored === '' ? null : stored;
  } catch {
    return null;
  }
}

function forgetSignupToken() {
  try {
    sessionStorage.removeItem(SIGNUP_TOKEN_STORAGE_KEY);
  } catch {
    // Best effort; it dies with the tab regardless.
  }
}

/**
 * STEP 7 TO 12. One press, and this is the only thing on the page that writes
 * anything anywhere.
 */
async function submitInvitation(form) {
  const data = new FormData(form);
  const username = String(data.get('username') ?? '').trim();
  const password = String(data.get('password') ?? '');
  state.username = username;

  if (username === '' || password === '') {
    showError(t('one.join.missingFields'));
    return;
  }

  state.phase = 'working';
  render();
  showError(null);

  const result = await api.completeOrganizationInvitation({
    invitationId: state.invitationId,
    signupToken: state.signupToken,
    username,
    password,
  });

  if (!result.ok) {
    // The person cannot act on any of these from this form, so they are told
    // what happened and what to do next on the page that exists for it.
    sendToErrorPage(refusalReason(result), result.message);
    return;
  }

  // The token is spent on the service's side now; dropping our copy stops a
  // stale value being offered by the next flow on a shared machine.
  forgetSignupToken();

  // STEP 11, THROUGH THE PRODUCT'S ONE SIGN-IN OPERATION. A failure here is not
  // the same as a failed acceptance and must not be reported as one: the seat
  // is taken and the account exists, so the person needs the sign-in page
  // rather than the error page.
  try {
    await api.signIn({username, password});
  } catch {
    goToPage('signin');
    return;
  }

  // STEP 12. The settings page is where the lockout lands everybody, and the
  // team's shared lists are visible from there.
  goToPage('settings');
}

/**
 * Sign the other person out and come back to this same address.
 *
 * `location.reload()` rather than a navigation, so the query keeps the handle
 * and this page re-runs its own boot. The invitation is untouched by any of it.
 */
async function signOutAndReturn() {
  showError(null);
  let providerLogout = null;
  try {
    providerLogout = await api.signOut();
  } catch {
    // The local session is dropped by `signOut` whatever the server answered,
    // so reloading still lands on the invitation form.
  }
  if (providerLogout !== null) {
    location.assign(providerLogout);
    return;
  }
  location.reload();
}

function render() {
  if (state.phase === 'missing-link') {
    renderAuth(missingLinkSurface());
    return;
  }
  if (state.phase === 'signed-in-elsewhere') {
    renderAuth(signedInElsewhereSurface());
    return;
  }
  renderAuth(invitationSurface(state));
  const first = document.getElementById('username');
  if (first instanceof HTMLElement && state.phase !== 'working') first.focus();
}

function installListeners() {
  document.addEventListener('submit', (event) => {
    if (!(event.target instanceof HTMLFormElement) || event.target.id !== 'joinForm') return;
    event.preventDefault();
    submitInvitation(event.target);
  });
  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null || el.getAttribute('data-action') !== 'join-logout') return;
    event.preventDefault();
    signOutAndReturn();
  });
}

export async function boot() {
  if (typeof document === 'undefined') return;

  installListeners();
  await loadStrings();

  state.signupToken = captureSignupToken();
  state.invitationId = invitationIdFromSearch(location.search);

  if (state.invitationId === null) {
    state.phase = 'missing-link';
    render();
    return;
  }

  // STEP 3: read whether anybody is signed in, and do nothing else with the
  // answer. A session belonging to somebody else is the administrator case, and
  // it gets its own screen rather than a silent failure.
  if (await api.initSession()) {
    state.phase = 'signed-in-elsewhere';
    render();
    return;
  }

  // STEP 4: what organisation and team does this handle name? A read, with no
  // session, whose credential is the token. Nothing is consumed.
  const preview = await api.previewOrganizationInvitation({
    invitationId: state.invitationId,
    signupToken: state.signupToken,
  });
  if (!preview.ok) {
    sendToErrorPage(refusalReason(preview), preview.message);
    return;
  }

  const details = api.readInvitationPreview(preview);
  state.organizationName = details.organizationName;
  state.teamName = details.teamName;
  state.invitedEmail = details.invitedEmail;
  state.phase = 'form';
  render();
}

/* Boot only on the real page. A test importing the pure functions has no
 * `#auth` in its document and gets no fetch, no storage and no render. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
