<template>
	<OrganizationPage :title="$t('organization.members.title')">
		<p class="mb-4">
			{{ $t('organization.members.inUse', {used: organization?.seatsOccupied, limit: seatLimit}) }}
		</p>

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
				</tr>
			</tbody>
		</table>

		<!--
			Inviting and removing are NOT controls here, and the absence is
			deliberate rather than unfinished.

			A member of an organization IS an entitlement projection, and both
			`organization_admin` and `seat_status` are authoritative from the
			commercial service — the contract states that this product must
			never grant, infer or repair them locally, because they are the
			flags that gate every access-expanding operation. A button here
			that wrote one would be Brazn Tasks deciding who has paid.

			Rules §1 also forbids rendering a control this actor cannot succeed
			at. So the roster is read here, where it belongs, and the acts that
			change it are performed where they are authoritative. Membership
			lifecycle is BRA-786's.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.members.managedElsewhere.title') }}
			</p>
			<p>{{ $t('organization.members.managedElsewhere.text') }}</p>
			<XButton
				variant="secondary"
				:href="commercialUrl"
			>
				{{ $t('organization.members.managedElsewhere.action') }}
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
