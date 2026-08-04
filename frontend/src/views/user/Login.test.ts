import {describe, it, expect, vi, beforeEach} from 'vitest'
import {mount} from '@vue/test-utils'

import Login from './Login.vue'
import {globalMountOptions, settle, apiRejection} from './testSupport'

const {loginMock, verifyEmailMock, setNeedsTotpPasscodeMock, pushMock} = vi.hoisted(() => ({
	loginMock: vi.fn(),
	verifyEmailMock: vi.fn(),
	setNeedsTotpPasscodeMock: vi.fn(),
	pushMock: vi.fn(),
}))

vi.mock('vue-router', async (importOriginal) => ({
	...(await importOriginal<typeof import('vue-router')>()),
	useRouter: () => ({push: pushMock}),
	useRoute: () => ({query: {}}),
}))

vi.mock('@/composables/useRedirectToLastVisited', () => ({
	useRedirectToLastVisited: () => ({redirectIfSaved: vi.fn()}),
}))

vi.mock('@/stores/auth', () => ({
	JUST_LOGGED_OUT_KEY: 'justLoggedOut',
	useAuthStore: () => ({
		authenticated: false,
		isLoading: false,
		needsTotpPasscode: false,
		login: loginMock,
		verifyEmail: verifyEmailMock,
		setNeedsTotpPasscode: setNeedsTotpPasscodeMock,
	}),
}))

vi.mock('@/stores/config', () => ({
	useConfigStore: () => ({
		auth: {
			local: {enabled: true, registrationEnabled: true},
			ldap: {enabled: false},
			openidConnect: {
				enabled: true,
				redirectUrl: '',
				providers: [{
					name: 'Google',
					key: 'google',
					authUrl: 'https://accounts.example.test/o',
					clientId: 'client',
					logoutUrl: '',
					scope: 'openid email profile',
				}],
			},
		},
	}),
}))

function mountLogin() {
	return mount(Login, {global: globalMountOptions})
}

/** Types credentials in and presses sign in. */
async function signIn(username: string, password: string) {
	const wrapper = mountLogin()
	await wrapper.find('#username').setValue(username)
	await wrapper.find('#password').setValue(password)
	await wrapper.find('#loginform').trigger('submit')
	await settle()
	return wrapper
}

async function messageFor(rejection: unknown, username = 'frederick', password = 'somepassword') {
	loginMock.mockRejectedValueOnce(rejection)
	const wrapper = await signIn(username, password)
	return wrapper.find('.message-wrapper[role="alert"]').text()
}

describe('Login', () => {
	beforeEach(() => {
		loginMock.mockReset()
		setNeedsTotpPasscodeMock.mockReset()
		pushMock.mockReset()
		verifyEmailMock.mockReset()
		verifyEmailMock.mockResolvedValue(false)
	})

	it('gives one message for a wrong username and a wrong password, and it is the same one', async () => {
		// The API answers both with error code 1011 and nothing else. Two
		// different sentences here would let anyone holding a list of addresses
		// find out which of them are customers.
		const rejection = () => apiRejection(1011, 'Wrong username or password.')

		const wrongUsername = await messageFor(rejection(), 'nobody-by-that-name', 'somepassword')
		const wrongPassword = await messageFor(rejection(), 'frederick', 'not-the-password')

		expect(wrongUsername).toBe(wrongPassword)
		// Pinned against the catalogue text as a literal, so this cannot pass by
		// comparing the screen with itself.
		expect(wrongUsername).toBe('Wrong username or password.')
		// And it does not quote back what was typed, which would say which half
		// was recognised.
		expect(wrongUsername).not.toContain('nobody-by-that-name')
	})

	it('says an account is unconfirmed for error code 1012 and for nothing else', async () => {
		const unconfirmed = 'This account has not been confirmed yet. Check your inbox for the confirmation link.'

		expect(await messageFor(apiRejection(1012, 'Please confirm your email address.'))).toBe(unconfirmed)

		// Every other rejection: a wrong credential, a disabled account, and a
		// refusal that carries no error code at all.
		expect(await messageFor(apiRejection(1011, 'Wrong username or password.'))).not.toContain('confirmed')
		expect(await messageFor(apiRejection(1020, 'This account is disabled.'))).not.toContain('confirmed')
		expect(await messageFor(apiRejection(undefined, 'Too Many Requests', 429))).not.toContain('confirmed')
	})

	it('phrases too many attempts against this browser rather than the account', async () => {
		const message = await messageFor(apiRejection(undefined, 'Too Many Requests', 429))

		expect(message).toBe('Too many sign-in attempts from this browser. Wait a few minutes and try again.')
		expect(message).not.toContain('account')
	})

	it('keeps the username and clears the password when the sign-in is rejected', async () => {
		loginMock.mockRejectedValueOnce(apiRejection(1011, 'Wrong username or password.'))

		const wrapper = await signIn('frederick', 'not-the-password')

		expect((wrapper.find('#username').element as HTMLInputElement).value).toBe('frederick')
		expect((wrapper.find('#password').element as HTMLInputElement).value).toBe('')
	})

	it('summarises the errors at the top of the form, naming each field it points at', async () => {
		const wrapper = mountLogin()

		await wrapper.find('#loginform').trigger('submit')
		await settle()

		const summary = wrapper.find('.error-summary')
		expect(summary.exists()).toBe(true)
		expect(summary.attributes('role')).toBe('alert')
		expect(summary.attributes('tabindex')).toBe('-1')

		const entries = summary.findAll('a')
		expect(entries.map(entry => entry.attributes('href'))).toEqual(['#username', '#password'])

		for (const entry of entries) {
			const href = entry.attributes('href') ?? ''
			expect(wrapper.find(href).exists()).toBe(true)
			expect(entry.text()).toContain(wrapper.find(`label[for="${href.slice(1)}"]`).text())
		}
	})

	it('renders no summary while nothing is wrong', () => {
		expect(mountLogin().find('.error-summary').exists()).toBe(false)
	})

	it('sets autocomplete on every field', () => {
		const wrapper = mountLogin()

		expect(wrapper.find('#username').attributes('autocomplete')).toBe('username')
		expect(wrapper.find('#password').attributes('autocomplete')).toBe('current-password')

		for (const input of wrapper.findAll('input[type="text"], input[type="password"]')) {
			expect(input.attributes('autocomplete')).toBeTruthy()
		}
	})

	it('offers the same two routes as registration, in the same order', () => {
		const wrapper = mountLogin()

		const buttons = wrapper.findAll('button.button')
		const google = buttons.findIndex(button => button.text() === 'Sign in with Google')
		const submit = buttons.findIndex(button => button.text() === 'Sign in')

		expect(google).toBeGreaterThanOrEqual(0)
		expect(submit).toBeGreaterThanOrEqual(0)
		expect(google).toBeLessThan(submit)

		expect(buttons[google].classes()).toContain('is-fullwidth')
		expect(buttons[submit].classes()).toContain('is-fullwidth')
		expect(wrapper.find('.or-rule').text()).toBe('or')
	})

	it('links out to resetting the password and to creating an account', () => {
		const destinations = mountLogin()
			.findAll('.router-link')
			.map(link => link.attributes('data-to'))

		expect(destinations).toContain('user.password-reset.request')
		expect(destinations).toContain('user.register')
	})
})
