<template>
	<OrganizationPage :title="$t('organization.administration.title')">
		<dl class="mb-4">
			<dt class="has-text-weight-bold">
				{{ $t('organization.administration.current') }}
			</dt>
			<dd>{{ organization?.administrator?.name || organization?.administrator?.username }}</dd>
		</dl>

		<p class="mb-4">
			{{ $t('organization.administration.exactlyOne') }}
		</p>

		<!--
			Transfer is a link out and not a control here, and the reason is the
			contract rather than scope. `organization_admin` is authoritative
			from the commercial service, which states that this fork must never
			grant, infer or repair it locally — it is precisely the flag that
			gates every access-expanding and protected-topology operation.

			What this product DOES enforce is the other half of AC3: it refuses
			every organization route while more than one subject in an
			organization claims administration, so a transfer that went wrong
			stops both claimants instead of letting either carry on.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.administration.transfer.title') }}
			</p>
			<p>{{ $t('organization.administration.transfer.text') }}</p>
			<XButton
				v-if="commercialUrl"
				variant="secondary"
				:href="commercialUrl"
			>
				{{ $t('organization.administration.transfer.action') }}
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
