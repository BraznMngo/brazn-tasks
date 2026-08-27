import {test, expect} from '../../support/fixtures'
import {ProjectFactory} from '../../factories/project'
import {seed} from '../../support/seed'
import {TaskFactory} from '../../factories/task'
import {BucketFactory} from '../../factories/bucket'
import {updateUserSettings} from '../../support/updateUserSettings'
import {createDefaultViews} from '../project/prepareProjects'
import type {APIRequestContext, Page} from '@playwright/test'

async function seedTasks(apiContext: APIRequestContext, numberOfTasks = 50, startDueDate = new Date()) {
	const project = (await ProjectFactory.create())[0]
	const views = await createDefaultViews(project.id)
	await BucketFactory.create(1, {
		project_view_id: views[3].id,
	})
	const tasks = []
	let dueDate = startDueDate
	for (let i = 0; i < numberOfTasks; i++) {
		const now = new Date()
		dueDate = new Date(new Date(dueDate).setDate(dueDate.getDate() + 2))
		tasks.push({
			id: i + 1,
			project_id: project.id,
			done: false,
			created_by_id: 1,
			title: 'Test Task ' + i,
			index: i + 1,
			due_date: dueDate.toISOString(),
			created: now.toISOString(),
			updated: now.toISOString(),
		})
	}
	await TaskFactory.seed(TaskFactory.table, tasks)
	return {tasks, project}
}

test.describe('Home Page Task Overview', () => {
	// Both ordering tests below make the same single assertion, and the shape of it is the
	// point. `toHaveText` given an array requires the page to hold exactly that many
	// elements, each with exactly that text, in that order, and it retries until it does —
	// so it waits for the list to render and it cannot be satisfied by a list that has not
	// rendered.
	//
	// What it replaces read the elements into an array with `.all()` and looped over them.
	// `.all()` does not wait, the array was empty every time, the loop body never ran, and
	// both tests passed having compared nothing. Verified rather than reasoned about: with
	// `order_by` in ShowTasks.vue changed from `['asc', 'desc']` to `['desc', 'desc']`, so
	// that the page renders all fifty tasks in exactly the wrong order, the old pair still
	// passed and this pair fails on the first entry.
	//
	// The comparison is against `.task-link`, which holds the title alone, rather than
	// against the whole task row. The row also carries the project name and the due date,
	// which forces a substring comparison, and a substring comparison cannot tell "Test
	// Task 1" from "Test Task 19" — it would accept a partially wrong order.
	const taskTitles = (page: Page) => page.locator('[data-cy="showTasks"] .card .task .task-link')

	test('Should show tasks with a near due date first on the home page overview', async ({authenticatedPage: page, apiContext}) => {
		const taskCount = 50
		const {tasks} = await seedTasks(apiContext, taskCount)

		await page.goto('/')

		// Seeded in ascending due date order, so this array is also the order the page owes
		// us: the task due soonest at the top.
		await expect(taskTitles(page)).toHaveText(tasks.map(task => task.title), {timeout: 15000})
	})

	test('Should show overdue tasks first, then show other tasks', async ({authenticatedPage: page, apiContext}) => {
		const now = new Date()
		const oldDate = new Date(new Date(now).setDate(now.getDate() - 14))
		const taskCount = 50
		const {tasks} = await seedTasks(apiContext, taskCount, oldDate)

		// This test earns its name only if the fixture holds both kinds of task. Due dates
		// start in the past and step forward two days at a time, so the earliest of the
		// fifty are already overdue and the rest are not. Pinning that here means a change
		// to seedTasks that left nothing overdue is reported as the fixture problem it is,
		// rather than passing as an ordering test that never saw an overdue task.
		const overdue = tasks.filter(task => new Date(task.due_date) < now)
		expect(overdue.length).toBeGreaterThan(0)
		expect(overdue.length).toBeLessThan(taskCount)

		await page.goto('/')

		// The overdue tasks are the first entries of the seeded array, so asserting the
		// whole order asserts that they come first and that what follows is still ordered.
		await expect(taskTitles(page)).toHaveText(tasks.map(task => task.title), {timeout: 15000})
	})

	test('Should show a new task with a very soon due date at the top', async ({authenticatedPage: page, apiContext}) => {
		const {tasks, project} = await seedTasks(apiContext, 49)
		const newTaskTitle = 'New Task'

		await page.goto('/')
		await page.waitForLoadState('networkidle')

		await TaskFactory.create(1, {
			id: 999,
			title: newTaskTitle,
			project_id: project.id,
			due_date: new Date().toISOString(),
		}, false)

		await page.goto(`/projects/${project.id}/1`)
		await page.waitForLoadState('networkidle')
		// Wait for the tasks list to load and contain the new task
		await expect(page.locator('.tasks')).toContainText(newTaskTitle)
		await page.goto('/')
		await page.waitForLoadState('networkidle')
		await expect(page.locator('[data-cy="showTasks"] .card .task').first()).toContainText(newTaskTitle)
	})

	test('Should not show a new task without a date at the bottom when there are > 50 tasks', async ({authenticatedPage: page, apiContext}) => {
		// We're not using the api here to create the task in order to verify the flow
		const {tasks} = await seedTasks(apiContext, 100)
		const newTaskTitle = 'New Task'

		await page.goto('/')
		await page.waitForLoadState('networkidle')

		await page.goto(`/projects/${tasks[0].project_id}/1`)
		await page.waitForLoadState('networkidle')
		const taskResponsePromise = page.waitForResponse('**/api/v1/projects/*/tasks')
		await page.locator('.task-add textarea').fill(newTaskTitle)
		await page.locator('.task-add textarea').press('Enter')
		await taskResponsePromise
		await page.goto('/')
		await page.waitForLoadState('networkidle')
		await expect(page.locator('[data-cy="showTasks"]')).not.toContainText(newTaskTitle)
	})

	test('Should show a new task without a date at the bottom when there are < 50 tasks', async ({authenticatedPage: page, apiContext}) => {
		const {project} = await seedTasks(apiContext, 40)
		const newTaskTitle = 'New Task'
		await TaskFactory.create(1, {
			id: 999,
			title: newTaskTitle,
			project_id: project.id,
		}, false)

		await page.goto('/')
		await page.waitForLoadState('networkidle')
		await expect(page.locator('[data-cy="showTasks"]')).toContainText(newTaskTitle)
	})

	test('Should show a task without a due date added via default project at the bottom', async ({authenticatedPage: page, apiContext}) => {
		const {project} = await seedTasks(apiContext, 40)

		// Navigate first to get access to localStorage
		await page.goto('/')
		await page.waitForLoadState('networkidle')
		const token = await page.evaluate(() => localStorage.getItem('token'))

		await updateUserSettings(apiContext, token, {
			default_project_id: project.id,
			overdue_tasks_reminders_time: '9:00',
		})

		const newTaskTitle = 'New Task'
		// Reload page to apply the new settings
		await page.reload()

		// Wait for page to be fully loaded
		await page.waitForLoadState('networkidle')

		// Wait for the add task input to be visible and ready
		const addTaskInput = page.locator('.add-task-textarea')
		await expect(addTaskInput).toBeVisible()

		await addTaskInput.fill(newTaskTitle)

		// Wait for the task creation request to complete
		const createTaskPromise = page.waitForResponse(response =>
			response.url().includes('/projects/') &&
			response.url().includes('/tasks') &&
			response.request().method() === 'PUT',
		)
		await addTaskInput.press('Enter')
		await createTaskPromise

		// Wait for the task to appear in the list (no due date tasks appear at the bottom)
		await expect(page.locator('[data-cy="showTasks"] .card .task').last()).toContainText(newTaskTitle, {timeout: 10000})
	})

	test('Should show the import hint when there are no tasks', async ({authenticatedPage: page}) => {
		// Need a project so that ShowTasks renders (which sets tasksLoaded=true),
		// but no tasks so the ImportHint becomes visible.
		const project = (await ProjectFactory.create())[0]
		await createDefaultViews(project.id)

		await page.goto('/')

		await expect(page.locator('.home.app-content .content')).toContainText('Import your projects and tasks from other services into ONE:')
	})

	test('Should not show the import hint when there are tasks', async ({authenticatedPage: page, apiContext}) => {
		await seedTasks(apiContext)

		await page.goto('/')

		// The hint is only mounted once ShowTasks has reported its tasks, so wait for a task to
		// render before asserting the hint is absent. Without this the negated expectation is
		// satisfied by the still-empty page on its first poll, and would pass even if the hint
		// appeared a moment later.
		await expect(page.locator('[data-cy="showTasks"] .card .task').first()).toBeVisible({timeout: 15000})

		await expect(page.locator('.home.app-content .content')).not.toContainText('Import your projects and tasks from other services into ONE:')
	})
})
