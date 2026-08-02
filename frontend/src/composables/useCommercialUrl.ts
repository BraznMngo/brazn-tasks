import {computed} from 'vue'
import type {ComputedRef} from 'vue'

import {useConfigStore} from '@/stores/config'

/**
 * Where the Organization area sends an administrator for anything the
 * commercial service is authoritative for: payment, invoices, plan, cadence,
 * seats, invitations, and the administrator role itself.
 *
 * It is one composable rather than a string in seven components because the
 * seven pages must all link to the same place, and because it is configuration
 * — changing where a customer is sent must not need a release.
 *
 * An empty value is a real answer and not a missing one: an instance with no
 * commercial service behind it renders no link, rather than a link that goes
 * nowhere.
 */
export function useCommercialUrl(): ComputedRef<string> {
	const configStore = useConfigStore()
	return computed(() => configStore.braznAccountUrl ?? '')
}
