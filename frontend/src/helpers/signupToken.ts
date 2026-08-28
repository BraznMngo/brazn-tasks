// The signup token the commercial service hands a customer who is entitled to an account
// here (BRA-1071, contract BRA-1080).
//
// IT ARRIVES IN THE URL FRAGMENT and never in a query parameter:
//
//     <fork-origin>/register#signup_token=<token>
//
// No browser transmits a fragment to any server, so it cannot appear in this
// instance's access logs, in a proxy log, or in a Referer header — RFC 7231
// requires the fragment to be stripped before a Referer is sent. It also
// survives an ordinary redirect, so the handoff needs no interstitial.
//
// WHAT THE FRAGMENT DOES NOT ESCAPE is browser history and the address bar,
// which is why read() clears it with history.replaceState the moment it has it.
// The server sends `Referrer-Policy: no-referrer` on these pages so the path is
// not disclosed either.

const FRAGMENT_KEY = 'signup_token'

// Where the token is kept between reading it and using it. sessionStorage
// rather than a module variable because the Google route leaves this origin
// entirely and comes back on a different page, and rather than localStorage
// because it must not outlive the tab: a token left behind would be offered by
// the next person to register on a shared machine.
//
// MUST MATCH `SIGNUP_TOKEN_STORAGE_KEY` in frontend/public/one/join.js BYTE
// FOR BYTE. The invitation acceptance page (BRA-1439 Story 5) stores the
// token it finds in its own fragment under this key so the flows here pick it
// up after the hand-off; the two files are separate bundles that may not
// import each other, so there is no shared constant and no test to catch a
// drift — grep for the literal there before renaming this.
const STORAGE_KEY = 'signupToken'

/**
 * Reads the signup token out of the current URL fragment, remembers it for this
 * tab, and removes it from the address bar and from history.
 *
 * Safe to call more than once and on a page that carries no token: the value
 * already remembered is kept, so the Google round trip does not lose it.
 */
export function readSignupTokenFromFragment(): string {
	const fragment = window.location.hash.replace(/^#/, '')
	if (fragment !== '') {
		const token = new URLSearchParams(fragment).get(FRAGMENT_KEY)
		if (token !== null && token !== '') {
			window.sessionStorage.setItem(STORAGE_KEY, token)

			// Drop the fragment without adding a history entry and without
			// reloading. The URL keeps its path and query so nothing else about
			// the page changes.
			window.history.replaceState(
				window.history.state,
				'',
				window.location.pathname + window.location.search,
			)
		}
	}

	return getSignupToken()
}

/**
 * The token this tab is holding, or an empty string.
 */
export function getSignupToken(): string {
	return window.sessionStorage.getItem(STORAGE_KEY) ?? ''
}

/**
 * Forgets the token. Called once a registration has succeeded — the token is
 * consumed server-side at that point, so keeping it would only offer a spent
 * value to the next attempt.
 */
export function clearSignupToken() {
	window.sessionStorage.removeItem(STORAGE_KEY)
}
