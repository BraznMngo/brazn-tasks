/**
 * password.js — one document, two states (BRA-1475).
 *
 * ASKING FOR A RESET, and SETTING THE NEW PASSWORD. Which one a person sees is
 * decided by whether the address carries a token, never by a control they press:
 * somebody arriving from an email is already past the asking.
 *
 * WHERE THE TOKEN ARRIVES FROM, and why two places are read. The server mails
 * the reset link today as the site root with the token in the QUERY
 * (`pkg/user/notifications.go`), which is fault 1 of this ticket: the lockout
 * redirects the root, a redirect carries only its destination, so the token is
 * discarded and the customer's one link is spent on a page that cannot help
 * them. Two changes fix that and NEITHER IS IN THIS FILE — the query name has to
 * survive the lockout, which rescues links already sitting in inboxes, and new
 * mail has to point straight here, so no future link depends on that list.
 * This page reads both forms so that whichever of the two lands first, a
 * customer holding either kind of link can finish:
 *
 *   * `?userPasswordReset=<token>` — the shape every link mailed so far has,
 *     wherever it is finally routed to;
 *   * `#token=<token>` — the shape a link should have, because no browser
 *     transmits a fragment to any server, so the token never reaches an access
 *     log, a proxy log or a Referer header on its way here.
 *
 * THE ANSWER TO A RESET REQUEST IS THE SAME WHETHER OR NOT AN ACCOUNT EXISTS,
 * and this page must not undo that. The server publishes it as a contract and
 * enforces it (`RequestUserPasswordResetTokenByEmail` returns nil for an
 * address with no account and for a disabled one), because answering
 * differently turns a route needing no credentials into a way of sorting a list
 * of addresses into customers and non-customers. So there is ONE sentence here
 * for both, and it never says whether a mail was sent to a real account.
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
  loadStrings,
  pageUrl,
  renderAuth,
  showError,
  tx,
} from './auth-shell.js';

/** A spent, unknown or expired reset token: 412 with code 1009 (`pkg/user/error.go`). */
const CODE_INVALID_RESET_TOKEN = 1009;

/**
 * The server's own minimum, and it is stated BEFORE the person types rather
 * than after a rejection. `user.PasswordReset` declares `minLength:8`
 * (`pkg/user/user_password_reset.go`); the number is mirrored here because
 * there is no module boundary to import it across, and the `minlength` on the
 * input and the sentence above it both come from this one constant so they
 * cannot drift apart.
 */
const MINIMUM_PASSWORD_LENGTH = 8;

/* ------------------------------------------------------------------ *
 * 1. Pure parsing
 * ------------------------------------------------------------------ */

/**
 * The reset token this arrival carries, from the fragment first and the query
 * second, or null when it carries none.
 *
 * THE FRAGMENT IS PREFERRED, NOT MERELY ACCEPTED. Both forms are read because
 * both exist in the wild, but a browser holding both should use the one that
 * never travelled: a link whose token is in the query has already been written
 * into every log between the reader and here by the time this function runs,
 * and preferring it would make the safer form pointless to adopt.
 */
export function resetTokenFrom(search, hash) {
  const raw = String(hash ?? '');
  if (!raw.startsWith('?')) {
    const fragment = new URLSearchParams(raw.replace(/^#/, '')).get('token');
    if (fragment !== null && fragment !== '') return fragment;
  }
  const query = new URLSearchParams(String(search ?? '').replace(/^\?/, '')).get('userPasswordReset');
  return query === null || query === '' ? null : query;
}

/* ------------------------------------------------------------------ *
 * 2. The two states
 * ------------------------------------------------------------------ */

/** State one: ask for a link. */
export function requestSurface(state) {
  if (state.sent === true) {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.password.request.sentTitle')}</h1>
        <p>${tx('one.password.request.sentBody')}</p>
      </div>
      <div class="auth-links">
        <a href="${esc(state.signInUrl)}">${tx('one.password.backToSignIn')}</a>
      </div>`;
  }

  return `${brandBlock()}
    <h1 class="auth-title">${tx('one.password.request.title')}</h1>
    <p class="auth-lead">${tx('one.password.request.lead')}</p>
    ${bannerBlock()}
    <form class="auth-form" id="requestForm" novalidate>
      <div class="auth-field">
        <label for="email">${tx('one.password.request.email')}</label>
        <input id="email" name="email" type="email" autocomplete="email"
          autocapitalize="none" spellcheck="false" value="${esc(state.email ?? '')}" required>
      </div>
      <button type="submit" class="auth-submit" ${state.phase === 'working' ? 'disabled' : ''}>
        ${state.phase === 'working' ? tx('one.password.working') : tx('one.password.request.submit')}
      </button>
    </form>
    <div class="auth-links">
      <a href="${esc(state.signInUrl)}">${tx('one.password.backToSignIn')}</a>
    </div>`;
}

/** State two: set the new password. */
export function setSurface(state) {
  if (state.done === true) {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.password.set.doneTitle')}</h1>
        <p>${tx('one.password.set.doneBody')}</p>
        <button type="button" class="auth-submit" data-action="password-sign-in">${tx('one.password.set.signIn')}</button>
      </div>`;
  }

  return `${brandBlock()}
    <h1 class="auth-title">${tx('one.password.set.title')}</h1>
    <p class="auth-lead">${tx('one.password.set.lead')}</p>
    ${bannerBlock()}
    <form class="auth-form" id="setForm" novalidate>
      <div class="auth-field">
        <label for="password">${tx('one.password.set.newPassword')}</label>
        <p class="auth-rule">${tx('one.password.set.rule', {count: MINIMUM_PASSWORD_LENGTH})}</p>
        <input id="password" name="password" type="password" autocomplete="new-password"
          minlength="${MINIMUM_PASSWORD_LENGTH}" required>
      </div>
      <button type="submit" class="auth-submit" ${state.phase === 'working' ? 'disabled' : ''}>
        ${state.phase === 'working' ? tx('one.password.working') : tx('one.password.set.submit')}
      </button>
    </form>`;
}

/* ------------------------------------------------------------------ *
 * 3. The impure spine
 * ------------------------------------------------------------------ */

const state = {
  phase: 'form',
  token: null,
  email: '',
  sent: false,
  done: false,
  signInUrl: '',
};

function render() {
  renderAuth(state.token === null ? requestSurface(state) : setSurface(state));
  const first = document.getElementById(state.token === null ? 'email' : 'password');
  if (first instanceof HTMLElement && state.phase !== 'working') first.focus();
}

async function submitRequest(form) {
  const email = String(new FormData(form).get('email') ?? '').trim();
  state.email = email;
  if (email === '') {
    showError(t('one.password.request.missingEmail'));
    return;
  }

  state.phase = 'working';
  render();
  showError(null);

  try {
    await api.requestPasswordReset(email);
  } catch (err) {
    state.phase = 'form';
    render();
    showError(err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.password.request.failed')
      : t('one.password.request.failed'));
    return;
  }

  // ONE SENTENCE, WHATEVER HAPPENED. The server tells this page nothing about
  // whether the address has an account, deliberately, and a page that guessed
  // would give away exactly what the server refuses to.
  state.phase = 'form';
  state.sent = true;
  render();
}

async function submitNewPassword(form) {
  const password = String(new FormData(form).get('password') ?? '');
  if (password.length < MINIMUM_PASSWORD_LENGTH) {
    showError(t('one.password.set.tooShort', {count: MINIMUM_PASSWORD_LENGTH}));
    return;
  }

  state.phase = 'working';
  render();
  showError(null);

  try {
    await api.setNewPassword(state.token, password);
  } catch (err) {
    state.phase = 'form';

    if (err instanceof api.ForkError && err.code === CODE_INVALID_RESET_TOKEN) {
      // The link is spent, unknown or expired. The person cannot fix that by
      // typing a different password, so the page goes back to the state that
      // CAN help them: asking for another link, with the reason said out loud.
      state.token = null;
      render();
      showError(t('one.password.set.linkExpired'));
      return;
    }

    render();
    showError(err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.password.set.failed')
      : t('one.password.set.failed'));
    return;
  }

  // Completing a reset also marks an account active when it was locked or
  // awaiting confirmation (`pkg/user/user_password_reset.go`), so signing in is
  // genuinely the next step and not a hopeful suggestion.
  state.phase = 'form';
  state.done = true;
  render();
}

function installListeners() {
  document.addEventListener('submit', (event) => {
    if (!(event.target instanceof HTMLFormElement)) return;
    if (event.target.id === 'requestForm') {
      event.preventDefault();
      submitRequest(event.target);
      return;
    }
    if (event.target.id === 'setForm') {
      event.preventDefault();
      submitNewPassword(event.target);
    }
  });
  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null || el.getAttribute('data-action') !== 'password-sign-in') return;
    event.preventDefault();
    goToPage('signin');
  });
}

export async function boot() {
  if (typeof document === 'undefined') return;

  installListeners();
  await loadStrings();

  state.signInUrl = pageUrl('signin');
  state.token = resetTokenFrom(location.search, location.hash);

  // A token in the address bar is a credential in the address bar, and it stays
  // there through every screenshot, every shared link and every browser
  // history sync until this line runs. It is held in memory from here on.
  if (state.token !== null) {
    try {
      history.replaceState(history.state, '', location.pathname);
    } catch {
      // Best effort. The token still works; only the tidying failed.
    }
  }

  render();
}

/* Boot only on the real page. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
