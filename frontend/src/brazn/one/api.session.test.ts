import {describe, it, expect, afterEach, beforeEach, vi} from 'vitest'

import * as api from '../../../public/one/api.js'

/*
 * The session half of frontend/public/one/api.js: refresh on load, the single in-flight refresh,
 * 401 -> refresh -> retry ONCE -> terminal.
 *
 * WHY THE IMPORT LOOKS LIKE THAT. `vitest --dir ./src` only discovers tests under src/, so the
 * page modules are reached by relative path; TypeScript resolves `./api.js` to the hand-written
 * `public/one/api.d.ts` next to it, which is what keeps this a plain .test.ts with no
 * `@ts-expect-error` in it (ruling C5).
 *
 * NOTHING HERE TOUCHES THE NETWORK. api.js resolves `globalThis.fetch` lazily and `configure()`
 * replaces it, so no global is patched and no request escapes (bar 9).
 */

// The page is same-origin by construction (bar 3) and the fork/commercial split is decided purely
// by the prefix (bar 6), so every URL below is asserted as a FULL string. A substring match would
// still pass for `/api/v1/v1/...`, which is the exact mistake bar 6 exists to catch.
const ORIGIN = 'https://dev.tasks.brazn.one'
const REFRESH_URL = 'https://dev.tasks.brazn.one/api/v1/user/token/refresh'
const USER_URL = 'https://dev.tasks.brazn.one/api/v2/user'

interface Call {
	url: string
	init: RequestInit
}

const calls: Call[] = []
let queue: Array<Response | Error> = []

const fetchStub = vi.fn(async (url: string, init: RequestInit = {}) => {
	calls.push({url, init})
	const next = queue.shift()
	if (next === undefined) throw new Error(`unstubbed request: ${url}`)
	if (next instanceof Error) throw next
	return next
})

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: {'content-type': 'application/json'},
	})
}

function headerOf(init: RequestInit | undefined, name: string): string | undefined {
	// Every init api.js builds carries a plain object, but the lookup is case-insensitive on
	// purpose: a test that only found `X-Vikunja-Format` spelled exactly one way would go green
	// on a rename that changes nothing the server sees.
	const headers = (init?.headers ?? {}) as Record<string, string>
	const hit = Object.keys(headers).find(key => key.toLowerCase() === name.toLowerCase())
	return hit === undefined ? undefined : headers[hit]
}

// `resetSession()` deliberately does NOT clear the session-lost listeners (api.js:236-240), so a
// subscription made in one case survives into every later one. Unsubscribing at the end of a test
// body only works while that body reaches its end: one failed assertion and a stray listener is
// counted by a case that never registered it, which reports two red tests where there is one bug.
// afterEach runs either way.
const subscriptions: Array<() => void> = []

function subscribe(listener: () => void): void {
	subscriptions.push(api.onSessionLost(listener))
}

describe('one/api.js session', () => {
	beforeEach(() => {
		calls.length = 0
		queue = []
		fetchStub.mockClear()
		api.resetSession()
		api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN})
	})

	afterEach(() => {
		while (subscriptions.length > 0) subscriptions.pop()?.()
	})

	it('refreshes on load against /api/v1 with the cookie attached, and keeps the token', async () => {
		queue = [json({token: 'access-1'})]

		expect(await api.initSession()).toBe(true)

		expect(fetchStub).toHaveBeenCalledTimes(1)
		// v1 is not a style choice: the refresh cookie's Path is hardcoded to
		// /api/v1/user/token/refresh, so a v2 refresh never receives it and always 401s.
		expect(calls[0].url).toBe(REFRESH_URL)
		expect(calls[0].init.method).toBe('POST')
		// MUTATION: changing forkV1Url('user/token/refresh') to forkV2Url(...) in performRefresh,
		// or changing credentials to 'omit', makes this test red. 'omit' is precisely what stops
		// the browser attaching the HttpOnly refresh cookie (bar 3).
		expect(calls[0].init.credentials).toBe('same-origin')
		// No bearer on the refresh: it must be provable from the cookie alone, because the access
		// token it is being asked to replace may already be expired.
		expect(headerOf(calls[0].init, 'authorization')).toBeUndefined()
		expect(api.getToken()).toBe('access-1')
		expect(api.hasSession()).toBe(true)
	})

	it('shares ONE in-flight refresh between concurrent callers', async () => {
		let release: ((value: Response) => void) | undefined
		fetchStub.mockImplementationOnce(async (url: string, init: RequestInit = {}) => {
			calls.push({url, init})
			return new Promise<Response>(resolve => {release = resolve})
		})

		const first = api.refreshSession()
		const second = api.refreshSession()
		release?.(json({token: 'access-2'}))

		expect(await first).toBe('access-2')
		expect(await second).toBe('access-2')
		// The refresh ROTATES the cookie, so a second rotation would invalidate the token the
		// first caller just received and produce a logout that looks random.
		// MUTATION: deleting the `if (refreshInFlight === null)` guard in refreshSession() makes
		// this red - the count becomes 2.
		expect(fetchStub).toHaveBeenCalledTimes(1)
	})

	it('retries a 401 exactly once, with the token the refresh just minted', async () => {
		api.setToken('access-stale')
		queue = [
			json({message: 'missing, malformed or expired jwt'}, 401),
			json({token: 'access-fresh'}),
			json({id: 7, username: 'ada'}),
		]

		await expect(api.getCurrentUser()).resolves.toEqual({id: 7, username: 'ada'})

		expect(calls.map(call => call.url)).toEqual([USER_URL, REFRESH_URL, USER_URL])
		// The stale bearer on the first attempt and the fresh one on the replay are the whole
		// point: the init is rebuilt after the refresh rather than reused.
		expect(headerOf(calls[0].init, 'authorization')).toBe('Bearer access-stale')
		// MUTATION: returning the 401 response instead of replaying the request in authedFetch, or
		// building `authInit(init)` once before the refresh instead of twice, makes this red.
		expect(headerOf(calls[2].init, 'authorization')).toBe('Bearer access-fresh')
		expect(calls[2].init.credentials).toBe('same-origin')
	})

	it('treats a second 401 as terminal: no third attempt, no third refresh', async () => {
		api.setToken('access-stale')
		queue = [
			json({message: 'expired'}, 401),
			json({token: 'access-fresh'}),
			json({message: 'expired'}, 401),
		]

		await expect(api.getCurrentUser()).rejects.toBeInstanceOf(api.SessionLostError)

		// A 401 carrying a token minted seconds earlier is not a token problem, so retrying again
		// would loop against a server that has already answered.
		// MUTATION: replacing the `markSessionLost(); throw` after the replay with another
		// refresh-and-retry makes this red - the count becomes 5.
		expect(fetchStub).toHaveBeenCalledTimes(3)
		expect(api.isSessionLost()).toBe(true)
		expect(api.hasSession()).toBe(false)
	})

	it('goes terminal when the refresh itself is refused, and issues nothing afterwards', async () => {
		const listener = vi.fn()
		subscribe(listener)
		api.setToken('access-stale')
		queue = [
			json({message: 'expired'}, 401),
			json({message: 'invalid refresh token'}, 401),
		]

		await expect(api.getCurrentUser()).rejects.toBeInstanceOf(api.SessionLostError)
		expect(listener).toHaveBeenCalledTimes(1)
		// The token is dropped, not kept for a later attempt: it is provably useless now.
		expect(api.getToken()).toBeNull()

		await expect(api.getCurrentUser()).rejects.toBeInstanceOf(api.SessionLostError)
		// MUTATION: deleting the `if (sessionLost) throw new SessionLostError()` guard at the top
		// of authedFetch makes this red - the second call would put a third request on the wire.
		expect(fetchStub).toHaveBeenCalledTimes(2)
	})

	it('refreshSession() answers null once terminal, without a request', async () => {
		queue = [json({message: 'invalid refresh token'}, 401)]
		expect(await api.refreshSession()).toBeNull()
		expect(fetchStub).toHaveBeenCalledTimes(1)

		// MUTATION: deleting `if (sessionLost) return Promise.resolve(null)` from refreshSession()
		// makes this red - a second refresh would be issued against a session already known dead.
		expect(await api.refreshSession()).toBeNull()
		expect(fetchStub).toHaveBeenCalledTimes(1)
	})

	it('treats a transport failure on the refresh as terminal rather than as a retryable error', async () => {
		queue = [new TypeError('Failed to fetch')]

		// A network failure is not proof the session is gone, but this page has nothing to render
		// without one, so it takes the same terminal path instead of throwing a TypeError at a
		// caller that cannot act on it.
		// MUTATION: removing the try/catch around the refresh in performRefresh makes this red -
		// initSession() rejects with the TypeError instead of resolving false.
		expect(await api.initSession()).toBe(false)
		expect(api.isSessionLost()).toBe(true)
	})

	it('notifies a listener that subscribes AFTER the session was already lost', async () => {
		queue = [json({message: 'invalid refresh token'}, 401)]
		await api.refreshSession()

		const late = vi.fn()
		subscribe(late)

		// initSession() can fail before app.js has finished wiring, and a hand-off that depended
		// on subscription order would be lost for exactly the users who need it.
		// MUTATION: deleting the `if (sessionLost) { listener(); return () => {} }` branch from
		// onSessionLost makes this red.
		expect(late).toHaveBeenCalledTimes(1)
	})
})
