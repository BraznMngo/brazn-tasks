import {computed} from 'vue'

import {useAuthStore} from '@/stores/auth'

/**
 * The capability vocabulary this ticket (BRA-1342) exposes to the UI, mirrored
 * 1:1 from the rule names route-classification.json and managed_gate.go use:
 * ruleProjectCreate, ruleProjectDuplicate, ruleProjectShare and ruleLinkShare.
 *
 * teamsSurface / teamCreate are BRA-1066 / COLLAB-5 hints for the teams nav.
 */
export interface ManagedCapabilities {
	projectCreate: boolean
	projectDuplicate: boolean
	projectShare: boolean
	linkShare: boolean
	/** Teams nav + /teams* routes (BRA-1066). False on Personal — they have no team. */
	teamsSurface: boolean
	/** Create-team affordances (BRA-1066 / COLLAB-5). */
	teamCreate: boolean
}

// Mirrors entitlement.EditionPersonal / EditionCollaboration
// (pkg/modules/brazn/entitlement/entitlement.go). Not imported: JWT carries
// plain strings, so these are the frontend's one copy of each.
export const PERSONAL_EDITION = 'personal-cloud'
export const COLLABORATION_EDITION = 'collaboration-cloud'

const PERSONAL_CAPABILITIES: ManagedCapabilities = {
	projectCreate: false,
	projectDuplicate: false,
	projectShare: false,
	linkShare: false,
	teamsSurface: false,
	teamCreate: false,
}

// Collaboration keeps the teams surface (retitled "Your Collaborators") but
// cannot create a second team (COLLAB-5).
const COLLABORATION_CAPABILITIES: ManagedCapabilities = {
	projectCreate: true,
	projectDuplicate: true,
	projectShare: true,
	linkShare: true,
	teamsSurface: true,
	teamCreate: false,
}

const PERMISSIVE_CAPABILITIES: ManagedCapabilities = {
	projectCreate: true,
	projectDuplicate: true,
	projectShare: true,
	linkShare: true,
	teamsSurface: true,
	teamCreate: true,
}

/**
 * The single source of truth for plan-capability UI hints (BRA-1342).
 *
 * EVERY CHECK HERE IS A HINT, NOT A SECOND POLICY LAYER. The server's managed
 * gate (RequireManagedPolicy) is the real refusal; if this ever disagrees with
 * it - a stale session, a rule added here before the server's, a bug - the
 * customer just sees today's flat error as a fallback. Nothing here may be
 * treated as authoritative, and nothing here should reimplement a decision
 * the server already makes.
 */
export function useManagedCapabilities() {
	const authStore = useAuthStore()

	const capabilities = computed<ManagedCapabilities>(() => {
		if (authStore.managedEdition === PERSONAL_EDITION) {
			return PERSONAL_CAPABILITIES
		}
		if (authStore.managedEdition === COLLABORATION_EDITION) {
			return COLLABORATION_CAPABILITIES
		}
		return PERMISSIVE_CAPABILITIES
	})

	return {
		writeRestricted: computed(() => authStore.writeRestricted),
		maxCollaborators: computed(() => authStore.maxCollaborators),
		capabilities,
	}
}

/**
 * Collaboration landing on teams.index opens the single team's detail — not
 * the list. Teams with exactly one team still gets the list (COLLAB-5).
 * Gate on edition, never on team count alone.
 */
export function collaborationDetailTeamId(
	edition: string | null,
	teams: {id: number}[],
): number | null {
	if (edition !== COLLABORATION_EDITION || teams.length === 0) {
		return null
	}
	return teams[0].id
}
