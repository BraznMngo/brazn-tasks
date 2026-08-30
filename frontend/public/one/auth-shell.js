/**
 * auth-shell.js — what the five signed-out documents share (BRA-1475).
 *
 * Sign in, the invitation, the password page, the confirmation result and the
 * general error page are five separate documents for the reason task.html and
 * settings.html are two: an address somebody can hand to another person has to
 * name the thing they will see. What they share is a look, a string catalogue,
 * an escape rule and one error surface, and that is all this file is. It holds
 * no journey and makes no decision — each page owns its own.
 *
 * IT IS NOT app.js. app.js is the signed-in spine: it boots a session, loads the
 * user, the organization and the teams, installs gates and renders a view. None
 * of that exists before somebody has an account. Importing it here would also
 * be actively wrong, because its `boot()` self-schedules on any document
 * carrying `#app` — which is why every document below uses `#auth` instead.
 *
 * NO USER-FACING STRING IS WRITTEN HERE OR IN ANY PAGE THAT USES IT. Every
 * sentence resolves through `t()` against the six-language catalogue, or is the
 * server's own sentence rendered verbatim (ruling C4). The fork-guards i18n
 * sweep proves the first half by grepping quoted namespace-anchored literals,
 * which is why every key below is written out in full and never assembled.
 */

'use strict';

import {t, init as initI18n} from './i18n.js';
import {oneUrl, isCurrentDocument} from './pages.js';
import {forkAppUrl} from './api.js';

/* ------------------------------------------------------------------ *
 * 1. Escaping, which is not optional on these pages
 * ------------------------------------------------------------------ */

/**
 * Every one of these pages renders a sentence the SERVER wrote — a wrong
 * password, a spent token, a refused invitation — and ruling C4 says such a
 * sentence is rendered verbatim. Verbatim means as TEXT: a sentence carrying
 * markup must reach the reader as characters rather than as elements. These are
 * the only documents in this product an unauthenticated stranger can put a
 * string into, so the escape is load-bearing rather than habitual.
 */
export function esc(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

/** A catalogue string, escaped. Catalogue values are ours, but the parameters interpolated into them are not. */
export function tx(key, params) {
  return esc(t(key, params));
}

/* ------------------------------------------------------------------ *
 * 2. The shell
 * ------------------------------------------------------------------ */

/**
 * Load the catalogue before anything paints.
 *
 * There is no user preference to honour: nobody is signed in, so the language
 * is negotiated from the browser alone, which is the same rule app.js uses on
 * its own no-session path. A catalogue that fails to load still renders —
 * `t()` falls back to the key path — because a page of key paths is legible and
 * a blank page is not.
 */
export async function loadStrings() {
  try {
    await initI18n(null, typeof navigator !== 'undefined' ? navigator.languages : []);
  } catch (err) {
    console.error('[one/auth] no string catalogue could be loaded', err);
  }
}

/**
 * Paint the card and reveal it.
 *
 * `hidden` is in every one of these documents' markup rather than left to
 * JavaScript, for the reason settings.html states: a deferred module that 404s,
 * or throws while evaluating, never reaches any line of this file. Starting
 * hidden makes such a failure INVISIBLE rather than WRONG — an empty card
 * bordered on a pale field, with no explanation, forever — and this call is the
 * only thing that reveals it.
 */
export function renderAuth(html) {
  const root = document.getElementById('auth');
  if (root === null) return;
  root.innerHTML = html;
  document.querySelector('.auth-stage')?.classList.remove('hidden');
}

/** The brand mark. Both files ship and the stylesheet picks by theme. */
export function brandBlock() {
  return `<div class="auth-brand">
    <img class="brand-logo light" src="./logo-light.v1.png" width="155" height="72" alt="ONE Tasks" data-i18n-alt="one.brand.logoAlt">
    <img class="brand-logo dark" src="./logo-dark.v1.png" width="155" height="72" alt="ONE Tasks" data-i18n-alt="one.brand.logoAlt">
  </div>`;
}

/**
 * The one place errors appear, empty until there is something to say.
 *
 * `role="alert"` is on the node from the first render, not added with the
 * message: a live region has to exist before it changes for a screen reader to
 * announce the change. `:empty` hides it, so an empty box costs no space.
 */
export function bannerBlock() {
  return '<p class="auth-banner danger" id="authError" role="alert"></p>';
}

/**
 * Put a sentence in the one error place, or clear it.
 *
 * Takes TEXT and writes `textContent`, never markup, so a caller cannot pass a
 * server sentence through by accident and have it interpreted.
 */
export function showError(message) {
  const el = document.getElementById('authError');
  if (el === null) return;
  el.textContent = message === null || message === undefined ? '' : String(message);
}

/* ------------------------------------------------------------------ *
 * 3. Going somewhere, which on these pages is the whole risk
 * ------------------------------------------------------------------ */

/**
 * The address of one of our own documents.
 *
 * EVERY NAVIGATION THIS FRONT END MAKES GOES THROUGH HERE, and that is the
 * browser-side half of the site-wide lockout. Before BRA-1475 these pages sent
 * a signed-out person to `/login`, `/register` and `/get-password-reset`, each
 * a page of the old Vue application whose router then runs the whole of it in
 * the browser. `pages.js` refuses a name that is not one of ours, so a typo is
 * a thrown assertion rather than a quiet trip into the application this work
 * replaces.
 */
export function pageUrl(name, parts) {
  return oneUrl(name, forkAppUrl(''), parts);
}

/**
 * Navigate to one of our documents, unless we are already standing on it.
 *
 * THE REDIRECT-LOOP GUARD'S BROWSER HALF. The server answers 404 rather than
 * redirect when a request's computed target equals the request itself, because
 * a build that failed to copy a page into `dist/` would otherwise become an
 * infinite bounce. A page can produce the same loop with no server involved —
 * deciding "you belong on the sign-in page" while it IS the sign-in page — and
 * nothing would report it, because no request leaves the browser.
 *
 * @returns {boolean} true when it navigated.
 */
export function goToPage(name, parts) {
  if (isCurrentDocument(name, location.pathname, location.search)) return false;
  location.assign(pageUrl(name, parts));
  return true;
}

/**
 * Where a server's own sentence waits while the browser moves to the general
 * error page.
 *
 * THE REASON TRAVELS IN THE QUERY AND THE SENTENCE DOES NOT, and the split is
 * deliberate. A reason is one word from a closed set that error.js owns, so it
 * is safe in an address: it survives a browser with no usable storage, it can be
 * pasted into a support conversation, and it discloses nothing. A SENTENCE is
 * the service's own text about somebody's account — an address, a seat count, an
 * organisation name — and putting that in an address writes it into every access
 * log between here and the reader.
 *
 * Per tab, and consumed on read, so a refusal cannot reappear on a later visit
 * that had nothing to do with it.
 */
const ERROR_SENTENCE_KEY = 'brazn.one.error-sentence';

/**
 * Send somebody to the general error page.
 *
 * The heading and the body are given by whoever sends them, which is the
 * ticket's own description of this page: it holds one sentence of its own and
 * takes the rest from the caller.
 *
 * @param {string} reason    one word, from the set error.js recognises
 * @param {string|null} sentence  the service's own sentence, if it sent one
 */
export function sendToErrorPage(reason, sentence = null) {
  try {
    if (sentence !== null && String(sentence).trim() !== '') {
      sessionStorage.setItem(ERROR_SENTENCE_KEY, String(sentence));
    } else {
      sessionStorage.removeItem(ERROR_SENTENCE_KEY);
    }
  } catch {
    // Storage refused. The reason still travels, so the reader is told what
    // happened; only the service's own wording is lost.
  }
  location.assign(pageUrl('error', {search: `reason=${encodeURIComponent(reason)}`}));
}

/** Read and consume the sentence a sender left behind. */
export function takeErrorSentence() {
  try {
    const value = sessionStorage.getItem(ERROR_SENTENCE_KEY);
    sessionStorage.removeItem(ERROR_SENTENCE_KEY);
    return value === null || value === '' ? null : value;
  } catch {
    return null;
  }
}

/* ------------------------------------------------------------------ *
 * 4. The Google control
 * ------------------------------------------------------------------ */

/**
 * The official four-colour Google mark, byte-identical to the one the page this
 * replaces carries (`frontend/src/views/user/Login.vue`), because it is
 * Google's own brand asset and the branding guidelines do not allow a redrawn
 * one. Inline rather than a file, so it costs no request and no cache entry;
 * `aria-hidden` because the button's own text names it.
 *
 * No `xmlns` attribute: this is inline SVG in an HTML document, where the
 * namespace is implied, and the only absolute URL that would otherwise appear
 * anywhere under this directory is that namespace identifier.
 */
export function googleMark() {
  return `<svg viewBox="0 0 18 18" aria-hidden="true">
    <path fill="#4285F4" d="M17.64 9.2c0-.637-.057-1.251-.164-1.84H9v3.481h4.844c-.209 1.125-.843 2.078-1.796 2.717v2.258h2.908c1.702-1.567 2.684-3.874 2.684-6.615z"/>
    <path fill="#34A853" d="M9 18c2.43 0 4.467-.806 5.956-2.18l-2.908-2.259c-.806.54-1.837.86-3.048.86-2.344 0-4.328-1.584-5.036-3.711H.957v2.332C2.438 15.983 5.482 18 9 18z"/>
    <path fill="#FBBC05" d="M3.964 10.71A5.41 5.41 0 013.682 9c0-.593.102-1.17.282-1.71V4.958H.957A8.996 8.996 0 000 9c0 1.452.348 2.827.957 4.042l3.007-2.332z"/>
    <path fill="#EA4335" d="M9 3.58c1.321 0 2.508.454 3.44 1.345l2.582-2.58C13.463.891 11.426 0 9 0 5.482 0 2.438 2.017.957 4.958L3.964 7.29C4.672 5.163 6.656 3.58 9 3.58z"/>
  </svg>`;
}

/* ------------------------------------------------------------------ *
 * 5. Reading the server's refusal
 * ------------------------------------------------------------------ */

/**
 * The sentence to show for a failed fork call.
 *
 * THE SERVER'S OWN SENTENCE WINS whenever there is one (ruling C4). The
 * catalogue key is the fallback for the case the server sent nothing at all —
 * a transport failure, or a refusal with an empty envelope — and never a
 * paraphrase of a sentence that did arrive. A status code is never shown: "403"
 * tells a customer nothing they can act on.
 */
export function forkErrorSentence(err, fallbackKey) {
  const sentence = typeof err?.serverMessage === 'string' ? err.serverMessage.trim() : '';
  return sentence !== '' ? sentence : t(fallbackKey);
}
