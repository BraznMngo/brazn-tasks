import {describe, it, expect, vi, beforeEach} from 'vitest'
import {mount, flushPromises} from '@vue/test-utils'

import Deletion from './Deletion.vue'
import {globalMountOptions} from '../testSupport'
import Card from '@/components/misc/Card.vue'

/*
 * BRA-1404: a managed account's self-service deletion never reaches this
 * fork's own (gated) `/user/deletion/request` — it goes straight to Percy
 * Cloud's `/v1/account/erasure` via `services/accountErasure.ts`, immediately
 * and with no mailed confirmation. These tests are about that branch; the
 * community/local-password branch below it is unchanged from before BRA-1404
 * and gets one sanity check to confirm the refactor did not disturb it.
 */

const {
	logoutMock,
	organizationLoadMock,
	fetchSuccessorCandidatesMock,
	eraseManagedAccountMock,
	successMock,
} = vi.hoisted(() => ({
	logoutMock: vi.fn(),
	organizationLoadMock: vi.fn(),
	fetchSuccessorCandidatesMock: vi.fn(),
	eraseManagedAccountMock: vi.fn(),
	successMock: vi.fn(),
}))

let authStoreState: {
	info: {isLocalUser: boolean, deletionScheduledAt: string | null} | null
	managedEdition: string | null
}

vi.mock('@/stores/auth', () => ({
	useAuthStore: () => ({
		get info() { return authStoreState.info },
		get managedEdition() { return authStoreState.managedEdition },
		logout: logoutMock,
		refreshUserInfo: vi.fn(),
	}),
}))

vi.mock('@/stores/config', () => ({
	useConfigStore: () => ({userDeletionEnabled: true}),
}))

let organizationMembers: {userId: number, username: string, name: string}[]

vi.mock('@/stores/organization', () => ({
	useOrganizationStore: () => ({
		get organization() { return {members: organizationMembers} },
		load: organizationLoadMock,
	}),
}))

vi.mock('@/services/accountErasure', () => ({
	fetchSuccessorCandidates: fetchSuccessorCandidatesMock,
	eraseManagedAccount: eraseManagedAccountMock,
}))

vi.mock('@/message', () => ({
	success: successMock,
}))

function mountDeletion() {
	return mount(Deletion, {
		global: {
			...globalMountOptions,
			components: {...globalMountOptions.components, Card},
		},
	})
}

describe('Deletion — managed accounts (BRA-1404)', () => {
	beforeEach(() => {
		logoutMock.mockReset()
		organizationLoadMock.mockReset().mockResolvedValue(undefined)
		fetchSuccessorCandidatesMock.mockReset()
		eraseManagedAccountMock.mockReset().mockResolvedValue(undefined)
		successMock.mockReset()
		organizationMembers = []
		authStoreState = {
			info: {isLocalUser: false, deletionScheduledAt: null},
			managedEdition: 'personal-cloud',
		}
	})

	it('a managed account with no organization to hand over deletes immediately, with no successor picker', async () => {
		fetchSuccessorCandidatesMock.mockResolvedValue([])
		const wrapper = mountDeletion()
		await flushPromises()

		expect(wrapper.find('select').exists()).toBe(false)
		await wrapper.find('button.is-danger').trigger('click')
		await flushPromises()

		expect(eraseManagedAccountMock).toHaveBeenCalledWith(null)
		expect(logoutMock).toHaveBeenCalledOnce()
	})

	it('an administrator of an organization with other members must choose a successor before the button proceeds — DELETE-THE-GUARD: removing the empty-selection check here erases the account with no successor named', async () => {
		organizationMembers = [
			{userId: 42, username: 'grace', name: 'Grace Hopper'},
			{userId: 7, username: 'ada', name: 'Ada Lovelace'},
		]
		fetchSuccessorCandidatesMock.mockResolvedValue([{userId: '42'}, {userId: '7'}])
		const wrapper = mountDeletion()
		await flushPromises()

		expect(wrapper.text()).toContain('Grace Hopper')
		expect(wrapper.text()).toContain('Ada Lovelace')

		await wrapper.find('button.is-danger').trigger('click')
		await flushPromises()

		expect(eraseManagedAccountMock).not.toHaveBeenCalled()
		expect(logoutMock).not.toHaveBeenCalled()
		expect(wrapper.text()).toContain('Please choose a successor.')
	})

	it('choosing a successor and confirming erases with that id, then logs the session out', async () => {
		organizationMembers = [{userId: 42, username: 'grace', name: 'Grace Hopper'}]
		fetchSuccessorCandidatesMock.mockResolvedValue([{userId: '42'}])
		const wrapper = mountDeletion()
		await flushPromises()

		await wrapper.find('select').setValue('42')
		await wrapper.find('button.is-danger').trigger('click')
		await flushPromises()

		expect(eraseManagedAccountMock).toHaveBeenCalledWith('42')
		expect(successMock).toHaveBeenCalledOnce()
		expect(logoutMock).toHaveBeenCalledOnce()
	})

	it('a failed erasure call shows an error and does not log the session out', async () => {
		fetchSuccessorCandidatesMock.mockResolvedValue([])
		eraseManagedAccountMock.mockRejectedValue(new Error('network blip'))
		const wrapper = mountDeletion()
		await flushPromises()

		await wrapper.find('button.is-danger').trigger('click')
		await flushPromises()

		expect(logoutMock).not.toHaveBeenCalled()
		expect(wrapper.text()).toContain('We could not delete your account')
	})

	it('a managed account never sees the local-password deletion form, even when isLocalUser would otherwise say so', async () => {
		// Should not happen in practice (a managed account is always OIDC), but
		// this pins that `isManaged` gates the branch outright rather than
		// falling through to the password form for any reason.
		authStoreState.info = {isLocalUser: true, deletionScheduledAt: null}
		fetchSuccessorCandidatesMock.mockResolvedValue([])
		const wrapper = mountDeletion()
		await flushPromises()

		expect(wrapper.find('input[type="password"]').exists()).toBe(false)
	})
})

describe('Deletion — community/self-hosted accounts (unchanged by BRA-1404)', () => {
	beforeEach(() => {
		logoutMock.mockReset()
		successMock.mockReset()
		fetchSuccessorCandidatesMock.mockReset()
		eraseManagedAccountMock.mockReset()
		organizationLoadMock.mockReset()
		authStoreState = {
			info: {isLocalUser: true, deletionScheduledAt: null},
			managedEdition: null,
		}
	})

	it('a local, unmanaged user still sees the password-gated deletion form', async () => {
		const wrapper = mountDeletion()
		await flushPromises()

		expect(wrapper.find('input[type="password"]').exists()).toBe(true)
		expect(fetchSuccessorCandidatesMock).not.toHaveBeenCalled()
	})
})
