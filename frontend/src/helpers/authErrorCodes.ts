// The API's error codes for the account screens, and the two readers that dig
// them out of whatever an API client threw.
//
// The numbers are written here as literals rather than derived from anything,
// because they are the API's and not ours: each one is stated next to its
// definition in pkg/user/error.go. If one of these ever disagrees with the
// server, a screen shows the wrong sentence, so the value is pinned here in one
// place instead of appearing as a bare number in three components.

/** pkg/user/error.go — ErrorCodeUsernameExists. */
export const ERROR_USERNAME_EXISTS = 1001
/** pkg/user/error.go — ErrorCodeUserEmailExists. */
export const ERROR_EMAIL_EXISTS = 1002
/** pkg/user/error.go — ErrCodeEmailNotConfirmed. */
export const ERROR_EMAIL_NOT_CONFIRMED = 1012

/** RFC 9110 §15.5.29. The rate limiter answers with this and no error code. */
export const HTTP_TOO_MANY_REQUESTS = 429

/**
 * The API error code carried by a rejection, if it carries one.
 *
 * Two shapes reach a caller: the auth store rethrows `e.response.data` when the
 * body has a message, and passes the client's own error through otherwise. Both
 * are read here so a caller does not have to know which one it got.
 */
export function errorCodeOf(error: unknown): number | undefined {
	if (error === null || typeof error !== 'object') {
		return undefined
	}

	const direct = (error as {code?: unknown}).code
	if (typeof direct === 'number') {
		return direct
	}

	const nested = (error as {response?: {data?: {code?: unknown}}}).response?.data?.code
	return typeof nested === 'number' ? nested : undefined
}

/**
 * The HTTP status a rejection came back with, if it carries one. The rate
 * limiter is the reason this exists: it refuses before any handler runs, so its
 * answer has a status and no error code at all.
 */
export function httpStatusOf(error: unknown): number | undefined {
	if (error === null || typeof error !== 'object') {
		return undefined
	}

	const status = (error as {response?: {status?: unknown}}).response?.status
	return typeof status === 'number' ? status : undefined
}
