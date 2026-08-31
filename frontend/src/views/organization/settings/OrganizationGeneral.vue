<template>
	<OrganizationPage :title="$t('organization.general.title')">
		<!--
			BRA-1479 #5: rename uses the existing commercial route
			POST /v1/organizations/rename (same call as ONE's view-settings pencil).
			The name shown after save is re-read from the fork, not invented from
			the commercial reply.
		-->
		<form
			class="mb-4"
			@submit.prevent="save"
		>
			<label
				class="label"
				for="org-name"
			>
				{{ $t('organization.general.name') }}
			</label>
			<div class="field has-addons">
				<div class="control is-expanded">
					<input
						id="org-name"
						v-model="name"
						class="input"
						:placeholder="$t('organization.general.renamePlaceholder')"
						required
						:disabled="working"
					>
				</div>
				<div class="control">
					<XButton
						:loading="working"
						:disabled="!canSave"
						@click="save"
					>
						{{ $t('organization.general.renameSave') }}
					</XButton>
				</div>
			</div>
		</form>

		<Message
			v-if="refusal"
			class="mb-4"
			variant="danger"
		>
			{{ refusal }}
		</Message>

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
import {computed, ref, watch} from 'vue'
import {useI18n} from 'vue-i18n'

import Message from '@/components/misc/Message.vue'
import XButton from '@/components/input/Button.vue'
import OrganizationPage from '@/components/organization/OrganizationPage.vue'
import {success} from '@/message'
import {renameOrganization} from '@/services/organizationRename'
import {useOrganizationStore} from '@/stores/organization'

const {t} = useI18n({useScope: 'global'})

const organizationStore = useOrganizationStore()
const organization = computed(() => organizationStore.organization)

const name = ref('')
const working = ref(false)
const refusal = ref('')

watch(
	organization,
	(org) => {
		name.value = org?.organizationName ?? ''
	},
	{immediate: true},
)

const canSave = computed(() => {
	const trimmed = name.value.trim()
	if (!trimmed || working.value || !organization.value?.id) {
		return false
	}
	return trimmed !== (organization.value.organizationName ?? '')
})

async function save() {
	const org = organization.value
	const trimmed = name.value.trim()
	if (!org?.id || !trimmed || working.value || !canSave.value) {
		return
	}

	working.value = true
	refusal.value = ''

	try {
		await renameOrganization(org.id, trimmed)
		await organizationStore.load()
		success({message: t('organization.general.renameSuccess')})
	} catch (e) {
		// Server refusal verbatim — admin-only route or commercial gate.
		const data = (e as {response?: {data?: {message?: string}}})?.response?.data
		refusal.value = data?.message || t('organization.general.renameNotAllowed')
	} finally {
		working.value = false
	}
}
</script>
