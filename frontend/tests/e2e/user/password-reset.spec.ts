import {test, expect} from '../../support/fixtures'
import {UserFactory, type UserAttributes} from '../../factories/user'
import {TokenFactory, type TokenAttributes} from '../../factories/token'

test.describe('Password Reset', () => {
	let user: UserAttributes

	test.beforeEach(async ({page, apiContext}) => {
		const users = await UserFactory.create(1)
		user = users[0] as UserAttributes
	})

	test('Should allow a user to reset their password with a valid token', async ({page, apiContext}) => {
		const tokenArray = await TokenFactory.create(1, {user_id: user.id as number, kind: 1})
		const token: TokenAttributes = tokenArray[0] as TokenAttributes

		await page.goto(`/?userPasswordReset=${token.token}`)
		await expect(page).toHaveURL(`/password-reset?userPasswordReset=${token.token}`)

		const newPassword = 'newSecurePassword123'
		await page.locator('input[id=password]').fill(newPassword)
		await page.locator('#password-reset-submit').click()

		// The success copy is `passwordResetSuccess`, which this ticket rewrote.
		// A fragment rather than the sentence: what matters is that the change
		// took, not the wording that says so.
		await expect(page.locator('.message.success')).toContainText('Your password was changed')

		// The way out of the success screen is a link to /login, not the sign-in
		// form's submit — `#login-submit` does not exist on this screen at all.
		// It reads the same because both render `user.auth.login`, which is why
		// the blanket replacement of that label put the wrong id here.
		await page.locator('#password-reset-login').click()
		await expect(page).toHaveURL('/login')

		// Try to login with the new password
		await page.locator('input[id=username]').fill(user.username)
		await page.locator('input[id=password]').fill(newPassword)
		await page.locator('#login-submit').click()
		await expect(page).toHaveURL('/')
	})

	test('Should show an error for an invalid token', async ({page, apiContext}) => {
		await page.goto('/?userPasswordReset=invalidtoken123')
		await expect(page).toHaveURL('/password-reset?userPasswordReset=invalidtoken123')

		// Attempt to reset password
		const newPassword = 'newSecurePassword123'
		await page.locator('input[id=password]').fill(newPassword)
		await page.locator('#password-reset-submit').click()

		// This screen used to print the server's own sentence, "Invalid token to
		// reset a user's password." The restyle sends the rejection through
		// `getErrorText` instead, which resolves code 1009 to `error.1009`,
		// "Invalid password reset token." Both say the same thing and they share
		// no usable substring, so the old assertion could not survive the swap.
		//
		// `error.1009` is the shared API error catalogue, which this ticket does
		// not touch — unlike the screen copy it replaced. Asserted on the danger
		// variant specifically, so a success banner cannot satisfy it.
		await expect(page.locator('.message.danger')).toContainText('Invalid password reset token')
	})

	test('Should redirect to login if no token is present in query param when visiting /password-reset directly', async ({page, apiContext}) => {
		await page.goto('/password-reset')
		// Wait for redirect to login page
		await expect(page).toHaveURL('/login')
	})

	test('Should redirect to login if userPasswordReset token is not present in query param when visiting root', async ({page, apiContext}) => {
		await page.goto('/')
		await expect(page).toHaveURL('/login')
	})
})
