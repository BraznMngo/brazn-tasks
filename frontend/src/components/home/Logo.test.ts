import {describe, it, expect} from 'vitest'
import {existsSync, readFileSync, readdirSync, statSync} from 'node:fs'
import {resolve, join} from 'node:path'

// The two files the product's real logo actually lives in, written out as
// literals on purpose. Reading these paths back off the component would make the
// test agree with the component whatever it renders, which is the one failure
// this guards against: somebody pointing the logo at a placeholder again.
const LIGHT_SRC = '/one/logo-light.v1.png'
const DARK_SRC = '/one/logo-dark.v1.png'

// The stand-ins BRA-926 left behind when the upstream Vikunja artwork was removed
// for licence reasons. Each typed the letters "BT" in a rounded square using
// whatever sans-serif the machine had. They are deleted; these names exist here
// so that reintroducing one fails rather than ships.
const PLACEHOLDER_ASSETS = ['logo.svg', 'logo-full.svg', 'logo-full-pride.svg']

const FRONTEND = process.cwd()
const SRC = resolve(FRONTEND, 'src')
const PUBLIC = resolve(FRONTEND, 'public')

// What this file does NOT prove, said plainly rather than left to be assumed:
// it does not exercise the light/dark switching, because no component carrying a
// public-directory `src` can be mounted under vitest here. Importing Ready.vue,
// which has shipped that way since BRA-1444, fails the same way. That behaviour
// was checked in a browser instead, across all six combinations of system theme
// and root class, and the result is recorded in the pull request.

function sourceFilesUnder(dir: string): string[] {
	return readdirSync(dir, {withFileTypes: true}).flatMap(entry => {
		const full = join(dir, entry.name)
		if (entry.isDirectory()) return sourceFilesUnder(full)
		return /\.(vue|ts|js)$/.test(entry.name) ? [full] : []
	})
}

describe('the app logo', () => {
	it('is the real ONE logo in Logo.vue, one file per colour scheme', () => {
		const component = readFileSync(resolve(SRC, 'components/home/Logo.vue'), 'utf8')

		expect(component).toContain(LIGHT_SRC)
		expect(component).toContain(DARK_SRC)
	})

	it('draws no lettering of its own', () => {
		// The placeholder was an inline <svg> with a <text> element. A real logo is
		// a file, so an inline vector in this component means a stand-in is back.
		const component = readFileSync(resolve(SRC, 'components/home/Logo.vue'), 'utf8')

		expect(component).not.toContain('<svg')
		expect(component).not.toContain('<text')
	})

	it('is not reached for anywhere else in the frontend', () => {
		// Logo.vue is not the only place that could import a placeholder --
		// MigrationHandler.vue imported one directly, which is how it kept drawing
		// the "BT" square after the header stopped. Check the whole tree, so the
		// next one is caught wherever it is written.
		const offenders = sourceFilesUnder(SRC).filter(file => {
			const text = readFileSync(file, 'utf8')
			return PLACEHOLDER_ASSETS.some(asset => text.includes(`@/assets/${asset}`))
		})

		expect(offenders).toEqual([])
	})

	it('points at files that are really there, and that are really images', () => {
		// A src that resolves to nothing renders as broken alt text, and reading
		// the component text alone cannot see that: the string still matches.
		// Assert the directory first, so running the tests from somewhere else
		// fails as "public is not here" rather than as "both logos are missing".
		expect(existsSync(PUBLIC), `expected the served directory at ${PUBLIC}`).toBe(true)

		const PNG_SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])

		for (const src of [LIGHT_SRC, DARK_SRC]) {
			const onDisk = resolve(PUBLIC, src.replace(/^\//, ''))

			expect(existsSync(onDisk), `${src} is referenced but not in frontend/public`).toBe(true)
			expect(statSync(onDisk).size).toBeGreaterThan(0)
			expect(
				readFileSync(onDisk).subarray(0, 8).equals(PNG_SIGNATURE),
				`${src} does not begin with a PNG signature`,
			).toBe(true)
		}
	})

	it('has no placeholder asset left in the tree', () => {
		for (const asset of PLACEHOLDER_ASSETS) {
			expect(
				existsSync(resolve(SRC, 'assets', asset)),
				`src/assets/${asset} is a placeholder and should not exist`,
			).toBe(false)
		}
	})
})
