// @vitest-environment node
// Every toast asks for a kind the toast has a look for. Anything else is
// drawn as plain info: a success that does not read as one, a warning that
// does not stand out, and nothing anywhere says so.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const root = fileURLToPath(new URL('..', import.meta.url))
const KINDS = ['info', 'success', 'warning', 'error']

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return files(path)
    return /\.(vue|js)$/.test(name) && !name.endsWith('.test.js') ? [path] : []
  })
}

describe('toasts', () => {
  it('ask only for the kinds the toast draws', () => {
    const wrong = []
    for (const file of files(root)) {
      const text = readFileSync(file, 'utf8')
      for (const m of text.matchAll(/notify\([^;]*?,\s*'([a-z]+)'\s*\)/g)) {
        if (!KINDS.includes(m[1])) wrong.push(`'${m[1]}' in ${file.slice(root.length)}`)
      }
    }
    expect(wrong).toEqual([])
  })
})
