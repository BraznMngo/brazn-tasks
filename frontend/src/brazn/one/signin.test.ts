import {describe, it, expect, beforeEach, afterEach, beforeAll, vi} from 'vitest'

import * as api from '../../../public/one/api.js'
import {
	allowedDestination,
	desktopAuthorizationFrom,
	destinationFromHash,
	signInSurface,
} from '../../../public/one/signin.js'
import {passwordField} from '../../../public/one/auth-shell.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import {
	ORIGIN,
	card,
	cardText,
	enqueue,
	fetchStub,
	forkRefusal,
	json,
	mountAuthCard,
	navigations,
	requests,
	captureDocumentListeners,
	releaseDocumentListeners,
	resetHarness,
	restoreLocation,
	settle,
	standAt,
	submitForm,
} from './auth-page-harness'

/*
 * THE SIGN-IN PAGE, TESTED FROM THE TICKET (BRA-1475), NOT FROM THE DIFF.
 *
 * Each block below names the acceptance criterion it decides. The criteria were written down
 * before this file's subject was opened, which is the whole reason the assertions are about what
 * a person can do rather than about the fields the implementation happens to carry.
 *
 * WHAT NONE OF THIS PROVES. Nothing in continuous integration starts a deployment. A green run
 * here says the browser sends the right request and renders the right sentence; it says nothing
 * about whether a real customer signs in, and criterion 11 is only settled against a running task
 * server holding a real account.
 *
 * The real English catalogue is served to i18n.js through its only seam, the global fetch, so
 * every assertion below is against the SENTENCE a reader sees rather than a key path. Criterion
 * 13 quotes its sentence exactly, and a key-path assertion could not have decided it.
 */

const REGISTRATION_ROUTE = '/api/v1/register'

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
	mountAuthCard()
	sessionStorage.clear()
	localStorage.clear()
	standAt('/one/signin.html')
})

afterEach(() => {
	releaseDocumentListeners()
	api.configure({fetch: null, origin: null, randomUUID: null})
	document.body.innerHTML = ''
	restoreLocation()
})

/** No session: the refresh cookie is absent, so the fork answers 401 and the form is drawn. */
function noSession(): Response {
	return json({message: 'missing refresh token'}, 401)
}

function info(extra: Record<string, unknown> = {}): Response {
	return json({auth: {openid_connect: {enabled: false, providers: []}}, ...extra})
}

/*
 * A FRESH COPY OF THE PAGE FOR EVERY TEST, AND THE REASON IS A REAL PROPERTY OF THE PAGE.
 *
 * signin.js keeps its screen state in one module-level object, and `needsTotp` stays true for the
 * rest of that module's life on purpose — a mistyped passcode must not hide the box again. In a
 * browser the module's life is one page load, so that is right. In a test file it means the
 * passcode box left over from one test decides the next one, which is precisely the "the result
 * you observed came from somewhere else" shape `docs/Testing-Rules.md` names third. Resetting the
 * module registry gives each test the page a person actually opens.
 */
let pageCounter = 0

async function freshPage() {
	// A CACHE-BUSTING SPECIFIER RATHER THAN `vi.resetModules()`, deliberately. Resetting the whole
	// registry gives signin.js a SECOND copy of api.js that nothing configured, so every call goes
	// to the global fetch and the page fails for a reason that has nothing to do with the test. A
	// query suffix makes only this module fresh; `./api.js` inside it still resolves to the one
	// instance `beforeEach` configured.
	//
	// THE CARD IS TAKEN DOWN WHILE THE MODULE EVALUATES. signin.js self-schedules its own boot()
	// when `#auth` already exists — which is right on the real page and would, here, boot the page
	// a second time behind the explicit call, drain the queued replies out of order and fire every
	// form submission twice. Evaluating with no card means the page boots exactly once, when the
	// test says so.
	document.body.innerHTML = ''
	pageCounter += 1
	const page = await import(/* @vite-ignore */ `../../../public/one/signin.js?fresh=${pageCounter}`)
	mountAuthCard()
	return page
}

/* ================================================================== *
 * CRITERION 11 — "An existing customer signs in with a username or email
 * address and a password, including a customer whose account was created by
 * the paid-account service, which is every account in production."
 * ================================================================== */

describe('criterion 11 — an existing customer signs in', () => {
	it('sends whatever was typed as the username, unchanged, for a username AND for an email address', async () => {
		// ONE FIELD, BECAUSE THE SERVER MAKES NO DISTINCTION. `resolveLoginUser` falls through to a
		// lookup that takes either (pkg/user/user.go), so a page offering two fields, or validating
		// this one as an email address, would refuse a username that is perfectly good.
		//
		// The two values are asserted as LITERALS rather than against anything the page computed,
		// which is the self-referential-comparison trap Testing-Rules names first.
		for (const typed of ['ada', 'ada@example.com']) {
			resetHarness()
			api.resetSession()
			mountAuthCard()
			standAt('/one/signin.html')
			enqueue(info(), noSession(), json({token: 'access-1'}))

			const {boot} = await freshPage()
			await boot()
			await settle()
			submitForm('signInForm', {username: typed, password: 'correct horse'})
			await settle()

			const signIn = requests().find(c => c.url.endsWith('/api/v1/login'))
			expect(signIn, `no sign-in request for ${typed}`).toBeDefined()
			expect(signIn?.body).toEqual({username: typed, password: 'correct horse', totp_passcode: ''})
		}
	})

	it('offers one text field for the two, never an email-validated one', async () => {
		enqueue(info(), noSession())
		const {boot} = await freshPage()
		await boot()
		await settle()

		const field = document.getElementById('username')
		expect(field).toBeInstanceOf(HTMLInputElement)
		// MUTATION: `type="email"` on this input makes this red, and a customer whose account is
		// named `ada` is refused by the browser before the server ever sees the attempt.
		expect((field as HTMLInputElement).type).toBe('text')
		expect(cardText()).toContain('Username or email address')
	})

	it('treats a two-factor challenge as a FOLLOW-UP and not as a rejection', async () => {
		// The ticket names this one in as many words: "the sign-in operation raises a two-factor
		// challenge as a follow-up rather than a rejection ... Treating either as a plain failure
		// locks out people who could have got in."
		enqueue(info(), noSession(), forkRefusal(1017, 'Invalid totp passcode.'))

		const {boot} = await freshPage()
		await boot()
		await settle()
		submitForm('signInForm', {username: 'ada', password: 'correct horse'})
		await settle()

		// The consequence a person would see: a box to put the code in, and a sentence telling them
		// their credentials were fine.
		expect(document.getElementById('totp')).not.toBeNull()
		expect(cardText()).toContain('This account has a second step')
		// MUTATION: deleting the `err.code === CODE_TOTP_REQUIRED` branch makes this red, and
		// everybody with a second factor is told their password is wrong.
		expect(cardText()).not.toContain('Check your details and try again')

		// And the follow-up genuinely completes: the passcode is sent with the same credentials.
		enqueue(json({token: 'access-1'}))
		submitForm('signInForm', {username: 'ada', password: 'correct horse', totp: '123456'})
		await settle()

		const second = requests().filter(c => c.url.endsWith('/api/v1/login'))
		expect(second).toHaveLength(2)
		expect(second[1].body).toEqual({username: 'ada', password: 'correct horse', totp_passcode: '123456'})
	})

	it('gives an unconfirmed address its own answer, distinct from a plain failure', async () => {
		enqueue(info(), noSession(), forkRefusal(1012, 'Please confirm your email address.'))

		const {boot} = await freshPage()
		await boot()
		await settle()
		submitForm('signInForm', {username: 'ada', password: 'correct horse'})
		await settle()

		const unconfirmed = cardText()
		expect(unconfirmed).toContain('Your email address has not been confirmed yet')
		// MUTATION: deleting the `err.code === CODE_EMAIL_NOT_CONFIRMED` branch makes this red, and
		// somebody who only has to open an email is told their password is wrong instead.
		expect(unconfirmed).not.toContain('Check your details and try again')
	})

	it('shows the plain failure sentence for a genuinely wrong password', async () => {
		// The other side of the two branches above: they must not swallow a real refusal.
		enqueue(info(), noSession(), forkRefusal(1004, 'Wrong username or password.', 412))

		const {boot} = await freshPage()
		await boot()
		await settle()
		submitForm('signInForm', {username: 'ada', password: 'nope'})
		await settle()

		// The server's own sentence wins (ruling C4), and no code path claims a second factor.
		expect(cardText()).toContain('Wrong username or password.')
		expect(document.getElementById('totp')).toBeNull()
	})
})

/* ================================================================== *
 * CRITERION 5 — "That person can close the browser, sign in with what they
 * chose, and get back in."
 * ================================================================== */

describe('criterion 5 — signing in with chosen credentials actually gets somebody in', () => {
	it('adopts the session and lands the person in the product', async () => {
		enqueue(info(), noSession(), json({token: 'access-1'}))

		const {boot} = await freshPage()
		await boot()
		await settle()
		submitForm('signInForm', {username: 'ada', password: 'correct horse'})
		await settle()

		// GETTING IN IS TWO THINGS AND BOTH ARE ASSERTED. The token is adopted, so the next call is
		// authenticated without a second round trip; and the browser is sent to the product.
		expect(api.hasSession()).toBe(true)
		enqueue(json({id: 1, username: 'ada'}))
		await api.getCurrentUser()
		const authed = requests()[requests().length - 1]
		expect(authed?.init.headers).toMatchObject({Authorization: 'Bearer access-1'})

		expect(navigations()).toHaveLength(1)
		expect(navigations()[0]).toBe(`${ORIGIN}/one/settings.html`)
	})

	it('leaves no session behind when the password was wrong', async () => {
		enqueue(info(), noSession(), forkRefusal(1004, 'Wrong username or password.'))

		const {boot} = await freshPage()
		await boot()
		await settle()
		submitForm('signInForm', {username: 'ada', password: 'nope'})
		await settle()

		// MUTATION: adopting a token before checking the reply makes this red. Nobody is navigated
		// anywhere, and no session exists to be carried into the product.
		expect(api.hasSession()).toBe(false)
		expect(navigations()).toEqual([])
		expect(document.getElementById('signInForm')).not.toBeNull()
	})
})

/* ================================================================== *
 * CRITERION 13 — the account-creation link, and the input that supplies it
 * ================================================================== */

describe('criterion 13 — "you don\'t have an account yet? click here"', () => {
	it('renders the ticket\'s sentence exactly, pointing at where accounts are created', async () => {
		enqueue(info({brazn_checkout_url: 'https://brazn.one/checkout'}), noSession())

		const {boot} = await freshPage()
		await boot()
		await settle()

		const link = document.querySelector('[data-action="signin-create-account"]')
		expect(link).not.toBeNull()
		// The ticket quotes this sentence and says "reads, exactly". Asserted as a literal, against
		// the shipped English catalogue, so a reworded catalogue entry is caught here.
		expect(link?.textContent?.trim()).toBe("you don't have an account yet? click here")
		expect(link?.getAttribute('href')).toBe('https://brazn.one/checkout')
	})

	it('RENDERS NO LINK AT ALL when the instance published nowhere to send people', async () => {
		// THIS IS THE FINDING, WRITTEN AS A TEST. The address comes from `brazn_checkout_url` in
		// GET /api/v1/info, which the fork defaults to "" (`BraznCheckoutURL.setDefault("")`,
		// pkg/config/config.go) and the deploy passes through from an environment variable that
		// ships blank in `deploy/vikunja/.env.example`. On a deployment where nobody set it, this
		// page has no account-creation link, and acceptance criterion 13 is UNMET there with
		// nothing failing anywhere.
		enqueue(info({brazn_checkout_url: ''}), noSession())

		const {boot} = await freshPage()
		await boot()
		await settle()

		expect(document.querySelector('[data-action="signin-create-account"]')).toBeNull()
		expect(cardText()).not.toContain("you don't have an account yet")
	})

	it('never offers a route into the old application', async () => {
		// The ticket's "must not carry": a second registration form, or any link into the old
		// application. `/register` and `/login` are its routes; the whole application runs in the
		// browser the moment one of them is served.
		enqueue(info({brazn_checkout_url: 'https://brazn.one/checkout'}), noSession())

		const {boot} = await freshPage()
		await boot()
		await settle()

		const hrefs = [...document.querySelectorAll('a')].map(a => a.getAttribute('href') ?? '')
		for (const href of hrefs) {
			expect(new URL(href, ORIGIN).pathname).not.toBe('/register')
			expect(new URL(href, ORIGIN).pathname).not.toBe('/login')
		}
		expect(card()).not.toContain('name="email"')
		expect(document.querySelectorAll('form')).toHaveLength(1)
	})
})

/* ================================================================== *
 * CRITERION 15, BROWSER HALF — "The browser never submits credentials to the
 * task server's registration route."
 * ================================================================== */

describe('criterion 15 — credentials never reach the registration route', () => {
	it('sends the password to the sign-in operation and to nothing else', async () => {
		enqueue(info({brazn_checkout_url: 'https://brazn.one/checkout'}), noSession(), json({token: 'access-1'}))

		const {boot} = await freshPage()
		await boot()
		await settle()
		submitForm('signInForm', {username: 'ada', password: 'correct horse'})
		await settle()

		const carryingPassword = requests().filter(c => JSON.stringify(c.body ?? '').includes('correct horse'))
		expect(carryingPassword).toHaveLength(1)
		// MUTATION: pointing `signIn` at the registration route makes this red.
		expect(carryingPassword[0].url).toBe(`${ORIGIN}/api/v1/login`)
		for (const call of requests()) expect(call.url).not.toContain(REGISTRATION_ROUTE)
	})
})

/* ================================================================== *
 * THE OPEN REDIRECT, which criterion 5's "get back in" quietly depends on
 * ================================================================== */

describe('the destination a sign-in is allowed to return somebody to', () => {
	it('refuses an origin this instance never published', () => {
		// A sign-in page that lands somebody on a stranger's copy of itself is the most valuable
		// open redirect in a product: the second password prompt looks exactly like the first.
		expect(allowedDestination('/one/settings.html', ORIGIN)).toBe(`${ORIGIN}/one/settings.html`)
		// MUTATION: returning `target.toString()` unconditionally makes every one of these red.
		expect(allowedDestination('https://evil.example/steal', ORIGIN)).toBeNull()
		// Protocol-relative is a DIFFERENT ORIGIN wearing a path's clothes.
		expect(allowedDestination('//evil.example/steal', ORIGIN)).toBeNull()
		expect(allowedDestination('javascript:alert(1)', ORIGIN)).toBeNull()
		// Published origins are allowed, and only those.
		expect(allowedDestination('https://brazn.one/download', ORIGIN, ['https://brazn.one/checkout']))
			.toBe('https://brazn.one/download')
		expect(allowedDestination('https://brazn.one/download', ORIGIN, [''])).toBeNull()
	})

	it('reads the destination from the fragment and refuses to find one in a query', () => {
		// The fragment placement is a security property: no browser transmits a fragment, so a
		// desktop application's parameters never reach an access log.
		expect(destinationFromHash('#redirect=%2Fone%2Ftask.html')).toBe('/one/task.html')
		// MUTATION: dropping the leading-`?` guard makes this red, and a caller handing over
		// `location.search` would quietly legitimise moving the destination into the query.
		expect(destinationFromHash('?redirect=%2Fone%2Ftask.html')).toBeNull()
		expect(destinationFromHash('')).toBeNull()
	})

	it('requires all five desktop parameters before offering a desktop exchange', () => {
		const full = '?response_type=code&client_id=one&redirect_uri=percy%3A%2F%2Fcb&code_challenge=abc&code_challenge_method=S256'
		expect(desktopAuthorizationFrom(full)).toMatchObject({client_id: 'one', state: ''})
		// MUTATION: guessing a default for a missing parameter makes this red, and a desktop
		// client is handed a code it cannot redeem.
		expect(desktopAuthorizationFrom(full.replace('&code_challenge_method=S256', ''))).toBeNull()
		expect(desktopAuthorizationFrom('')).toBeNull()
	})
})

/* ================================================================== *
 * THE SHARED PASSWORD FIELD, and the one thing about it that is positional
 * ================================================================== */

describe('the reveal control sits inside the box a person types in', () => {
	/*
	 * WHAT THIS GUARDS, AND WHY IT IS A TEST RATHER THAN A COMMENT. The control is centred with
	 * `top: 50%`, which resolves against whatever the wrapper turns out to be. When the wrapper was
	 * put on the whole `.auth-field` - a grid holding a label, a gap and the input, 63px tall
	 * against the input's 42 - the control rode ten pixels above the box and overlapped the label,
	 * on all three password fields at once. Nothing failed: the markup was valid, every unit test
	 * passed, and it was found only by measuring a rendered page.
	 *
	 * The stylesheet now carries a comment asking for the two to be kept together. A comment is not
	 * a guard, so this is the guard: the wrapper holds the input and the button, and the label is
	 * NOT inside it. That is a structural fact a test can hold, and it is the whole difference
	 * between a centred control and a misplaced one.
	 */
	function wrapperOf(markup: string): Element {
		const host = document.createElement('div')
		host.innerHTML = markup
		const wrap = host.querySelector('.auth-reveal-wrap')
		if (wrap === null) throw new Error('no password field emitted a reveal wrapper')
		return wrap
	}

	it('wraps the INPUT ALONE, with the label outside it', () => {
		const wrap = wrapperOf(passwordField('password', 'one.auth.signIn.password', 'name="password"'))

		// MUTATION: putting `auth-reveal-wrap` back on the `.auth-field` element makes this red.
		expect(wrap.querySelector('input')).not.toBeNull()
		expect(wrap.querySelector('[data-action="reveal-password"]')).not.toBeNull()
		expect(wrap.querySelector('label')).toBeNull()
		// The label still exists and still points at the field - it moved out of the wrapper, it
		// was not dropped.
		const host = document.createElement('div')
		host.innerHTML = passwordField('password', 'one.auth.signIn.password', 'name="password"')
		expect(host.querySelector('label')?.getAttribute('for')).toBe('password')
		expect(host.querySelector('.auth-field')).not.toBeNull()
	})

	it('is emitted through the one shared helper on every page that asks for a password', async () => {
		// A page that hand-rolled its own password field would miss the reveal control, or place it
		// against the wrong wrapper, and neither would show up anywhere else.
		const join = await import('../../../public/one/join.js')
		const password = await import('../../../public/one/password.js')

		const surfaces = [
			['sign in', signInSurface({phase: 'form', providers: [], checkoutUrl: null, passwordUrl: '/one/password.html'})],
			['invitation', join.invitationSurface({phase: 'form', teamName: 'Design', organizationName: 'Ack', invitedEmail: 'ada@example.com', username: ''})],
			['set password', password.setSurface({phase: 'form'})],
		] as const

		for (const [name, markup] of surfaces) {
			const wrap = wrapperOf(markup)
			expect(wrap.querySelector('input'), name).not.toBeNull()
			expect(wrap.querySelector('label'), name).toBeNull()
			// `type="button"` is load-bearing: a bare button inside a form defaults to submit, so
			// revealing the password would send the form.
			const button = wrap.querySelector('[data-action="reveal-password"]') as HTMLButtonElement
			expect(button.getAttribute('type'), name).toBe('button')
			expect(button.getAttribute('aria-pressed'), name).toBe('false')
			expect(button.getAttribute('data-reveals'), name).toBe('password')
		}
	})
})

/* ================================================================== *
 * The surface, asserted without a network
 * ================================================================== */

describe('the sign-in surface', () => {
	it('carries no remember-me control and no second registration form', () => {
		// Three of the ticket's "must not carry" bullets, in one place.
		const surface = signInSurface({phase: 'form', providers: [], checkoutUrl: 'https://brazn.one/checkout', passwordUrl: '/one/password.html'})
		expect(surface).not.toContain('long_token')
		expect(surface.toLowerCase()).not.toContain('remember')
		expect(surface.match(/<form/g) ?? []).toHaveLength(1)
	})

	it('escapes a published address rather than interpolating it as markup', () => {
		const surface = signInSurface({
			phase: 'form', providers: [], passwordUrl: '/one/password.html',
			checkoutUrl: '"><img src=x onerror=alert(1)>',
		})
		// MUTATION: dropping esc() around state.checkoutUrl makes this red. The brand block above
		// carries its own <img>, so the assertion names the injected tag rather than the character.
		expect(surface).not.toContain('<img src=x')
		expect(surface).toContain('&lt;img src=x')
	})
})
