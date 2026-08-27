<template>
	<div class="no-auth-wrapper">
		<div class="noauth-container">
			<section
				class="image"
				:class="{ 'has-message': motd !== '' }"
			>
				<Message v-if="motd !== ''">
					{{ motd }}
				</Message>
			</section>
			<main
				id="main-content"
				tabindex="-1"
				class="content"
			>
				<div>
					<!--
						The real ONE logo, which the settings and task pages have
						shown all along (frontend/public/one/settings.html). What
						stood here was a hand-drawn circle — a lone "O" — beside
						the product name in text, so the sign-in screens were the
						only place a customer met a mark that is not the mark
						(BRA-1444). Same two files rather than a copy, so the
						sign-in screens and the ONE pages cannot drift apart.

						Two images and not a <picture>: the palette is switched by
						a `.dark` class on the root as well as by the media query,
						and prefers-color-scheme alone cannot see that class. Same
						three-state selectors the ONE stylesheet uses, for the
						same reason.

						Drops CUSTOM_LOGO_URL white-labeling here, unlike
						AppHeader/Navigation.
					-->
					<div class="brand-header">
						<img
							class="brand-logo light"
							src="/one/logo-light.v1.png"
							width="155"
							height="72"
							:alt="$t('misc.brandName')"
						>
						<img
							class="brand-logo dark"
							src="/one/logo-dark.v1.png"
							width="155"
							height="72"
							:alt="$t('misc.brandName')"
						>
						<span class="brand-tagline">{{ $t('misc.brandTagline') }}</span>
					</div>
					<h1
						v-if="title"
						class="title"
					>
						{{ title }}
					</h1>
					<p
						v-if="subtitle"
						class="subtitle"
					>
						{{ subtitle }}
					</p>
					<ApiConfig v-if="shouldShowApiConfig" />
					<Message
						v-if="motd !== ''"
						class="is-hidden-tablet mbe-4"
					>
						{{ motd }}
					</Message>
					<slot />
				</div>
				<div>
					<!--
						Choosing a language before signing in. Everything on these
						screens is writing — what the form wants, why a refusal
						happened, where accounts come from — and before BRA-1444
						somebody who could not read it had no way to change it:
						the only language control in the product sits in settings,
						behind the sign-in they are stuck at.

						A plain <select> with a real <label>: it is one control on
						a page whose job is to get out of the way, it works with a
						keyboard and a screen reader without any code of ours, and
						it is what the ONE website's own footer uses.
					-->
					<div class="language-choice">
						<label
							class="language-label"
							for="no-auth-language"
						>{{ $t('user.settings.general.language') }}</label>
						<select
							id="no-auth-language"
							class="language-select"
							:value="currentLanguage"
							@change="onLanguageChange"
						>
							<option
								v-for="option in languageOptions"
								:key="option.code"
								:value="option.code"
							>
								{{ option.title }}
							</option>
						</select>
					</div>
					<Legal />
					<!--
						The AGPL section 13 source offer has to be reachable by anyone
						interacting with the instance over a network, which includes
						someone sitting at the login page who has no account. The
						sidebar and link-share placements only cover authenticated and
						shared views, so this is the third render site rather than a
						duplicate of them.
					-->
					<PoweredByLink utm-medium="no_auth" />
				</div>
			</main>
		</div>
	</div>
</template>

<script lang="ts" setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'

import Message from '@/components/misc/Message.vue'
import Legal from '@/components/misc/Legal.vue'
import ApiConfig from '@/components/misc/ApiConfig.vue'
import PoweredByLink from '@/components/home/PoweredByLink.vue'

import { useTitle } from '@/composables/useTitle'
import { useConfigStore } from '@/stores/config'
import { isDesktopApp } from '@/helpers/desktopAuth'
import {
	ONE_LAUNCH_LOCALES,
	SUPPORTED_LOCALES,
	saveLanguage,
	setLanguage,
	type SupportedLocale,
} from '@/i18n'

const props = withDefaults(
	defineProps<{
		showApiConfig?: boolean;
		showSubtitle?: boolean;
	}>(),
	{
		showApiConfig: false,
		showSubtitle: true,
	},
)

const isDesktop = isDesktopApp()
const hasStoredApiUrl = isDesktop && localStorage.getItem('API_URL') !== null

const configStore = useConfigStore()
const motd = computed(() => configStore.motd)

// Upstream's chooser for pointing this frontend at a different server — "Using
// the installation at …, change". It is meaningful on a self-hosted copy, where
// somebody really does run their own server and has to say where it is. On the
// hosted product there is exactly one server, nobody may point the app at
// another one, and the control offered a customer a choice that is not theirs
// to make (BRA-1444). Hidden rather than refused: this is a capability the
// person structurally does not have here, which Brazn-Tasks-Rules §1 renders
// not at all.
const shouldShowApiConfig = computed(() =>
	props.showApiConfig &&
	!configStore.braznManagedMode &&
	(!isDesktop || hasStoredApiUrl),
)

const route = useRoute()
const { t, locale } = useI18n({ useScope: 'global' })
const title = computed(() =>
	route.meta?.title ? t(route.meta.title as string) : '',
)
// Read from route meta rather than a prop, same as `title`: App.vue renders
// this wrapper around a bare <RouterView>, so a view never gets to pass one
// in directly. `showSubtitle` exists for Ready.vue, which renders this
// wrapper's slot for an unrelated API-connectivity error without navigating
// away from the current route, so route.meta's subtitle would otherwise leak
// in underneath that error.
const subtitle = computed(() =>
	props.showSubtitle && route.meta?.subtitle ? t(route.meta.subtitle as string) : '',
)
useTitle(() => title.value)

const currentLanguage = computed(() => locale.value)
const languageOptions = ONE_LAUNCH_LOCALES.map(code => ({
	code,
	title: SUPPORTED_LOCALES[code],
}))

async function onLanguageChange(event: Event) {
	const chosen = (event.target as HTMLSelectElement).value as SupportedLocale
	await setLanguage(chosen)
	// Remembered rather than merely applied: the very next thing that happens on
	// this screen is a reload — signing in, or leaving for checkout — and a
	// choice that does not survive it was never really offered.
	saveLanguage(chosen)
}
</script>

<style lang="scss" scoped>
// Neumorphic shadows need a background close in value to their own colour, so these tokens (consumed via var() from Login.vue's :deep() rules too) flip per-theme like --shadow-* does, rather than just inheriting --scheme-main.
.no-auth-wrapper {
	--neumorphic-surface: linear-gradient(160deg, hsl(220, 24%, 97%), hsl(230, 30%, 95%));
	--neumorphic-shadow-dark: hsla(228, 25%, 68%, 0.28);
	--neumorphic-shadow-light: hsla(0, 0%, 100%, 0.85);
	--neumorphic-input-bg: hsl(220, 24%, 97%);

	background: var(--neumorphic-surface);
	min-block-size: 100vh;
	display: flex;
	flex-direction: column;
	place-items: center;
	padding-block: 2rem;
}

@media screen {
	:root.dark .no-auth-wrapper {
		--neumorphic-surface: linear-gradient(160deg, hsl(224, 24%, 12%), hsl(228, 28%, 9%));
		--neumorphic-shadow-dark: hsla(0, 0%, 0%, 0.55);
		--neumorphic-shadow-light: hsla(224, 20%, 34%, 0.30);
		--neumorphic-input-bg: hsl(224, 20%, 16%);
	}
}
@media screen and (prefers-color-scheme: dark) {
	:root:not(.light) .no-auth-wrapper {
		--neumorphic-surface: linear-gradient(160deg, hsl(224, 24%, 12%), hsl(228, 28%, 9%));
		--neumorphic-shadow-dark: hsla(0, 0%, 0%, 0.55);
		--neumorphic-shadow-light: hsla(224, 20%, 34%, 0.30);
		--neumorphic-input-bg: hsl(224, 20%, 16%);
	}
}

.noauth-container {
	max-inline-size: $desktop;
	inline-size: 100%;
	min-block-size: 60vh;
	display: flex;
	background-color: var(--white);
	overflow: hidden;
	border-radius: 28px;
	// Neumorphic: a soft dark shadow to the bottom-right and a soft light
	// highlight to the top-left, on a background close to the shadow's own
	// tone - the pair is what reads as "raised" rather than merely "dropped".
	box-shadow:
		20px 20px 48px var(--neumorphic-shadow-dark),
		-20px -20px 48px var(--neumorphic-shadow-light);

	@media screen and (max-width: $desktop) {
		border-radius: 20px;
		margin-inline: 1rem;
	}
}

.image {
	inline-size: 50%;

	@media screen and (max-width: $tablet) {
		display: none;
	}

	@media screen and (min-width: $tablet) {
		// The artwork already carries the ONE wordmark, tagline and its own
		// vignette baked in (see comment on the asset import above), so unlike
		// the photo this replaces, no darkening overlay or caption is layered
		// on top - the image is the whole panel.
		background: url("@/assets/one-splash.jpg") no-repeat center/cover;
	}

	// Only reachable when an admin has set a message of the day - the one
	// piece of real content this panel can still carry.
	&.has-message {
		padding: 1.5rem;
		display: flex;
		align-items: flex-start;
	}
}

.content {
	display: flex;
	justify-content: space-between;
	flex-direction: column;
	padding: 2rem 2rem 1.5rem;

	@media screen and (max-width: $desktop) {
		inline-size: 100%;
		max-inline-size: 450px;
		margin-inline: auto;
	}

	@media screen and (min-width: $desktop) {
		inline-size: 50%;
	}
}

.brand-header {
	display: grid;
	justify-items: start;
	gap: 0.5rem;
	margin-block-end: 1.75rem;
}

.brand-logo {
	inline-size: 132px;
	block-size: auto;
	max-inline-size: 100%;
}

// The theme-paired pair: light is the default and dark is revealed by either
// the explicit `.dark` class or the media query, matching frontend/public/one/one.css.
.brand-logo.dark {
	display: none;
}

@media screen {
	:root.dark .brand-logo.light {
		display: none;
	}

	:root.dark .brand-logo.dark {
		display: inline-block;
	}
}

@media screen and (prefers-color-scheme: dark) {
	:root:not(.light) .brand-logo.light {
		display: none;
	}

	:root:not(.light) .brand-logo.dark {
		display: inline-block;
	}
}

.brand-tagline {
	font-size: 0.75rem;
	color: var(--grey-500);
	line-height: 1.3;
}

.language-choice {
	display: flex;
	align-items: center;
	justify-content: flex-end;
	gap: 0.5rem;
	margin-block-end: 0.75rem;
}

.language-label {
	font-size: 0.75rem;
	color: var(--grey-500);
}

// 44px minimum target size, the same floor Login.vue and Register.vue apply to
// their own controls.
.language-select {
	min-block-size: 2.75rem;
	padding-inline: 0.5rem;
	border: 1px solid var(--grey-200);
	border-radius: 8px;
	background: var(--neumorphic-input-bg);
	color: var(--grey-800);
	font-size: 0.85rem;
}

.subtitle {
	margin-block: -0.5rem 1.5rem;
	color: var(--grey-500);
	font-size: 0.9rem;
	line-height: 1.5;
}

// PoweredByLink is styled for the dark sidebar, where --grey-300 reads fine.
// On this white card it would be close to invisible, and a licence notice
// nobody can read does not discharge the obligation. Scoped styles reach a
// child component's root element, which is what .menu-bottom-link is.
.menu-bottom-link {
	color: var(--grey-500);
	padding-block: 0.5rem 0;
	text-align: end;
}
</style>
