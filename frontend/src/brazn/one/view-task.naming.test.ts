import {describe, it, expect, beforeAll, beforeEach, afterEach, vi} from 'vitest'

import * as api from '../../../public/one/api.js'
import {init as initI18n} from '../../../public/one/i18n.js'
import {boot, identityBlock, setViewState} from '../../../public/one/app.js'
import type {GateFacts, ViewContext} from '../../../public/one/app.js'
import {mount, render} from '../../../public/one/view-task.js'

import taskHtml from '../../../public/one/task.html?raw'
import enRaw from '../../../public/one/i18n/en.json?raw'

/*
 * EVERY PLACE THE TASK PAGE PRINTS A PROJECT NAME, asserted at the place that prints it.
 *
 * The stored title of the project every account gets on registration is the English word
 * "Inbox", and no customer may ever read it - `projectTitle` in i18n.js turns it into the
 * translated "Your Tasks". BRA-1414's reviewer found the gap this file closes, and the finding is
 * worth restating because it is what every assertion below is shaped by: the helper had four
 * tests and not one of them was a test OF A PRINT SITE. Reverting either call site to
 * `project.title` left the whole suite green, so the helper was guarded and the page was not.
 *
 * THE GUARD IS THE CALL. So each case here drives the SHIPPED CONTROL and reads the SHIPPED
 * MARKUP, and none of them calls `projectTitle` at all:
 *
 *   view-task.js  the project chip in the task header       - `render()`
 *   view-task.js  the destination picker in Move task       - clicking the chip
 *   view-task.js  the scope line under the relation search  - clicking Add relation
 *   view-task.js  that scope line again, after a re-type    - typing over a chosen task
 *   app.js        the project picker in Add task            - clicking the header's + button
 *
 * Each asserts BOTH halves: the page says "Your Tasks", and the page does not say "Inbox". The
 * negative is the half that catches a site printing the column beside a helper that was called
 * for something else, and the fixtures below put the word "Inbox" nowhere except the stored
 * title, so a match can only have come from the print under test.
 *
 * WHAT DRIVES THE CLICKS. `app.js` owns the only delegated click listener on the page and
 * installs it in `boot()`, before `boot` awaits anything - so `boot()` against a refused session
 * gives a real dispatcher without a real page. The sign-in hand-off is disarmed through its own
 * marker, which is what stops the module reaching for `location.assign` (`shouldHandOffToLogin`).
 */

const ORIGIN = 'https://dev.tasks.brazn.one'

// The shipped shell, minus its own module tag: app.js is imported above and leaving the tag in
// would ask the document to fetch and evaluate a second copy of it. Same treatment, and the same
// reason, as `app.dom.test.ts`.
const SHELL = (/<body>([\s\S]*)<\/body>/.exec(taskHtml) ?? ['', ''])[1]
	.replace(/<script[\s\S]*?<\/script>/g, '')

// `sessionStorage` key from app.js. A marker that is already present means "the hand-off has
// been tried and came back", which is the one state in which boot refuses to navigate.
const LOGIN_HANDOFF_MARKER = 'brazn.one.login-handoff'

const FACTS: GateFacts = {
	hasEdition: true,
	personalEdition: false,
	orgAdmin: true,
	writeRestricted: false,
	teams: {'7': {readable: true, admin: true}},
}

const TASK_ID = 12

const CTX: ViewContext = {route: {taskId: TASK_ID, view: 'task', tab: 'account'}, facts: FACTS}

// The project as the SERVER stores it. `title: 'Inbox'` is not a fixture choice - it is the
// literal `models.InboxProjectTitle` that registration writes into the column for every account,
// and printing it is the defect.
const INBOX_PROJECT = {id: 4, title: 'Inbox'}

/**
 * A task sitting in that project, and deliberately dull everywhere else: no title, identifier,
 * comment or bucket carries the word this file is hunting for, so `not.toContain('Inbox')` can
 * only be about the project name.
 */
function seedTaskState(patch: Record<string, unknown> = {}): void {
	setViewState('task', {
		taskId: TASK_ID,
		status: 'ready',
		task: {
			id: TASK_ID,
			identifier: 'PRJ-12',
			title: 'A task somebody filed',
			project_id: INBOX_PROJECT.id,
			done: false,
			percent_done: 0,
			priority: 0,
			labels: [],
			assignees: [],
			attachments: [],
			related_tasks: {},
			buckets: [],
			created_by: {username: 'user1', name: 'A Customer'},
		},
		comments: [],
		projects: [INBOX_PROJECT],
		projectUsers: [],
		error: null,
		commentDraft: '',
		editingCommentId: null,
		commentEditDraft: '',
		dueBeforeLock: null,
		commentOrder: 'asc',
		resourceTab: 'attachments',
		scheduleOpen: false,
		...patch,
	})
}

/** Put a render on the page, so the controls that are clicked below are the shipped ones. */
function paintTask(patch: Record<string, unknown> = {}): void {
	seedTaskState(patch)
	const app = document.getElementById('app')
	if (app === null) throw new Error('the shell has no #app')
	app.innerHTML = render(CTX)
}

function modalMarkup(): string {
	return document.getElementById('modalRoot')?.innerHTML ?? ''
}

function click(selector: string): void {
	const el = document.querySelector(selector)
	if (el === null) throw new Error(`no shipped control matches ${selector}`)
	;(el as HTMLElement).click()
}

// One fetch for the whole file. The refresh is refused, which is what keeps boot on its
// no-session path; the projects read is the one request an assertion depends on.
const fetchStub = vi.fn(async (url: string) => {
	const target = String(url)
	if (target.includes('/user/token/refresh')) return new Response('', {status: 401})
	if (target.includes('/projects')) {
		return new Response(JSON.stringify([INBOX_PROJECT]), {
			headers: {'content-type': 'application/json'},
		})
	}
	return new Response(JSON.stringify([]), {headers: {'content-type': 'application/json'}})
})

beforeAll(async () => {
	// The REAL English catalogue, through i18n.js's only seam. Loading it is what lets every
	// assertion below name the sentence a customer reads rather than a key path - and "Your
	// Tasks" is a value in that shipped file, not a string this test invented.
	vi.stubGlobal('fetch', async (input: string) => (
		String(input).includes('/en.json')
			? new Response(enRaw, {headers: {'content-type': 'application/json'}})
			: new Response('not found', {status: 404})
	))
	await initI18n('en', ['en'])

	document.body.innerHTML = SHELL
	sessionStorage.setItem(LOGIN_HANDOFF_MARKER, '1')
	api.configure({fetch: fetchStub as unknown as typeof fetch, origin: ORIGIN})

	// Installs the delegated click listener and then finds no session. The rejection is swallowed
	// rather than asserted: this file is not a test of boot, it is a test of what the controls
	// boot makes live go on to print.
	await boot().catch(() => undefined)
	vi.unstubAllGlobals()
})

beforeEach(() => {
	// A fresh #modalRoot per case, so nothing reads a modal a previous case left open.
	document.body.innerHTML = SHELL
	fetchStub.mockClear()
	// Clear the terminal session state boot's refused refresh left behind. Every case here is
	// about a person who IS signed in and has a task open in front of them; without this, the one
	// case that reads from the server would be testing a signed-out page, where the dialog never
	// opens at all and the assertion would be about the wrong thing entirely.
	api.resetSession()
})

afterEach(() => {
	vi.useRealTimers()
})

describe('the task page never shows a customer the stored word "Inbox"', () => {
	it('names the project in the task header chip', () => {
		// THE BUSIEST PRINT ON THE PAGE: it is drawn on every task a customer opens, and it was
		// the site that carried the defect. `projectLabel` returned the column verbatim, so the
		// header read "Inbox" while the settings page called that same project "Your Tasks".
		//
		// MUTATION, TRACED: returning `String(found.title ?? '')` from `projectLabel` reddens
		// this case and the two relation cases below - the three that read the funnel - and
		// leaves both picker cases green, because those two call the helper themselves.
		seedTaskState()

		const html = render(CTX)

		expect(html).toContain('Your Tasks')
		expect(html).not.toContain('Inbox')
	})

	it('names the destination in the Move task picker', async () => {
		// Clicked, not called: `moveModal` is private and is reached the way a person reaches it,
		// through the chip the render above just drew.
		//
		// MUTATION, TRACED: `esc(project?.title)` at the option in `moveModal` reddens THIS CASE
		// ALONE - which is the point of clicking through rather than testing the helper, because
		// this site does not share the funnel the header chip goes through.
		paintTask()

		click('[data-action="move"]')
		await vi.waitFor(() => expect(modalMarkup()).toContain('Move task'))

		expect(modalMarkup()).toContain('Your Tasks')
		expect(modalMarkup()).not.toContain('Inbox')
	})

	it('names the project in the scope line under the relation search', async () => {
		// The relations panel is a tab, so the shipped Add relation button exists only once the
		// view state says the tab is open - which is the state a person is in when they click it.
		//
		// MUTATION, TRACED, TWO OF THEM. Breaking the funnel (see the header case) reddens this.
		// So does leaving the funnel intact and having `relationModal` read the project out of
		// `state.projects` itself, which reddens this case alone - so this site is guarded
		// against bypassing `projectLabel`, not merely against `projectLabel` being wrong.
		paintTask({resourceTab: 'relations'})

		click('[data-action="add-relation"]')
		await vi.waitFor(() => expect(modalMarkup()).toContain('Showing tasks in'))

		expect(modalMarkup()).toContain('Showing tasks in Your Tasks.')
		expect(modalMarkup()).not.toContain('Inbox')
	})

	it('names it again when the search box is typed over after a task was chosen', async () => {
		// The SECOND print of the same sentence, and a separate site: `paintRelationHelp` rewrites
		// the line without re-rendering the modal, so a correct `relationModal` does not make this
		// one correct. The journey is real - choose a task, change your mind, type again - and it
		// is the only way back to the scope sentence, because choosing replaces it with the name
		// of what was chosen.
		//
		// MUTATION, TRACED, TWO OF THEM, exactly as for the case above: breaking the funnel
		// reddens this, and so does having `paintRelationHelp` read `state.projects` itself,
		// which reddens this case alone and leaves the modal's own line green.
		paintTask({resourceTab: 'relations'})
		const app = document.getElementById('app')
		if (app === null) throw new Error('the shell has no #app')
		// `mount` is what installs this view's `input` listener; without it the keystroke below
		// reaches nothing. It starts no read - the state already holds this task id.
		mount(app, CTX)

		click('[data-action="add-relation"]')
		await vi.waitFor(() => expect(document.getElementById('relationSearch')).not.toBeNull())

		// A row from the results list, exactly as `runRelationSearch` paints one: the handler
		// takes the id and the title off the row's own attributes and parses nothing.
		const results = document.getElementById('relationResults')
		if (results === null) throw new Error('the relation modal drew no results list')
		results.innerHTML = '<button data-action="pick-relation-task" data-task-id="88"'
			+ ' data-task-title="Another task">Another task</button>'
		click('[data-action="pick-relation-task"]')
		expect(document.getElementById('relationSearchHelp')?.textContent).toContain('Another task')

		// Fake timers from here: the keystroke arms a debounced search, and a real timer would
		// fire it after the assertion, against a page the next case has already replaced.
		vi.useFakeTimers()
		const box = document.getElementById('relationSearch') as HTMLInputElement
		box.value = 'something else'
		box.dispatchEvent(new Event('input', {bubbles: true}))

		const help = document.getElementById('relationSearchHelp')?.textContent ?? ''
		expect(help).toBe('Showing tasks in Your Tasks.')
		expect(help).not.toContain('Inbox')
	})

	it('names the project in the Add task picker', async () => {
		// app.js's own print, and the one control that is not the task view's: the + button in the
		// shared identity block. Rendered here from the shipped `identityBlock`, so the button
		// carrying `data-action="add-task"` is the page's rather than this file's.
		//
		// MUTATION, TRACED: `escapeHtml(project?.title)` at the option in `openAddTask` reddens
		// THIS CASE ALONE. It is also the site furthest from the others - a different module,
		// reached from a button on both pages - so nothing the task view does can guard it.
		document.body.insertAdjacentHTML('beforeend', identityBlock(FACTS))

		click('[data-action="add-task"]')
		await vi.waitFor(() => expect(modalMarkup()).not.toBe(''))

		expect(modalMarkup()).toContain('Your Tasks')
		expect(modalMarkup()).not.toContain('Inbox')
	})
})
