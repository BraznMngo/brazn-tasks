/**
 * confirmed.js — the two result screens for a mailed confirmation (BRA-1475).
 *
 * The mail carries a token; this page spends it and says what happened. It is a
 * RESULT screen and not a form: there is nothing for the reader to fill in.
 *
 * TWO THINGS ARE CONFIRMED BY MAIL IN THIS PRODUCT, and they are not the same
 * event, so this page has two states rather than one:
 *
 *   * `?userEmailConfirm=` — an email address. Unauthenticated, idempotent from
 *     the reader's point of view, and the whole purpose of following the link.
 *   * `?accountDeletionConfirm=` — a request to DELETE THE ACCOUNT. This one is
 *     not in the ticket's table of five documents, and it needs a home anyway:
 *     it cannot keep reaching the old application without leaving criterion 17
 *     unmet, and it cannot be redirected without destroying the token, which is
 *     the fault this whole ticket exists to fix. Showing somebody who just
 *     confirmed the deletion of their account a screen about their email
 *     address would be worse than either.
 *
 * THE DELETION CONFIRMATION NEEDS A SESSION AND THE ADDRESS CONFIRMATION DOES
 * NOT, which is the difference that shapes the code below. `UserConfirmDeletion`
 * resolves the account from the SESSION and uses the token only as a second
 * factor (pkg/routes/api/v1/user_deletion.go), and that is the right shape for
 * an irreversible act — a token read out of somebody's mailbox should not on its
 * own be enough to destroy their account. So a person following a deletion link
 * while signed out cannot be told "done"; they are told, truthfully, that they
 * have to sign in, and sent there with this address carried in the fragment so
 * the confirmation finishes when they come back.
 *
 * SPENDING A TOKEN ON LOAD IS CORRECT HERE and would be wrong on the invitation
 * page, so the difference is worth stating. Following either of these links is
 * an unambiguous instruction to do the one thing the link is for. An invitation
 * is not: it takes a seat, creates an account and picks a username, none of
 * which anybody has agreed to yet, which is why that page does nothing until a
 * button is pressed.
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
 * What this arrival is confirming, and with which token.
 *
 * Returns `{kind, token}` where kind is `email` or `deletion`, or null when the
 * address carries neither.
 *
 * THE DELETION QUERY IS READ FIRST. An address carrying both is not a shape
 * anything produces, but if one ever arrived, treating it as the address
 * confirmation would silently skip the more consequential of the two and report
 * success for the other.
 *
 * A `#token=` fragment is read for the address confirmation as well, and second,
 * for the reason password.js reads one: no browser transmits a fragment to any
 * server, so a link built that way never reaches an access log. Nothing mails it
 * today. There is deliberately no fragment form for the deletion, because that
 * token is useless without a session anyway and a second shape would be a second
 * thing to keep correct for no gain.
 */
export function confirmationFrom(search, hash) {
  const params = new URLSearchParams(String(search ?? '').replace(/^\?/, ''));

  const deletion = params.get('accountDeletionConfirm');
  if (deletion !== null && deletion !== '') return {kind: 'deletion', token: deletion};

  const raw = String(hash ?? '');
  if (!raw.startsWith('?')) {
    const fragment = new URLSearchParams(raw.replace(/^#/, '')).get('token');
    if (fragment !== null && fragment !== '') return {kind: 'email', token: fragment};
  }

  const email = params.get('userEmailConfirm');
  if (email !== null && email !== '') return {kind: 'email', token: email};

  return null;
}

/* ------------------------------------------------------------------ *
 * 2. The surfaces
 * ------------------------------------------------------------------ */

/**
 * `working` matters even though it is usually brief: this page makes a network
 * call before it can say anything, and a card that is blank while it waits reads
 * as a broken page rather than a busy one.
 *
 * No outcome here shows a status code. "403" tells a customer nothing they can
 * act on, and on the deletion path it would be the last thing they ever read
 * from this product.
 */
export function confirmedSurface(state) {
  if (state.phase === 'working') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.confirmed.workingTitle')}</h1>
        <p>${tx('one.confirmed.workingBody')}</p>
      </div>`;
  }

  // The deletion is confirmed. There is no way onward from here on purpose:
  // offering somebody a sign-in button seconds after they confirmed the end of
  // their account invites them to walk into a refusal.
  if (state.phase === 'deletion-done') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.confirmed.deletion.doneTitle')}</h1>
        <p>${tx('one.confirmed.deletion.doneBody')}</p>
      </div>`;
  }

  // The deletion link is valid but nobody is signed in. This says so rather than
  // reporting a confirmation that did not happen, and the button carries this
  // exact address so the confirmation finishes on the way back.
  if (state.phase === 'deletion-needs-session') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.confirmed.deletion.signInTitle')}</h1>
        <p>${tx('one.confirmed.deletion.signInBody')}</p>
        <button type="button" class="auth-submit" data-action="confirmed-sign-in">${tx('one.confirmed.signIn')}</button>
      </div>`;
  }

  if (state.phase === 'deletion-failed') {
    return `${brandBlock()}
      <div class="auth-result">
        <h1 class="auth-title">${tx('one.confirmed.deletion.failedTitle')}</h1>
        <p>${esc(state.sentence ?? '')}</p>
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

const state = {phase: 'working', sentence: null, returnTo: null};

function render() {
  renderAuth(confirmedSurface(state));
}

/**
 * Take the token out of the address bar.
 *
 * It is a credential, and until this runs it sits in the address bar through
 * every screenshot, every shared link and every browser history sync. It is held
 * in memory from here on.
 */
function stripTokenFromAddress() {
  try {
    history.replaceState(history.state, '', location.pathname);
  } catch {
    // Best effort. The token still works; only the tidying failed.
  }
}

async function confirmDeletion(token) {
  // A SESSION IS THE PRECONDITION, not an optimisation: the route resolves the
  // account from it. Asking first is what lets this page tell "you are not
  // signed in" apart from "the token was refused", which are two completely
  // different things to say to somebody deleting their account.
  if (!await api.initSession()) {
    state.phase = 'deletion-needs-session';
    render();
    return;
  }

  try {
    await api.confirmAccountDeletion(token);
  } catch (err) {
    state.phase = 'deletion-failed';
    state.sentence = err instanceof api.ForkError
      ? forkErrorSentence(err, 'one.confirmed.deletion.failedBody')
      : t('one.confirmed.deletion.failedBody');
    render();
    return;
  }

  state.phase = 'deletion-done';
  render();
}

async function confirmEmail(token) {
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

export async function boot() {
  if (typeof document === 'undefined') return;

  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null || el.getAttribute('data-action') !== 'confirmed-sign-in') return;
    event.preventDefault();
    // The address this page was reached at travels in the FRAGMENT, so the
    // sign-in page can return the person here to finish, and no proxy or access
    // log sees the token on the way.
    goToPage('signin', state.returnTo === null ? undefined : {
      hash: `redirect=${encodeURIComponent(state.returnTo)}`,
    });
  });

  await loadStrings();

  const confirmation = confirmationFrom(location.search, location.hash);
  if (confirmation === null) {
    // No token at all. This is not a failed confirmation — there was nothing to
    // confirm — so it says so plainly rather than reporting a refusal nobody
    // made.
    state.phase = 'failed';
    state.sentence = t('one.confirmed.noToken');
    render();
    return;
  }

  // Recorded BEFORE the address bar is tidied, because it is what the sign-in
  // page needs to bring a signed-out person back to.
  state.returnTo = location.pathname + location.search;
  render();
  stripTokenFromAddress();

  if (confirmation.kind === 'deletion') {
    await confirmDeletion(confirmation.token);
    return;
  }
  await confirmEmail(confirmation.token);
}

/* Boot only on the real page. A test importing the pure functions has no
 * `#auth` in its document and gets no fetch, no storage and no render. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
