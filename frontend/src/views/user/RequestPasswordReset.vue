<template>
	<div>
		<Message
			v-if="errorMsg"
			variant="danger"
			class="mbe-4"
		>
			{{ errorMsg }}
		</Message>
		<div
			v-if="isSuccess"
			class="has-text-centered mbe-4"
		>
			<!--
				NO ENUMERATION. This sentence is the reply to every address, and
				it is this component's own string rather than anything the server
				sent back: an address with an account and an address without one
				produce the same words, in the same place, with nothing in
				between to compare. The server side of the same rule is in
				pkg/routes/api/shared/auth.go's RequestPasswordResetToken, which
				answers both with the same status and the same body.
			-->
			<Message variant="success">
				{{ $t('user.auth.resetPasswordSuccess') }}
			</Message>
			<XButton
				:to="{ name: 'user.login' }"
				class="mbs-4"
			>
				{{ $t('user.auth.login') }}
			</XButton>
		</div>
		<form
			v-if="!isSuccess"
			@submit.prevent="requestPasswordReset"
		>
			<ErrorSummary :errors="fieldErrors" />
			<FormField
				id="email"
				v-model="passwordReset.email"
				v-focus
				:label="$t('user.auth.email')"
				name="email"
				:placeholder="$t('user.auth.emailPlaceholder')"
				required
				type="email"
				autocomplete="email"
				:error="emailError"
				@keyup="handleEmailKeyup"
				@focusout="validateEmail(); emailTouched = true"
			/>

			<div class="actions">
				<XButton
					type="submit"
					:loading="passwordResetService.loading"
					:aria-busy="passwordResetService.loading"
					:aria-disabled="passwordResetService.loading"
				>
					{{ $t('user.auth.resetPasswordAction') }}
				</XButton>
				<XButton
					:to="{ name: 'user.login' }"
					variant="secondary"
				>
					{{ $t('user.auth.login') }}
				</XButton>
			</div>
		</form>
	</div>
</template>

<script setup lang="ts">
import {computed, ref, shallowReactive} from 'vue'
import {useI18n} from 'vue-i18n'

import PasswordResetModel from '@/models/passwordReset'
import PasswordResetService from '@/services/passwordReset'
import Message from '@/components/misc/Message.vue'
import FormField from '@/components/input/FormField.vue'
import ErrorSummary from '@/components/misc/ErrorSummary.vue'
import type {IFieldError} from '@/types/IFieldError'
import {isEmail} from '@/helpers/isEmail'
import {getErrorText} from '@/message'

const {t} = useI18n()

const passwordResetService = shallowReactive(new PasswordResetService())
const passwordReset = ref(new PasswordResetModel())
const errorMsg = ref('')
const isSuccess = ref(false)
const emailTouched = ref(false)
const emailValid = ref(true)

function validateEmail() {
	emailValid.value = passwordReset.value.email !== '' && isEmail(passwordReset.value.email)
}

// Only re-check while typing once the field has been left or submitted, so a
// half-typed address is not called invalid at the third character.
function handleEmailKeyup() {
	if (emailTouched.value) {
		validateEmail()
	}
}

const emailError = computed(() => emailValid.value ? null : t('user.auth.emailInvalid'))

const fieldErrors = computed<IFieldError[]>(() => emailValid.value
	? []
	: [{target: 'email', label: t('user.auth.email'), message: t('user.auth.emailInvalid')}],
)

async function requestPasswordReset() {
	errorMsg.value = ''
	emailTouched.value = true
	validateEmail()

	if (!emailValid.value) {
		return
	}

	try {
		// The response is deliberately discarded. It is the same for an address
		// with an account and an address without one, and rendering it would
		// make this screen depend on that staying true forever.
		await passwordResetService.requestResetPassword(passwordReset.value)
		isSuccess.value = true
	} catch (e) {
		errorMsg.value = getErrorText(e)
	}
}
</script>

<style scoped>
.actions {
	display: flex;
	flex-wrap: wrap;
	gap: 0.4rem;
}

/* 44px minimum target size (Percy-Account-Path.md §5). */
:deep(.input),
:deep(.button) {
	min-block-size: 2.75rem;
}
</style>
