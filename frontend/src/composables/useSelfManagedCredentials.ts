import {computed} from 'vue'
import type {ComputedRef} from 'vue'

import {useAuthStore} from '@/stores/auth'
import {useConfigStore} from '@/stores/config'

/**
 * Whether this account changes its own sign-in details HERE — password, email
 * address and second factor.
 *
 * ONE composable rather than the same pair of conditions written at each screen,
 * because the four places that ask this question have already diverged once: the
 * screens tested `isLocalUser` alone, which is true for a provisioned account
 * and so drew three forms that could never be submitted successfully. A
 * provisioned user is created with the local issuer and nothing later changes
 * it, so "is this a local account" cannot answer this question, and every site
 * that asks it locally gets it wrong in the same way.
 *
 * `isLocalUser` still has to be part of the answer: an OIDC account has no
 * password here either, which is what the screens were already right about.
 * What is added is the instance: under managed mode the commercial service owns
 * account lifecycle, and the managed gate refuses these routes for everyone on
 * the instance including its administrator — so the granularity is the instance,
 * matching the gate rather than guessing at a per-account rule the server does
 * not have.
 */
export function useSelfManagedCredentials(): ComputedRef<boolean> {
	const authStore = useAuthStore()
	const configStore = useConfigStore()

	return computed(() => Boolean(authStore.info?.isLocalUser) && !configStore.braznManagedMode)
}
