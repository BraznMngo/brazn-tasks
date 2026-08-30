import {describe, it, expect, beforeEach, afterEach, beforeAll, vi} from 'vitest'

import * as api from '../../../public/one/api.js'
import {resetTokenFrom} from '../../../public/one/password.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import {
	ORIGIN,
	cardText,
	captureDocumentListeners,
	enqueue,
	fetchStub,
	forkRefusal,
	json,
	mountAuthCard,
	navigations,
	releaseDocumentListeners,
	requests,
	resetHarness,
	restoreLocation,
	settle,
	standAt,
	submitForm,
} from './auth-page-harness'

/*
 * THE PASSWORD PAGE — ACCEPTANCE CRITERION 12, WRITTEN FROM THE TICKET.
 *
 * "A customer who forgets their password gets back in. A stranger asking for a reset on an address
 * with no account sees the same answer as one with an account."
 *
 * WHAT THIS FILE CANNOT DECIDE, said here rather than buried. Criterion 12's first half — that a
 * customer actually gets back in — is settled by a real reset against a running deployment and by
 * nothing else. The ticket's own record says nobody has done that: "Nobody executed the
 * password-reset chain against production. Every link was read in code and each address confirmed
 * served by a read-only request, but no reset was requested, no password set, and no sign-in
 * performed." That sentence is carried here verbatim because it is the one most often dropped.
 */

beforeAll(() => {
	vi.stubGlobal('fetch', async (input: string) => (
		String(input).includes('/en.json')
			? new Response(enRaw, {headers: {'content-type': 'application/json'}})
			: new Response('not found', {status: 404})
	))
})

beforeEach(() => {
	resetHarness()
	captureDocumentListeners()
	api.resetSession()
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
	sessionStorage.clear()
	standAt('/one/password.html')
})

afterEach(() => {
	releaseDocumentListeners()
	api.configure({fetch: null, origin: null, randomUUID: null})
	document.body.innerHTML = ''
	restoreLocation()
})

let pageCounter = 0

/** A fresh copy of the page, evaluated with no card so it does not self-boot behind the test. */
async function freshPage() {
	document.body.innerHTML = ''
	pageCounter += 1
	const page = await import(/* @vite-ignore */ `../../../public/one/password.js?fresh=${pageCounter}`)
	mountAuthCard()
	return page
}

/* ================================================================== *
 * CRITERION 12, SECOND HALF — the answer is the same either way
 * ================================================================== */

describe('criterion 12 — a stranger and a customer see the same answer', () => {
	it('renders one identical sentence for an address with an account and one without', async () => {
		// THE SERVER ANSWERS 200 IN BOTH CASES ON PURPOSE. `RequestUserPasswordResetTokenByEmail`
		// returns nil for an address with no account and for a disabled one
		// (pkg/user/user_password_reset.go), because answering differently turns a route needing no
		// credentials into a way of sorting a list of addresses into customers and non-customers.
		// This asserts the BROWSER does not undo that.
		const seen: string[] = []
		for (const address of ['ada@example.com', 'nobody@example.com']) {
			resetHarness()
			standAt('/one/password.html')
			const {boot} = await freshPage()
			enqueue(json({message: 'Reset email sent.'}))

			await boot()
			await settle()
			submitForm('requestForm', {email: address})
			await settle()

			seen.push(cardText())
		}

		// Asserted against each other AND against a fixed sentence. Against each other alone would
		// pass if the page rendered nothing at all for both.
		expect(seen[0]).toBe(seen[1])
		expect(seen[0]).toContain('If there is an account for that address, a link to set a new password is on its way')
		// MUTATION: rendering "no account for that address" on the refusal path makes this red.
		expect(seen[0].toLowerCase()).not.toContain('no account')
		expect(seen[0].toLowerCase()).not.toContain('does not exist')
	})

	it('says nothing about the address when the request itself fails', async () => {
		// The page renders the SERVER's own sentence on a failure (ruling C4). That is right for a
		// transport failure and would be an enumeration oracle if the server ever answered
		// differently per address — so this pins that the page does not invent one of its own.
		resetHarness()
		const {boot} = await freshPage()
		enqueue(json({message: 'Service unavailable.'}, 503))

		await boot()
		await settle()
		submitForm('requestForm', {email: 'ada@example.com'})
		await settle()

		const text = cardText()
		expect(text).not.toContain('If there is an account for that address')
		expect(text.toLowerCase()).not.toContain('no such')
	})
})

/* ================================================================== *
 * CRITERION 12, FIRST HALF — the page that sets the password
 * ================================================================== */

describe('criterion 12 — the customer sets a new password', () => {
	it('shows the set-password state for a mailed link, and sends the token with the new password', async () => {
		// THE MAILED SHAPE, AND IT IS THE ONE THAT MATTERS. Every reset link in a customer's inbox
		// today is `<site root>?userPasswordReset=<token>`, and fault 1 of this ticket was that the
		// lockout REDIRECTED such an address, discarding the token. The page must read it from
		// where the mail actually puts it.
		standAt('/', '?userPasswordReset=tok-live')
		const {boot} = await freshPage()

		await boot()
		await settle()

		expect(document.getElementById('setForm')).not.toBeNull()
		expect(document.getElementById('requestForm')).toBeNull()

		enqueue(json({message: 'The password was updated.'}))
		submitForm('setForm', {password: 'a new long password'})
		await settle()

		const reset = requests()[requests().length - 1]
		expect(reset?.url).toBe(`${ORIGIN}/api/v1/user/password/reset`)
		// MUTATION: dropping the token from the payload makes this red, and every reset is refused.
		expect(reset?.body).toEqual({token: 'tok-live', new_password: 'a new long password'})
		expect(cardText()).toContain('Sign in')
	})

	it('takes the token out of the address bar once it has been read', async () => {
		// A token in the address bar is a credential in the address bar, and it stays there through
		// every screenshot, shared link and history sync until this runs.
		standAt('/', '?userPasswordReset=tok-live')
		const replaced: string[] = []
		const realReplaceState = history.replaceState.bind(history)
		history.replaceState = ((state: unknown, title: string, url?: string) => {
			replaced.push(String(url))
		}) as typeof history.replaceState

		try {
			const {boot} = await freshPage()
			await boot()
			await settle()
			// MUTATION: deleting the history.replaceState call makes this red.
			expect(replaced).toEqual(['/'])
		} finally {
			history.replaceState = realReplaceState
		}
	})

	it('sends a spent link back to the state that can actually help, saying why', async () => {
		// A person cannot fix a spent token by typing a different password. Leaving them on a form
		// that will refuse every attempt is the dead end the ticket is about.
		standAt('/one/password.html', '?userPasswordReset=tok-spent')
		const {boot} = await freshPage()

		await boot()
		await settle()
		enqueue(forkRefusal(1009, 'Invalid password reset token.'))
		submitForm('setForm', {password: 'a new long password'})
		await settle()

		// MUTATION: deleting the `err.code === CODE_INVALID_RESET_TOKEN` branch makes this red, and
		// the customer is left on a form that can never succeed.
		expect(document.getElementById('requestForm')).not.toBeNull()
		expect(cardText()).toContain('link')
	})

	it('refuses a password shorter than the server will accept, before asking the server', async () => {
		standAt('/one/password.html', '?userPasswordReset=tok-live')
		const {boot} = await freshPage()

		await boot()
		await settle()
		const before = requests().length
		submitForm('setForm', {password: 'short'})
		await settle()

		// MUTATION: deleting the length check makes this red — the request goes out and the person
		// is told by the server, one round trip later, what the page already knew.
		expect(requests()).toHaveLength(before)
		expect(cardText()).toContain('8')
	})
})

/* ================================================================== *
 * Where the token is read from
 * ================================================================== */

describe('the reset token this page will accept', () => {
	it('reads the query the mail actually uses, and prefers a fragment when both are present', () => {
		expect(resetTokenFrom('?userPasswordReset=q-token', '')).toBe('q-token')
		expect(resetTokenFrom('', '#token=f-token')).toBe('f-token')
		// A query token has already been written into every log between the reader and here by the
		// time this runs, so a browser holding both should use the one that never travelled.
		expect(resetTokenFrom('?userPasswordReset=q-token', '#token=f-token')).toBe('f-token')
		expect(resetTokenFrom('', '')).toBeNull()
		// MUTATION: dropping the leading-`?` guard makes this red, and a caller handing over
		// `location.search` would find a token in the fragment reader.
		expect(resetTokenFrom('', '?token=q-token')).toBeNull()
	})
})

/* ================================================================== *
 * CRITERION 15, browser half — this page too
 * ================================================================== */

describe('criterion 15 — the password page reaches no registration route', () => {
	it('addresses only the two password operations, and never sends a username', async () => {
		standAt('/one/password.html')
		const {boot} = await freshPage()
		enqueue(json({message: 'Reset email sent.'}))

		await boot()
		await settle()
		submitForm('requestForm', {email: 'ada@example.com'})
		await settle()

		for (const call of requests()) {
			expect(call.url).not.toContain('/api/v1/register')
			expect(JSON.stringify(call.body ?? '')).not.toContain('username')
		}
		expect(navigations()).toEqual([])
	})
})
