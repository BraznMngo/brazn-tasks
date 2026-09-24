<template>
	<div
		class="content loader-container is-max-width-desktop"
		:class="{ 'is-loading': teamService.loading}"
	>
		<XButton
			v-if="capabilities.teamCreate"
			:to="{name:'teams.create'}"
			class="is-pulled-end"
			icon="plus"
		>
			{{ $t('team.create.title') }}
		</XButton>

		<h1>{{ surfaceTitle }}</h1>
		<Card
			v-if="teams.length > 0"
			:padding="false"
			:has-content="false"
		>
			<ul class="teams">
				<li
					v-for="team in teams"
					:key="team.id"
				>
					<RouterLink :to="{name: 'teams.edit', params: {id: team.id}}">
						<p>
							{{ team.name }}
						</p>
					</RouterLink>
				</li>
			</ul>
		</Card>
		<p
			v-else-if="!teamService.loading"
			class="has-text-centered has-text-grey is-italic"
		>
			{{ $t('team.noTeams') }}
			<template v-if="capabilities.teamCreate">
				<RouterLink :to="{name: 'teams.create'}">
					{{ $t('team.create.title') }}.
				</RouterLink>
			</template>
		</p>
	</div>
</template>

<script setup lang="ts">
import {computed, ref, shallowReactive} from 'vue'
import {useI18n} from 'vue-i18n'
import {useRouter} from 'vue-router'

import Card from '@/components/misc/Card.vue'
import TeamService from '@/services/team'
import {useTitle} from '@/composables/useTitle'
import {
	COLLABORATION_EDITION,
	collaborationDetailTeamId,
	useManagedCapabilities,
} from '@/composables/useManagedCapabilities'
import {useAuthStore} from '@/stores/auth'

const {t} = useI18n({useScope: 'global'})
const router = useRouter()
const authStore = useAuthStore()
const {capabilities} = useManagedCapabilities()

const isCollaboration = computed(() => authStore.managedEdition === COLLABORATION_EDITION)
const surfaceTitle = computed(() =>
	isCollaboration.value ? t('team.collaboratorsTitle') : t('team.title'),
)
useTitle(() => surfaceTitle.value)

const teams = ref<{id: number, name: string}[]>([])
const teamService = shallowReactive(new TeamService())
teamService.getAll().then((result) => {
	const detailId = collaborationDetailTeamId(authStore.managedEdition, result)
	if (detailId !== null) {
		router.replace({name: 'teams.edit', params: {id: detailId}})
		return
	}
	teams.value = result
})
</script>

<style lang="scss" scoped>
ul.teams {
  padding: 0;
  margin-block-start: 0;
  margin-inline-start: 0;
  border-radius: $radius;
  overflow: hidden;

  li {
    list-style: none;
    margin: 0;
    border-inline-end: 1px solid var(--grey-200);

    a {
      color: var(--text);
      display: block;
      padding: 0.5rem 1rem;
      transition: background-color $transition;

      &:hover {
        background: var(--white);
        transition: background-color $transition;
      }
    }

    &:not(:last-child) {
      border-block-end: 1px solid var(--grey-200);
    }
  }
}
</style>
