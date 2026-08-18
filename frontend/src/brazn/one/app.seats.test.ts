import {describe, it, expect, beforeEach, afterEach, vi} from 'vitest'

import {SEATS_PER_TEAM, readSeatMeter, requiredSeatsForTeams} from '../../../public/one/app.js'

/*
 * THE SEAT FORMULA.
 *
 * The server rule, written out here as a literal and NOT imported:
 *
 *     seats_purchased >= 3 * (teams_used + 1)
 *
 * and it IGNORES member count entirely. Asserting against a value the page computed would be a
 * self-referential comparison - the exact shape CLAUDE.md section 4 names - so every expectation
 * below is either a hand-written number or derived from the contract above, never from
 * SEATS_PER_TEAM or from readSeatMeter's own arithmetic.
 *
 * The supplied mapping got this wrong twice over, and so does the prototype at lines 602-604: it
 * invents `seats_per_team || 3` (the `||` turns a legitimate 0 into 3) AND folds members plus
 * pending invitations into the requirement.
 *
 * THE RATIO IS THE SERVER'S OR IT IS UNKNOWN. The meter has no local fallback of any kind - not
 * the prototype's `||` and not a gentler `?? SEATS_PER_TEAM` either, which is why every payload
 * below that expects a number carries `seats_per_team` explicitly. api.js says why at
 * getOrganization: a constant duplicated either side of a boundary is checked by neither, and
 * filling the gap from the page's own copy would state a requirement the server never sent on
 * the one number a customer is asked to spend money against.
 */

// The contract, transcribed by hand from pkg/models/brazn_organization.go:265-270.
const CONTRACT_RATIO = 3

// teams_used -> seats that must already be purchased before ONE MORE team is allowed.
const CONTRACT_TABLE: ReadonlyArray<readonly [number, number]> = [
	[0, 3],
	[1, 6],
	[2, 9],
	[3, 12],
	[4, 15],
	[10, 33],
]

describe('one/app.js seat formula', () => {
	beforeEach(() => {
		vi.spyOn(console, 'warn').mockImplementation(() => {})
	})

	afterEach(() => {
		vi.restoreAllMocks()
	})

	it('pins the page constant to the contract', () => {
		// If the server rule ever changes, this is the line that has to be edited deliberately -
		// which is the point of writing 3 out twice in two different files.
		// MUTATION: changing SEATS_PER_TEAM in app.js makes this red.
		expect(SEATS_PER_TEAM).toBe(CONTRACT_RATIO)
	})

	it('requires 3 x (teams_used + 1) purchased seats for the next team', () => {
		for (const [teamsUsed, required] of CONTRACT_TABLE) {
			// Both halves are pinned: the pure helper takes the resulting TEAM COUNT, and the meter
			// is what the view renders. The payload carries the server's ratio because the meter
			// asks the server for it and never assumes one.
			expect(requiredSeatsForTeams(teamsUsed + 1), `teams_used=${teamsUsed}`).toBe(required)
			expect(
				readSeatMeter({seats_per_team: CONTRACT_RATIO, teams_used: teamsUsed}).requiredForNextTeam,
				`teams_used=${teamsUsed}`,
			).toBe(required)
		}
	})

	it('ignores member count completely', () => {
		const base = {seats_per_team: CONTRACT_RATIO, teams_used: 2, seats_purchased: 9}
		const empty = readSeatMeter({...base, seats_occupied: 1})
		const crowded = readSeatMeter({...base, seats_occupied: 99})

		// MUTATION: folding members (or members + pending invitations, as the prototype does) back
		// into requiredForNextTeam makes this red.
		expect(empty.requiredForNextTeam).toBe(9)
		expect(crowded.requiredForNextTeam).toBe(9)
		expect(empty.meetsNextTeamRule).toBe(true)
		expect(crowded.meetsNextTeamRule).toBe(true)
	})

	it('honours a server ratio of 0 instead of reading it as 3', () => {
		const meter = readSeatMeter({seats_per_team: 0, teams_used: 1})

		// MUTATION: restoring the prototype's `seats_per_team || 3` makes this red - a legitimate 0
		// would be read as 3 and the page would demand 6 seats the server does not require.
		expect(meter.seatsPerTeam).toBe(0)
		expect(meter.requiredForNextTeam).toBe(0)
	})

	it('states NO requirement at all when the server sent no ratio', () => {
		// The other half of the same discipline, and the one a `??` fallback hides. An absent
		// `seats_per_team` is not an invitation to substitute the page's copy of the constant: the
		// requirement is unknown, the view renders organization.teams.capped.unknown ("we cannot
		// read how many seats you have bought"), and that sentence is true.
		//
		// In practice the field is never absent from a 200 - pkg/models/brazn_organization.go:200
		// sets it unconditionally - so this is the payload that came back through something else,
		// which is precisely when guessing would be worst.
		// MUTATION: restoring `const ratio = seatsPerTeam ?? SEATS_PER_TEAM` in readSeatMeter makes
		// this red: requiredForNextTeam becomes 6 and seatsPerTeam becomes 3, neither of which the
		// server said.
		const meter = readSeatMeter({teams_used: 1, seats_purchased: 6})

		expect(meter.seatsPerTeam).toBeNull()
		expect(meter.requiredForNextTeam).toBeNull()
		expect(meter.meetsNextTeamRule).toBeNull()
		// Everything the payload DID carry still reads through - the ratio being unknown costs the
		// requirement and nothing else.
		expect(meter.purchased).toBe(6)
		expect(meter.teamsUsed).toBe(1)
	})

	it('warns when the server ratio and the page constant disagree, and only then', () => {
		readSeatMeter({seats_per_team: 3, teams_used: 1})
		// The negative half first: a warning that always fires proves nothing about the positive
		// half below.
		expect(console.warn).not.toHaveBeenCalled()

		readSeatMeter({seats_per_team: 4, teams_used: 1})
		// A constant duplicated in Go and in JS is checked by neither. This warning is the only
		// thing on either side of the boundary that would catch it drifting.
		// MUTATION: deleting the drift warning from readSeatMeter makes this red.
		expect(console.warn).toHaveBeenCalledTimes(1)
		// The server's number is the ONLY number: the arithmetic follows what it sent, and the
		// page constant does not get a vote beyond raising the warning above.
		expect(readSeatMeter({seats_per_team: 4, teams_used: 1}).requiredForNextTeam).toBe(8)
	})

	it('treats a null seats_purchased as unknown - neither zero nor unlimited', () => {
		const meter = readSeatMeter({
			seats_per_team: CONTRACT_RATIO,
			seats_purchased: null,
			teams_allowed: null,
			teams_used: 1,
			seats_occupied: 4,
		})

		// `seats_purchased` and `teams_allowed` are *int and are null TOGETHER, meaning "this
		// instance cannot answer".
		// MUTATION: coercing a null seats_purchased to 0 makes this red - meetsNextTeamRule would
		// become false and the page would tell a customer to buy seats they may already own.
		expect(meter.purchased).toBeNull()
		expect(meter.teamsAllowed).toBeNull()
		expect(meter.meetsNextTeamRule).toBeNull()
		expect(meter.fillRatio).toBeNull()
		// The requirement itself is still knowable, and the capacity notice needs it.
		expect(meter.requiredForNextTeam).toBe(6)
	})

	it('reads can_create_team as sent, even when the seat rule disagrees', () => {
		const meter = readSeatMeter({
			seats_per_team: CONTRACT_RATIO,
			can_create_team: false,
			seats_purchased: 999,
			teams_used: 0,
		})

		// The server sends this precisely so a client renders the same answer the route enforces.
		// The payload above is deliberately contradictory: the rule is comfortably met and the
		// server still says no.
		// MUTATION: recomputing canCreateTeam from meetsNextTeamRule makes this red.
		expect(meter.meetsNextTeamRule).toBe(true)
		expect(meter.canCreateTeam).toBe(false)

		// And the other direction: anything that is not exactly `true` is not permission.
		expect(readSeatMeter({teams_used: 0}).canCreateTeam).toBe(false)
		expect(readSeatMeter({can_create_team: 'yes'}).canCreateTeam).toBe(false)
	})

	it('computes a fill ratio only when there is a denominator, and clamps it', () => {
		expect(readSeatMeter({seats_occupied: 3, seats_purchased: 12}).fillRatio).toBe(0.25)
		// A meter wider than its track is a rendering bug; over-occupancy is a real state after a
		// downgrade.
		expect(readSeatMeter({seats_occupied: 5, seats_purchased: 4}).fillRatio).toBe(1)
		// MUTATION: dropping the `purchased <= 0` guard makes this red with Infinity/NaN, which
		// renders as a blank or a full bar depending on the browser.
		expect(readSeatMeter({seats_occupied: 3, seats_purchased: 0}).fillRatio).toBeNull()
	})

	it('answers safely for an organization payload that never arrived', () => {
		// The 403 case: getOrganization() resolves null for every non-administrator, so the meter
		// is asked to describe nothing at all rather than never being called.
		// MUTATION: dereferencing `org.seats_occupied` without the optional chain makes this red
		// with a TypeError on the most common role on the instance.
		const meter = readSeatMeter(null)

		expect(meter.occupied).toBeNull()
		expect(meter.purchased).toBeNull()
		expect(meter.teamsUsed).toBeNull()
		expect(meter.requiredForNextTeam).toBeNull()
		expect(meter.meetsNextTeamRule).toBeNull()
		expect(meter.canCreateTeam).toBe(false)
		// Null, not the page constant: there is no organization, so there is no ratio to report.
		expect(meter.seatsPerTeam).toBeNull()
	})

	it('rejects non-numeric counts rather than rendering them', () => {
		// The fork sends *int, and a JSON payload that has been through a proxy can carry a string.
		// MUTATION: replacing intOrNull with Number(value) makes this red - '9' would render as a
		// seat count the server never sent, and NaN would render as a blank meter.
		const meter = readSeatMeter({
			seats_per_team: '3',
			seats_occupied: '4',
			seats_purchased: '9',
			teams_used: '2',
		})

		expect(meter.occupied).toBeNull()
		expect(meter.purchased).toBeNull()
		expect(meter.teamsUsed).toBeNull()
		expect(meter.seatsPerTeam).toBeNull()
		expect(meter.requiredForNextTeam).toBeNull()
	})
})
