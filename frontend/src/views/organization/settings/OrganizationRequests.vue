<template>
	<OrganizationPage :title="$t('organization.requests.title')">
		<Message
			v-if="refusal"
			variant="danger"
		>
			{{ refusal }}
		</Message>

		<Message
			v-if="decided"
			variant="success"
		>
			{{ decided }}
		</Message>

		<!--
			THE ONE RUNTIME CAPABILITY CHECK ON THIS PAGE, and it is free because
			the page already has to read the queue before it can draw anything.
			This route answers 401, 403, 500, 502 and 503 - never a 404 - so a 404
			is not a state of this organization, it is the route not being there:
			an instance whose commercial service has not been deployed this far
			yet. Drawing an empty queue would say nobody has asked, which is a
			different and false statement.
		-->
		<Message
			v-if="unavailable"
			variant="info"
		>
			<p class="has-text-weight-bold">
				{{ $t('organization.requests.unavailable.title') }}
			</p>
			<p>{{ $t('organization.requests.unavailable.text') }}</p>
		</Message>

		<p v-else-if="requests.length === 0">
			{{ $t('organization.requests.none') }}
		</p>

		<template v-else>
			<!--
				THE MONEY, BEFORE THE BUTTON AND NOT AFTER IT.

				Approving past the purchased seats buys one and prorates it, which
				is a charge the administrator authorises by clicking approve. It is
				stated here, above the queue, and again in the confirmation — an
				administrator who learns what an approval cost from an invoice was
				never asked.

				What is NOT claimed here is an amount. The decision route answers an
				outcome and nothing about price, and the seat figures come from this
				organization's own read model, so this says what will happen and
				what the current position is. A number invented locally would be
				the one thing on this page nobody could stand behind.
			-->
			<Message
				v-if="approvalBuysASeat"
				variant="warning"
			>
				<p class="has-text-weight-bold">
					{{ $t('organization.requests.buysASeat.title') }}
				</p>
				<p>
					{{ organization?.seatsPurchased === null
						? $t('organization.requests.buysASeat.unknown')
						: $t('organization.requests.buysASeat.text', {
							used: organization?.seatsOccupied,
							limit: organization?.seatsPurchased,
						}) }}
				</p>
			</Message>

			<p
				v-else
				class="mb-4"
			>
				{{ $t('organization.requests.withinSeats', {
					used: organization?.seatsOccupied,
					limit: organization?.seatsPurchased,
				}) }}
			</p>

			<table class="table has-actions is-fullwidth is-striped is-hoverable">
				<tbody>
					<tr
						v-for="request in requests"
						:key="request.requestId"
					>
						<td>
							<!--
								The address is the whole identity of the person
								asking. This layer holds no name for them - they
								are not a member yet - so an administrator who
								could not see it would be approving an unknown.
							-->
							<strong>{{ request.requesterEmail }}</strong>
							<br>
							<span v-if="request.message">{{ request.message }}</span>
						</td>
						<td>{{ teamName(request.teamId) }}</td>
						<td>
							{{ $t('organization.requests.asked', {since: formatDateSince(request.requestedAt)}) }}
							<br>
							<span v-if="request.verifiedAt">
								{{ $t('organization.requests.confirmed', {since: formatDateSince(request.verifiedAt)}) }}
							</span>
						</td>
						<td class="has-text-right">
							<XButton
								data-act="approve-request"
								:data-request-id="request.requestId"
								:loading="working"
								@click="confirm(request, 'approved')"
							>
								{{ $t('organization.requests.approve') }}
							</XButton>
							<XButton
								data-act="decline-request"
								:data-request-id="request.requestId"
								variant="secondary"
								:loading="working"
								@click="confirm(request, 'declined')"
							>
								{{ $t('organization.requests.decline') }}
							</XButton>
						</td>
					</tr>
				</tbody>
			</table>
		</template>

		<Modal
			v-if="confirming !== null"
			@close="confirming = null"
			@submit="decide()"
		>
			<template #header>
				<span>{{ confirming.decision === 'approved'
					? $t('organization.requests.approveTitle', {email: confirming.requesterEmail})
					: $t('organization.requests.declineTitle', {email: confirming.requesterEmail}) }}</span>
			</template>

			<template #text>
				<template v-if="confirming.decision === 'approved'">
					<p>{{ $t('organization.requests.approveText') }}</p>
					<p v-if="approvalBuysASeat">
						{{ $t('organization.requests.approveBuysASeat') }}
					</p>
				</template>
				<p v-else>
					{{ $t('organization.requests.declineText') }}
				</p>
			</template>
		</Modal>
	</OrganizationPage>
</template>

<script setup lang="ts">
import {computed, onMounted, ref} from 'vue'
import {useI18n} from 'vue-i18n'

import Message from '@/components/misc/Message.vue'
import Modal from '@/components/misc/Modal.vue'
import XButton from '@/components/input/Button.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {useOrganizationStore} from '@/stores/organization'
import {formatDateSince} from '@/helpers/time/formatDate'
import {AuthenticatedHTTPFactory, commercialV1Url} from '@/helpers/fetcher'

interface JoinRequest {
	requestId: string
	requesterEmail: string
	message: string
	teamId: string
	requestedAt: string
	verifiedAt: string | null
}

const {t} = useI18n({useScope: 'global'})

const organizationStore = useOrganizationStore()
const organization = computed(() => organizationStore.organization)

const working = ref(false)
const refusal = ref('')
const decided = ref('')
const requests = ref<JoinRequest[]>([])
const unavailable = ref(false)
const confirming = ref<(JoinRequest & {decision: 'approved' | 'declined'}) | null>(null)

/**
 * Whether the next approval buys a seat rather than filling one already paid
 * for.
 *
 * Both numbers come from the server's own read model, so this cannot disagree
 * with the rule that will actually be applied. A null `seatsPurchased` is not
 * zero and not unlimited — it is "this instance cannot say" — and it is treated
 * as "assume it costs", because the expensive mistake is telling somebody an
 * approval is free when it is not.
 */
const approvalBuysASeat = computed(() => {
	const purchased = organization.value?.seatsPurchased
	if (purchased === null || purchased === undefined) {
		return true
	}
	return (organization.value?.seatsOccupied ?? 0) >= purchased
})

function teamName(teamId: string): string {
	const team = (organization.value?.teams ?? []).find(candidate => String(candidate.teamId) === teamId)
	return team?.name ?? teamId
}

async function load() {
	const HTTP = AuthenticatedHTTPFactory()
	try {
		const {data} = await HTTP.get(commercialV1Url('team-access-requests'))
		requests.value = ((data?.requests ?? []) as Record<string, string | null>[]).map(row => ({
			requestId: String(row.request_id),
			requesterEmail: row.requester_email ?? '',
			message: row.message ?? '',
			teamId: String(row.team_id),
			requestedAt: String(row.requested_at),
			verifiedAt: row.verified_at ?? null,
		}))
	} catch (e) {
		const status = (e as {response?: {status?: number}})?.response?.status
		if (status === 404) {
			unavailable.value = true
			return
		}
		// ONE 403 FOR TWO CASES, deliberately on the service's side: no
		// organization, and an organization this account does not administer. It
		// is not distinguished here either, because distinguishing it is exactly
		// what the flattening exists to prevent.
		refusal.value = status === 403
			? t('organization.requests.notAdministrator')
			: t('organization.error.text')
	}
}

onMounted(load)

function confirm(request: JoinRequest, decision: 'approved' | 'declined') {
	if (working.value) {
		return
	}
	refusal.value = ''
	decided.value = ''
	confirming.value = {...request, decision}
}

async function decide() {
	const pending = confirming.value
	const organizationId = organization.value?.id
	if (pending === null || organizationId === undefined || working.value) {
		return
	}

	working.value = true
	refusal.value = ''
	decided.value = ''
	confirming.value = null

	const HTTP = AuthenticatedHTTPFactory()
	try {
		const {data} = await HTTP.post(commercialV1Url('team-access-requests/decide'), {
			organization_id: organizationId,
			// STRINGS, both of them. The service validates every identifier as an
			// opaque string and answers a bare 400 for a number.
			request_id: pending.requestId,
			decision: pending.decision,
		})

		const outcome = (data as {outcome?: string} | undefined)?.outcome
		if (outcome === 'approved') {
			decided.value = t('organization.requests.approved', {email: pending.requesterEmail})
		} else if (outcome === 'declined') {
			decided.value = t('organization.requests.declined', {email: pending.requesterEmail})
		} else if (outcome === 'not_admitted') {
			// A 200 THAT IS A REFUSAL. The administrator approved and NOBODY was
			// seated; the request is deliberately left open so the same approval
			// can be made again once the cause is fixed. Reporting this as a
			// success is the specific defect reading the status alone produces.
			const why = (data as {invitation_outcome?: string | null} | undefined)?.invitation_outcome
			refusal.value = why === 'not_invitable'
				? t('organization.requests.notInvitable', {email: pending.requesterEmail})
				: t('organization.requests.notAdmitted', {email: pending.requesterEmail})
		} else {
			refusal.value = t('organization.error.text')
		}

		// Re-read both sides after every answer, including a refusal: an approval
		// that seated somebody moves the seat count, and one that did not still
		// leaves a queue this page must show as the server now has it.
		await Promise.all([load(), organizationStore.load()])
	} catch (e) {
		const status = (e as {response?: {status?: number}})?.response?.status
		if (status === 403) {
			refusal.value = t('organization.requests.notAdministrator')
		} else if (status === 404) {
			// Unknown, already decided, or never confirmed - flattened by the
			// service into one answer. The queue is re-read rather than argued
			// with, because whatever the cause, what is on screen is stale.
			refusal.value = t('organization.requests.gone')
			await load()
		} else {
			refusal.value = t('organization.error.text')
		}
	} finally {
		working.value = false
	}
}
</script>
