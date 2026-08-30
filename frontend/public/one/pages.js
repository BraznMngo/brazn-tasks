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
 * `name` is what callers use. `file` is what ships. `answersAt` is what the SERVER must route to
 * that file, written here so the two halves of the lockout are readable side by side; nothing in
 * the browser consults it, because a browser is never asked to route `/login` — it is handed the
 * document the server already chose. It is the machine-readable form of the ticket's five-row
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
    answersAt: Object.freeze(['/', '/one/settings.html']),
  }),
  Object.freeze({
    name: 'task',
    file: 'task.html',
    answersAt: Object.freeze(['/one/task.html', '/tasks/*']),
  }),
  Object.freeze({
    name: 'signin',
    file: 'signin.html',
    // `/oauth/authorize` is here and has no document of its own, which is the ticket's own
    // ruling: signing in is one thing and where somebody lands afterwards is three things.
    // `/auth/openid/*` is the address registered with Google, byte for byte, and serving our own
    // document there changes nothing in the Google console.
    answersAt: Object.freeze(['/one/signin.html', '/login', '/oauth/authorize', '/auth/openid/*']),
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
    answersAt: Object.freeze(['/one/error.html']),
  }),
]);

/**
 * The two query names a mailed link carries AT THE SITE ROOT, and the document each one belongs
 * to.
 *
 * These are the reason an allowlist of paths is not enough, and they are the whole of fault 1 in
 * the ticket. The server mails `<public url>?userPasswordReset=<token>` — the root with a query,
 * which no path allowlist can express — and a redirect carries only its destination, so the
 * token is discarded and the customer's link is spent on a page that cannot help them.
 *
 * `userPasswordReset` IS THE ONE THAT IS MISSING FROM THE SERVER TODAY. This table names it so
 * the browser knows where such an arrival belongs; the server's own `restrictedUIConfirmationQueries`
 * has to name it too, or the request never reaches a document at all. That server-side line is
 * outside this file and outside this front end.
 */
export const ROOT_QUERY_DOCUMENTS = Object.freeze({
  userEmailConfirm: 'confirmed',
  userPasswordReset: 'password',
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

  for (const [query, name] of Object.entries(ROOT_QUERY_DOCUMENTS)) {
    if (path !== '/') continue;
    if (new URLSearchParams(String(search ?? '').replace(/^\?/, '')).get(query) !== null) return name;
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
export function isCurrentDocument(name, pathname) {
  const doc = documentNamed(name);
  if (doc === null) return false;
  return String(pathname ?? '').endsWith(`/one/${doc.file}`);
}
