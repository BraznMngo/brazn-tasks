/**
 * confirmed.js — the address-confirmed result screen (BRA-1475).
 *
 * The mail carries a token; this page spends it and says what happened. It is a
 * RESULT screen and not a form: there is nothing for the reader to fill in, and
 * the only thing they can do afterwards is sign in.
 *
 * THIS IS THE ONE PAGE HERE WHOSE MECHANISM ALREADY WORKED. `userEmailConfirm`
 * is already on the list of query names that survive the site-wide lockout at
 * the root (`restrictedUIConfirmationQueries`), which is exactly why confirming
 * an address works today and resetting a password does not — the reset token's
 * name is not on that list. What changes here is only which document answers:
 * ours rather than the old application's.
 *
 * SPENDING THE TOKEN ON LOAD IS CORRECT HERE and would be wrong on the
 * invitation page, so the difference is worth stating. Confirming an address is
 * the entire purpose of following this link, it is idempotent from the reader's
 * point of view, and it takes no seat and creates no account. An invitation is
 * none of those things, which is why that page does nothing until a button is
 * pressed.
 */

'use strict';

import * as api from './api.js';
import {t} from './i18n.js';
import {
  brandBlock,
  esc,
  forkErrorSentence,
  goToPage,
  loadStrings,
  renderAuth,
  tx,
} from './auth-shell.js';

/* ------------------------------------------------------------------ *
 * 1. Pure parsing
 * ------------------------------------------------------------------ */

/**
 * The confirmation token, from the fragment first and the query second, or null.
 *
 * Both are read for the reason password.js reads both: every link mailed so far
 * carries `?userEmailConfirm=` at the site root, and a fragment is the shape a
 * link should have, because no browser transmits a fragment to any server.
 */
export function confirmationTokenFrom(search, hash) {
  const raw = String(hash ?? '');
  if (!raw.startsWith('?')) {
    const fragment = new URLSearchParams(raw.replace(/^#/, '')).get('token');
    if (fragment !== null && fragment !== '') return fragment;
  }
  const query = new URLSearchParams(String(search ?? '').replace(/^\?/, '')).get('userEmailConfirm');
  return query === null || query === '' ? null : query;
}

/* ------------------------------------------------------------------ *
 * 2. The surface
 * ------------------------------------------------------------------ */

/**
 * Three outcomes, three sentences, and none of them is a status code.
 *
 * `working` matters even though it is usually brief: this page makes a network
 * call before it can say anything, and a card that is blank while it waits
 * reads as a broken page rather than a busy one.
 */
export function confirmedSurface(state) {
  if (state.phase === 'working') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.confirmed.workingTitle')}</h1>
        <p>${tx('one.confirmed.workingBody')}</p>
      </div>`;
  }

  if (state.phase === 'failed') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.confirmed.failedTitle')}</h1>
        <p>${esc(state.sentence ?? '')}</p>
        <button type="button" class="auth-submit" data-action="confirmed-sign-in">${tx('one.confirmed.signIn')}</button>
      </div>`;
  }

  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx('one.confirmed.title')}</h1>
      <p>${tx('one.confirmed.body')}</p>
      <button type="button" class="auth-submit" data-action="confirmed-sign-in">${tx('one.confirmed.signIn')}</button>
    </div>`;
}

/* ------------------------------------------------------------------ *
 * 3. The impure spine
 * ------------------------------------------------------------------ */

const state = {phase: 'working', sentence: null};

function render() {
  renderAuth(confirmedSurface(state));
}

export async function boot() {
  if (typeof document === 'undefined') return;

  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null || el.getAttribute('data-action') !== 'confirmed-sign-in') return;
    event.preventDefault();
    goToPage('signin');
  });

  await loadStrings();

  const token = confirmationTokenFrom(location.search, location.hash);
  if (token === null) {
    // No token at all. This is not a failure of confirmation — there was
    // nothing to confirm — so it says so plainly rather than reporting a
    // refusal nobody made.
    state.phase = 'failed';
    state.sentence = t('one.confirmed.noToken');
    render();
    return;
  }

  render();

  try {
    history.replaceState(history.state, '', location.pathname);
  } catch {
    // Best effort tidying of a credential out of the address bar.
  }

  try {
    await api.confirmEmailAddress(token);
  } catch (err) {
    state.phase = 'failed';
    state.sentence = err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.confirmed.failedBody')
      : t('one.confirmed.failedBody');
    render();
    return;
  }

  state.phase = 'done';
  render();
}

/* Boot only on the real page. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
