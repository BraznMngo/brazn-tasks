<template>
	<div>
		<h1 class="confirm-heading">
			{{ $t(`user.confirm.${state}.heading`) }}
		</h1>

		<!-- Green, not amber, for a link that was already used. A second click
		     is not a failure, and presenting it as one makes people think they
		     broke something. -->
		<Message
			v-if="state === 'confirmed' || state === 'alreadyUsed'"
			variant="success"
			class="mbe-4"
		>
			{{ $t(`user.confirm.${state}.body`) }}
		</Message>
		<Message
			v-else-if="state === 'expired' || state === 'unreadable'"
			variant="warning"
			class="mbe-4"
		>
			{{ $t(`user.confirm.${state}.body`) }}
		</Message>
		<p
			v-else-if="state === 'inbox'"
			class="mbe-4"
		>
			{{ inboxBody }}
		</p>
		<p
			v-else
			class="mbe-4"
		>
			{{ $t('user.confirm.verifying.body') }}
		</p>

		<template v-if="state === 'inbox'">
			<p class="mbe-2">
				{{ $t('user.confirm.inbox.spam') }}
			</p>
			<p class="mbe-4">
				{{ $t('user.confirm.inbox.lifetime') }}
			</p>
			<p class="mbe-4">
				{{ $t('user.confirm.inbox.addressMoves') }}
			</p>
		</template>

		<!-- A live region, not a new page: they are watching an inbox and must
		     not lose their place. -->
		<div
			role="status"
			aria-live="polite"
			class="resend-status"
		>
			<Message
				v-if="resendNotice !== ''"
				variant="info"
			>
				{{ resendNotice }}
			</Message>
		</div>

		<form
			v-if="needsAddress"
			class="mbs-4"
			@submit.prevent="resend"
		>
			<FormField
				id="confirm-email"
				v-model="address"
				v-focus
				:label="$t('user.auth.email')"
				name="email"
				type="email"
				autocomplete="email"
				required
				:error="addressError"
			/>
			<XButton
				id="confirm-resend"
				:loading="isSending"
				:disabled="isSending"
				@click="resend"
			>
				{{ $t('user.confirm.sendNewLink') }}
			</XButton>
		</form>

		<div
			v-else-if="state === 'inbox'"
			class="mbs-4 confirm-actions"
		>
			<XButton
				id="confirm-resend"
				:loading="isSending"
				:disabled="isSending"
				@click="resend"
			>
				{{ $t('user.confirm.sendAgain') }}
			</XButton>
			<button
				type="button"
				class="link-button"
				@click="askForAddress"
			>
				{{ $t('user.confirm.addressIsWrong') }}
			</button>
		</div>

		<div
			v-if="state === 'confirmed' || state === 'alreadyUsed'"
			id="confirm-sign-in"
			class="mbs-4"
		>
			<XButton :to="{name: 'user.login'}">
				{{ $t('user.auth.login') }}
			</XButton>
		</div>
	</div>
</template>

<script setup lang="ts">
import {computed, onBeforeMount, ref} from 'vue'
import {useI18n} from 'vue-i18n'
import {useRoute} from 'vue-router'

import Message from '@/components/misc/Message.vue'
import FormField from '@/components/input/FormField.vue'
import XButton from '@/components/input/Button.vue'
import {HTTPFactory} from '@/helpers/fetcher'
import {isEmail} from '@/helpers/isEmail'
import {
	forgetPendingConfirmation,
	getPendingConfirmation,
	looksLikeConfirmToken,
} from '@/helpers/emailConfirmation'

// The six states docs/Percy-Account-Path.md §3 designs, plus the moment the
// request is in flight. 'resent' is deliberately NOT one of them: a resend is a
// live region on the state you are already looking at, not a new page.
type ConfirmState =
	| 'verifying'
	| 'inbox'
	| 'confirmed'
	| 'alreadyUsed'
	| 'expired'
	| 'unreadable'

// The two answers the server gives for a link it will not accept. They are
// separate codes because they are separate sentences: one is recoverable by
// asking for another link, the other usually means the mail client broke it.
const ERROR_INVALID_CONFIRM_TOKEN = 1010
const ERROR_EXPIRED_CONFIRM_TOKEN = 1035

// Long enough that a second press is a considered one rather than an impatient
// one, short enough that somebody whose mail really did not arrive is not stuck.
const RESEND_COOLDOWN_MS = 60 * 1000

const {t} = useI18n()
const route = useRoute()

const state = ref<ConfirmState>('inbox')
const address = ref('')
const addressError = ref<string | null>(null)
const resendNotice = ref('')
const isSending = ref(false)
const askingForAddress = ref(false)
let lastResendAt = 0

// The states that cannot go anywhere without an address. Nobody reaching them
// necessarily has the tab that registered, because a confirmation link is
// usually opened on a different device from the form - and the inbox state is
// one of them whenever this tab does not know the address, because "send it
// again" with nothing to send it to would report success and do nothing.
const needsAddress = computed(() =>
	askingForAddress.value ||
	state.value === 'expired' ||
	state.value === 'unreadable' ||
	(state.value === 'inbox' && address.value === ''),
)

// The address is quoted back whenever this tab knows it, because a typo in that
// field is otherwise invisible forever. It usually does not: a confirmation
// link is normally opened on a different device from the one the form was
// filled in on, and the sentence has to work either way.
const inboxBody = computed(() => address.value !== ''
	? t('user.confirm.inbox.bodyWithAddress', {email: address.value})
	: t('user.confirm.inbox.body'),
)

onBeforeMount(() => {
	address.value = getPendingConfirmation()

	const token = (route.query.userEmailConfirm as string | undefined) ?? ''
	if (token === '') {
		state.value = 'inbox'
		return
	}

	// The token has done its job the moment it is read. Take it out of the
	// address bar and out of history without reloading, so it is not left
	// sitting in a shared browser or copied out of a screenshot.
	window.history.replaceState(
		window.history.state,
		'',
		window.location.pathname,
	)

	if (!looksLikeConfirmToken(token)) {
		state.value = 'unreadable'
		return
	}

	verifyToken(token)
})

async function verifyToken(token: string) {
	state.value = 'verifying'
	try {
		const response = await HTTPFactory().post('user/confirm', {token})
		state.value = response.data?.already_confirmed === true
			? 'alreadyUsed'
			: 'confirmed'
		forgetPendingConfirmation()
	} catch (e) {
		const code = e?.response?.data?.code
		if (code === ERROR_EXPIRED_CONFIRM_TOKEN) {
			state.value = 'expired'
			return
		}
		if (code === ERROR_INVALID_CONFIRM_TOKEN) {
			state.value = 'unreadable'
			return
		}
		// Anything else is this instance having a bad day rather than a
		// judgement on the link, so the person is not told their link is wrong.
		state.value = 'inbox'
		resendNotice.value = t('user.confirm.somethingWentWrong')
	}
}

function askForAddress() {
	askingForAddress.value = true
	resendNotice.value = ''
}

async function resend() {
	addressError.value = null

	if (needsAddress.value) {
		if (!isEmail(address.value)) {
			addressError.value = t('user.auth.emailInvalid')
			return
		}
	}

	const sinceLast = Date.now() - lastResendAt
	if (lastResendAt !== 0 && sinceLast < RESEND_COOLDOWN_MS) {
		// Says so rather than silently doing nothing. A control that appears to
		// do nothing gets pressed again, and again.
		resendNotice.value = t('user.confirm.resendCooldown', {
			seconds: Math.ceil((RESEND_COOLDOWN_MS - sinceLast) / 1000),
		})
		return
	}

	isSending.value = true
	try {
		await HTTPFactory().post('user/confirm/resend', {email: address.value})
	} catch {
		// The endpoint answers the same for every address, so there is nothing
		// a failure here could tell the customer that is both true and useful.
		// They are told a link is on its way either way, which is the same
		// sentence a successful send gets - see the endpoint's own comment.
	} finally {
		isSending.value = false
	}

	lastResendAt = Date.now()
	askingForAddress.value = false
	state.value = 'inbox'
	// The same words whether or not that address has an account waiting. A
	// different answer per address would turn this screen into a way of finding
	// out who is a customer.
	resendNotice.value = t('user.confirm.resent')
}
</script>

<style lang="scss" scoped>
.confirm-heading {
	font-size: 1.5rem;
	font-weight: 700;
	margin-block-end: 1rem;
}

.resend-status:empty {
	display: none;
}

.confirm-actions {
	display: flex;
	flex-wrap: wrap;
	gap: .5rem;
	align-items: center;
}

// 44px, per docs/Percy-Account-Path.md §5. Applied here rather than globally:
// this ticket owns these screens and not the rest of the product.
:deep(.button),
:deep(.input),
.link-button {
	min-block-size: 2.75rem;
}

.link-button {
	background: none;
	border: none;
	padding-block: 0;
	padding-inline: .5rem;
	color: var(--link, #485fc7);
	text-decoration: underline;
	cursor: pointer;
	font-size: 1rem;
}
</style>
