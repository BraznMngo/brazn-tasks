import {computed, readonly, ref} from 'vue'
import {acceptHMRUpdate, defineStore} from 'pinia'

import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'
import {objectToCamelCase} from '@/helpers/case'

export interface IOrganizationMember {
	userId: number
	username: string
	name: string
	email: string
	administrator: boolean
}

export interface IOrganizationTeam {
	teamId: number
	name: string
	projectId: number
	/**
	 * Carried by the server, not worked out here. A client deciding "the first
	 * one" would draw a removal control on the one team that can never be
	 * removed the moment the list arrived in a different order.
	 */
	primary: boolean
}

export interface IOrganization {
	id: string
	edition: string
	administrator: IOrganizationMember | null
	members: IOrganizationMember[]
	teams: IOrganizationTeam[]
	seatsOccupied: number
	/**
	 * null means this instance could not read how many seats were bought — which
	 * is not zero and not unlimited. Every capacity decision taken against null
	 * refuses, and the surface says so rather than showing a number it guessed.
	 */
	seatsPurchased: number | null
	teamsUsed: number
	teamsAllowed: number | null
	canCreateTeam: boolean
	/**
	 * The ratio the seat rule is expressed in, sent by the server so nothing
	 * here holds its own copy. A constant duplicated across a boundary is
	 * checked by neither side.
	 */
	seatsPerTeam: number
}

/**
 * The four states the Organization area can be in, which are the four the
 * design draws. `stale` is the fourth refusal class BRA-920 asked for and the
 * product rules do not name: a view that was correct when it loaded and is not
 * any more, because the role changed in another window.
 */
export type OrganizationState = 'idle' | 'loading' | 'ready' | 'error' | 'unavailable' | 'stale'

/**
 * The Organization area's data.
 *
 * WHAT MAKES THE MENU ENTRY APPEAR IS THE SERVER'S ANSWER, and nothing else.
 * There is no local role, no claim read out of the token and no configuration
 * flag: `isAdministrator` is true only once GET /brazn/organization has
 * returned an organization. A member's request 403s, the flag stays false, and
 * the seven entries are never rendered.
 *
 * That is only half of BRA-917 AC1 and it is the half that does not enforce
 * anything. The other half is the route: every organization route refuses a
 * non-administrator server-side, so a member who types the URL, keeps a stale
 * tab open or calls the API directly is stopped by something a browser cannot
 * talk out of. This store is the discovery half.
 */
export const useOrganizationStore = defineStore('organization', () => {
	const organization = ref<IOrganization | null>(null)
	const state = ref<OrganizationState>('idle')

	const isAdministrator = computed(() => organization.value !== null)

	async function load(): Promise<void> {
		state.value = 'loading'

		const HTTP = AuthenticatedHTTPFactory()
		try {
			const {data} = await HTTP.get('brazn/organization')
			organization.value = objectToCamelCase(data) as IOrganization
			state.value = 'ready'
		} catch (e) {
			organization.value = null

			// A 403 is the ordinary answer for everybody who is not the
			// administrator, so it is not an error to report — there is simply
			// no Organization area for this account and nothing is drawn. Any
			// other failure is ours and is shown as one.
			const status = (e as {response?: {status?: number}})?.response?.status
			state.value = status === 403 ? 'idle' : 'error'
		}
	}

	/**
	 * markStale is what a route guard calls when it finds the server no longer
	 * agrees this account administers anything. The page says it was correct
	 * when it loaded and offers a reload, rather than leaving somebody looking
	 * at controls that would now fail for reasons that are not their fault.
	 */
	function markStale(): void {
		organization.value = null
		state.value = 'stale'
	}

	return {
		organization: readonly(organization),
		state: readonly(state),
		isAdministrator,
		load,
		markStale,
	}
})

if (import.meta.hot) {
	import.meta.hot.accept(acceptHMRUpdate(useOrganizationStore, import.meta.hot))
}
