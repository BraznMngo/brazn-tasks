import {describe, it, expect, beforeEach} from 'vitest'
import {setActivePinia, createPinia} from 'pinia'
import {defineComponent, h} from 'vue'
import {mount} from '@vue/test-utils'

import {
	collaborationDetailTeamId,
	useManagedCapabilities,
	type ManagedCapabilities,
} from './useManagedCapabilities'
import {useAuthStore} from '@/stores/auth'
import {saveToken, removeToken} from '@/helpers/auth'
import {AUTH_TYPES} from '@/modelTypes/IUser'

// authStore.managedEdition and .writeRestricted are populated only by
// checkAuth() decoding the session JWT (BRA-1342) - there is no public setter,
// so the only faithful way to arrange them for a test is to hand checkAuth() a
// real-shaped token, the same way the browser would. Using AUTH_TYPES.LINK_SHARE
// keeps checkAuth() on the branch that neither calls the network (refreshUserInfo
// is skipped for link shares) nor fetches an avatar (setUser skips that for
// link shares too), so this stays a pure unit test.
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

function runComposable(): {
	capabilities: ManagedCapabilities
	writeRestricted: boolean
	maxCollaborators: number | null
} {
	let result: {
		capabilities: ManagedCapabilities
		writeRestricted: boolean
		maxCollaborators: number | null
	} | undefined
	const Comp = defineComponent({
		setup() {
			const {capabilities, writeRestricted, maxCollaborators} = useManagedCapabilities()
			result = {
				capabilities: capabilities.value,
				writeRestricted: writeRestricted.value,
				maxCollaborators: maxCollaborators.value,
			}
			return () => h('div')
		},
	})
	mount(Comp)
	return result as {
		capabilities: ManagedCapabilities
		writeRestricted: boolean
		maxCollaborators: number | null
	}
}

describe('useManagedCapabilities', () => {
	beforeEach(() => {
		setActivePinia(createPinia())
		removeToken()
	})

	it('denies every Personal-edition capability for a personal-cloud account', async () => {
		await setManagedClaims({brazn_edition: 'personal-cloud'})

		const {capabilities} = runComposable()

		// Reverting the PERSONAL_EDITION check in useManagedCapabilities.ts (e.g.
		// comparing against the wrong string, or defaulting to permissive)
		// makes this go red.
		expect(capabilities).toEqual({
			projectCreate: false,
			projectDuplicate: false,
			projectShare: false,
			linkShare: false,
			teamsSurface: false,
			teamCreate: false,
		})
	})

	it('is permissive for a teams-cloud account', async () => {
		await setManagedClaims({brazn_edition: 'teams-cloud'})

		const {capabilities} = runComposable()

		expect(capabilities).toEqual({
			projectCreate: true,
			projectDuplicate: true,
			projectShare: true,
			linkShare: true,
			teamsSurface: true,
			teamCreate: true,
		})
	})

	it('keeps the teams surface but refuses team create for Collaboration (COLLAB-5)', async () => {
		await setManagedClaims({
			brazn_edition: 'collaboration-cloud',
			brazn_max_collaborators: 10,
		})

		const {capabilities, maxCollaborators} = runComposable()

		expect(capabilities).toEqual({
			projectCreate: true,
			projectDuplicate: true,
			projectShare: true,
			linkShare: true,
			teamsSurface: true,
			teamCreate: false,
		})
		expect(maxCollaborators).toBe(10)
	})

	it('is permissive when the session token carries no edition at all', async () => {
		await setManagedClaims({})

		const {capabilities} = runComposable()

		expect(capabilities.projectCreate).toBe(true)
		expect(capabilities.projectShare).toBe(true)
	})

	it('surfaces writeRestricted from the auth store unchanged', async () => {
		await setManagedClaims({brazn_write_restricted: true})

		const {writeRestricted} = runComposable()

		// Reverting the `writeRestricted: computed(() => authStore.writeRestricted)`
		// line (e.g. hardcoding false) makes this go red.
		expect(writeRestricted).toBe(true)
	})
})

describe('collaborationDetailTeamId (COLLAB-5)', () => {
	it('sends Collaboration to the first team detail id', () => {
		expect(collaborationDetailTeamId('collaboration-cloud', [{id: 7}, {id: 9}])).toBe(7)
	})

	it('leaves Teams on the list even when they have exactly one team', () => {
		// CHEAP CHECK: gating on team count === 1 instead of edition makes this fail.
		expect(collaborationDetailTeamId('teams-cloud', [{id: 7}])).toBeNull()
	})

	it('does nothing when Collaboration has no team yet', () => {
		expect(collaborationDetailTeamId('collaboration-cloud', [])).toBeNull()
	})
})
