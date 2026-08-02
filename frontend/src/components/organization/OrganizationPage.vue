<template>
	<Card
		:title="title"
		class="organization-page"
	>
		<Loading v-if="state === 'loading' || state === 'idle'" />

		<!--
			Three refusals, three wordings, and the differences are the point
			(Brazn Tasks Rules §1). "We cannot read your subscription" is
			temporary and says the rest of the product still works; "this page
			did not load" is ours and carries a reference; "correct when it
			loaded" is the fourth class — the view is out of date, and the one
			action that fixes it is a reload.

			None of them is a tooltip, a hover or a colour. Each is a heading, a
			reason and one action, which is what makes a refusal legible on a
			phone and to a screen reader.
		-->
		<Message
			v-else-if="state === 'stale'"
			variant="warning"
		>
			<p class="is-size-5 has-text-weight-bold">
				{{ $t('organization.stale.title') }}
			</p>
			<p>{{ $t('organization.stale.text') }}</p>
			<XButton
				variant="secondary"
				@click="reload"
			>
				{{ $t('organization.stale.action') }}
			</XButton>
		</Message>

		<Message
			v-else-if="state === 'unavailable'"
			variant="warning"
		>
			<p class="is-size-5 has-text-weight-bold">
				{{ $t('organization.unavailable.title') }}
			</p>
			<p>{{ $t('organization.unavailable.text') }}</p>
			<XButton
				variant="secondary"
				@click="retry"
			>
				{{ $t('organization.retry') }}
			</XButton>
		</Message>

		<Message
			v-else-if="state === 'error'"
			variant="danger"
		>
			<p class="is-size-5 has-text-weight-bold">
				{{ $t('organization.error.title') }}
			</p>
			<p>{{ $t('organization.error.text') }}</p>
			<XButton
				variant="secondary"
				@click="retry"
			>
				{{ $t('organization.retry') }}
			</XButton>
		</Message>

		<slot v-else />
	</Card>
</template>

<script setup lang="ts">
import {computed} from 'vue'

import Card from '@/components/misc/Card.vue'
import Loading from '@/components/misc/Loading.vue'
import Message from '@/components/misc/Message.vue'
import XButton from '@/components/input/Button.vue'

import {useOrganizationStore} from '@/stores/organization'

// One wrapper for all seven pages, so a state is worded once and cannot drift
// between them. A page renders its own content only in `ready`; every other
// state is this component's.
defineProps<{
	title: string
}>()

const organizationStore = useOrganizationStore()

const state = computed(() => organizationStore.state)

function retry() {
	return organizationStore.load()
}

function reload() {
	window.location.reload()
}
</script>

<style lang="scss" scoped>
// German is the constraint here, not English: Organisationsadministrator is 26
// characters in a chip at the end of a member row, and an ellipsis would hide
// the distinction the row exists to carry. Wrapping is the answer, at every
// width (BRA-921 §5).
.organization-page :deep(dd),
.organization-page :deep(td) {
	overflow-wrap: anywhere;
}
</style>
