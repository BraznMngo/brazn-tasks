import {describe, it, expect, beforeEach, afterEach, vi} from 'vitest'

import * as api from '../../../public/one/api.js'
import {PENDING_JOIN_KEY} from '../../../public/one/app.js'
import {
	acceptedOutcome,
	boot,
	invitationIdFromSearch,
	joinSurface,
	signupTokenFromHash,
} from '../../../public/one/join.js'

/*
 * The invitation acceptance page (BRA-1439 Story 5), tested the only way this surface can be
 * (bar 9): pure functions, and the boot flow against a stubbed fetch. Nothing here is evidence
 * that POST /v1/organizations/invitations/accept works on a deployment - CI starts no commercial
 * service, and the ticket's standing rule is that /v1 is not E2E-testable from here.
 *
 * i18n is deliberately left unloaded (the global fetch is stubbed to reject), so every t() call
 * resolves to its key path. The assertions match on key paths because of that, which also pins
 * that the page renders SOMETHING legible when the catalogue cannot load.
 */

const ORIGIN = 'https://dev.tasks.brazn.one'

interface Call {
	url: string
	init: RequestInit
}

const calls: Call[] = []
let queue: Response[] = []

const fetchStub = vi.fn(async (url: string, init: RequestInit = {}) => {
	calls.push({url, init})
	const next = queue.shift()
	if (next === undefined) throw new Error(`unstubbed request: ${url}`)
	return next.clone()
})

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {status, headers: {'content-type': 'application/json'}})
}

describe('one/join.js pure parsing', () => {
	it('reads the invitation id from ?i= and nothing else', () => {
		expect(invitationIdFromSearch('?i=inv-9')).toBe('inv-9')
		expect(invitationIdFromSearch('?x=1&i=inv-9')).toBe('inv-9')
		// MUTATION: defaulting a missing id to '' - the page would POST an empty invitation_id
		// instead of rendering the missing-link sentence.
		expect(invitationIdFromSearch('?i=')).toBeNull()
		expect(invitationIdFromSearch('')).toBeNull()
		expect(invitationIdFromSearch(null)).toBeNull()
	})

	it('reads the signup token from the FRAGMENT, never from a query string', () => {
		expect(signupTokenFromHash('#signup_token=tok-1')).toBe('tok-1')
		expect(signupTokenFromHash('#a=b&signup_token=tok-1')).toBe('tok-1')
		expect(signupTokenFromHash('')).toBeNull()
		expect(signupTokenFromHash('#')).toBeNull()
		// The fragment placement is a SECURITY PROPERTY: no browser transmits a fragment, so the
		// token cannot reach a server log. A parser that also accepted `?signup_token=` would
		// invite the commercial side to move it into the query, where every proxy would see it.
		// MUTATION: reading location.search as a fallback in signupTokenFromHash's caller makes
		// this red only if the parser itself stays fragment-only - so the parser is pinned here.
		expect(signupTokenFromHash('?signup_token=tok-1')).toBeNull()
	})

	it('tells a fresh admission from a seat already held', () => {
		// Both are affirmative (api.js ACCEPT_INVITATION) and they are two different sentences:
		// rendering "you have joined" for a seat that was always theirs is the fake-success
		// direction bar 8 exists to prevent, one level below the guard.
		// MUTATION: collapsing the branch to always 'accepted' makes this red.
		expect(acceptedOutcome({outcome: 'admitted'})).toBe('accepted')
		expect(acceptedOutcome({outcome: 'already_member'})).toBe('already-member')
	})
})

describe('one/join.js surfaces', () => {
	it('renders each state with its own sentence and controls', () => {
		expect(joinSurface({kind: 'missing-link'})).toContain('one.join.missingLink')

		const choices = joinSurface({kind: 'choices'})
		expect(choices).toContain('data-action="join-signin"')
		expect(choices).toContain('data-action="join-set-password"')

		expect(joinSurface({kind: 'accepting'})).toContain('one.join.accepting')

		expect(joinSurface({kind: 'done', outcome: 'accepted'})).toContain('one.join.accepted')
		const already = joinSurface({kind: 'done', outcome: 'already-member'})
		expect(already).toContain('one.join.alreadyMember')
		expect(already).not.toContain('one.join.accepted')

		const refused = joinSurface({kind: 'refused', sentence: 'The service said no.'})
		expect(refused).toContain('The service said no.')
		expect(refused).toContain('data-action="join-retry"')
	})

	it('escapes the refusal sentence - it is server text, rendered as text', () => {
		// Ruling C4 renders the server sentence verbatim, and verbatim means TEXT: a sentence
		// carrying markup must reach the reader as characters, not as elements.
		// MUTATION: interpolating state.sentence without esc() makes this red.
		const surface = joinSurface({kind: 'refused', sentence: '<img src=x onerror=alert(1)>'})
		expect(surface).not.toContain('<img')
		expect(surface).toContain('&lt;img')
	})

	it('falls back to the missing-link surface for a state from nowhere', () => {
		expect(joinSurface(null)).toContain('one.join.missingLink')
		expect(joinSurface({})).toContain('one.join.missingLink')
	})
})

describe('one/join.js boot flow (stubbed fetch)', () => {
	beforeEach(() => {
		calls.length = 0
		queue = []
		fetchStub.mockClear()
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN, randomUUID: () => 'idem-1'})
		// i18n.js has no injection seam; a rejecting global keeps the catalogue unloaded so every
		// surface renders key paths, which the assertions above already rely on.
		vi.stubGlobal('fetch', vi.fn(async () => {
			throw new Error('no catalogue in tests')
		}))
		document.body.innerHTML = '<div class="stage hidden"><main id="join"></main></div>'
		localStorage.clear()
		sessionStorage.clear()
		history.replaceState(null, '', '/one/join.html')
	})

	afterEach(() => {
		vi.unstubAllGlobals()
		api.configure({fetch: null, origin: null, randomUUID: null})
		document.body.innerHTML = ''
	})

	it('accepts the invitation when a session exists, and consumes the return-leg marker', async () => {
		history.replaceState(null, '', '/one/join.html?i=inv-9')
		localStorage.setItem(PENDING_JOIN_KEY, JSON.stringify({i: 'inv-9', at: Date.now()}))
		queue = [
			json({token: 'access-1'}),
			json({outcome: 'admitted', organization_id: 'org-1'}),
		]

		await boot()

		// The refresh, then the acceptance - through the commercial base, with the fragment
		// discipline's sibling rule: the id travels in the BODY, exactly one field.
		expect(calls[0].url).toBe('https://dev.tasks.brazn.one/api/v1/user/token/refresh')
		expect(calls[1].url).toBe('https://dev.tasks.brazn.one/v1/organizations/invitations/accept')
		expect(JSON.parse(String(calls[1].init.body))).toEqual({invitation_id: 'inv-9'})

		expect(document.getElementById('join')?.innerHTML).toContain('one.join.accepted')
		// Terminal outcome, so the marker is spent: this is what bounds app.js's bounce at one.
		// MUTATION: deleting clearPendingJoin() from accept() makes this red, and every later
		// visit to the settings page bounces back here.
		expect(localStorage.getItem(PENDING_JOIN_KEY)).toBeNull()
	})

	it('renders already_member as its own sentence, not as a fresh admission', async () => {
		history.replaceState(null, '', '/one/join.html?i=inv-9')
		queue = [
			json({token: 'access-1'}),
			json({outcome: 'already_member', organization_id: 'org-1'}),
		]

		await boot()

		const surface = document.getElementById('join')?.innerHTML ?? ''
		expect(surface).toContain('one.join.alreadyMember')
		expect(surface).not.toContain('one.join.accepted')
	})

	it('renders a refusal when the guard refuses, and still consumes the marker', async () => {
		history.replaceState(null, '', '/one/join.html?i=inv-9')
		localStorage.setItem(PENDING_JOIN_KEY, JSON.stringify({i: 'inv-9', at: Date.now()}))
		queue = [
			json({token: 'access-1'}),
			json({outcome: 'no_invitation', organization_id: null}),
		]

		await boot()

		expect(document.getElementById('join')?.innerHTML).toContain('one.join.refusedLead')
		expect(localStorage.getItem(PENDING_JOIN_KEY)).toBeNull()
	})

	it('offers the sign-in choices when there is no session, and remembers the invitation', async () => {
		history.replaceState(null, '', '/one/join.html?i=inv-9')
		// The refresh cookie is absent or stale: the fork answers 401 and the session never opens.
		queue = [json({message: 'missing refresh token'}, 401)]

		await boot()

		const surface = document.getElementById('join')?.innerHTML ?? ''
		expect(surface).toContain('data-action="join-signin"')
		expect(surface).toContain('data-action="join-set-password"')
		// The marker is what lets app.js bring a freshly signed-in person back here.
		// MUTATION: dropping writePendingJoin() from the no-session branch makes this red, and a
		// recipient who had to sign in lands on the settings page with the invitation unaccepted.
		const marker = JSON.parse(localStorage.getItem(PENDING_JOIN_KEY) ?? 'null')
		expect(marker?.i).toBe('inv-9')
	})

	it('captures the fragment token under the Vue helper\'s own key and strips the fragment', async () => {
		history.replaceState(null, '', '/one/join.html?i=inv-9#signup_token=tok-1')
		queue = [json({message: 'missing refresh token'}, 401)]

		await boot()

		// 'signupToken' is frontend/src/helpers/signupToken.ts's STORAGE_KEY, byte for byte - the
		// hand-off contract that lets the Vue app's register and Google flows find the token.
		// MUTATION: renaming either side's literal makes this red.
		expect(sessionStorage.getItem('signupToken')).toBe('tok-1')
		// The fragment is gone from the address bar; the query survives.
		expect(location.hash).toBe('')
		expect(location.search).toBe('?i=inv-9')
	})

	it('renders the missing-link sentence when the link carries no invitation id', async () => {
		history.replaceState(null, '', '/one/join.html')

		await boot()

		expect(document.getElementById('join')?.innerHTML).toContain('one.join.missingLink')
		// No session probe, no acceptance call: there is nothing to accept.
		expect(fetchStub).not.toHaveBeenCalled()
	})
})
