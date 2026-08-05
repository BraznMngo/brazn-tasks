<template>
	<Card
		v-if="managedMode || userDeletionEnabled"
		:title="$t('user.deletion.title')"
	>
		<!--
			Cancelling a scheduled deletion is deliberately available in both
			modes. `/user/deletion/cancel` is classified `ordinary` rather than
			service-managed, so this path is never the one that 403s, and it is
			left exactly as it was.
		-->
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

		<!--
			MANAGED MODE ERASES THROUGH THE COMMERCIAL SERVICE, AND THE LOCAL
			BUTTON WOULD 403.

			`/user/deletion/request` is classified `service-managed`, and that gate
			refuses everyone on the instance including its administrator, with the
			reason stated in the rule: a change made through this API would leave
			the service's view of the account behind. So the screen that was here
			drew a button which could not once have succeeded — the Rules §1
			violation the Organization area cites as its own reason for restraint.

			The commercial route is also the ENTRY POINT and not a mirror of the
			local one. It sends the account-deletion message before anything is
			destroyed and completes only if that send succeeded, then erases the
			subject in this fork. Deleting anything locally first would invert
			that order.
		-->
		<template v-else-if="managedMode">
			<!--
				The successor read answers 401, 500, 502 and 503 and never a 404,
				so a 404 is the route not being there rather than anything about
				this account - an instance whose commercial service has not been
				deployed this far yet. The local button is NOT the fallback: it is
				service-managed and would 403. So the screen says where deletion
				lives and offers nothing it cannot complete.
			-->
			<Message
				v-if="erasureUnavailable"
				variant="info"
			>
				<p class="has-text-weight-bold">
					{{ $t('user.deletion.erasure.unavailable.title') }}
				</p>
				<p>{{ $t('user.deletion.erasure.unavailable.text') }}</p>
				<XButton
					v-if="commercialUrl"
					variant="secondary"
					:href="commercialUrl"
				>
					{{ $t('user.deletion.erasure.unavailable.action') }}
				</XButton>
			</Message>

			<template v-else>
				<Message
					v-if="refusal"
					variant="danger"
				>
					{{ refusal }}
				</Message>

				<p>{{ $t('user.deletion.text1') }}</p>
				<p>{{ $t('user.deletion.erasure.text') }}</p>

				<!--
					Offered only when the service says a choice has to be made. An
					empty candidate list is a real answer covering three situations —
					not an administrator, the only member, or unknown to the service —
					and in every one of them erasure proceeds without a successor.
					Drawing an empty picker would invent a decision nobody has to make.
				-->
				<template v-if="candidates.length > 0">
					<p class="has-text-weight-bold mbs-4">
						{{ $t('user.deletion.successor.title') }}
					</p>
					<p>{{ $t('user.deletion.successor.text') }}</p>
					<div class="field">
						<div class="select is-fullwidth">
							<select
								v-model="successor"
								data-act="successor"
							>
								<option value="">
									{{ $t('user.deletion.successor.placeholder') }}
								</option>
								<option
									v-for="candidate in candidates"
									:key="candidate.userId"
									:value="candidate.userId"
								>
									{{ candidate.label }}
								</option>
							</select>
						</div>
					</div>
				</template>

				<XButton
					class="is-fullwidth mbs-4 is-danger"
					:loading="working"
					@click="confirmErasure()"
				>
					{{ $t('user.deletion.confirm') }}
				</XButton>
			</template>
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
	</Card>

	<Modal
		v-if="confirming"
		@close="confirming = false"
		@submit="eraseAccount()"
	>
		<template #header>
			<span>{{ $t('user.deletion.erasure.confirmTitle') }}</span>
		</template>

		<template #text>
			<p>{{ $t('user.deletion.erasure.confirmText') }}</p>
			<p v-if="successorLabel">
				{{ $t('user.deletion.erasure.confirmSuccessor', {name: successorLabel}) }}
			</p>
		</template>
	</Modal>
</template>

<script setup lang="ts">
import {ref, shallowReactive, computed, onMounted} from 'vue'
import {useI18n} from 'vue-i18n'

import AccountDeleteService from '@/services/accountDelete'
import {parseDateOrNull} from '@/helpers/parseDateOrNull'
import {formatDateSince, formatDisplayDate} from '@/helpers/time/formatDate'
import {useTitle} from '@/composables/useTitle'
import {success} from '@/message'
import {useAuthStore} from '@/stores/auth'
import {useConfigStore} from '@/stores/config'
import {useCommercialUrl} from '@/composables/useCommercialUrl'
import {useOrganizationStore} from '@/stores/organization'
import {AuthenticatedHTTPFactory, commercialV1Url} from '@/helpers/fetcher'
import FormField from '@/components/input/FormField.vue'
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
const commercialUrl = useCommercialUrl()

const userDeletionEnabled = computed(() => configStore.userDeletionEnabled)
const managedMode = computed(() => configStore.braznManagedMode)
const deletionScheduledAt = computed(() => parseDateOrNull(authStore.info?.deletionScheduledAt))

const isLocalUser = computed(() => authStore.info?.isLocalUser)

const passwordInput = ref()

const working = ref(false)
const refusal = ref('')
const confirming = ref(false)
const successor = ref('')
const candidates = ref<{userId: string, label: string}[]>([])
const erasureUnavailable = ref(false)

const successorLabel = computed(() =>
	candidates.value.find(candidate => candidate.userId === successor.value)?.label ?? '')

/**
 * The candidate list, joined to names this product holds.
 *
 * The commercial service answers user ids and nothing else — it carries no name
 * and no mailbox for anybody, which is the same ruling that makes erasure there
 * genuinely destroy an address rather than leave a copy behind. So the ids are
 * resolved against the organization roster HERE. An id with no matching member
 * is shown as the bare id rather than dropped: a candidate the picker hid would
 * be a person the administrator could not hand the organization to.
 */
async function loadCandidates() {
	if (!managedMode.value) {
		return
	}

	const HTTP = AuthenticatedHTTPFactory()
	try {
		const {data} = await HTTP.get(commercialV1Url('account/successor-candidates'))
		const ids: string[] = (data?.candidates ?? []).map((candidate: {user_id: string}) => candidate.user_id)
		if (ids.length === 0) {
			candidates.value = []
			return
		}

		// Only an administrator is ever offered a choice, and only an
		// administrator can read this, so the two conditions coincide.
		await organizationStore.load()
		const members = organizationStore.organization?.members ?? []
		candidates.value = ids.map(id => {
			const member = members.find(m => String(m.userId) === id)
			return {
				userId: id,
				label: member ? `${member.name || member.username} (${member.email})` : id,
			}
		})
	} catch (e) {
		candidates.value = []
		// See the template: a 404 here is the route's absence, not this account's
		// state, and it is the one failure that must change what is drawn.
		if ((e as {response?: {status?: number}})?.response?.status === 404) {
			erasureUnavailable.value = true
		}
		// Anything else is not fatal on its own: erasure itself refuses with a
		// 409 if a successor was required, so the worst case is that the person
		// is told to choose and the list is fetched again.
	}
}

onMounted(loadCandidates)

function confirmErasure() {
	if (working.value) {
		return
	}

	refusal.value = ''
	if (candidates.value.length > 0 && successor.value === '') {
		refusal.value = t('user.deletion.successor.required')
		return
	}
	confirming.value = true
}

async function eraseAccount() {
	confirming.value = false
	working.value = true
	refusal.value = ''

	const HTTP = AuthenticatedHTTPFactory()
	try {
		await HTTP.post(commercialV1Url('account/erasure'), {
			// Null and omitted mean the same thing to the service, deliberately,
			// so an unselected picker is not a malformed body. Sent explicitly so
			// the request has one shape rather than two.
			successor_user_id: successor.value === '' ? null : successor.value,
		})

		// 204 AND NO BODY. There is no account to come back to, so the session
		// goes with it rather than being left holding a token for somebody who
		// no longer exists.
		await authStore.logout()
	} catch (e) {
		const status = (e as {response?: {status?: number}})?.response?.status
		if (status === 409) {
			// ONE STATUS, TWO CAUSES, and the service does not distinguish them on
			// the wire: a successor is required and none was named, or the one
			// named is no longer eligible. Both are answered the same way — read
			// the list again and choose from what it says now.
			refusal.value = t('user.deletion.successor.required')
			successor.value = ''
			await loadCandidates()
		} else if (status === 404) {
			refusal.value = t('user.deletion.erasure.noAccount')
		} else {
			refusal.value = t('user.deletion.erasure.failed')
		}
	} finally {
		working.value = false
	}
}

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
