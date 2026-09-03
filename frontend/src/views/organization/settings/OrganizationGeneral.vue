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
			<!-- Null name gets its own sentence, never the org_ identifier alone (BRA-1439 Story 2). -->
			<p
				v-if="organization && organization.organizationName === null"
				class="help"
			>
				{{ $t('organization.general.nameUnknown') }}
			</p>
			<!-- BRA-1495: id stays visible so two companies with the same name are distinguishable. -->
			<p
				v-if="organization"
				class="help"
			>
				{{ organization.id }}
			</p>
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
import {getErrorText, success} from '@/message'
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
		// Name on screen comes from the fork re-read, never from the commercial body.
		await organizationStore.load()
		success({message: t('organization.general.renameSuccess')})
	} catch (e) {
		// Prefer the server's own sentence. Commercial failures often arrive as
		// `{error, debug}` with no `message` (http.ts `fail`); do not pretend
		// those mean "not the administrator" — this page is already admin-gated
		// (BRA-1479 #5).
		const status = (e as {response?: {status?: number}})?.response?.status
		const data = (e as {response?: {data?: unknown}})?.response?.data
		refusal.value = commercialRefusalText(data, status, e)
	} finally {
		working.value = false
	}
}

/**
 * Word the refusal the commercial (or proxy) failure actually carried.
 * `renameNotAllowed` is only for a bare 403.
 */
function commercialRefusalText(data: unknown, status: number | undefined, e: unknown): string {
	if (data !== null && typeof data === 'object') {
		const body = data as {message?: unknown, error?: unknown, debug?: unknown}
		if (typeof body.message === 'string' && body.message.trim() !== '') {
			return body.message
		}
		if (typeof body.error === 'string' && body.error.trim() !== '') {
			const debug = typeof body.debug === 'string' && body.debug.trim() !== ''
				? body.debug
				: ''
			if (body.error === 'internal_error') {
				return t('organization.error.text')
			}
			return debug ? `${body.error}: ${debug}` : body.error
		}
	}
	if (status === 403) {
		return t('organization.general.renameNotAllowed')
	}
	return getErrorText(e) || t('organization.error.text')
}
</script>
