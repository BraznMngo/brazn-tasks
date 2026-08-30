import {vi} from 'vitest'

/*
 * The harness the five signed-out pages share (BRA-1475 QA).
 *
 * It exists because every one of those pages does the same three impure things — it fetches, it
 * navigates, and it reads a string catalogue — and a test that could not observe all three would
 * be testing the markup rather than the journey. Nothing here is production code; it is the
 * instrument that lets an acceptance criterion be asserted as an OUTCOME a person would see.
 *
 * NAVIGATION IS RECORDED RATHER THAN PERFORMED. `location.assign` in a test environment either
 * throws or silently moves the document out from under the page being tested, and in both cases
 * the assertion that matters — WHERE the person was sent — is lost. So `window.location` is
 * replaced by a recorder, and `navigations()` is the evidence.
 */

export const ORIGIN = 'https://dev.tasks.brazn.one'

export interface Call {
	url: string
	init: RequestInit
	body: unknown
}

const calls: Call[] = []
let queue: Response[] = []
const navigated: string[] = []
let realLocation: Location | null = null

export const fetchStub = vi.fn(async (url: string, init: RequestInit = {}) => {
	let body: unknown = null
	if (typeof init.body === 'string') {
		try {
			body = JSON.parse(init.body)
		} catch {
			body = init.body
		}
	}
	calls.push({url: String(url), init, body})
	const next = queue.shift()
	if (next === undefined) throw new Error(`unstubbed request: ${String(url)}`)
	return next.clone()
})

/** Every request the page made, in order. */
export function requests(): Call[] {
	return calls
}

/** Every address the page sent the browser to, in order. */
export function navigations(): string[] {
	return navigated
}

export function enqueue(...responses: Response[]): void {
	queue.push(...responses)
}

export function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {status, headers: {'content-type': 'application/json'}})
}

/** The fork's own refusal shape: 412 with a numeric code in the body (`pkg/user/error.go`). */
export function forkRefusal(code: number, message: string, status = 412): Response {
	return json({code, message}, status)
}

/**
 * Stand the browser at an address, with navigation recorded instead of performed.
 *
 * The parts are given rather than parsed so a test can put a token in the fragment and still be
 * sure the page read it from there — happy-dom's own parsing is not the thing under test.
 */
export function standAt(pathname: string, search = '', hash = ''): void {
	if (realLocation === null) realLocation = window.location
	Object.defineProperty(window, 'location', {
		configurable: true,
		value: {
			origin: ORIGIN,
			protocol: 'https:',
			host: 'dev.tasks.brazn.one',
			hostname: 'dev.tasks.brazn.one',
			pathname,
			search,
			hash,
			href: `${ORIGIN}${pathname}${search}${hash}`,
			assign: (url: string) => {navigated.push(String(url))},
			replace: (url: string) => {navigated.push(String(url))},
			reload: () => {navigated.push('RELOAD')},
			toString: () => `${ORIGIN}${pathname}${search}${hash}`,
		},
	})
}

export function restoreLocation(): void {
	if (realLocation !== null) {
		Object.defineProperty(window, 'location', {configurable: true, value: realLocation})
		realLocation = null
	}
}

export function resetHarness(): void {
	calls.length = 0
	queue = []
	navigated.length = 0
	fetchStub.mockClear()
}

/*
 * EVERY ONE OF THESE PAGES INSTALLS ITS LISTENERS ON `document`, AND `document` OUTLIVES A TEST.
 *
 * In a browser each page boots once, so `installListeners()` runs once and nothing is wrong. In a
 * test file that boots the same page six times, six copies of the submit handler accumulate on one
 * document, and the seventh test observes six sign-in requests from one press. That is an artefact
 * of the harness rather than a defect in the page — but it is exactly the "unrelated cause
 * produced the result you observed" shape, so it is removed rather than worked around.
 *
 * Every listener the page under test registers on `document` is recorded here and taken off again
 * between tests.
 */
const documentListeners: Array<[string, EventListenerOrEventListenerObject, unknown]> = []
let realAddEventListener: typeof document.addEventListener | null = null

export function captureDocumentListeners(): void {
	if (realAddEventListener === null) realAddEventListener = document.addEventListener.bind(document)
	document.addEventListener = ((type: string, listener: EventListenerOrEventListenerObject, options?: unknown) => {
		documentListeners.push([type, listener, options])
		realAddEventListener?.(type, listener, options as AddEventListenerOptions)
	}) as typeof document.addEventListener
}

export function releaseDocumentListeners(): void {
	for (const [type, listener, options] of documentListeners) {
		document.removeEventListener(type, listener, options as EventListenerOptions)
	}
	documentListeners.length = 0
	if (realAddEventListener !== null) {
		document.addEventListener = realAddEventListener
		realAddEventListener = null
	}
}

/** The card these five documents render into, with nothing else in it. */
export function mountAuthCard(): void {
	document.body.innerHTML = '<div class="auth-stage hidden"><main class="auth-card" id="auth"></main></div>'
}

export function card(): string {
	return document.getElementById('auth')?.innerHTML ?? ''
}

/** The rendered text a reader would see, with markup and whitespace collapsed away. */
export function cardText(): string {
	return (document.getElementById('auth')?.textContent ?? '').replace(/\s+/g, ' ').trim()
}

/** Press a control by its `data-action`, the way a person does. */
export function press(action: string): void {
	const el = document.querySelector(`[data-action="${action}"]`)
	if (el === null) throw new Error(`no control with data-action="${action}"`)
	el.dispatchEvent(new Event('click', {bubbles: true, cancelable: true}))
}

/** Fill a field and submit its form, the way a person does. */
export function submitForm(formId: string, values: Record<string, string>): void {
	const form = document.getElementById(formId)
	if (!(form instanceof HTMLFormElement)) throw new Error(`no form #${formId}`)
	for (const [name, value] of Object.entries(values)) {
		const field = form.elements.namedItem(name)
		if (field instanceof HTMLInputElement) field.value = value
	}
	form.dispatchEvent(new Event('submit', {bubbles: true, cancelable: true}))
}

/** Let every queued microtask and promise continuation settle. */
export async function settle(times = 6): Promise<void> {
	for (let i = 0; i < times; i++) await Promise.resolve()
	await new Promise(resolve => setTimeout(resolve, 0))
	for (let i = 0; i < times; i++) await Promise.resolve()
}
