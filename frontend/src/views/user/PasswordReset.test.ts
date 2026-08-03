import {describe, it, expect, vi, beforeEach} from 'vitest'
import {mount} from '@vue/test-utils'

import PasswordReset from './PasswordReset.vue'
import {globalMountOptions, settle} from './testSupport'

const {resetPasswordMock} = vi.hoisted(() => ({
	resetPasswordMock: vi.fn(),
}))

vi.mock('vue-router', async (importOriginal) => ({
	...(await importOriginal<typeof import('vue-router')>()),
	useRoute: () => ({query: {userPasswordReset: 'a-reset-token'}}),
}))

vi.mock('@/services/passwordReset', () => ({
	default: class {
		loading = false
		resetPassword = resetPasswordMock
		requestResetPassword = vi.fn()
	},
}))

function mountScreen() {
	return mount(PasswordReset, {global: globalMountOptions})
}

async function chooseNewPassword(password: string) {
	const wrapper = mountScreen()
	await wrapper.find('#password').setValue(password)
	await wrapper.find('#form').trigger('submit')
	await settle()
	return wrapper
}

describe('PasswordReset', () => {
	beforeEach(() => {
		resetPasswordMock.mockReset()
	})

	it('asks for a new password, not the current one', () => {
		expect(mountScreen().find('#password').attributes('autocomplete')).toBe('new-password')
	})

	it('states the minimum before it can be broken, and ties it to the field', () => {
		const wrapper = mountScreen()

		expect(wrapper.find('#password-hint').text()).toBe('Use at least 8 characters.')

		const describedBy = wrapper.find('#password').attributes('aria-describedby') ?? ''
		expect(describedBy.split(' ')).toContain('password-hint')
	})

	it('summarises the error at the top of the form, naming the field it points at', async () => {
		const wrapper = await chooseNewPassword('short')

		const summary = wrapper.find('.error-summary')
		expect(summary.exists()).toBe(true)
		expect(summary.attributes('role')).toBe('alert')
		expect(summary.attributes('tabindex')).toBe('-1')

		const entry = summary.find('a')
		expect(entry.attributes('href')).toBe('#password')
		expect(entry.text()).toContain(wrapper.find('label[for="password"]').text())
		// The number in the summary is the number in the hint.
		expect(entry.text()).toContain('8')

		expect(resetPasswordMock).not.toHaveBeenCalled()
	})

	it('renders no summary while nothing is wrong', () => {
		expect(mountScreen().find('.error-summary').exists()).toBe(false)
	})

	it('says the password changed in its own words and leads to signing in', async () => {
		resetPasswordMock.mockResolvedValueOnce({message: 'The password was updated successfully.'})

		const wrapper = await chooseNewPassword('a-long-enough-password')

		const said = wrapper.find('.message').text()
		expect(said).toBe('Your password was changed. You can sign in with it now.')
		expect(said).not.toContain('updated successfully')

		expect(wrapper.find('#form').exists()).toBe(false)
		const destinations = wrapper.findAll('.router-link').map(link => link.attributes('data-to'))
		expect(destinations).toContain('user.login')
	})
})
