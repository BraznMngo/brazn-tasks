<template>
	<OrganizationPage :title="$t('organization.members.title')">
		<p class="mb-4">
			{{ $t('organization.members.inUse', {used: organization?.seatsOccupied, limit: seatLimit}) }}
		</p>

		<Message
			v-if="refusal"
			variant="danger"
		>
			{{ refusal }}
		</Message>

		<Message
			v-if="removed"
			variant="success"
		>
			{{ removed }}
		</Message>

		<table class="table has-actions is-fullwidth is-striped is-hoverable">
			<tbody>
				<tr
					v-for="member in organization?.members ?? []"
					:key="member.userId"
				>
					<td>
						<strong>{{ member.name || member.username }}</strong>
						<br>
						{{ member.email }}
					</td>
					<td>
						{{ member.administrator
							? $t('organization.members.roleAdministrator')
							: $t('organization.members.roleMember') }}
					</td>
					<td class="has-text-right">
						<!--
							Two people never carry this control, and neither is
							drawn disabled: the signed-in administrator, because
							an organization is never left without one, and
							whoever holds the role, because the service refuses
							that removal outright (`still_administrator`) and
							the way the role moves is a transfer.

							Hiding it is NOT what enforces either rule — the
							service decides, on the resolved bearer, and answers
							a refusal to anyone who asks anyway. This is the
							roster declining to offer an act that cannot
							succeed, which is the same reason the primary team
							carries no removal control on the Teams page.
						-->
						<XButton
							v-if="canRemove(member)"
							data-act="remove-member"
							:data-member-id="member.userId"
							variant="secondary"
							danger
							:loading="working"
							@click="confirmRemoval(member)"
						>
							{{ $t('organization.members.remove.action') }}
						</XButton>
					</td>
				</tr>
			</tbody>
		</table>

		<!--
			REMOVING IS A CONTROL HERE. INVITING IS NOT, AND THE DIFFERENCE IS THE
			DOOR.

			This page used to carry a comment forbidding both, on the grounds that
			membership and `seat_status` are authoritative from the commercial
			service and "a button here that wrote one would be Brazn Tasks
			deciding who has paid". The premise was right; the conclusion has
			since stopped following from it, because a removal control does not
			write anything locally.

			The button POSTs to the commercial service, which decides, writes
			`seat_status` and `effective_state` itself, revokes the member's
			authorizations and delivers the new projection back to this fork —
			failing closed if this fork does not acknowledge it. Nothing here
			grants, infers or repairs a flag. Brazn Tasks is not deciding who has
			paid; it is asking the service that does, and then re-reading the
			answer.

			Rules §1 — do not render a control this actor cannot succeed at — is
			the other half of the old reason, and it is why the control could not
			have been drawn before 2026-08-04: there was no endpoint to call, so
			no administrator could have succeeded at it. There is one now.

			Inviting stays elsewhere for a reason that has NOT expired: it needs
			an address, a seat check and a mail this product does not send. It is
			BRA-917's, and the message below says so rather than leaving the
			absence to be read as an oversight.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.members.managedElsewhere.title') }}
			</p>
			<p>{{ $t('organization.members.managedElsewhere.text') }}</p>
			<XButton
				v-if="commercialUrl"
				variant="secondary"
				:href="commercialUrl"
			>
				{{ $t('organization.members.managedElsewhere.action') }}
			</XButton>
		</Message>

		<!--
			Confirmed before it is sent, because it cannot be undone: access ends
			at once and the member's Inbox closes with them. Header and text
			slots only, which is the same shape every other destructive
			confirmation in this product uses.
		-->
		<Modal
			v-if="confirming !== null"
			@close="confirming = null"
			@submit="remove()"
		>
			<template #header>
				<span>{{ $t('organization.members.remove.confirm.title', {name: confirming?.name}) }}</span>
			</template>

			<template #text>
				<p>{{ $t('organization.members.remove.confirm.access') }}</p>
				<p>{{ $t('organization.members.remove.confirm.inbox') }}</p>
				<p>{{ $t('organization.members.remove.confirm.team') }}</p>
			</template>
		</Modal>
	</OrganizationPage>
</template>

<script setup lang="ts">
import {computed, ref} from 'vue'
import {useI18n} from 'vue-i18n'

import Message from '@/components/misc/Message.vue'
import Modal from '@/components/misc/Modal.vue'
import XButton from '@/components/input/Button.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {useOrganizationStore} from '@/stores/organization'
import {useAuthStore} from '@/stores/auth'
import {useCommercialUrl} from '@/composables/useCommercialUrl'
import {AuthenticatedHTTPFactory, commercialV1Url} from '@/helpers/fetcher'
import {httpStatusOf} from '@/helpers/authErrorCodes'

const {t} = useI18n({useScope: 'global'})

const organizationStore = useOrganizationStore()
const authStore = useAuthStore()
const organization = computed(() => organizationStore.organization)
const seatLimit = computed(() => organization.value?.seatsPurchased ?? '—')
const commercialUrl = useCommercialUrl()

const working = ref(false)
const refusal = ref('')
const removed = ref('')
const confirming = ref<{userId: number, name: string} | null>(null)

function canRemove(member: {userId: number, administrator: boolean}): boolean {
	return member.userId !== authStore.info?.id && !member.administrator
}

function confirmRemoval(member: {userId: number, name: string, username: string}) {
	confirming.value = {userId: member.userId, name: member.name || member.username}
}

async function remove() {
	const member = confirming.value
	const organizationId = organization.value?.id
	if (member === null || organizationId === undefined || working.value) {
		return
	}

	working.value = true
	refusal.value = ''
	removed.value = ''
	confirming.value = null

	const HTTP = AuthenticatedHTTPFactory()
	try {
		const {data} = await HTTP.post(commercialV1Url('organizations/members/removal'), {
			organization_id: organizationId,
			// A DECIMAL STRING. This service's identifiers are opaque strings and
			// it refuses a number outright; the value is the same `users.id` it
			// resolved the bearer to, rendered the way it renders every id.
			member_user_id: String(member.userId),
		})

		// ALL THREE OUTCOMES ANSWER 200 and are told apart only by this field, so
		// reading the status alone would report a refused removal as a success.
		const outcome = (data as {outcome?: string} | undefined)?.outcome
		if (outcome === 'removed') {
			removed.value = t('organization.members.remove.removed', {name: member.name})
			// RE-READ, never decrement. A 200 is the guarantee that the service
			// has already delivered the new projection to this fork and had it
			// acknowledged — it fails closed otherwise — so this returns the new
			// number rather than the old one, and returns `seatsOccupied`,
			// `members`, `teamsUsed` and `canCreateTeam` derived together by the
			// side that owns them.
			await organizationStore.load()
		} else if (outcome === 'still_administrator') {
			refusal.value = t('organization.members.remove.stillAdministrator')
		} else if (outcome === 'not_a_member') {
			// The roster on screen disagrees with the server, so it is re-read
			// here too: leaving the row visible under a message saying it is not
			// there would be a worse answer than the one we started with.
			refusal.value = t('organization.members.remove.notAMember')
			await organizationStore.load()
		} else {
			refusal.value = t('organization.error.text')
		}
	} catch (e) {
		// Worded here rather than taken from the response, which is the one place
		// this departs from the Teams page: these routes answer a bare status
		// with no body, so there is no server sentence to show and a blank
		// refusal would be worse than a local one that is accurate about which
		// refusal it was.
		const status = httpStatusOf(e)
		if (status === 403) {
			refusal.value = t('organization.members.remove.notAdministrator')
		} else if (status === 503) {
			refusal.value = t('organization.members.remove.unavailable')
		} else {
			refusal.value = t('organization.error.text')
		}
	} finally {
		working.value = false
	}
}
</script>
