<template>
	<Card
		v-if="userDeletionEnabled"
		:title="$t('user.deletion.title')"
	>
		<!--
			Managed accounts (BRA-1404) never reach the form below — this fork's
			own `/user/deletion/request` is deliberately gated on a managed
			instance (route-classification.json's `service-managed` class), so a
			managed account's ONLY working path is the commercial service's own erasure
			(`services/accountErasure.ts`): immediate, no mailed confirmation, and
			carrying the successor handover a community account's flow never
			needed. `deletionScheduledAt` never fires for a managed account either
			— nothing here schedules it.
		-->
		<template v-if="isManaged">
			<p>{{ $t('user.deletion.managedText1') }}</p>
			<p>{{ $t('user.deletion.managedTextImmediate') }}</p>

			<template v-if="successorCandidates.length > 0">
				<p>{{ $t('user.deletion.managedSuccessorRequired') }}</p>
				<FormField
					:label="$t('user.deletion.managedSuccessorLabel')"
					layout="two-col"
				>
					<FormSelect
						id="managedDeletionSuccessor"
						v-model="selectedSuccessor"
						:error="errSuccessorRequired ? $t('user.deletion.managedSuccessorRequiredError') : null"
						:options="successorOptions"
						:disabled="erasing"
					/>
				</FormField>
			</template>

			<Message
				v-if="managedError"
				variant="danger"
			>
				{{ managedError }}
			</Message>

			<XButton
				:loading="loadingCandidates || erasing"
				class="is-fullwidth mbs-4 is-danger"
				@click="deleteManagedAccount()"
			>
				{{ $t('user.deletion.confirm') }}
			</XButton>
		</template>

		<template v-else>
			<template v-if="deletionScheduledAt !== null">
				<form @submit.prevent="cancelDeletion()">
					<p>
						{{
							$t('user.deletion.scheduled', {
								date: formatDisplayDate(deletionScheduledAt),
								dateSince: formatDateSince(deletionScheduledAt),
							})
						}}
					</p>
					<template v-if="isLocalUser">
						<p>
							{{ $t('user.deletion.scheduledCancelText') }}
						</p>
						<FormField
							id="currentPasswordAccountDelete"
							ref="passwordInput"
							v-model="password"
							:label="$t('user.settings.currentPassword')"
							:placeholder="$t('user.settings.currentPasswordPlaceholder')"
							type="password"
							:error="errPasswordRequired ? $t('user.deletion.passwordRequired') : null"
							@keyup="() => errPasswordRequired = password === ''"
						/>
					</template>
					<p v-else>
						{{ $t('user.deletion.scheduledCancelButton') }}
					</p>
				</form>

				<XButton
					:loading="accountDeleteService.loading"
					class="is-fullwidth mbs-4"
					@click="cancelDeletion()"
				>
					{{ $t('user.deletion.scheduledCancelConfirm') }}
				</XButton>
			</template>
			<template v-else>
				<p>
					{{ $t('user.deletion.text1') }}
				</p>
				<form
					v-if="isLocalUser"
					@submit.prevent="deleteAccount()"
				>
					<p>
						{{ $t('user.deletion.text2') }}
					</p>
					<FormField
						id="currentPasswordAccountDelete"
						ref="passwordInput"
						v-model="password"
						:label="$t('user.settings.currentPassword')"
						:class="{'is-danger': errPasswordRequired}"
						:placeholder="$t('user.settings.currentPasswordPlaceholder')"
						type="password"
						:error="errPasswordRequired ? $t('user.deletion.passwordRequired') : null"
						@keyup="() => errPasswordRequired = password === ''"
					/>
				</form>
				<p v-else>
					{{ $t('user.deletion.text3') }}
				</p>

				<XButton
					:loading="accountDeleteService.loading"
					class="is-fullwidth mbs-4 is-danger"
					@click="deleteAccount()"
				>
					{{ $t('user.deletion.confirm') }}
				</XButton>
			</template>
		</template>
	</Card>
</template>

<script setup lang="ts">
import {ref, shallowReactive, computed, onMounted} from 'vue'
import {useI18n} from 'vue-i18n'

import AccountDeleteService from '@/services/accountDelete'
import {fetchSuccessorCandidates, eraseManagedAccount} from '@/services/accountErasure'
import {parseDateOrNull} from '@/helpers/parseDateOrNull'
import {formatDateSince, formatDisplayDate} from '@/helpers/time/formatDate'
import {useTitle} from '@/composables/useTitle'
import {success} from '@/message'
import {useAuthStore} from '@/stores/auth'
import {useConfigStore} from '@/stores/config'
import {useOrganizationStore} from '@/stores/organization'
import FormField from '@/components/input/FormField.vue'
import FormSelect, {type SelectOption} from '@/components/input/FormSelect.vue'
import Message from '@/components/misc/Message.vue'

defineOptions({name: 'UserSettingsDeletion'})

const {t} = useI18n({useScope: 'global'})
useTitle(() => `${t('user.deletion.title')} - ${t('user.settings.title')}`)

const accountDeleteService = shallowReactive(new AccountDeleteService())
const password = ref('')
const errPasswordRequired = ref(false)

const authStore = useAuthStore()
const configStore = useConfigStore()
const organizationStore = useOrganizationStore()

const userDeletionEnabled = computed(() => configStore.userDeletionEnabled)
const deletionScheduledAt = computed(() => parseDateOrNull(authStore.info?.deletionScheduledAt))

const isLocalUser = computed(() => authStore.info?.isLocalUser)

// Read out of the same JWT claim `useManagedCapabilities` already reads
// (BRA-1342) — null is the permissive, unmanaged case.
const isManaged = computed(() => authStore.managedEdition !== null)

const passwordInput = ref()

// --- Managed-account erasure (BRA-1404) ---

const successorCandidates = ref<{userId: string}[]>([])
const selectedSuccessor = ref<string | null>(null)
const errSuccessorRequired = ref(false)
const loadingCandidates = ref(false)
const erasing = ref(false)
const managedError = ref<string | null>(null)

const successorOptions = computed<SelectOption[]>(() => {
	// Names are resolved from the Organization area's own member list, loaded
	// alongside the candidate ids below — a picker showing bare numeric ids
	// would be correct and unusable. `String(...)`: the commercial service's ids are
	// this fork's own numeric user id, serialized as a string
	// (`adoptAccountUserId`), so the comparison has to cross that back.
	const members = organizationStore.organization?.members ?? []
	const named = successorCandidates.value.map(candidate => {
		const member = members.find(m => String(m.userId) === candidate.userId)
		return {value: candidate.userId, label: member?.name || member?.username || candidate.userId}
	})
	// A real, unselectable placeholder rather than pre-selecting the first
	// real candidate — silently defaulting to a name here would mean the
	// wrong person can inherit an organization because nobody looked.
	return [{value: '', label: t('user.deletion.managedSuccessorPlaceholder'), disabled: true}, ...named]
})

onMounted(async () => {
	if (!isManaged.value) return
	loadingCandidates.value = true
	try {
		const [candidates] = await Promise.all([
			fetchSuccessorCandidates(),
			// Loaded for its member list only (name resolution above); a 403 here
			// (a managed account that is not an administrator) just leaves that
			// list empty, which is fine — this account will never have a
			// non-empty candidate list of its own to name in the first place.
			organizationStore.load(),
		])
		successorCandidates.value = candidates
	} catch {
		managedError.value = t('user.deletion.managedCandidatesLoadFailed')
	} finally {
		loadingCandidates.value = false
	}
})

async function deleteManagedAccount() {
	if (successorCandidates.value.length > 0 && !selectedSuccessor.value) {
		errSuccessorRequired.value = true
		return
	}
	errSuccessorRequired.value = false
	managedError.value = null
	erasing.value = true
	try {
		await eraseManagedAccount(selectedSuccessor.value || null)
	} catch {
		managedError.value = t('user.deletion.managedDeleteFailed')
		erasing.value = false
		return
	}
	success({message: t('user.deletion.managedDeleteSuccess')})
	await authStore.logout()
}

// --- Community/self-hosted deletion (BRA-1367, unchanged) ---

async function deleteAccount() {
	if (isLocalUser.value && password.value === '') {
		errPasswordRequired.value = true
		passwordInput.value.focus()
		return
	}

	await accountDeleteService.request(password.value)
	success({message: t('user.deletion.requestSuccess')})
	password.value = ''
}

async function cancelDeletion() {
	if (isLocalUser.value && password.value === '') {
		errPasswordRequired.value = true
		passwordInput.value.focus()
		return
	}

	await accountDeleteService.cancel(password.value)
	success({message: t('user.deletion.scheduledCancelSuccess')})
	authStore.refreshUserInfo()
	password.value = ''
}
</script>
