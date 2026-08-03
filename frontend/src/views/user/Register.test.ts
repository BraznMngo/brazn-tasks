import {describe, it, expect, vi, beforeEach} from 'vitest'
import {mount} from '@vue/test-utils'

import Register from './Register.vue'
import {globalMountOptions, settle} from './testSupport'
import {validatePassword} from '@/helpers/validatePasswort'
import en from '@/i18n/lang/en.json'
import de from '@/i18n/lang/de-DE.json'

const {registerMock, pushMock} = vi.hoisted(() => ({
	registerMock: vi.fn(),
	pushMock: vi.fn(),
}))

vi.mock('@/router', () => ({default: {push: pushMock}}))

vi.mock('@/composables/useRedirectToLastVisited', () => ({
	useRedirectToLastVisited: () => ({redirectIfSaved: vi.fn()}),
}))

vi.mock('@/stores/auth', () => ({
	useAuthStore: () => ({
		authenticated: false,
		isLoading: false,
		register: registerMock,
	}),
}))

vi.mock('@/stores/config', () => ({
	useConfigStore: () => ({
		demoModeEnabled: false,
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

function mountRegister() {
	return mount(Register, {global: globalMountOptions})
}

async function fillValidForm(wrapper: ReturnType<typeof mountRegister>) {
	await wrapper.find('#username').setValue('frederick')
	await wrapper.find('#email').setValue('frederick@example.com')
	await wrapper.find('#password').setValue('a-long-enough-password')
	await settle()
}

describe('Register', () => {
	beforeEach(() => {
		registerMock.mockReset()
		pushMock.mockReset()
		window.sessionStorage.clear()
	})

	it('summarises the errors at the top of the form, naming each field it points at', async () => {
		const wrapper = mountRegister()

		await wrapper.find('#register-submit').trigger('click')
		await settle()

		const summary = wrapper.find('.error-summary')
		expect(summary.exists()).toBe(true)
		expect(summary.attributes('role')).toBe('alert')
		expect(summary.attributes('tabindex')).toBe('-1')

		const entries = summary.findAll('a')
		expect(entries.length).toBeGreaterThan(0)

		for (const entry of entries) {
			const href = entry.attributes('href') ?? ''
			expect(href.startsWith('#')).toBe(true)

			// The field it points at exists, and the entry names it.
			const target = wrapper.find(href)
			expect(target.exists()).toBe(true)

			const label = wrapper.find(`label[for="${href.slice(1)}"]`)
			expect(entry.text()).toContain(label.text())
		}

		// The three fields of this form, each named once.
		const targets = entries.map(entry => entry.attributes('href'))
		expect(targets).toEqual(['#username', '#email', '#password'])
	})

	it('renders no summary while nothing is wrong', () => {
		expect(mountRegister().find('.error-summary').exists()).toBe(false)
	})

	it('sets autocomplete on every field', () => {
		const wrapper = mountRegister()

		expect(wrapper.find('#username').attributes('autocomplete')).toBe('username')
		expect(wrapper.find('#email').attributes('autocomplete')).toBe('email')
		expect(wrapper.find('#password').attributes('autocomplete')).toBe('new-password')

		for (const input of wrapper.findAll('input')) {
			expect(input.attributes('autocomplete')).toBeTruthy()
		}
	})

	it('states the password minimum before it can be broken, and ties it to the field', () => {
		const wrapper = mountRegister()

		const hint = wrapper.find('#password-hint')
		expect(hint.exists()).toBe(true)
		expect(hint.text()).toBe('Use at least 8 characters.')

		const describedBy = wrapper.find('#password').attributes('aria-describedby') ?? ''
		expect(describedBy.split(' ')).toContain('password-hint')
	})

	it('offers Google above the form, at the same width as the submit', () => {
		const wrapper = mountRegister()

		const buttons = wrapper.findAll('button.button')
		const google = buttons.findIndex(button => button.text() === 'Sign up with Google')
		const submit = buttons.findIndex(button => button.attributes('id') === 'register-submit')

		expect(google).toBeGreaterThanOrEqual(0)
		expect(submit).toBeGreaterThanOrEqual(0)
		expect(google).toBeLessThan(submit)

		expect(buttons[google].classes()).toContain('is-fullwidth')
		expect(buttons[submit].classes()).toContain('is-fullwidth')

		// The rule that separates the two routes.
		expect(wrapper.find('.or-rule').text()).toBe('or')
	})

	it('reports its busy and disabled state on the submit rather than removing it', () => {
		const submit = mountRegister().find('#register-submit')

		expect(submit.attributes('aria-busy')).toBe('false')
		// Nothing is filled in yet, so the form cannot be sent - but the button
		// is still pressable, because a control that cannot be pressed can never
		// say why.
		expect(submit.attributes('aria-disabled')).toBe('true')
		expect(submit.attributes('disabled')).toBeUndefined()
	})

	it('says plainly that an address is already registered, and offers both ways on', async () => {
		// The shape authStore.register throws: it unwraps `e.response.data`
		// whenever the body carries a message, which every one of these does.
		registerMock.mockRejectedValue({code: 1002, message: 'A user with this email address already exists.'})

		const wrapper = mountRegister()
		await fillValidForm(wrapper)
		await wrapper.find('#register-submit').trigger('click')
		await settle()

		expect(wrapper.text()).toContain('You cannot have two accounts on one address.')

		const destinations = wrapper.findAll('.router-link').map(link => link.attributes('data-to'))
		expect(destinations).toContain('user.login')
		expect(destinations).toContain('user.password-reset.request')
	})

	it('treats an unconfirmed account as a created one, not as a failed registration', async () => {
		window.sessionStorage.setItem('signupToken', 'a-token')
		registerMock.mockRejectedValue({code: 1012, message: 'Please confirm your email address.'})

		const wrapper = mountRegister()
		await fillValidForm(wrapper)
		await wrapper.find('#register-submit').trigger('click')
		await settle()

		expect(wrapper.text()).toContain('Your account was created.')
		expect(wrapper.find('#registerform').exists()).toBe(false)
		// The registration succeeded, so the token is spent server-side.
		expect(window.sessionStorage.getItem('signupToken')).toBeNull()
	})
})

describe('the stated password minimum and the enforced one', () => {
	// The number is written here as a literal on purpose. Reading it out of the
	// same catalogue the screen reads would make this agree with itself whatever
	// the value became.
	const MINIMUM = 8

	function statedMinimum(text: string): number[] {
		return (text.match(/\d+/g) ?? []).map(Number)
	}

	it('is 8 in the code', () => {
		expect(validatePassword('x'.repeat(MINIMUM - 1))).toBe('user.auth.passwordNotMin')
		expect(validatePassword('x'.repeat(MINIMUM))).toBe(true)
	})

	it('is 8 in the English hint and in the English error, and they cannot drift apart', () => {
		expect(statedMinimum(en.user.auth.passwordMinHint)).toEqual([MINIMUM])
		expect(statedMinimum(en.user.auth.passwordNotMin)).toEqual([MINIMUM])
	})

	it('is 8 in the German hint and in the German error, and they cannot drift apart', () => {
		expect(statedMinimum(de.user.auth.passwordMinHint)).toEqual([MINIMUM])
		expect(statedMinimum(de.user.auth.passwordNotMin)).toEqual([MINIMUM])
	})
})
