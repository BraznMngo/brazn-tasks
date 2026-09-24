import {computed} from 'vue'

import {useAuthStore} from '@/stores/auth'

/**
 * The capability vocabulary this ticket (BRA-1342) exposes to the UI, mirrored
 * 1:1 from the rule names route-classification.json and managed_gate.go use:
 * ruleProjectCreate, ruleProjectDuplicate, ruleProjectShare and ruleLinkShare.
 *
 * Only these four exist here because these are the only rules any component
 * currently consumes (ProjectSettingsDropdown, LinkSharing and the four
 * New-Project entry points). Add a field only when a consumer needs it -
 * mirroring the rest of the vocabulary speculatively would be a policy table
 * with no reader.
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

// Mirrors entitlement.EditionPersonal (pkg/modules/brazn/entitlement/entitlement.go).
// Not imported from anywhere: the Go constant lives server-side and the JWT
// carries its value as a plain string, so this is the frontend's one copy of it.
const PERSONAL_EDITION = 'personal-cloud'

// The Personal edition's policy table (pkg/routes/managed_rules_personal.go,
// BRA-782) is a flat, unconditional denyPersonal(...) for every one of these
// routes - there is no per-request decision to mirror, only a fixed "no".
const PERSONAL_CAPABILITIES: ManagedCapabilities = {
	projectCreate: false,
	projectDuplicate: false,
	projectShare: false,
	linkShare: false,
	teamsSurface: false,
	teamCreate: false,
}

// Every edition other than Personal - Teams, community/self-hosted, or no
// entitlement at all - defaults every capability to true (permissive). Teams
// has its own fixed rules too (managed_rules_teams.go), but giving them their
// own capability values is BRA-1343 and explicitly out of scope here.
// Collaboration keeps teamsSurface (COLLAB-5 retitles it); teamCreate flips
// false there in a later ticket.
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
		return authStore.managedEdition === PERSONAL_EDITION
			? PERSONAL_CAPABILITIES
			: PERMISSIVE_CAPABILITIES
	})

	return {
		writeRestricted: computed(() => authStore.writeRestricted),
		capabilities,
	}
}
