import wunderlistIcon from './icons/wunderlist.jpg'
import todoistIcon from './icons/todoist.svg?url'
import trelloIcon from './icons/trello.svg?url'
import microsoftTodoIcon from './icons/microsoft-todo.svg?url'
import vikunjaFileIcon from './icons/vikunja-file.png?url'
import tickTickIcon from './icons/ticktick.svg?url'
import wekanIcon from './icons/wekan.png?url'
import csvIcon from './icons/csv.svg?url'

export interface Migrator {
	id: string
	name: string
	/**
	 * Translation key that overrides `name` when set. Every other migrator names
	 * a third-party product, which is a proper noun and stays literal; only this
	 * fork's own entry needs wording that can change, so it lives in the string
	 * catalogue and a later edit is a catalogue change, not a code change.
	 */
	nameKey?: string
	isFileMigrator?: boolean
	isCSVMigrator?: boolean
	icon: string
}

/** Resolves a migrator's display name, preferring its catalogue entry. */
export function migratorName(
	migrator: Migrator,
	t: (key: string) => string,
): string {
	return migrator.nameKey ? t(migrator.nameKey) : migrator.name
}

interface IMigratorRecord {
	[key: Migrator['id']]: Migrator
 }

export const MIGRATORS = {
	wunderlist: {
		id: 'wunderlist',
		name: 'Wunderlist',
		icon: wunderlistIcon,
	},
	todoist: {
		id: 'todoist',
		name: 'Todoist',
		icon: todoistIcon as string,
	},
	trello: {
		id: 'trello',
		name: 'Trello',
		icon: trelloIcon as string,
	},
	'microsoft-todo': {
		id: 'microsoft-todo',
		name: 'Microsoft Todo',
		icon: microsoftTodoIcon as string,
	},
	// The id stays `vikunja-file`: it is the wire value the API expects and the
	// route segment. Only the label changes, and it names both formats because
	// the importer genuinely reads upstream Vikunja exports as well as ours.
	'vikunja-file': {
		id: 'vikunja-file',
		name: 'ONE / Vikunja export',
		nameKey: 'migrate.migrators.vikunjaFile',
		icon: vikunjaFileIcon,
		isFileMigrator: true,
	},
	ticktick: {
		id: 'ticktick',
		name: 'TickTick',
		icon: tickTickIcon as string,
		isFileMigrator: true,
	},
	wekan: {
		id: 'wekan',
		name: 'WeKan ®',
		icon: wekanIcon,
		isFileMigrator: true,
	},
	csv: {
		id: 'csv',
		name: 'CSV',
		icon: csvIcon as string,
		isFileMigrator: true,
		isCSVMigrator: true,
	},
} as const satisfies IMigratorRecord
