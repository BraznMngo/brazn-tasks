/**
 * pages.js — THE BROWSER-SIDE HALF OF THE SITE-WIDE LOCKOUT (BRA-1475).
 *
 * The server-side half exists and works by construction: `pkg/routes/static_brazn.go` decides,
 * per request, which addresses reach a document of ours and redirects everything else to
 * `/one/settings.html`. Until this file there was no browser-side half at all, and the hole it
 * leaves is not theoretical — it is the one every page here was walking through. `app.js` sent a
 * signed-out person to `/login`, and `join.js` offered three buttons that went to `/login`,
 * `/register` and `/get-password-reset`. Each of those is a page of the OLD Vue application, and
 * serving that form serves the whole application, because its router runs in the browser.
 *
 * So the lockout has two halves and they answer different questions:
 *
 *   * the SERVER decides what a request is allowed to receive;
 *   * the BROWSER decides where this front end is allowed to send somebody.
 *
 * A server-side allowlist cannot make the second decision, because a navigation this front end
 * performs is a fresh request that the server then has to allow — which is exactly why the
 * exemption list existed. With every page a signed-out person sees now being one of ours, the
 * browser never needs to leave `/one/`, and this file is what makes that a rule rather than a
 * habit: `oneUrl()` is the only way to build an address to navigate to, and it refuses a name
 * that is not on the list below.
 *
 * ONE LIST, IN ONE PLACE (acceptance criterion 18). `ONE_DOCUMENTS` is that list. The server's
 * own tables in `pkg/routes/static_brazn.go` are the other reader of it, and keeping the two in
 * agreement is a build-time job rather than a runtime one — see the note on `answersAt` below.
 *
 * NO STRINGS, NO DOM, NO FETCH. This module is pure and import-time pure, for `api.js`'s reason:
 * everything here is a table and four functions over it, so the whole of it can be reasoned
 * about without a browser.
 */

'use strict';

/**
 * Every document this front end ships, and the addresses each one answers at.
 *
 * `name` is what callers use. `file` is what ships. `answersAt` is every address the SERVER
 * SERVES THAT FILE AT, written here so the two halves of the lockout are readable side by side.
 *
 * SERVED AT, NEVER REDIRECTED TO, and the distinction is the whole value of the table. The server
 * does both: it serves a document in place at the addresses below, so a token in the query
 * survives to be read; and it redirects `/` and `/tasks/{id}` to a document's own address, which
 * discards the query and is why those two are deliberately absent. A row for a redirected address
 * would make `documentForAddress` claim a page is standing somewhere it never stands, and the
 * only reader of that claim is the guard that stops a page navigating to itself. It is the machine-readable form of the ticket's five-row
 * table, and it is what a build check compares against the server's tables.
 *
 * A path in `answersAt` is exact. A trailing `/*` means a prefix, which is how the OpenID return
 * address (`/auth/openid/{provider}`, one segment per provider) is expressed without naming
 * every provider — the same reason `restrictedUIAuthPrefix` is a prefix on the server.
 */
export const ONE_DOCUMENTS = Object.freeze([
  Object.freeze({
    name: 'settings',
    file: 'settings.html',
    // The site root is NOT here, and its absence is load-bearing. The server
    // REDIRECTS `/` to this document rather than serving it there
    // (`braznRestrictedUITarget`), so nothing is ever standing at `/` believing
    // it is the settings page. Listing it made `documentForAddress('/')` answer
    // "settings", and the only page that is ever at `/` is one of ours serving a
    // mailed token that has just tidied the token out of the address bar — so a
    // later "take me into the product" control on the password or confirmation
    // page would ask `isCurrentDocument('settings', '/')`, be told yes, skip the
    // navigation, and leave the person where they were with no error.
    answersAt: Object.freeze(['/one/settings.html']),
  }),
  Object.freeze({
    name: 'task',
    file: 'task.html',
    // `/tasks/{id}` is NOT here, for the reason the site root is not: the server
    // redirects a task deep link to `/one/task.html?task={id}` rather than
    // serving this document at it.
    answersAt: Object.freeze(['/one/task.html']),
  }),
  Object.freeze({
    name: 'signin',
    file: 'signin.html',
    // `/oauth/authorize` is here and has no document of its own, which is the ticket's own
    // ruling: signing in is one thing and where somebody lands afterwards is three things.
    // `/auth/` is a PREFIX rather than a list, and it covers the address registered with Google
    // (`/auth/openid/{provider}`, one segment per provider) byte for byte, so serving our own
    // document there changes nothing in the Google console.
    // `/register` is here and carries NO REGISTRATION FORM. Somebody who types that address
    // gets the sign-in page, whose account-creation link leads to the one surface that makes a
    // real account. A second registration form is a bullet of the ticket's own "what the fix
    // must not be", and a form posting to a route that refuses everybody is worse than none.
    answersAt: Object.freeze(['/one/signin.html', '/login', '/register', '/oauth/authorize', '/auth/*']),
  }),
  Object.freeze({
    name: 'join',
    file: 'join.html',
    // Reached from the invitation email and from nowhere else. The invitation handle is in the
    // query and the token is after the hash.
    answersAt: Object.freeze(['/one/join.html']),
  }),
  Object.freeze({
    name: 'password',
    file: 'password.html',
    answersAt: Object.freeze(['/one/password.html', '/password-reset', '/get-password-reset']),
  }),
  Object.freeze({
    name: 'confirmed',
    file: 'confirmed.html',
    answersAt: Object.freeze(['/one/confirmed.html', '/confirm']),
  }),
  Object.freeze({
    name: 'error',
    file: 'error.html',
    // THE SERVER NEVER ROUTES TO THIS ONE, and that is the difference worth noticing: every
    // other document here answers an address a person can arrive at from outside. Somebody
    // reaches this page only because one of ours sent them, so it has exactly one address.
    answersAt: Object.freeze(['/one/error.html']),
  }),
]);

/**
 * The three query names a mailed link carries, and the document each one belongs to.
 *
 * THESE ARE THE REASON AN ALLOWLIST OF PATHS IS NOT ENOUGH, and they are the whole of fault 1 in
 * the ticket. The server mails `<public url>?userPasswordReset=<token>` — an address with a
 * query, which no path allowlist can express — and a redirect carries only its destination, so
 * the token was discarded and the customer's one link was spent on a page that could not help
 * them. The server now hands the right document back AT THE ADDRESS THE PERSON ASKED FOR rather
 * than redirecting, which is what leaves the token intact for the page to read.
 *
 * A query name here is matched WHEREVER IT APPEARS, not only at the site root. The root is where
 * mail points today, but the shape of the fix is "this query names this document", and a rule
 * that also required a particular path would break the moment a link was written with one.
 *
 * `accountDeletionConfirm` HAS NO DOCUMENT IN THE TICKET'S TABLE OF FIVE, and it needs one
 * anyway: it cannot keep reaching the old application without leaving criterion 17 unmet, and it
 * cannot be redirected without destroying the token. It lands on the confirmation page, which
 * carries a second state for it rather than telling somebody who confirmed a deletion that their
 * email address is now confirmed.
 */
export const QUERY_DOCUMENTS = Object.freeze({
  userEmailConfirm: 'confirmed',
  userPasswordReset: 'password',
  accountDeletionConfirm: 'confirmed',
});

function documentNamed(name) {
  return ONE_DOCUMENTS.find(doc => doc.name === name) ?? null;
}

/**
 * The address of one of our documents, as a string to navigate to.
 *
 * THIS IS THE ONLY WAY THIS FRONT END MAY BUILD A DESTINATION, and refusing an unknown name is
 * the point rather than defensive tidiness: a typo that resolved to a relative path would
 * navigate somewhere the server then redirects, and under the old arrangement that meant landing
 * in the Vue application. A name that is not on the list is a programming error, so it throws
 * rather than returning something plausible.
 *
 * `base` is resolved by the caller, which is `api.js`'s `forkAppUrl` — this module deliberately
 * does not know the origin, so it stays free of both fetch and location.
 *
 * @param {string} name    one of `ONE_DOCUMENTS[].name`
 * @param {string} base    the application base, ending in a slash
 * @param {{search?: string, hash?: string}} [parts]
 */
export function oneUrl(name, base, {search = '', hash = ''} = {}) {
  const doc = documentNamed(name);
  if (doc === null) {
    const err = new Error(`pages.js assertion: no ONE document named ${String(name)}`);
    err.name = 'ApiAssertionError';
    err.code = 'unknown-one-document';
    throw err;
  }
  const url = new URL(`one/${doc.file}`, base);
  if (search !== '') url.search = search.startsWith('?') ? search : `?${search}`;
  if (hash !== '') url.hash = hash.startsWith('#') ? hash : `#${hash}`;
  return url.toString();
}

/**
 * Which of our documents answers a given address, or null when none does.
 *
 * Pure, and the browser mirror of what the server decided. It is what lets a page tell "I am the
 * document for this address" from "I am standing somewhere I was redirected to", which is the
 * difference between reading a mailed token out of the address and ignoring it.
 */
export function documentForAddress(pathname, search = '') {
  const path = String(pathname ?? '');

  // The query decides first, because it is the more specific statement: an address carrying a
  // mailed token names the document that can spend it, whatever path it happens to sit on.
  const params = new URLSearchParams(String(search ?? '').replace(/^\?/, ''));
  for (const [query, name] of Object.entries(QUERY_DOCUMENTS)) {
    if (params.get(query) !== null) return name;
  }

  for (const doc of ONE_DOCUMENTS) {
    for (const address of doc.answersAt) {
      if (address.endsWith('/*')) {
        if (path.startsWith(address.slice(0, -1))) return doc.name;
        continue;
      }
      if (path === address) return doc.name;
    }
  }
  return null;
}

/**
 * Whether the browser is already standing on the named document.
 *
 * THE REDIRECT-LOOP GUARD'S BROWSER HALF. `braznServeAppShell` answers 404 rather than redirect
 * when a request's computed target equals the request itself, because a build that failed to copy
 * a page into `dist/` would otherwise become an infinite bounce. The browser can produce the same
 * loop on its own — a page that decides "you belong on the sign-in page" while it IS the sign-in
 * page navigates to itself forever, and no server sees any of it. Every navigation in this front
 * end is guarded by this.
 */
export function isCurrentDocument(name, pathname, search = '') {
  if (documentNamed(name) === null) return false;
  // ASKED THROUGH `documentForAddress` RATHER THAN BY COMPARING FILE NAMES, because the server
  // hands a document back at the address the person asked for instead of redirecting to
  // `/one/…`. So the sign-in document is genuinely the current document at `/login`, and a file
  // name comparison would answer no and send the browser on a pointless trip to a second copy of
  // the page it is already looking at.
  return documentForAddress(pathname, search) === name;
}
