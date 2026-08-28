<script setup lang="ts">
import { computed } from 'vue'
import { useColorScheme } from '@/composables/useColorScheme'

const { isDark } = useColorScheme()

const CustomLogo = computed(() => {
	const lightLogo = window.CUSTOM_LOGO_URL
	const darkLogo = window.CUSTOM_LOGO_URL_DARK

	if (!lightLogo && !darkLogo) return ''
	if (!darkLogo) return lightLogo
	if (!lightLogo) return darkLogo

	return isDark.value ? darkLogo : lightLogo
})
</script>

<template>
	<div>
		<!--
			The real ONE logo, the same two files the sign-in screens, the loading
			splash and the ONE pages already use. What stood here was a placeholder
			that typed the letters "BT" in a rounded square using a system font,
			left behind when the upstream Vikunja artwork was removed for licence
			reasons (BRA-926). This is the signed-in header, the mobile sidebar and
			the page an anonymous link visitor lands on, so it was the last place a
			customer met a mark that is not the product's.

			Two images and not a <picture>: the palette is switched by a `.dark`
			class on the root as well as by the media query, and prefers-color-scheme
			alone cannot see that class. Same three-state selectors, and the same two
			files rather than a copy, as NoAuthWrapper and Ready, so these surfaces
			cannot drift apart.
		-->
		<template v-if="!CustomLogo">
			<img
				class="logo light"
				src="/one/logo-light.v1.png"
				width="155"
				height="72"
				:alt="$t('misc.brandName')"
			>
			<img
				class="logo dark"
				src="/one/logo-dark.v1.png"
				width="155"
				height="72"
				:alt="$t('misc.brandName')"
			>
		</template>
		<img
			v-show="CustomLogo"
			:src="CustomLogo"
			:alt="$t('misc.brandName')"
			class="logo custom"
		>
	</div>
</template>

<style lang="scss" scoped>
.logo {
	inline-size: auto;
	block-size: auto;
	max-inline-size: 168px;
	max-block-size: 48px;
}

// The theme-paired pair, same three-state selectors as NoAuthWrapper, Ready and
// frontend/public/one/one.css. `.custom` is deliberately outside these rules: a
// white-label logo is one image and has no light/dark twin.
.logo.dark {
	display: none;
}

@media screen {
	:root.dark .logo.light {
		display: none;
	}

	:root.dark .logo.dark {
		display: inline-block;
	}
}

@media screen and (prefers-color-scheme: dark) {
	:root:not(.light) .logo.light {
		display: none;
	}

	:root:not(.light) .logo.dark {
		display: inline-block;
	}
}
</style>
