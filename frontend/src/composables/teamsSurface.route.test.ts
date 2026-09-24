import {describe, it, expect, beforeEach} from 'vitest'
import {setActivePinia, createPinia} from 'pinia'
import {createRouter, createMemoryHistory} from 'vue-router'

import {useManagedCapabilities} from './useManagedCapabilities'
import {useAuthStore} from '@/stores/auth'
import {saveToken, removeToken} from '@/helpers/auth'
import {AUTH_TYPES} from '@/modelTypes/IUser'

// Same JWT arrangement as useManagedCapabilities.test.ts — checkAuth() is the
// only way managedEdition is populated (BRA-1342 / BRA-1066).
function makeJwt(claims: Record<string, unknown>): string {
	const payload = {
		id: 1,
		type: AUTH_TYPES.LINK_SHARE,
		sid: 'test-session',
		exp: Math.round(Date.now() / 1000) + 3600,
		...claims,
	}
	return `header.${btoa(JSON.stringify(payload))}.signature`
}

async function setManagedClaims(claims: Record<string, unknown>) {
	saveToken(makeJwt(claims), true)
	await useAuthStore().checkAuth()
}

/**
 * Cheap check for BRA-1066: Personal hitting /teams must land on not-found.
 * DELETE-THE-GUARD: remove the requiresTeamsSurface check (or the meta on
 * teams.index) and this resolves to teams.index instead.
 */
async function navigateTeamsIndex() {
	const router = createRouter({
		history: createMemoryHistory(),
		routes: [
			{path: '/', name: 'home', component: {template: '<div/>'}},
			{
				path: '/teams',
				name: 'teams.index',
				component: {template: '<div/>'},
				meta: {requiresTeamsSurface: true},
			},
			{path: '/not-found', name: 'not-found', component: {template: '<div/>'}},
		],
	})
	router.beforeEach((to) => {
		if (to.meta?.requiresTeamsSurface) {
			const {capabilities} = useManagedCapabilities()
			if (!capabilities.value.teamsSurface) {
				return {name: 'not-found'}
			}
		}
	})
	await router.push('/teams')
	await router.isReady()
	return router.currentRoute.value.name
}

describe('teams surface route guard (BRA-1066)', () => {
	beforeEach(() => {
		setActivePinia(createPinia())
		removeToken()
	})

	it('sends a Personal account from /teams to not-found', async () => {
		await setManagedClaims({brazn_edition: 'personal-cloud'})
		expect(await navigateTeamsIndex()).toBe('not-found')
	})

	it('lets a Teams account reach /teams', async () => {
		await setManagedClaims({brazn_edition: 'teams-cloud'})
		expect(await navigateTeamsIndex()).toBe('teams.index')
	})
})
