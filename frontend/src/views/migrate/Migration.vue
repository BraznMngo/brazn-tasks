<template>
	<div class="content">
		<h1>{{ $t('migrate.title') }}</h1>
		<p>{{ $t('migrate.description') }}</p>
		<div class="migration-services">
			<RouterLink
				v-for="migrator in availableMigrators"
				:key="migrator.id"
				class="migration-service-link"
				:to="migrator.isCSVMigrator ? {name: 'migrate.csv'} : {name: 'migrate.service', params: {service: migrator.id}}"
			>
				<img
					class="migration-service-image"
					:alt="migratorName(migrator, t)"
					:src="migrator.icon"
				>
				{{ migratorName(migrator, t) }}
			</RouterLink>
		</div>
	</div>
</template>

<script setup lang="ts">
import {computed} from 'vue'
import {useI18n} from 'vue-i18n'

import {MIGRATORS, migratorName} from './migrators'
import {useTitle} from '@/composables/useTitle'
import {useConfigStore} from '@/stores/config'

const {t} = useI18n({useScope: 'global'})

useTitle(() => t('migrate.title'))

const configStore = useConfigStore()
const availableMigrators = computed(() => configStore.availableMigrators
	.map((id) => MIGRATORS[id])
	.filter((item) => Boolean(item)),
)
</script>

<style lang="scss" scoped>
.migration-services {
  text-align: center;
}

.migration-service-link {
    display: inline-flex;
    flex-direction: column;
    align-items: center;
    justify-content: flex-end;
    inline-size: 100px;
    text-transform: capitalize;
    margin-inline-end: 1rem;
}

.migration-service-image {
	display: block;
	max-block-size: 80px;
	inline-size: auto;
	margin-block-end: 0.5rem;
}
</style>
