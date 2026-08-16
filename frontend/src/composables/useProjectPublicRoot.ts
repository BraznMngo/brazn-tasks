import {ref, toValue, watch, type MaybeRefOrGetter} from 'vue'

import {useAuthStore} from '@/stores/auth'
import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'
import type {IProject} from '@/modelTypes/IProject'

// Mirrors entitlement.EditionTeams (pkg/modules/brazn/entitlement/entitlement.go),
// the same way PERSONAL_EDITION does in useManagedCapabilities.ts: the Go
// constant lives server-side, and the JWT carries its value as a plain string.
const TEAMS_EDITION = 'teams-cloud'

/**
 * Whether a project already sits beneath the organization's Public root - the
 * one part of the Teams topology a link may be shared from
 * (decideTeamsLinkShare, pkg/routes/managed_rules_teams.go). BRA-1343's
 * link-share toggle needs this to disable itself honestly instead of being
 * drawn and then refused by the server.
 *
 * This is deliberately NOT part of useManagedCapabilities: the four
 * capabilities there are a fixed table per edition, decidable from the JWT
 * alone with no network call. Whether a given project sits under the Public
 * root is a per-object topology fact that only the server can answer, the
 * same distinction BRA-1343 draws between "genuinely static per-account" and
 * "genuinely per-object" - see GET /brazn/projects/{id}/public-root
 * (pkg/routes/api/v1/brazn_project_topology.go).
 *
 * Outside the Teams edition this question is never asked: Personal already
 * hides link sharing entirely via capabilities.linkShare, and every other
 * edition places no such restriction on it. `isUnderPublicRoot` therefore
 * stays `true` (the permissive default, same reasoning as
 * useManagedCapabilities' own PERMISSIVE_CAPABILITIES) whenever the account
 * is not on Teams, so this composable is a no-op for them - and again on a
 * network failure, because a hint that could not be read must fall back to
 * showing the control and letting the server's own refusal be the real
 * answer, never to hiding something that might have been allowed.
 */
export function useProjectPublicRoot(projectId: MaybeRefOrGetter<IProject['id']>) {
	const authStore = useAuthStore()
	const isUnderPublicRoot = ref(true)

	watch(
		() => [toValue(projectId), authStore.managedEdition] as const,
		async ([id, edition]) => {
			if (edition !== TEAMS_EDITION || !id) {
				isUnderPublicRoot.value = true
				return
			}

			try {
				const HTTP = AuthenticatedHTTPFactory()
				const {data} = await HTTP.get(`brazn/projects/${id}/public-root`)
				isUnderPublicRoot.value = data?.under_public_root === true
			} catch {
				isUnderPublicRoot.value = true
			}
		},
		{immediate: true},
	)

	return {isUnderPublicRoot}
}
