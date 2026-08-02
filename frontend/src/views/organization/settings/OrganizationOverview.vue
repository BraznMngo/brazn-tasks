<template>
	<OrganizationPage :title="$t('organization.overview.title')">
		<dl class="organization-facts">
			<div>
				<dt>{{ $t('organization.overview.edition') }}</dt>
				<dd>{{ organization?.edition }}</dd>
			</div>
			<div>
				<dt>{{ $t('organization.overview.members') }}</dt>
				<dd>{{ organization?.seatsOccupied }}</dd>
			</div>
			<div>
				<dt>{{ $t('organization.seats.title') }}</dt>
				<dd>{{ seats }}</dd>
			</div>
			<div>
				<dt>{{ $t('organization.teams.title') }}</dt>
				<dd>{{ teams }}</dd>
			</div>
			<div>
				<dt>{{ $t('organization.administration.title') }}</dt>
				<dd>{{ organization?.administrator?.name || organization?.administrator?.username }}</dd>
			</div>
		</dl>

		<!--
			Rules 2.3 requires the administrator to learn the Inbox boundary at
			the member list rather than at the point of needing to cross it. It
			is repeated here because Overview is the first page they see, and
			because it is the fact that makes removing somebody as final as it
			is.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.overview.inboxBoundary.title') }}
			</p>
			<p>{{ $t('organization.overview.inboxBoundary.text') }}</p>
		</Message>
	</OrganizationPage>
</template>

<script setup lang="ts">
import {computed} from 'vue'

import Message from '@/components/misc/Message.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {useOrganizationStore} from '@/stores/organization'
import {formatCapacity} from '@/helpers/organizationCapacity'

const organizationStore = useOrganizationStore()
const organization = computed(() => organizationStore.organization)

const seats = computed(() => formatCapacity(
	organization.value?.seatsOccupied ?? 0,
	organization.value?.seatsPurchased ?? null,
))
const teams = computed(() => formatCapacity(
	organization.value?.teamsUsed ?? 0,
	organization.value?.teamsAllowed ?? null,
))
</script>

<style lang="scss" scoped>
.organization-facts {
	display: flex;
	flex-wrap: wrap;
	gap: 1.5rem;
	margin-block-end: 1.5rem;

	dt {
		color: var(--grey-500);
		font-size: .85rem;
		text-transform: uppercase;
	}

	dd {
		font-size: 1.25rem;
		overflow-wrap: anywhere;
	}
}
</style>
