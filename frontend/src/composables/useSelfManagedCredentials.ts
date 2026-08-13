import {computed} from 'vue'
import type {ComputedRef} from 'vue'

import {useAuthStore} from '@/stores/auth'

/**
 * Whether this account changes its own sign-in details HERE — password, email
 * address and second factor.
 *
 * IT IS `isLocalUser` ALONE. An OIDC account has no password here, which is
 * the one thing every screen that asks this question needs. Managed mode is
 * deliberately NOT part of the answer: password, email and TOTP are classified
 * `ordinary` in pkg/routes/route-classification.json and carry no managed-mode
 * rule, by the same ruling that keeps them writable during a payment
 * restriction (Sebastian, 2026-08-03) — "locking someone out of their password
 * ... is a security problem rather than a payment lever." Nothing on the server
 * refuses these routes under managed mode, so gating them here would hide a
 * capability the account actually still has.
 */
export function useSelfManagedCredentials(): ComputedRef<boolean> {
	const authStore = useAuthStore()

	return computed(() => Boolean(authStore.info?.isLocalUser))
}
