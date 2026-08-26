<template>
	<OrganizationPage :title="$t('organization.general.title')">
		<dl class="mb-4">
			<dt class="has-text-weight-bold">
				{{ $t('organization.general.name') }}
			</dt>
			<!-- The registered name, never the org_ identifier: a missing name
			     gets its own sentence (BRA-1439 Story 2). -->
			<dd>{{ organization?.organizationName ?? $t('organization.general.nameUnknown') }}</dd>
		</dl>

		<!--
			BRA-917 asks for "approved viral-footer and referral controls". What
			the footer says, who may switch it off, and whether suppression is
			per organization or per member are BRA-769's, and BRA-769's own text
			was rewritten mid-implementation. Nothing here decides any of it.

			The switches are deliberately NOT drawn yet. A toggle that writes
			nowhere is worse than an absent one: somebody flips it, nothing
			happens, and the product has lied. Per the standing rule, the
			control is built when the functionality exists — this page states
			where it will live.
		-->
		<Message variant="info">
			<p class="has-text-weight-bold">
				{{ $t('organization.general.pending.title') }}
			</p>
			<p>{{ $t('organization.general.pending.text') }}</p>
		</Message>
	</OrganizationPage>
</template>

<script setup lang="ts">
import {computed} from 'vue'

import Message from '@/components/misc/Message.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {useOrganizationStore} from '@/stores/organization'

const organizationStore = useOrganizationStore()
const organization = computed(() => organizationStore.organization)
</script>
