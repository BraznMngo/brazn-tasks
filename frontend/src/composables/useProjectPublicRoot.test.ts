import {describe, it, expect, beforeEach, vi} from 'vitest'
import {setActivePinia, createPinia} from 'pinia'
import {defineComponent, h} from 'vue'
import {mount, flushPromises} from '@vue/test-utils'

import {useProjectPublicRoot} from './useProjectPublicRoot'
import {useAuthStore} from '@/stores/auth'
import {saveToken, removeToken} from '@/helpers/auth'
import {AUTH_TYPES} from '@/modelTypes/IUser'

const {getMock} = vi.hoisted(() => ({
	getMock: vi.fn(),
}))

// checkAuth() below goes through the full auth store, which pulls in
// AvatarService and other consumers of HTTPFactory - so both factories need a
// working stand-in (interceptors included, see auth.renewToken.test.ts), not
// just the one this composable calls.
function fakeHttp() {
	return {
		get: getMock,
		post: vi.fn().mockResolvedValue({data: {}}),
		request: vi.fn().mockResolvedValue({data: {}}),
		interceptors: {
			request: {use: vi.fn()},
			response: {use: vi.fn()},
		},
	}
}

vi.mock('@/helpers/fetcher', () => ({
	HTTPFactory: () => fakeHttp(),
	AuthenticatedHTTPFactory: () => fakeHttp(),
	getApiBaseUrl: () => 'http://localhost/api/v1/',
}))

// Same JWT-shaped arrangement as useManagedCapabilities.test.ts: managedEdition
// is populated only by checkAuth() decoding a real session token, so that is
// the only faithful way to put a test in the Teams edition.
function makeJwt(claims: Record<string, unknown>): string {
	const payload = {
		id: 1,
		type: AUTH_TYPES.LINK_SHARE,
		sid: 'test-session',
		exp: Math.round(Date.now() / 1000) + 3600,
		...claims,
	}
	const base64Payload = btoa(JSON.stringify(payload))
	return `header.${base64Payload}.signature`
}

async function setManagedClaims(claims: Record<string, unknown>) {
	saveToken(makeJwt(claims), true)
	await useAuthStore().checkAuth()
}

function mountComposable(projectId: number) {
	let result: ReturnType<typeof useProjectPublicRoot> | undefined
	const Comp = defineComponent({
		setup() {
			result = useProjectPublicRoot(projectId)
			return () => h('div')
		},
	})
	mount(Comp)
	return result as ReturnType<typeof useProjectPublicRoot>
}

describe('useProjectPublicRoot', () => {
	beforeEach(() => {
		setActivePinia(createPinia())
		removeToken()
		getMock.mockReset()
	})

	it('never asks the server and stays permissive for a personal-cloud account', async () => {
		await setManagedClaims({brazn_edition: 'personal-cloud'})

		const {isUnderPublicRoot} = mountComposable(42)
		await flushPromises()

		// Reverting the edition check to fire outside Teams too would make this
		// call the network for an edition that never needed the answer.
		expect(getMock).not.toHaveBeenCalled()
		expect(isUnderPublicRoot.value).toBe(true)
	})

	it('is permissive when the session token carries no edition at all', async () => {
		await setManagedClaims({})

		const {isUnderPublicRoot} = mountComposable(42)
		await flushPromises()

		expect(getMock).not.toHaveBeenCalled()
		expect(isUnderPublicRoot.value).toBe(true)
	})

	it('does not call the network when there is no project id yet', async () => {
		await setManagedClaims({brazn_edition: 'teams-cloud'})

		const {isUnderPublicRoot} = mountComposable(0)
		await flushPromises()

		expect(getMock).not.toHaveBeenCalled()
		expect(isUnderPublicRoot.value).toBe(true)
	})

	it('asks GET /brazn/projects/{id}/public-root for a Teams account and reflects true', async () => {
		await setManagedClaims({brazn_edition: 'teams-cloud'})
		getMock.mockResolvedValue({data: {under_public_root: true}})

		const {isUnderPublicRoot} = mountComposable(7)
		await flushPromises()

		expect(getMock).toHaveBeenCalledWith('brazn/projects/7/public-root')
		expect(isUnderPublicRoot.value).toBe(true)
	})

	// Reverting the `root.Kind == models.ProtectedKindPublicRoot` comparison on
	// the server, or hardcoding `isUnderPublicRoot` to true here, makes this red.
	it('reflects false for a Teams project outside the Public root', async () => {
		await setManagedClaims({brazn_edition: 'teams-cloud'})
		getMock.mockResolvedValue({data: {under_public_root: false}})

		const {isUnderPublicRoot} = mountComposable(9)
		await flushPromises()

		expect(isUnderPublicRoot.value).toBe(false)
	})

	it('falls back to permissive when the request fails, leaving the server the real answer', async () => {
		await setManagedClaims({brazn_edition: 'teams-cloud'})
		getMock.mockRejectedValue(new Error('network error'))

		const {isUnderPublicRoot} = mountComposable(9)
		await flushPromises()

		expect(isUnderPublicRoot.value).toBe(true)
	})
})
