import {describe, it, expect} from 'vitest'
import {readFileSync} from 'node:fs'
import {resolve} from 'node:path'

import {STANDARD_ZONES, timezoneOptions, zoneLabel} from '../../../public/one/timezones.js'

/*
 * BRA-1636 ITEM 2 — "A standard time zone list."
 *
 * The select showed the raw time zone database (EET, EST5EDT, Egypt, Eire, Etc/GMT+10 …). The
 * acceptance, asserted here: every entry reads "(UTC±hh:mm) City", sorted by today's offset and
 * then by city; no alias entries; the saved value stays the zone id; an account saved as `GMT`
 * still sees its own zone. Every expected label is written out by hand.
 *
 * Two fixed days, so daylight saving time is part of what is checked: 15 January and 15 July.
 */
const JANUARY = new Date('2026-01-15T12:00:00Z')
const JULY = new Date('2026-07-15T12:00:00Z')

/** What the fork's /user/timezones returns: the standard zones, and the aliases it also lists. */
const SERVER_ZONES = [...STANDARD_ZONES, 'EET', 'EST5EDT', 'Egypt', 'Eire', 'Etc/GMT+10', 'GMT']

describe('BRA-1636 item 2: the time zone list', () => {
	it('labels each zone with its offset today and a city, daylight saving time included', () => {
		expect(zoneLabel('Europe/Berlin', JANUARY)).toBe('(UTC+01:00) Berlin')
		expect(zoneLabel('Europe/Berlin', JULY)).toBe('(UTC+02:00) Berlin')
		expect(zoneLabel('America/New_York', JANUARY)).toBe('(UTC-05:00) New York')
		expect(zoneLabel('America/Argentina/Buenos_Aires', JANUARY)).toBe('(UTC-03:00) Buenos Aires')
	})

	it('covers the half-hour and 45-minute offsets people live in', () => {
		const labels = timezoneOptions(SERVER_ZONES, '', JANUARY).map((option) => option.label)
		expect(labels).toContain('(UTC+05:30) Kolkata')
		expect(labels).toContain('(UTC+05:45) Kathmandu')
		expect(labels).toContain('(UTC+09:30) Darwin')
		expect(labels).toContain('(UTC-09:30) Marquesas')
	})

	it('offers no alias and no Etc/GMT name, and every entry reads "(UTC±hh:mm) City"', () => {
		const options = timezoneOptions(SERVER_ZONES, '', JANUARY)
		const ids = options.map((option) => option.id)
		for (const alias of ['EET', 'EST5EDT', 'Egypt', 'Eire', 'Etc/GMT+10', 'GMT']) {
			expect(ids).not.toContain(alias)
		}
		for (const {label} of options) {
			expect(label).toMatch(/^\(UTC[+-]\d{2}:\d{2}\) \S/)
		}
	})

	it('sorts by offset, then by city', () => {
		const options = timezoneOptions(
			['Europe/Paris', 'Asia/Tokyo', 'Europe/Berlin', 'America/New_York'],
			'',
			JANUARY,
		)
		expect(options).toEqual([
			{id: 'America/New_York', label: '(UTC-05:00) New York'},
			{id: 'Europe/Berlin', label: '(UTC+01:00) Berlin'},
			{id: 'Europe/Paris', label: '(UTC+01:00) Paris'},
			{id: 'Asia/Tokyo', label: '(UTC+09:00) Tokyo'},
		])
	})

	it('keeps the zone id as what is saved, so choosing Berlin saves Europe/Berlin', () => {
		const berlin = timezoneOptions(SERVER_ZONES, '', JANUARY)
			.find((option) => option.label === '(UTC+01:00) Berlin')
		expect(berlin?.id).toBe('Europe/Berlin')
	})

	it('still offers an account its own zone when it is not on the list, labelled the same way', () => {
		const options = timezoneOptions(SERVER_ZONES, 'GMT', JANUARY)
		expect(options).toContainEqual({id: 'GMT', label: '(UTC+00:00) GMT'})
	})

	it('offers nothing the server does not list', () => {
		expect(timezoneOptions(['Europe/Berlin'], '', JANUARY))
			.toEqual([{id: 'Europe/Berlin', label: '(UTC+01:00) Berlin'}])
	})
})

/*
 * BRA-1636 ITEM 3 — "Add member" on the same line as the team dropdown. Every `.select` is
 * `width:100%`, which made the team dropdown fill the row and push the button under it. The
 * dropdown is as wide as its content now. A layout this test cannot render; it fails if the rule
 * that keeps the dropdown off the full width goes.
 */
describe('BRA-1636 item 3: Add member beside the team dropdown', () => {
	it('sizes the team dropdown to its content rather than the full row', () => {
		const oneCss = readFileSync(resolve(process.cwd(), 'public/one/one.css'), 'utf8')
		const rule = /\.team-selector\{([^}]*)\}/.exec(oneCss)?.[1] ?? ''
		expect(rule).toContain('inline-size:auto')
		expect(rule).toContain('max-inline-size:100%')
	})
})
