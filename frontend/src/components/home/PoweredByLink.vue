<template>
	<BaseButton
		class="menu-bottom-link"
		:href="computedUrl"
		target="_blank"
	>
		<!--
			Deliberately not translated. This is the AGPL modification notice and
			source offer, not product copy: it has to be present and accurate in
			every locale, and we cannot supply translations we have not had
			reviewed. Full notice, including the upstream link and the pinned
			upstream version, lives in NOTICE.md in the linked repository.

			BRA-1476 F8: the product customers see is ONE. Keep the Vikunja fork
			attribution; do not present "Brazn Tasks" as the product brand here.
		-->
		ONE — a modified fork of Vikunja. Source code.
	</BaseButton>
</template>

<script setup lang="ts">
import {computed} from 'vue'
import BaseButton from '@/components/base/BaseButton.vue'
import {POWERED_BY as poweredByUrl} from '@/urls'

const props = defineProps<{
	utmMedium: string;
}>()

const computedUrl = computed(() => `${poweredByUrl}&utm_medium=${props.utmMedium}`)
</script>

<style lang="scss">
.menu-bottom-link {
	color: var(--grey-300);
	text-align: center;
	display: block;
	padding-block-start: 1rem;
	padding-block-end: 1rem;
	font-size: .8rem;
}
</style>
