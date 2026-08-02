<template>
	<OrganizationPage :title="$t('organization.teams.title')">
		<p class="is-size-4">
			{{ capacity }}
		</p>
		<p class="mb-4">
			{{ organization?.seatsPurchased === null
				? $t('organization.seats.capacity.unknown')
				: $t('organization.seats.capacity.text', {
					teams: organization?.teamsAllowed,
					seats: organization?.seatsPurchased,
				}) }}
		</p>

		<!--
			Rendered only when the seat rule would let it succeed, which is the
			hide-versus-refuse rule applied literally: a control exists if and
			only if this actor could succeed at it in some state they can reach
			by themselves. An administrator CAN reach a state where another team
			fits — by buying seats — so what is drawn when they cannot is the
			refusal with the number, not a greyed-out button.
		-->
		<form
			v-if="organization?.canCreateTeam"
			@submit.prevent="create"
		>
			<div class="field has-addons">
				<div class="control is-expanded">
					<input
						v-model="name"
						class="input"
						:placeholder="$t('organization.teams.namePlaceholder')"
						required
					>
				</div>
				<div class="control">
					<XButton
						:loading="working"
						@click="create"
					>
						{{ $t('organization.teams.create') }}
					</XButton>
				</div>
			</div>
		</form>

		<Message
			v-else
			variant="warning"
		>
			<p class="has-text-weight-bold">
				{{ $t('organization.teams.capped.title') }}
			</p>
			<p>
				{{ organization?.seatsPurchased === null
					? $t('organization.teams.capped.unknown')
					: $t('organization.teams.capped.text', {seats: seatsNeeded}) }}
			</p>
			<XButton
				v-if="commercialUrl"
				variant="secondary"
				:href="commercialUrl"
			>
				{{ $t('organization.seats.add') }}
			</XButton>
		</Message>

		<Message
			v-if="refusal"
			variant="danger"
		>
			{{ refusal }}
		</Message>

		<!--
			The primary team carries no removal control at all, for either
			actor. It is a protected root and every member navigates by it, so
			"cannot be removed" is not a refusal to render — it is a control
			that does not exist.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.teams.removal.title') }}
			</p>
			<p>{{ $t('organization.teams.removal.text') }}</p>
		</Message>
	</OrganizationPage>
</template>

<script setup lang="ts">
import {computed, ref} from 'vue'
import {useI18n} from 'vue-i18n'

import Message from '@/components/misc/Message.vue'
import XButton from '@/components/input/Button.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {useOrganizationStore} from '@/stores/organization'
import {useCommercialUrl} from '@/composables/useCommercialUrl'
import {formatCapacity} from '@/helpers/organizationCapacity'
import {AuthenticatedHTTPFactory} from '@/helpers/fetcher'

const {t} = useI18n({useScope: 'global'})

const organizationStore = useOrganizationStore()
const organization = computed(() => organizationStore.organization)
const commercialUrl = useCommercialUrl()

const name = ref('')
const working = ref(false)
const refusal = ref('')

const capacity = computed(() => formatCapacity(
	organization.value?.teamsUsed ?? 0,
	organization.value?.teamsAllowed ?? null,
))

// The number a customer would have to reach, computed from what the server
// already told us rather than from a copy of the rule. The server refuses with
// the same figure, so the two cannot disagree about what buying more would buy.
const seatsNeeded = computed(() => (organization.value?.teamsUsed ?? 0) * 3 + 3)

async function create() {
	if (!name.value || working.value) {
		return
	}

	working.value = true
	refusal.value = ''

	const HTTP = AuthenticatedHTTPFactory()
	try {
		await HTTP.put('brazn/organization/teams', {name: name.value})
		name.value = ''
		await organizationStore.load()
	} catch (e) {
		// The server's refusal is what is shown, not a locally reconstructed
		// one: it is the only one that reflects what the rule actually decided.
		const data = (e as {response?: {data?: {message?: string}}})?.response?.data
		refusal.value = data?.message || t('organization.error.text')
	} finally {
		working.value = false
	}
}
</script>
