import {describe, it, expect, vi, beforeEach} from 'vitest'
import {mount} from '@vue/test-utils'

import RequestPasswordReset from './RequestPasswordReset.vue'
import {globalMountOptions, settle} from './testSupport'

const {requestResetPasswordMock} = vi.hoisted(() => ({
	requestResetPasswordMock: vi.fn(),
}))

vi.mock('@/services/passwordReset', () => ({
	default: class {
		loading = false
		requestResetPassword = requestResetPasswordMock
		resetPassword = vi.fn()
	},
}))

function mountScreen() {
	return mount(RequestPasswordReset, {global: globalMountOptions})
}

async function askFor(address: string) {
	const wrapper = mountScreen()
	await wrapper.find('#email').setValue(address)
	await wrapper.find('form').trigger('submit')
	await settle(50)
	return wrapper
}

describe('RequestPasswordReset', () => {
	beforeEach(() => {
		requestResetPasswordMock.mockReset()
	})

	it('replies identically to an address with an account and one without', async () => {
		// The two servers' answers are made deliberately different. If this
		// screen showed what came back rather than its own sentence, the two
		// replies below would differ and this test would fail - which is the
		// point: the screen must not be the thing that leaks what the endpoint
		// no longer does.
		requestResetPasswordMock.mockResolvedValueOnce({message: 'Token was sent to frederick.'})
		const registered = await askFor('frederick@example.com')

		requestResetPasswordMock.mockResolvedValueOnce({message: 'Token was sent.'})
		const stranger = await askFor('nobody@example.com')

		const said = registered.find('.message').text()
		expect(stranger.find('.message').text()).toBe(said)
		expect(said).toBe('If that address has an account, a password reset link is on its way to it. Check your inbox.')
		expect(said).not.toContain('frederick')
		expect(said).not.toContain('Token was sent')
	})

	it('sets autocomplete on the address field', () => {
		expect(mountScreen().find('#email').attributes('autocomplete')).toBe('email')
	})

	it('summarises the error at the top of the form, naming the field it points at', async () => {
		const wrapper = mountScreen()

		await wrapper.find('#email').setValue('not-an-address')
		await wrapper.find('form').trigger('submit')
		await settle(50)

		const summary = wrapper.find('.error-summary')
		expect(summary.exists()).toBe(true)
		expect(summary.attributes('role')).toBe('alert')
		expect(summary.attributes('tabindex')).toBe('-1')

		const entry = summary.find('a')
		expect(entry.attributes('href')).toBe('#email')
		expect(entry.text()).toContain(wrapper.find('label[for="email"]').text())

		// Nothing was sent, and the field says so about itself as well.
		expect(requestResetPasswordMock).not.toHaveBeenCalled()
		expect(wrapper.find('#email').attributes('aria-invalid')).toBe('true')
		const describedBy = wrapper.find('#email').attributes('aria-describedby') ?? ''
		expect(wrapper.find(`#${describedBy}`).text()).toBe('Please enter a valid email address.')
	})

	it('renders no summary while nothing is wrong', () => {
		expect(mountScreen().find('.error-summary').exists()).toBe(false)
	})

	it('leads to signing in once the request is in', async () => {
		requestResetPasswordMock.mockResolvedValueOnce({message: 'Token was sent.'})

		const wrapper = await askFor('frederick@example.com')

		const destinations = wrapper.findAll('.router-link').map(link => link.attributes('data-to'))
		expect(destinations).toContain('user.login')
	})
})
