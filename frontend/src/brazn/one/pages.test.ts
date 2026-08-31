import {describe, it, expect} from 'vitest'

import {
	ONE_DOCUMENTS,
	QUERY_DOCUMENTS,
	documentForAddress,
	isCurrentDocument,
	oneUrl,
} from '../../../public/one/pages.js'

/*
 * THE BROWSER-SIDE HALF OF THE SITE-WIDE LOCKOUT.
 *
 * The server decides what a request may receive; this decides where this front end is allowed to
 * send somebody. Before BRA-1475 the second decision had no owner at all, which is how a
 * signed-out person was sent to `/login`, `/register` and `/get-password-reset` — every one a
 * route of the old Vue application, whose router then runs the whole application in the browser.
 *
 * This file supports criteria 15 and 17 and is not on its own evidence for either. Criterion 17
 * says "a signed-out person cannot reach any page of the old application FROM ANY ADDRESS", and
 * only a running deployment answers that: what the server serves at `/login` is decided in
 * `pkg/routes/static_brazn.go`, not here.
 */

const ORIGIN = 'https://dev.tasks.brazn.one/'

describe('the one list of documents this front end may navigate to', () => {
	it('refuses a name that is not on the list, rather than producing a plausible address', () => {
		expect(oneUrl('signin', ORIGIN)).toBe('https://dev.tasks.brazn.one/one/signin.html')
		expect(oneUrl('password', ORIGIN)).toBe('https://dev.tasks.brazn.one/one/password.html')
		// MUTATION: returning a relative path for an unknown name makes this red — and a typo would
		// then navigate somewhere the server redirects, which under the old arrangement meant
		// landing in the Vue application.
		expect(() => oneUrl('login', ORIGIN)).toThrow()
		expect(() => oneUrl('register', ORIGIN)).toThrow()
		expect(() => oneUrl('', ORIGIN)).toThrow()
	})

	it('never names a document outside /one/', () => {
		// Every file this front end can navigate to ships in one directory. A name resolving
		// anywhere else is a way back into the application this work replaces.
		for (const doc of ONE_DOCUMENTS) {
			expect(new URL(oneUrl(doc.name, ORIGIN)).pathname.startsWith('/one/')).toBe(true)
		}
	})

	it('carries a query and a fragment through without losing either', () => {
		expect(oneUrl('error', ORIGIN, {search: 'reason=seats-full'}))
			.toBe('https://dev.tasks.brazn.one/one/error.html?reason=seats-full')
		expect(oneUrl('signin', ORIGIN, {hash: 'redirect=%2Fone%2Ftask.html'}))
			.toBe('https://dev.tasks.brazn.one/one/signin.html#redirect=%2Fone%2Ftask.html')
	})
})

describe('which document answers a mailed link', () => {
	it('sends the reset token to the page that can spend it, WHEREVER the link put it', () => {
		// THIS IS FAULT 1 OF THE TICKET, IN ONE LINE. The server mails `<site root>?userPasswordReset=`
		// — an address with a query, which no path allowlist can express — and the lockout
		// redirected it, so the token was discarded and the customer's one link was spent.
		expect(documentForAddress('/', '?userPasswordReset=tok')).toBe('password')
		// A rule that also required a particular path would break the moment a link was written
		// with one, so the query decides wherever it appears.
		expect(documentForAddress('/one/task.html', '?userPasswordReset=tok')).toBe('password')
		expect(documentForAddress('/', '?userEmailConfirm=tok')).toBe('confirmed')
		expect(documentForAddress('/', '?accountDeletionConfirm=tok')).toBe('confirmed')
		// MUTATION: deleting a name from QUERY_DOCUMENTS makes this red, and that mailed link goes
		// back to being discarded.
		expect(Object.keys(QUERY_DOCUMENTS).sort())
			.toEqual(['accountDeletionConfirm', 'userEmailConfirm', 'userPasswordReset'])
	})

	it('answers at every address the old application used to own', () => {
		// The six the lockout used to exempt, plus the two prefixes. With all of them ours, the
		// exemption list is empty — which is the second half of criterion 17.
		expect(documentForAddress('/login')).toBe('signin')
		expect(documentForAddress('/register')).toBe('signin')
		expect(documentForAddress('/oauth/authorize')).toBe('signin')
		expect(documentForAddress('/auth/openid/google')).toBe('signin')
		expect(documentForAddress('/password-reset')).toBe('password')
		expect(documentForAddress('/get-password-reset')).toBe('password')
		expect(documentForAddress('/confirm')).toBe('confirmed')
		expect(documentForAddress('/')).toBe('settings')
	})

	it('claims nothing it does not own', () => {
		expect(documentForAddress('/share/abc')).toBeNull()
		expect(documentForAddress('/api/v1/login')).toBeNull()
		expect(documentForAddress('/user/settings/general')).toBeNull()
	})
})

describe('the redirect-loop guard, browser half', () => {
	it('knows it is already standing on the document it was about to navigate to', () => {
		// The server answers 404 rather than redirect when a request's computed target equals the
		// request itself, because a build that failed to copy a page into dist/ would otherwise be
		// an infinite bounce. A page can produce the same loop with NO SERVER INVOLVED — deciding
		// "you belong on the sign-in page" while it IS the sign-in page — and nothing would report
		// it, because no request leaves the browser.
		expect(isCurrentDocument('signin', '/one/signin.html')).toBe(true)
		// Asked through the address rather than by comparing file names, because the server hands a
		// document back AT the address the person asked for. A file-name comparison would answer no
		// here and send the browser on a pointless trip to a second copy of the page it is looking
		// at — which is a loop when it happens on every load.
		// MUTATION: comparing `pathname` against `one/<file>` makes this red.
		expect(isCurrentDocument('signin', '/login')).toBe(true)
		expect(isCurrentDocument('password', '/', '?userPasswordReset=tok')).toBe(true)
		expect(isCurrentDocument('settings', '/one/signin.html')).toBe(false)
		expect(isCurrentDocument('not-a-document', '/one/signin.html')).toBe(false)
	})
})

describe('the list itself', () => {
	it('names one file per document, and no two documents share a file', () => {
		const files = ONE_DOCUMENTS.map(doc => doc.file)
		expect(new Set(files).size).toBe(files.length)
		const names = ONE_DOCUMENTS.map(doc => doc.name)
		expect(new Set(names).size).toBe(names.length)
	})

	it('holds the five documents a signed-out person sees, plus the two behind a session', () => {
		// The ticket's table of five, and the two that were already here. A document added without
		// a thought about what a signed-out person can reach is what this counts.
		expect(ONE_DOCUMENTS.map(doc => doc.name).sort())
			.toEqual(['confirmed', 'error', 'join', 'password', 'settings', 'signin', 'task'])
	})

	it('gives the general error page exactly one address, because nobody arrives at it from outside', () => {
		const error = ONE_DOCUMENTS.find(doc => doc.name === 'error')
		expect(error?.answersAt).toEqual(['/one/error.html'])
	})
})
