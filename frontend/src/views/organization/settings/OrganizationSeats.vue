<template>
	<OrganizationPage :title="$t('organization.seats.title')">
		<p class="is-size-4">
			{{ $t('organization.members.inUse', {used: organization?.seatsOccupied, limit: seatLimit}) }}
		</p>
		<p class="mb-4">
			{{ $t('organization.seats.explanation') }}
		</p>

		<!--
			What seats decide that is NOT money: how many teams the organization
			may hold. This is the one consequence of a seat count that lives in
			this product rather than in the commercial one, because teams are
			this product's rows.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.seats.capacity.title') }}
			</p>
			<p>
				{{ organization?.seatsPurchased === null
					? $t('organization.seats.capacity.unknown')
					: $t('organization.seats.capacity.text', {
						teams: organization?.teamsAllowed,
						seats: organization?.seatsPurchased,
					}) }}
			</p>
		</Message>

		<!--
			AC5: price, cadence, the three-seat floor, VAT and invoices are the
			commercial surface's. This page counts seats and links out. A second
			invoice list here is exactly how the two drift apart.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.seats.priced.title') }}
			</p>
			<p>{{ $t('organization.seats.priced.text') }}</p>
			<XButton
				v-if="commercialUrl"
				variant="secondary"
				:href="commercialUrl"
			>
				{{ $t('organization.billing.open') }}
			</XButton>
		</Message>
	</OrganizationPage>
</template>

<script setup lang="ts">
import {computed} from 'vue'

import Message from '@/components/misc/Message.vue'
import XButton from '@/components/input/Button.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {useOrganizationStore} from '@/stores/organization'
import {useCommercialUrl} from '@/composables/useCommercialUrl'

const organizationStore = useOrganizationStore()
const organization = computed(() => organizationStore.organization)
const seatLimit = computed(() => organization.value?.seatsPurchased ?? '—')
const commercialUrl = useCommercialUrl()
</script>
