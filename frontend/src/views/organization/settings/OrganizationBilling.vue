<template>
	<OrganizationPage :title="$t('organization.billing.title')">
		<!--
			Three rows and a link, and that is the whole page (AC5). All three
			are read from the entitlement projection this product already holds,
			so nothing here is a second copy of a commercial record — and there
			is no invoice list, because a second invoice list is exactly how two
			systems drift apart.
		-->
		<dl class="organization-facts">
			<div>
				<dt>{{ $t('organization.overview.edition') }}</dt>
				<dd>{{ organization?.edition }}</dd>
			</div>
			<div>
				<dt>{{ $t('organization.seats.title') }}</dt>
				<dd>{{ organization?.seatsPurchased ?? '—' }}</dd>
			</div>
			<div>
				<dt>{{ $t('organization.billing.members') }}</dt>
				<dd>{{ organization?.seatsOccupied }}</dd>
			</div>
		</dl>

		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.billing.elsewhere.title') }}
			</p>
			<p>{{ $t('organization.billing.elsewhere.text') }}</p>
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
const commercialUrl = useCommercialUrl()
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
