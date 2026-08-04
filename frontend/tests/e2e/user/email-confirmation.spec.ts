import {test, expect} from '../../support/fixtures'
import {UserFactory} from '../../factories/user'
import {TokenFactory} from '../../factories/token'
import {TEST_PASSWORD, TEST_PASSWORD_HASH} from '../../support/constants'

test.describe('Email Confirmation', () => {
	let user
	let confirmationToken

	test.beforeEach(async ({page, apiContext}) => {
		// Create a user with status = 1 (StatusEmailConfirmationRequired)
		const users = await UserFactory.create(1, {
			username: 'unconfirmeduser',
			email: 'unconfirmed@example.com',
			password: TEST_PASSWORD_HASH,
			status: 1, // StatusEmailConfirmationRequired
		})
		user = users[0]

		// 64 alphanumeric characters, which is what utils.CryptoRandomString(64)
		// produces. The fixture used to be a 57-character hyphenated string -
		// a shape this instance has never issued - and a token of the wrong
		// shape is now recognised as a broken link before it is ever sent, so a
		// fixture like that would have tested the wrong screen.
		// kind: 2 = TokenEmailConfirm
		confirmationToken = 'e2eEmailConfirmToken0000000000000000000000000000000000000000abcd'
		await TokenFactory.create(1, {
			user_id: user.id,
			kind: 2,
			token: confirmationToken,
		})
	})

	test('Should fail login before email is confirmed', async ({page, apiContext}) => {
		await page.goto('/login')
		await page.locator('input[id=username]').fill(user.username)
		await page.locator('input[id=password]').fill(TEST_PASSWORD)
		await page.locator('#login-submit').click()

		// `notConfirmedYet`, not the raw `1012` string. This screen now maps that
		// code to copy written for the person reading it, so asserting the API's
		// own sentence would fail against a correct build - which is exactly what
		// it did when the restyle landed.
		//
		// A FRAGMENT RATHER THAN THE WHOLE SENTENCE, deliberately: the tail tells
		// people where to look and is expected to be reworded. This clause is what
		// separates "unconfirmed" from every other sign-in refusal, and that is the
		// whole of what this test exists to prove.
		await expect(page.locator('div.message.danger')).toContainText('has not been confirmed yet')
	})

	// The mail sends people to the app root with the token in the query. The
	// router hands that to the confirmation screen, which reads the token,
	// clears it out of the address bar and asks the server about it.
	test('Should confirm email when clicking the link from the email', async ({page, apiContext}) => {
		const confirmEmailPromise = page.waitForResponse(response =>
			response.url().includes('/user/confirm') && response.request().method() === 'POST',
		)

		await page.goto(`/?userEmailConfirm=${confirmationToken}`)

		await expect(page).toHaveURL(/\/confirm/)

		const confirmResponse = await confirmEmailPromise
		expect(confirmResponse.status()).toBe(200)

		await expect(page.locator('.message.success')).toBeVisible({timeout: 10000})
		await expect(page.locator('.message.success')).toContainText('Your address is confirmed')

		// The token does not stay in the address bar or in history.
		await expect(page).not.toHaveURL(new RegExp(confirmationToken))

		// And signing in now works.
		await page.goto('/login')
		await page.locator('input[id=username]').fill(user.username)
		await page.locator('input[id=password]').fill(TEST_PASSWORD)
		await page.locator('#login-submit').click()

		await expect(page).not.toHaveURL(/\/login/)
		await expect(page.locator('body')).toContainText(user.username)
	})

	// THE RULING, in a real browser with a link that was genuinely used: the
	// first visit really confirms, and the second visit of the SAME link comes
	// back green. A second click is not a failure, and showing it as one makes
	// people think they broke something.
	test('A link that was already used confirms again rather than failing', async ({page, apiContext}) => {
		const firstConfirm = page.waitForResponse(response =>
			response.url().includes('/user/confirm') && response.request().method() === 'POST',
		)
		await page.goto(`/?userEmailConfirm=${confirmationToken}`)
		expect((await firstConfirm).status()).toBe(200)
		await expect(page.locator('.message.success')).toBeVisible({timeout: 10000})

		const secondConfirm = page.waitForResponse(response =>
			response.url().includes('/user/confirm') && response.request().method() === 'POST',
		)
		await page.goto(`/?userEmailConfirm=${confirmationToken}`)
		expect((await secondConfirm).status()).toBe(200)

		await expect(page.locator('.message.success')).toBeVisible({timeout: 10000})
		await expect(page.locator('.message.success')).toContainText('You have already used this link')
		await expect(page.locator('.message.danger')).toHaveCount(0)
		await expect(page.locator('.message.warning')).toHaveCount(0)
	})

	// A link this instance never issued. Well-formed, so it really is asked
	// about, and the answer is the recoverable one - here is a new link - and
	// not a dead end.
	test('An unknown link offers a new one instead of a dead end', async ({page, apiContext}) => {
		const confirmEmailPromise = page.waitForResponse(response =>
			response.url().includes('/user/confirm') && response.request().method() === 'POST',
		)

		const unissued = 'thisTokenWasNeverIssuedByThisInstance000000000000000000000000xyz'
		await page.goto(`/?userEmailConfirm=${unissued}`)

		expect((await confirmEmailPromise).status()).toBe(412)

		await expect(page.locator('.message.warning')).toBeVisible({timeout: 10000})
		await expect(page.locator('input[type="email"]')).toBeVisible()

		// The account is untouched: signing in still says it is unconfirmed.
		await page.goto('/login')
		await page.locator('input[id=username]').fill(user.username)
		await page.locator('input[id=password]').fill(TEST_PASSWORD)
		await page.locator('#login-submit').click()

		// Same clause as the first test, and for the same reason. This assertion
		// is the load-bearing half of this case: an unissued token must leave the
		// account exactly as it found it, and the only way to see that from the
		// outside is that sign-in still refuses for the unconfirmed reason rather
		// than any other.
		await expect(page.locator('div.message.danger')).toContainText('has not been confirmed yet')
	})

	// What a mail client does to a long link: breaks it, so what arrives is not
	// the shape this instance issues. That is recognised without asking.
	test('A link the mail client broke is not sent to the server at all', async ({page, apiContext}) => {
		let asked = false
		page.on('request', request => {
			if (request.url().includes('/user/confirm') && request.method() === 'POST') {
				asked = true
			}
		})

		await page.goto('/?userEmailConfirm=e2eEmailConfirmToken00000000000%0A0000000000000abcd')

		await expect(page.locator('.message.warning')).toBeVisible({timeout: 10000})
		await expect(page.locator('input[type="email"]')).toBeVisible()
		expect(asked).toBe(false)
	})

	// AC7: the resend says the same thing for an address that is waiting and
	// for one that has no account at all.
	test('Asking for a new link says the same thing whatever address is given', async ({page, apiContext}) => {
		const noticeFor = async (address: string) => {
			// A fresh navigation each time, so the resend cooldown from the
			// previous one is not what is being measured.
			await page.goto('/confirm')
			await page.locator('input[type="email"]').fill(address)
			const resend = page.waitForResponse(response =>
				response.url().includes('/user/confirm/resend') && response.request().method() === 'POST',
			)
			await page.locator('#confirm-resend').click()
			expect((await resend).status()).toBe(200)
			return (await page.locator('.resend-status[role="status"]').innerText()).trim()
		}

		const waiting = await noticeFor(user.email)
		const unknown = await noticeFor('nobody-has-this-address@example.com')

		expect(waiting).toBe(unknown)
		expect(waiting).toContain('a new link is on its way')
	})
})

test.describe('Email change', () => {
	test('rejects email change with wrong current password', async ({authenticatedPage: page}) => {
		await page.goto('/user/settings/email-update')
		await page.locator('#newEmail').fill('new@example.com')
		await page.locator('#currentPasswordEmail').fill('WRONG_PASSWORD')

		const resp = page.waitForResponse(r => r.url().includes('/user/settings/email'))
		await page.getByRole('button', {name: 'Save'}).last().click()
		const r = await resp
		expect(r.status()).toBeGreaterThanOrEqual(400)

		await expect(page.locator('.global-notification .vue-notification.error')).toBeVisible()
	})
})
