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
			<Message variant="success">
				{{ $t('user.auth.passwordResetSuccess') }}
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
			id="form"
			@submit.prevent="resetPassword"
		>
			<ErrorSummary :errors="fieldErrors" />
			<div class="field">
				<label
					class="label"
					for="password"
				>{{ $t('user.auth.password') }}</label>
				<Password
					:validate-initially="validatePasswordInitially"
					autocomplete="new-password"
					described-by="password-hint"
					@submit="resetPassword"
					@update:modelValue="v => credentials.password = v"
				/>
				<!--
					The minimum is stated before it can be broken, and the error
					string quotes the same number.
				-->
				<p
					id="password-hint"
					class="field-hint"
				>
					{{ $t('user.auth.passwordMinHint') }}
				</p>
			</div>

			<XButton
				class="is-fullwidth"
				:loading="passwordResetService.loading"
				:aria-busy="passwordResetService.loading"
				:aria-disabled="passwordResetService.loading"
				@click="resetPassword"
			>
				{{ $t('user.auth.resetPassword') }}
			</XButton>
		</form>
	</div>
</template>

<script setup lang="ts">
import {computed, ref, reactive} from 'vue'
import {useRoute} from 'vue-router'
import {useI18n} from 'vue-i18n'

import PasswordResetModel from '@/models/passwordReset'
import PasswordResetService from '@/services/passwordReset'
import Message from '@/components/misc/Message.vue'
import Password from '@/components/input/Password.vue'
import ErrorSummary from '@/components/misc/ErrorSummary.vue'
import type {IFieldError} from '@/types/IFieldError'
import {validatePassword} from '@/helpers/validatePasswort'
import {getErrorText} from '@/message'

const credentials = reactive({
	password: '',
})

const route = useRoute()
const {t} = useI18n()

const passwordResetService = reactive(new PasswordResetService())
const errorMsg = ref('')
const isSuccess = ref(false)
const validatePasswordInitially = ref(false)

const passwordSummaryError = computed(() => {
	if (!validatePasswordInitially.value) {
		return null
	}
	const valid = validatePassword(credentials.password)
	return valid === true ? null : t(valid)
})

const fieldErrors = computed<IFieldError[]>(() => passwordSummaryError.value === null
	? []
	: [{target: 'password', label: t('user.auth.password'), message: passwordSummaryError.value}],
)

async function resetPassword() {
	errorMsg.value = ''
	validatePasswordInitially.value = true
	const token = route.query.userPasswordReset as string

	if (!token) {
		errorMsg.value = t('user.auth.passwordResetTokenMissing')
		return
	}

	if (validatePassword(credentials.password) !== true) {
		return
	}

	const passwordReset = new PasswordResetModel({newPassword: credentials.password, token: token})
	try {
		await passwordResetService.resetPassword(passwordReset)
		// This screen's own sentence rather than the server's, so the way out is
		// the same one every other success on this path offers: sign in.
		isSuccess.value = true
	} catch (e) {
		errorMsg.value = getErrorText(e)
	}
}
</script>

<style scoped>
.field-hint {
	margin-block: -0.5rem 0.75rem;
	color: var(--grey-500);
	font-size: 0.85rem;
}

/* 44px minimum target size (Percy-Account-Path.md §5). */
:deep(.input),
:deep(.button) {
	min-block-size: 2.75rem;
}

:deep(.password-field-type-toggle) {
	display: inline-flex;
	align-items: center;
	justify-content: center;
	min-block-size: 2.75rem;
	min-inline-size: 2.75rem;
}
</style>
