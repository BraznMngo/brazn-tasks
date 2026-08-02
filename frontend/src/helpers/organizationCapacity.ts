/**
 * How an "x of y" capacity reads when y is not known.
 *
 * The em dash is not decoration. `seats_purchased` is optional in the
 * entitlement contract, and an absent count means this instance could not be
 * told how many seats were bought — which is neither zero nor unlimited.
 * Rendering it as "5 / 0" would say the customer bought none, and "5" alone
 * would quietly drop the half of the sentence that matters. Both are guesses
 * about somebody's subscription, and the surface does not make them: it shows
 * that the number is missing, which is also what every capacity decision taken
 * against it does — refuse.
 */
export function formatCapacity(used: number, limit: number | null): string {
	return limit === null ? `${used} / —` : `${used} / ${limit}`
}
