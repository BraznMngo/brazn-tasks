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
					<!-- Replaces <Logo> with the ONE mark on no-auth screens only; drops CUSTOM_LOGO_URL white-labeling here, unlike AppHeader/Navigation. Decorative icon, name beside it is the accessible text. -->
					<div class="brand-header">
						<span
							class="brand-icon"
							aria-hidden="true"
						>
							<svg
								viewBox="0 0 40 40"
								fill="none"
							>
								<circle
									cx="20"
									cy="20"
									r="13"
									stroke="url(#brandIconGradient)"
									stroke-width="5"
								/>
								<defs>
									<linearGradient
										id="brandIconGradient"
										x1="4"
										y1="8"
										x2="36"
										y2="32"
									>
										<stop
											offset="0"
											stop-color="#2563eb"
										/>
										<stop
											offset="1"
											stop-color="#7c3aed"
										/>
									</linearGradient>
								</defs>
							</svg>
						</span>
						<div class="brand-text">
							<strong>{{ $t('misc.brandName') }}</strong>
							<span>{{ $t('misc.brandTagline') }}</span>
						</div>
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
const shouldShowApiConfig = computed(() => props.showApiConfig && (!isDesktop || hasStoredApiUrl))

const configStore = useConfigStore()
const motd = computed(() => configStore.motd)

const route = useRoute()
const { t } = useI18n({ useScope: 'global' })
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
	display: flex;
	align-items: center;
	gap: 0.75rem;
	margin-block-end: 1.75rem;
}

.brand-icon {
	inline-size: 44px;
	block-size: 44px;
	flex: none;
	border-radius: 14px;
	display: grid;
	place-items: center;
	background: var(--neumorphic-input-bg);
	// The same raised neumorphic treatment as .noauth-container, scaled down -
	// one visual language for every "soft surface" this screen introduces.
	box-shadow:
		4px 4px 10px var(--neumorphic-shadow-dark),
		-4px -4px 10px var(--neumorphic-shadow-light);

	svg {
		inline-size: 22px;
		block-size: 22px;
	}
}

.brand-text {
	display: grid;
	line-height: 1.3;

	strong {
		font-size: 0.95rem;
		font-weight: 700;
		color: var(--grey-800);
	}

	span {
		font-size: 0.75rem;
		color: var(--grey-500);
	}
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
