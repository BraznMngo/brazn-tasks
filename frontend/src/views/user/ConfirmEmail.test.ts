import {beforeEach, describe, expect, it, vi} from 'vitest'
import {mount} from '@vue/test-utils'
import {createI18n} from 'vue-i18n'

import en from '@/i18n/lang/en.json'
import de from '@/i18n/lang/de-DE.json'
import ConfirmEmail from './ConfirmEmail.vue'

const {post, routeQuery} = vi.hoisted(() => ({
	post: vi.fn(),
	routeQuery: {value: {} as Record<string, string>},
}))

vi.mock('@/helpers/fetcher', () => ({
	HTTPFactory: () => ({post}),
}))

vi.mock('vue-router', () => ({
	useRoute: () => ({query: routeQuery.value}),
}))

// The sentences are written out here rather than read back out of en.json.
//
// Two reasons, and the second is the one that matters. The catalogues are
// precompiled by the i18n build plugin, so importing one yields message ASTs
// rather than strings, and an assertion against `en.user.confirm...` compares
// against an object and fails whatever the screen renders. And a test that
// asserted against the catalogue would agree with itself: edit a string, and
// the screen and the expectation move together while the test stays green.
// These are fixed, independently written expectations.
const COPY = {
	en: {
		inboxHeading: 'Check your inbox',
		confirmed: 'Your address is confirmed. You can sign in now.',
		alreadyUsed: 'You have already used this link. Your address is confirmed, so there is nothing left to do.',
		expired: 'A confirmation link works for 24 hours. Enter your address and we will send you a new one.',
		unreadable: 'Some mail programs break long links across two lines. Enter your address and we will send you a new one.',
		sendNewLink: 'Send a new link',
		resent: 'If that address is waiting to be confirmed, a new link is on its way.',
	},
	de: {
		alreadyUsed: 'Diesen Link hast du schon benutzt. Deine Adresse ist bestätigt, es ist also nichts mehr zu tun.',
	},
} as const

// A token of the shape the server issues: 64 alphanumeric characters, from
// utils.CryptoRandomString(64). Pinned as a literal against the contract.
const WELL_FORMED_TOKEN = 'a'.repeat(63) + 'Z'

function mountConfirm(locale: 'en' | 'de' = 'en') {
	const i18n = createI18n({legacy: false, locale, messages: {en, de}})
	return mount(ConfirmEmail, {
		global: {
			plugins: [i18n],
			stubs: ['RouterLink'],
			directives: {focus: {}},
		},
	})
}

// An axios rejection carrying one of the server's numeric error codes.
function serverRefusal(code: number) {
	return {response: {data: {code}}}
}

describe('ConfirmEmail', () => {
	beforeEach(() => {
		post.mockReset()
		routeQuery.value = {}
		window.sessionStorage.clear()
		window.history.replaceState(null, '', '/confirm')
	})

	it('shows the inbox state when there is no link to check', async () => {
		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(wrapper.text()).toContain(COPY.en.inboxHeading)
		expect(post).not.toHaveBeenCalled()
	})

	// "Send it again" with nothing to send it to would report success and do
	// nothing, which is the worst of both.
	it('asks for the address rather than offering to resend to nothing', async () => {
		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(wrapper.find('input[type="email"]').exists()).toBe(true)

		await wrapper.find('#confirm-resend').trigger('click')
		await flush(wrapper)

		expect(post).not.toHaveBeenCalled()
	})

	it('quotes the address back when this tab knows it', async () => {
		window.sessionStorage.setItem('pendingConfirmationEmail', 'someone@example.com')

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(wrapper.text()).toContain('someone@example.com')
	})

	it('confirms a link the server accepts', async () => {
		routeQuery.value = {userEmailConfirm: WELL_FORMED_TOKEN}
		post.mockResolvedValue({data: {already_confirmed: false}})

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(post).toHaveBeenCalledWith('user/confirm', {token: WELL_FORMED_TOKEN})
		expect(wrapper.text()).toContain(COPY.en.confirmed)
		expect(wrapper.find('.message.success').exists()).toBe(true)
	})

	// THE RULING THIS PROTECTS. A second click on a link that already worked is
	// a success, in green, with the way onward - not an error. Asserted on the
	// body rather than the heading: both states share a heading, so a test that
	// read the heading would pass with the branch deleted.
	it('renders a link that was already used as a success, not an error', async () => {
		routeQuery.value = {userEmailConfirm: WELL_FORMED_TOKEN}
		post.mockResolvedValue({data: {already_confirmed: true}})

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(wrapper.text()).toContain(COPY.en.alreadyUsed)
		expect(wrapper.text()).not.toContain(COPY.en.confirmed)
		expect(wrapper.find('.message.success').exists()).toBe(true)
		expect(wrapper.find('.message.danger').exists()).toBe(false)
		expect(wrapper.find('.message.warning').exists()).toBe(false)
		// And the way onward is offered, which is what makes it a success
		// rather than a dead end.
		expect(wrapper.find('#confirm-sign-in').exists()).toBe(true)
	})

	it('offers a new link when the server says this one expired', async () => {
		routeQuery.value = {userEmailConfirm: WELL_FORMED_TOKEN}
		post.mockRejectedValue(serverRefusal(1035))

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(wrapper.text()).toContain(COPY.en.expired)
		expect(wrapper.find('input[type="email"]').exists()).toBe(true)
		expect(wrapper.text()).toContain(COPY.en.sendNewLink)
	})

	// 1010 is "we never issued this", which is what a link broken across two
	// lines by a mail client arrives as. Different code, different sentence.
	it('tells expired and unreadable apart by the code the server sent', async () => {
		routeQuery.value = {userEmailConfirm: WELL_FORMED_TOKEN}
		post.mockRejectedValue(serverRefusal(1010))

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(wrapper.text()).toContain(COPY.en.unreadable)
		expect(wrapper.text()).not.toContain(COPY.en.expired)
	})

	// Deleting the shape check makes this fail: a mangled token would be sent
	// to the server and the assertion on post would not hold.
	it('does not ask the server about a link that cannot be one of ours', async () => {
		routeQuery.value = {userEmailConfirm: 'this-is-not-a-token'}

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(post).not.toHaveBeenCalled()
		expect(wrapper.text()).toContain(COPY.en.unreadable)
	})

	it('takes the token out of the address bar', async () => {
		window.history.replaceState(null, '', `/confirm?userEmailConfirm=${WELL_FORMED_TOKEN}`)
		routeQuery.value = {userEmailConfirm: WELL_FORMED_TOKEN}
		post.mockResolvedValue({data: {already_confirmed: false}})

		const wrapper = mountConfirm()
		await flush(wrapper)

		expect(window.location.search).toBe('')
		expect(window.location.href).not.toContain(WELL_FORMED_TOKEN)
	})

	it('reports a resend in a live region rather than on a new page', async () => {
		window.sessionStorage.setItem('pendingConfirmationEmail', 'someone@example.com')
		post.mockResolvedValue({data: {}})

		const wrapper = mountConfirm()
		await flush(wrapper)

		await wrapper.find('#confirm-resend').trigger('click')
		await flush(wrapper)

		expect(post).toHaveBeenCalledWith('user/confirm/resend', {email: 'someone@example.com'})
		const status = wrapper.find('[role="status"]')
		expect(status.exists()).toBe(true)
		expect(status.text()).toContain(COPY.en.resent)
		// Still the same screen: the inbox state, not a page of its own.
		expect(wrapper.text()).toContain(COPY.en.inboxHeading)
	})

	// Deleting the cooldown makes this fail: the second press would post again.
	it('says so instead of silently ignoring a second press', async () => {
		window.sessionStorage.setItem('pendingConfirmationEmail', 'someone@example.com')
		post.mockResolvedValue({data: {}})

		const wrapper = mountConfirm()
		await flush(wrapper)

		await wrapper.find('#confirm-resend').trigger('click')
		await flush(wrapper)
		await wrapper.find('#confirm-resend').trigger('click')
		await flush(wrapper)

		expect(post).toHaveBeenCalledTimes(1)
		expect(wrapper.find('[role="status"]').text()).not.toBe('')
		expect(wrapper.find('[role="status"]').text()).not.toContain(COPY.en.resent)
	})

	// AC7. The screen is reachable by anybody, so what it says after a resend
	// must not depend on whose address was typed. Two addresses, one of which
	// this instance would know about and one it would not, and the words that
	// come back have to be the same words.
	it('says the same thing whatever address is given', async () => {
		post.mockResolvedValue({data: {}})

		const notices: string[] = []
		for (const address of ['user4@example.com', 'nobody-at-all@example.com']) {
			// No address remembered, so the screen asks for one rather than
			// offering to resend to nothing.
			const wrapper = mountConfirm()
			await flush(wrapper)

			await wrapper.find('input[type="email"]').setValue(address)
			await wrapper.find('#confirm-resend').trigger('click')
			await flush(wrapper)

			notices.push(wrapper.find('[role="status"]').text())
		}

		expect(notices[0]).toBe(notices[1])
		expect(notices[0]).toContain(COPY.en.resent)
	})

	// AC6, for the strings these screens use. The German tree has to carry the
	// same keys, or a German session renders English.
	it('renders in German without falling back', async () => {
		routeQuery.value = {userEmailConfirm: WELL_FORMED_TOKEN}
		post.mockResolvedValue({data: {already_confirmed: true}})

		const wrapper = mountConfirm('de')
		await flush(wrapper)

		expect(wrapper.text()).toContain(COPY.de.alreadyUsed)
		expect(wrapper.text()).not.toContain(COPY.en.alreadyUsed)
	})
})

// The component awaits an HTTP call inside a lifecycle hook, so one tick is not
// enough to see the state it settles in.
async function flush(wrapper: ReturnType<typeof mountConfirm>) {
	await wrapper.vm.$nextTick()
	await Promise.resolve()
	await Promise.resolve()
	await wrapper.vm.$nextTick()
}
