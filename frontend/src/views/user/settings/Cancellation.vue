<template>
	<Card
		v-if="managedMode"
		:title="$t('user.cancellation.title')"
	>
		<Message
			v-if="refusal"
			variant="danger"
		>
			{{ refusal }}
		</Message>

		<Message
			v-if="cancelled !== null"
			variant="success"
		>
			<p class="has-text-weight-bold">
				{{ $t('user.cancellation.done.title') }}
			</p>
			<p>
				{{ $t('user.cancellation.done.text', {date: formatDisplayDate(cancelled)}) }}
			</p>
		</Message>

		<template v-else>
			<p>{{ $t('user.cancellation.text') }}</p>
			<p>{{ $t('user.cancellation.keeps') }}</p>

			<XButton
				class="is-fullwidth mbs-4 is-danger"
				data-act="cancel-subscription"
				:loading="working"
				@click="confirming = true"
			>
				{{ $t('user.cancellation.confirm') }}
			</XButton>
		</template>
	</Card>

	<Modal
		v-if="confirming"
		@close="confirming = false"
		@submit="cancel()"
	>
		<template #header>
			<span>{{ $t('user.cancellation.confirmTitle') }}</span>
		</template>

		<template #text>
			<p>{{ $t('user.cancellation.confirmText') }}</p>
		</template>
	</Modal>
</template>

<script setup lang="ts">
import {computed, ref} from 'vue'
import {useI18n} from 'vue-i18n'

import Message from '@/components/misc/Message.vue'
import {useTitle} from '@/composables/useTitle'
import {useConfigStore} from '@/stores/config'
import {formatDisplayDate} from '@/helpers/time/formatDate'
import {AuthenticatedHTTPFactory, commercialV1Url} from '@/helpers/fetcher'

defineOptions({name: 'UserSettingsCancellation'})

const {t} = useI18n({useScope: 'global'})
useTitle(() => `${t('user.cancellation.title')} - ${t('user.settings.title')}`)

const configStore = useConfigStore()
const managedMode = computed(() => configStore.braznManagedMode)

const working = ref(false)
const refusal = ref('')
const confirming = ref(false)
const cancelled = ref<Date | null>(null)

/**
 * ONE key for the life of this screen, not one per click.
 *
 * That is what makes it an idempotency key rather than a formality: a
 * double-click, or a retry after the answer was lost on the way back, has to
 * reach the service as the SAME request. The service answers a repeat with the
 * first cancellation's instants, so a second attempt cannot appear to move the
 * date — and it only cannot because the key did not change.
 */
const idempotencyKey = crypto.randomUUID()

async function cancel() {
	confirming.value = false
	if (working.value) {
		return
	}

	working.value = true
	refusal.value = ''

	const HTTP = AuthenticatedHTTPFactory()
	try {
		const {data} = await HTTP.post(commercialV1Url('subscription/cancellation'), {
			idempotency_key: idempotencyKey,
		})
		// `access_ends_at` is always the FIRST cancellation's date, so this is
		// what the customer keeps, not what this particular click bought.
		cancelled.value = new Date((data as {access_ends_at: string}).access_ends_at)
	} catch (e) {
		const status = (e as {response?: {status?: number}})?.response?.status
		if (status === 403) {
			// The one refusal here that names a different person's action. A
			// Teams member holds a seat their organization pays for, and
			// cancelling it is not theirs to do — before this arm existed, a
			// member reaching the same route cancelled the organization's seat.
			refusal.value = t('user.cancellation.teamsMember')
		} else if (status === 404) {
			refusal.value = t('user.cancellation.noSubscription')
		} else if (status === 409) {
			// THREE CAUSES, ONE STATUS, and the service does not tell them apart
			// on the wire: nothing to cancel, already closed, or no dated period
			// end — which is also what a customer still inside the free trial
			// gets, deliberately. Inventing a specific sentence here would be
			// guessing at which one, so this says what is true of all three.
			refusal.value = t('user.cancellation.notCancellable')
		} else {
			refusal.value = t('user.cancellation.failed')
		}
	} finally {
		working.value = false
	}
}
</script>
