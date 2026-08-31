import {describe, it, expect, beforeEach, afterEach, beforeAll, vi} from 'vitest'

import * as api from '../../../public/one/api.js'
import {
	byteLength,
	invitationIdFromSearch,
	invitationSurface,
	refusalReason,
	signupTokenFromHash,
	usernameBlockedKey,
	usernameIsBlocked,
} from '../../../public/one/join.js'
import {init as initI18n, t} from '../../../public/one/i18n.js'
import enRaw from '../../../public/one/i18n/en.json?raw'
import {
	ORIGIN,
	captureDocumentListeners,
	cardText,
	enqueue,
	fetchStub,
	json,
	mountAuthCard,
	navigations,
	press,
	releaseDocumentListeners,
	requests,
	resetHarness,
	restoreLocation,
	settle,
	standAt,
	submitForm,
} from './auth-page-harness'

/*
 * THE INVITATION PAGE, REWRITTEN FROM THE TICKET.
 *
 * WHAT THIS FILE REPLACES, AND WHY IT IS A REWRITE RATHER THAN AN EDIT. The ten cases that stood
 * here asserted the behaviour BRA-1475 calls the defect: that the page accepts the invitation the
 * moment it loads, using whoever happens to be signed in, and that a signed-out visitor is offered
 * three buttons into the old Vue application. Those assertions were correct about the old page and
 * are now assertions that the fault is still present, so keeping them would be keeping a test that
 * fails when the bug is fixed.
 *
 * WHAT WAS KEPT, because deleting it would have lost a real protection:
 *
 *   * the two pure parsers, unchanged — the fragment-only token rule is a SECURITY property, not a
 *     style, and the old file's reasoning about it is reproduced below;
 *   * the storage-key contract with the Vue helper (`signupToken`, byte for byte), which nothing
 *     else would catch;
 *   * that the fragment is stripped from the address bar and the query survives;
 *   * the missing-link case, which still makes no request at all;
 *   * that a sentence the SERVICE wrote is rendered as text and never as markup.
 *
 * WHAT NONE OF IT PROVES. Criterion 4 is "a person ... is working in the team's shared lists", and
 * that is settled by a real second person on the live system and by nothing here. Criterion 23
 * says so explicitly. Every assertion below is about what the BROWSER does with a given answer.
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
	localStorage.clear()
	standAt('/one/join.html', '?i=inv-9')
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
	const page = await import(/* @vite-ignore */ `../../../public/one/join.js?fresh=${pageCounter}`)
	mountAuthCard()
	return page
}

/** A token the well-formedness check accepts, so a test never fails for the wrong reason. */
const TOKEN = 'a'.repeat(43)

function noSession(): Response {
	return json({message: 'missing refresh token'}, 401)
}

function withSession(): Response {
	return json({token: 'access-admin'})
}

/** The summary answer, at HTTP 200 — every state arrives that way, refusals included. */
function summary(state: string, extra: Record<string, unknown> = {}): Response {
	return json({
		state,
		organization_name: 'Ackermann GmbH',
		team_name: 'Design',
		email: 'ada@example.com',
		...extra,
	})
}

/** Stand at the invitation address with the token in the fragment, as the mail sends it. */
function arriveFromEmail(): void {
	standAt('/one/join.html', '?i=inv-9', `#signup_token=${TOKEN}`)
}

/* ================================================================== *
 * CRITERION 8 — the administrator who clicks their own invitation
 * ================================================================== */

describe('criterion 8 — an administrator clicks an invitation they sent', () => {
	it('shows the logged-in screen with its Logout button, and no form', async () => {
		arriveFromEmail()
		enqueue(withSession())
		const {boot} = await freshPage()

		await boot()
		await settle()

		// Both sentences are the ticket's, "to be used as written".
		expect(cardText()).toContain('You are currently logged into another account. Log out to register for this account.')
		expect(document.querySelector('[data-action="join-logout"]')).not.toBeNull()
		// MUTATION: deleting the `signed-in-elsewhere` branch makes this red, and the administrator
		// gets the registration form for somebody else's invitation.
		expect(document.getElementById('joinForm')).toBeNull()
	})

	it('LEAVES HAVING CONSUMED NOTHING — the only request is the session probe', async () => {
		// The ticket's step 3: "The invitation page opens and does nothing on its own. It reads
		// whether anybody is signed in and never accepts, consumes or submits anything by itself."
		// Asserted over the SET of requests, not by looking for the absence of one name, because a
		// renamed route would slip past the second and not the first.
		arriveFromEmail()
		enqueue(withSession())
		const {boot} = await freshPage()

		await boot()
		await settle()

		expect(requests()).toHaveLength(1)
		expect(requests()[0].url).toBe(`${ORIGIN}/api/v1/user/token/refresh`)
		// Not a single call carried the token anywhere. That is what "consumed nothing" means.
		for (const call of requests()) expect(JSON.stringify(call.body ?? '')).not.toContain(TOKEN)
		expect(navigations()).toEqual([])
	})

	it('keeps the invitation across signing out, and comes back to the same address', async () => {
		arriveFromEmail()
		enqueue(withSession(), json({message: 'Logged out.'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		press('join-logout')
		await settle()

		// MUTATION: calling forgetSignupToken() anywhere on this path makes this red, and the
		// administrator destroys the invitation they were only looking at.
		expect(sessionStorage.getItem('signupToken')).toBe(TOKEN)
		// Reloaded rather than navigated, so the handle in the query survives.
		expect(navigations()).toEqual(['RELOAD'])
	})
})

/* ================================================================== *
 * THE PAGE DOES NOTHING ON ITS OWN — the signed-out arrival
 * ================================================================== */

describe('the page consumes nothing on load, signed out either', () => {
	it('makes exactly two reads, and neither carries a credential the person typed', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'))
		const {boot} = await freshPage()

		await boot()
		await settle()

		expect(requests()).toHaveLength(2)
		expect(requests()[0].url).toBe(`${ORIGIN}/api/v1/user/token/refresh`)
		expect(requests()[1].url).toBe(`${ORIGIN}/v1/invitations/summary`)
		// The summary's credential is the token, and nothing else is offered to it.
		expect(requests()[1].body).toEqual({invitation_id: 'inv-9', signup_token: TOKEN})
	})

	it('captures the token under the Vue helper\'s own key and strips the fragment', async () => {
		// 'signupToken' is frontend/src/helpers/signupToken.ts's STORAGE_KEY, byte for byte. The two
		// files are separate bundles that may not import each other, so nothing but this would catch
		// a drift.
		arriveFromEmail()
		enqueue(noSession(), summary('usable'))
		const replaced: string[] = []
		const realReplaceState = history.replaceState.bind(history)
		history.replaceState = ((_s: unknown, _t: string, url?: string) => {replaced.push(String(url))}) as typeof history.replaceState

		try {
			const {boot} = await freshPage()
			await boot()
			await settle()
			expect(sessionStorage.getItem('signupToken')).toBe(TOKEN)
			// The fragment goes; the query, which carries the handle, stays.
			expect(replaced).toEqual(['/one/join.html?i=inv-9'])
		} finally {
			history.replaceState = realReplaceState
		}
	})

	it('says so, and asks nothing, when the link carries no handle at all', async () => {
		standAt('/one/join.html')
		const {boot} = await freshPage()

		await boot()
		await settle()

		expect(cardText()).toContain('This link is incomplete')
		// No session probe, no read: there is nothing to look up and nothing to join.
		expect(requests()).toEqual([])
	})

	it('catches a mangled link before the request rather than after it', async () => {
		// Both routes answer a bodiless 400 for a malformed body, which arrives indistinguishable
		// from a bug in this page — so a link truncated by a mail client would produce a blank
		// refusal instead of advice.
		standAt('/one/join.html', '?i=inv-9', '#signup_token=short')
		const {boot} = await freshPage()

		await boot()
		await settle()

		// MUTATION: deleting the invitationCredentialsAreWellFormed check makes this red, and a
		// truncated link costs a round trip to learn nothing.
		expect(requests()).toEqual([])
		expect(navigations()[0]).toContain('/one/error.html?reason=invitation-unknown')
	})
})

/* ================================================================== *
 * THE STATE IS THE VERDICT, AND `ok` IS NOT — the attack the coordinator named
 * ================================================================== */

describe('the summary\'s state decides, not the fact that it answered', () => {
	it('shows the form for `usable` and for NOTHING ELSE', async () => {
		// EVERY STATE ARRIVES AT HTTP 200, refusals included. A page that read the success flag
		// alone would show the invitation form to somebody whose invitation was withdrawn.
		const refused = ['invitation_withdrawn', 'invitation_expired', 'token_expired']
		for (const state of refused) {
			resetHarness()
			arriveFromEmail()
			enqueue(noSession(), summary(state))
			const {boot} = await freshPage()

			await boot()
			await settle()

			// MUTATION: replacing `details.state !== 'usable'` with a check on `summary.ok` makes
			// every one of these red, and each of those three people gets a working-looking form
			// that spends their one link on a refusal.
			expect(document.getElementById('joinForm'), `${state} showed the form`).toBeNull()
			expect(navigations()[0], state).toContain('/one/error.html?reason=')
		}
	})

	it('FAILS CLOSED on a state invented after this page was written', async () => {
		// The one that matters most and the one no implementer thinks of: a state added later, to
		// keep somebody out, which this page has never read. It must not open the form.
		resetHarness()
		arriveFromEmail()
		enqueue(noSession(), summary('under_legal_hold'))
		const {boot} = await freshPage()

		await boot()
		await settle()

		expect(document.getElementById('joinForm')).toBeNull()
		// Something true and vague, rather than something meaningless and specific.
		expect(navigations()[0]).toContain('reason=invitation-failed')
	})

	it('treats a bodiless refusal as "open the link again", not as a working invitation', async () => {
		resetHarness()
		arriveFromEmail()
		enqueue(noSession(), new Response('', {status: 404}))
		const {boot} = await freshPage()

		await boot()
		await settle()

		expect(document.getElementById('joinForm')).toBeNull()
		expect(navigations()[0]).toContain('reason=invitation-unknown')
	})
})

/* ================================================================== *
 * `already_member` IS NEVER AN ERROR — from either route
 * ================================================================== */

describe('somebody who already holds a seat gets a welcome, never a refusal', () => {
	it('from the summary, when they open the link again', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('already_member'))
		const {boot} = await freshPage()

		await boot()
		await settle()

		// MUTATION: letting `already_member` fall through to the `!== 'usable'` branch makes this
		// red, and a member in good standing is told their invitation is broken.
		expect(navigations()).toEqual([])
		expect(cardText()).toContain('already')
		expect(document.querySelector('[data-action="join-sign-in"]')).not.toBeNull()
		// Nothing was spent or created, so the way in is the credentials they already have.
		expect(document.getElementById('joinForm')).toBeNull()
	})

	it('from the completion, when they press the button anyway', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'already_member', organization_id: 'org-1'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		expect(navigations()).toEqual([])
		expect(cardText()).toContain('already')
		// No sign-in attempt with credentials that made no account.
		expect(requests().some(c => c.url.endsWith('/api/v1/login'))).toBe(false)
	})
})

/* ================================================================== *
 * `team_unavailable` — a partial success, said as one
 * ================================================================== */

describe('the account exists and the team join did not', () => {
	it('says both halves, rather than "something went wrong" or nothing at all', async () => {
		// THIS IS WHAT EVERY COMPLETION ANSWERS UNTIL THE TASK SERVER IS DEPLOYED, so it is the
		// common path right now rather than a rare one.
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'team_unavailable', organization_id: 'org-1'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		const text = cardText()
		// MUTATION: adding `team_unavailable` to the descriptor's affirmative list, so the page
		// signs them in and lands them in the product, makes this red — and recreates the exact
		// defect this ticket exists to fix, with the product looking empty and nobody told why.
		expect(navigations()).toEqual([])
		expect(text).not.toContain('Something went wrong')
		expect(document.querySelector('[data-action="join-sign-in"]')).not.toBeNull()
		expect(text.length).toBeGreaterThan(20)
	})
})

/* ================================================================== *
 * `account_exists` — a collision the person can fix in the field they are in
 * ================================================================== */

describe('a taken name keeps somebody on the form', () => {
	it('does not end the journey, and does not make them retype the password', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'account_exists'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		// MUTATION: sending `account_exists` to the general error page makes this red, and somebody
		// whose only problem is their first choice of username goes looking for an administrator
		// they do not need.
		expect(navigations()).toEqual([])
		expect(document.getElementById('joinForm')).not.toBeNull()
		expect((document.getElementById('password') as HTMLInputElement).value).toBe('a long enough password')
	})

	it('names NEITHER collision, because the page cannot tell them apart', async () => {
		// One answer covers a taken address and a taken username on purpose, so an unauthenticated
		// channel cannot be walked to discover who has an account here. A sentence implying the
		// page knows which it was would be a false statement half the time.
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'account_exists'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		const text = cardText().toLowerCase()
		expect(text).not.toContain('this account already exists')
		expect(text).not.toContain('email address is already')
	})
})

/* ================================================================== *
 * `joined` — the whole point
 * ================================================================== */

describe('a completed invitation signs the person in and lands them in the product', () => {
	it('uses the product\'s ONE sign-in operation and then goes to the product', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'joined', organization_id: 'org-1'}), json({token: 'access-new'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		const signIn = requests().find(c => c.url.endsWith('/api/v1/login'))
		// "Do not build a second way to sign in": step 11 calls the same operation the sign-in page
		// calls, with the credentials the person just chose.
		expect(signIn?.body).toEqual({username: 'ada', password: 'a long enough password', totp_passcode: ''})
		expect(navigations()).toEqual([`${ORIGIN}/one/settings.html`])
		// The token is spent on the service's side, so this tab drops its copy.
		expect(sessionStorage.getItem('signupToken')).toBeNull()
	})

	it('sends them to sign in rather than to an error page when only the sign-in fails', async () => {
		// The seat is taken and the account exists. Reporting a failed acceptance would be false.
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'joined'}), json({message: 'nope'}, 412))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		expect(navigations()).toEqual([`${ORIGIN}/one/signin.html`])
	})
})

/* ================================================================== *
 * CRITERION 15, browser half
 * ================================================================== */

describe('criterion 15 — the invitation page reaches no registration route', () => {
	it('sends the chosen password to the paid-account service and to nowhere else', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'joined'}), json({token: 'access-new'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		const carrying = requests().filter(c => JSON.stringify(c.body ?? '').includes('a long enough password'))
		// Exactly two: the completion, and the sign-in that follows it. Neither is the task
		// server's registration route, which is closed to everybody and stays closed.
		expect(carrying.map(c => c.url)).toEqual([
			`${ORIGIN}/v1/invitations/completion`,
			`${ORIGIN}/api/v1/login`,
		])
		for (const call of requests()) expect(call.url).not.toContain('/api/v1/register')
	})

	it('never offers the locked address to the service as a second way to say who this is', async () => {
		// "Do not accept an invitation by matching the email address instead of the recorded
		// identity", enforced by the shape of the request rather than by anybody remembering.
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'joined'}), json({token: 'access-new'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		const completion = requests().find(c => c.url.endsWith('/v1/invitations/completion'))
		expect(Object.keys(completion?.body as object).sort())
			.toEqual(['invitation_id', 'password', 'signup_token', 'username'])
	})
})

/* ================================================================== *
 * CRITERION 4, page half — one heading, one sentence, three fields, one button
 * ================================================================== */

describe('criterion 4, the page half — what the invited person sees', () => {
	it('names the team and the organisation, locks the address, and asks for two things', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'))
		const {boot} = await freshPage()

		await boot()
		await settle()

		const text = cardText()
		expect(text).toContain('You have been invited to join the Design team of Ackermann GmbH for ONE Personal Assistant.')
		const email = document.getElementById('email') as HTMLInputElement
		expect(email.value).toBe('ada@example.com')
		// Readonly rather than disabled: a disabled field is out of the tab order and unreadable to
		// some assistive technology, so a person would be told nothing about the one field on the
		// form they cannot change.
		expect(email.readOnly).toBe(true)
		expect(email.disabled).toBe(false)
		expect(document.getElementById('username')).not.toBeNull()
		expect(document.getElementById('password')).not.toBeNull()
		// ONE BUTTON THAT SUBMITS, which is the property the ticket's "one button" was written to
		// protect: no second action can complete the journey, and there is no second place to sign
		// in with existing credentials. It is NOT a count of button elements, and narrowing it is
		// deliberate rather than a concession - the reveal control added after the first review is
		// a second button and is neither of those things. It carries type="button", so it submits
		// nothing; a bare button inside a form defaults to type="submit", which is exactly what
		// this assertion still catches if that attribute is ever dropped.
		const submitters = [...document.querySelectorAll('#joinForm button')]
			.filter(b => (b as HTMLButtonElement).type === 'submit')
		expect(submitters).toHaveLength(1)
		expect(document.querySelectorAll('form')).toHaveLength(1)
		// And the extra button really is the reveal control, rather than anything else that crept in.
		const others = [...document.querySelectorAll('#joinForm button')]
			.filter(b => (b as HTMLButtonElement).type !== 'submit')
		expect(others.map(b => b.getAttribute('data-action'))).toEqual(['reveal-password'])
	})

	it('renders an organisation name carrying markup as characters, not as elements', () => {
		// These names were typed into another system by somebody else. Ruling C4 renders such a
		// sentence verbatim, and verbatim means TEXT.
		const surface = invitationSurface({
			phase: 'form',
			teamName: '<script>alert(1)</script>',
			organizationName: '"><img src=x onerror=alert(1)>',
			invitedEmail: 'ada@example.com',
		})
		// MUTATION: interpolating either name without esc() makes this red.
		expect(surface).not.toContain('<script>')
		expect(surface).not.toContain('<img src=x')
		expect(surface).toContain('&lt;script&gt;')
	})
})

/* ================================================================== *
 * The live username check — advice, never authority
 * ================================================================== */

describe('the username check while somebody types', () => {
	it('blocks ONLY on a definite verdict about the exact name on the form', () => {
		// The whole rule as a table. Every other combination allows, because a check that failed
		// closed on a network error would stop an invited person joining at all — a worse fault
		// than the one it fixes.
		expect(usernameIsBlocked('ada', 'ada', 'taken')).toBe(true)
		// MUTATION: dropping the name comparison makes this red, and a verdict about `ada` blocks
		// somebody who has since typed `adamite`.
		expect(usernameIsBlocked('adamite', 'ada', 'taken')).toBe(false)
		expect(usernameIsBlocked('ada', 'ada', 'free')).toBe(false)
		// `unknown` ALWAYS ALLOWS. This is the state the shipped code is permanently in today,
		// because the service half of the check does not exist yet.
		expect(usernameIsBlocked('ada', 'ada', 'unknown')).toBe(false)
		expect(usernameIsBlocked('ada', '', 'unknown')).toBe(false)
	})

	/*
	 * THE LINE IS "DOES THE SERVICE KNOW", NOT "IS THE NEWS BAD", and this block is where that is
	 * decided. At the first review this seam answered `unknown` unconditionally and issued no
	 * request; it is wired now, and the distinction below is the one that will be got wrong later,
	 * in both directions. Folding `invalid` back into `unknown` lets somebody type a name the task
	 * server reserves, get no warning while typing, and then meet a refusal that never mentions
	 * their username. Folding a transport failure into a blocking verdict stops an invited person
	 * joining at all because their network was slow, which is worse than the fault being fixed.
	 */
	it('BLOCKS on a definite verdict - taken, and equally invalid', async () => {
		for (const [wire, verdict] of [['taken', 'taken'], ['invalid', 'invalid']] as const) {
			resetHarness()
			enqueue(json({status: wire}))
			const answer = await api.checkInvitationUsername({invitationId: 'inv-9', signupToken: TOKEN, username: 'ada'})
			expect(answer, wire).toBe(verdict)
			// MUTATION: mapping `invalid` to `unknown` in checkInvitationUsername makes this red.
			expect(usernameIsBlocked('ada', 'ada', answer), wire).toBe(true)
		}
	})

	it('ALLOWS on available, and on every shape of NOT KNOWING', async () => {
		resetHarness()
		enqueue(json({status: 'available'}))
		expect(await api.checkInvitationUsername({invitationId: 'inv-9', signupToken: TOKEN, username: 'ada'}))
			.toBe('free')
		expect(usernameIsBlocked('ada', 'ada', 'free')).toBe(false)

		const notKnowing: Array<[string, Response | null]> = [
			['a bodiless 400 - a malformed body', new Response('', {status: 400})],
			['a bodiless 404 - the token proves nothing', new Response('', {status: 404})],
			['a 429 - too many checks against this invitation', new Response('', {status: 429})],
			['a status word this page has not read', json({status: 'deferred'})],
			['a body that is not an object at all', json('available')],
			// THE ONE NOBODY WOULD THINK OF, and the descriptor's own comment names it: an UNROUTED
			// /v1/... is answered by the fork's static handler with the app shell at HTTP 200. That
			// is exactly what a browser gets on an instance where this route is not deployed.
			['the app shell served at 200 by an unrouted address',
				new Response('<!doctype html><html><body>ONE</body></html>',
					{status: 200, headers: {'content-type': 'text/html'}})],
			// AND THE SAME THING WEARING A JSON BODY, which is the row that actually isolates the
			// content-type check. The row above is refused TWICE OVER - the markup also fails to
			// parse as JSON - so deleting the content-type check leaves it green, and the guard it
			// names would have gone untested while the case read as thorough. That was found by
			// running the delete-the-guard check rather than by reading it. This body parses
			// perfectly and is refused ONLY because of its content type, which is what a proxy or a
			// static handler answering `{"status":"available"}` as text/html would produce.
			['a parseable JSON body served with a non-JSON content type',
				new Response('{"status":"available"}',
					{status: 200, headers: {'content-type': 'text/html'}})],
			// Nothing queued, so the stub throws: this is the transport failure.
			['no answer at all - the network failed', null],
		]

		for (const [label, response] of notKnowing) {
			resetHarness()
			if (response !== null) enqueue(response)
			const answer = await api.checkInvitationUsername({invitationId: 'inv-9', signupToken: TOKEN, username: 'ada'})
			// MUTATION: returning anything but 'unknown' from the `!result.ok` branch makes every one
			// of these red. Dropping the content-type check inside readCommercialResult makes only
			// the last-but-one red - the JSON row - and that asymmetry is the point: without it,
			// an instance where this route is not deployed would read every name as "not taken"
			// and the form would allow all of them.
			expect(answer, label).toBe('unknown')
			expect(usernameIsBlocked('ada', 'ada', answer), label).toBe(false)
		}
	})

	it('gives the two blocking verdicts DIFFERENT sentences, and neither says the other thing', async () => {
		await initI18n('en', ['en'])
		const taken = t(usernameBlockedKey('taken') as string)
		const unusable = t(usernameBlockedKey('invalid') as string)

		expect(taken).not.toBe(unusable)
		// `taken` means somebody else holds it, and the person needs to know a different name works.
		expect(taken.toLowerCase()).toContain('taken')
		// `invalid` means the server would refuse that string WHOEVER held it. Saying it is taken
		// would send somebody hunting for a collision that does not exist, and would state
		// something untrue about another account.
		// MUTATION: pointing usernameBlockedKey('invalid') at one.join.usernameTaken makes this red.
		expect(unusable.toLowerCase()).not.toContain('taken')
		expect(unusable.toLowerCase()).not.toContain('already')
		// Neither verdict earns a sentence when the service did not give one.
		expect(usernameBlockedKey('free')).toBeNull()
		expect(usernameBlockedKey('unknown')).toBeNull()
		expect(usernameBlockedKey(undefined)).toBeNull()
	})

	it('lets a submission through while a check is still in flight', async () => {
		arriveFromEmail()
		enqueue(noSession(), summary('usable'), json({outcome: 'joined'}), json({token: 'access-new'}))
		const {boot} = await freshPage()

		await boot()
		await settle()
		const field = document.getElementById('username') as HTMLInputElement
		field.value = 'ada'
		field.dispatchEvent(new Event('input', {bubbles: true}))
		// No pause: the check has not run, let alone answered.
		submitForm('joinForm', {username: 'ada', password: 'a long enough password'})
		await settle()

		// MUTATION: blocking on anything but a definite `taken` makes this red, and a slow network
		// swallows the press.
		expect(requests().some(c => c.url.endsWith('/v1/invitations/completion'))).toBe(true)
	})
})

/* ================================================================== *
 * Pure parsing, kept from the file this replaces
 * ================================================================== */

describe('one/join.js pure parsing', () => {
	it('reads the invitation id from ?i= and nothing else', () => {
		expect(invitationIdFromSearch('?i=inv-9')).toBe('inv-9')
		expect(invitationIdFromSearch('?x=1&i=inv-9')).toBe('inv-9')
		// MUTATION: defaulting a missing id to '' — the page would send an empty invitation_id
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
		expect(signupTokenFromHash('?signup_token=tok-1')).toBeNull()
	})

	it('maps each refusal word to a sentence, and an unread word to the general one', () => {
		expect(refusalReason('invitation_withdrawn')).toBe('invitation-revoked')
		expect(refusalReason('invitation_expired')).toBe('invitation-expired')
		expect(refusalReason('at_seat_ceiling')).toBe('seats-full')
		expect(refusalReason('account_exists')).toBe('account-exists')
		// MUTATION: rendering an unread word as its own reason makes this red, and a vocabulary the
		// service grows later shows the reader something meaningless and specific.
		expect(refusalReason('under_legal_hold')).toBe('invitation-failed')
		expect(refusalReason(null)).toBe('invitation-failed')
		expect(refusalReason(undefined)).toBe('invitation-failed')
	})

	it('measures a password in BYTES, because that is what the service bounds', () => {
		// bcrypt's limit is 72 BYTES, so a passphrase of twenty-five accented or Japanese
		// characters is over the line while looking comfortably short. Measuring `.length` would
		// let such a password through to a bodiless 400 this page could only call "went wrong".
		expect(byteLength('abcdefgh')).toBe(8)
		// MUTATION: returning String(value).length makes this red.
		expect(byteLength('パスワード')).toBe(15)
		expect(byteLength('')).toBe(0)
	})
})
