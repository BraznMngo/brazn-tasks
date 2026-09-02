/**
 * error.js — the general error page (BRA-1475).
 *
 * IT IS GIVEN ITS HEADING AND BODY BY WHOEVER SENDS SOMEBODY TO IT. That is the
 * ticket's own description and it is the whole design: the page that knows what
 * went wrong is the page that was doing the thing, and a general error page
 * that tried to work it out for itself would be re-deriving a fact somebody
 * already held.
 *
 * WHAT ARRIVES, AND WHY IT ARRIVES THE WAY IT DOES. A `reason` travels in the
 * query, because it is one word from the closed set below: it survives a
 * browser with no usable storage, it can be pasted into a support conversation,
 * and it discloses nothing about anybody. The SERVICE'S OWN SENTENCE travels in
 * per-tab storage instead, because it is text about a person's account — an
 * address, a seat count, an organisation name — and an address bar is written
 * into every access log between the reader and here.
 *
 * ONE SENTENCE IS DEFINED HERE AND ONLY ONE, which the ticket quotes: an
 * address that already has an account is an error rather than a branch, and the
 * person is told to ask their administrator to add them as an existing user
 * instead. Everything else on this page is a heading and a body some other page
 * chose.
 *
 * EVERY SENTENCE IS IN THE READER'S LANGUAGE. Acceptance criterion 9 asks for
 * that in those words, so every reason below resolves through the six-language
 * catalogue, and the only untranslated text that can appear is the service's
 * own sentence, which is rendered verbatim (ruling C4) because paraphrasing a
 * refusal is how a customer ends up told something that is not true.
 */

'use strict';

import {
  brandBlock,
  esc,
  goToPage,
  installAuthLanguage,
  loadStrings,
  renderAuth,
  takeErrorSentence,
  tx,
} from './auth-shell.js';

/**
 * The reasons this page recognises, and the heading and body each one gets.
 *
 * A CLOSED SET, and closed on purpose: `reason` arrives in the address, so
 * anybody can put anything there. An unrecognised value falls to the general
 * pair rather than being rendered, so this page cannot be made to display a
 * stranger's text, and a reason a later change forgets to add here says
 * something true and vague rather than nothing at all.
 *
 * Every key is written out in full rather than assembled from the reason, so
 * the fork-guards i18n sweep — which greps quoted namespace-anchored literals —
 * can prove each one exists in the catalogue.
 */
const REASONS = Object.freeze({
  'invitation-expired': {
    title: 'one.error.invitationExpired.title',
    body: 'one.error.invitationExpired.body',
  },
  'invitation-revoked': {
    title: 'one.error.invitationRevoked.title',
    body: 'one.error.invitationRevoked.body',
  },
  'invitation-unknown': {
    title: 'one.error.invitationUnknown.title',
    body: 'one.error.invitationUnknown.body',
  },
  /**
   * THE LINK RAN OUT, THE INVITATION DID NOT, and the difference is the whole
   * reason this is not folded into the expiry above. Telling somebody to ask
   * for a new invitation when their invitation is perfectly good sends them to
   * an administrator who then cannot help — the administrator's own screen
   * shows a live invitation and no reason to replace it.
   *
   * IT CANNOT HAPPEN IN THE SHIPPED CONFIGURATION. Both lifetimes are seven
   * days and the invitation's deadline is evaluated first, so the invitation
   * expiry always wins. It is here because a configuration change would make it
   * live, and because a value that arrived without a home would otherwise fall
   * to the general sentence, which is the one case where the general sentence
   * is actively wrong.
   */
  'link-expired': {
    title: 'one.error.linkExpired.title',
    body: 'one.error.linkExpired.body',
  },
  'seats-full': {
    title: 'one.error.seatsFull.title',
    body: 'one.error.seatsFull.body',
  },
  // THE ONE SENTENCE THIS PAGE DEFINES ITSELF, quoted in the ticket: "This
  // account already exists. Please ask your administrator to add you as an
  // existing user instead."
  'account-exists': {
    title: 'one.error.accountExists.title',
    body: 'one.error.accountExists.body',
  },
  'not-invitable': {
    title: 'one.error.notInvitable.title',
    body: 'one.error.notInvitable.body',
  },
  'invitation-failed': {
    title: 'one.error.general.title',
    body: 'one.error.general.body',
  },
});

const GENERAL = Object.freeze({title: 'one.error.general.title', body: 'one.error.general.body'});

/* ------------------------------------------------------------------ *
 * 1. Pure
 * ------------------------------------------------------------------ */

/** The heading and body keys for a reason, falling to the general pair. */
export function pairForReason(reason) {
  return Object.prototype.hasOwnProperty.call(REASONS, String(reason ?? ''))
    ? REASONS[String(reason)]
    : GENERAL;
}

export function reasonFromSearch(search) {
  const value = new URLSearchParams(String(search ?? '').replace(/^\?/, '')).get('reason');
  return value === null || value === '' ? null : value;
}

export function errorSurface(state) {
  const pair = pairForReason(state.reason);
  // The service's own sentence is an EXTRA line under the body, never a
  // replacement for it: the body says what to do next, which is the half a
  // refusal never carries.
  const sentence = state.sentence === null || state.sentence === undefined || state.sentence === ''
    ? ''
    : `<p>${esc(state.sentence)}</p>`;
  return `${brandBlock()}
    <div class="auth-result">
      <h1 class="auth-title">${tx(pair.title)}</h1>
      <p>${tx(pair.body)}</p>
      ${sentence}
      <button type="button" class="auth-submit" data-action="error-sign-in">${tx('one.error.signIn')}</button>
    </div>`;
}

/* ------------------------------------------------------------------ *
 * 2. The impure spine
 * ------------------------------------------------------------------ */

const errorState = {reason: null, sentence: null};

function render() {
  renderAuth(errorSurface(errorState));
}

export async function boot() {
  if (typeof document === 'undefined') return;

  document.addEventListener('click', (event) => {
    const el = event.target instanceof Element ? event.target.closest('[data-action]') : null;
    if (el === null || el.getAttribute('data-action') !== 'error-sign-in') return;
    event.preventDefault();
    goToPage('signin');
  });

  installAuthLanguage(render);
  await loadStrings();

  errorState.reason = reasonFromSearch(location.search);
  // Consumed on read, so a refusal cannot reappear on a later visit that had
  // nothing to do with it. Kept in memory so a language change can re-render.
  errorState.sentence = takeErrorSentence();
  render();
}

/* Boot only on the real page. */
if (typeof document !== 'undefined' && document.getElementById('auth') !== null) {
  queueMicrotask(() => {boot();});
}
