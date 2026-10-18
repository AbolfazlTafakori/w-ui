// @vitest-environment node
// Every design token a stylesheet leans on without a fallback is defined
// somewhere. An undefined one fails without a sound: the whole declaration
// is dropped, and a card loses its border, a corner its radius, a drawer
// its slide -- nothing in the console, nothing in the build.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const root = fileURLToPath(new URL('..', import.meta.url))

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return files(path)
    return /\.(vue|css|js)$/.test(name) ? [path] : []
  })
}

describe('design tokens', () => {
  it('are all defined where they are used bare', () => {
    const sources = files(root).map((f) => [f, readFileSync(f, 'utf8')])
    const defined = new Set()
    for (const [, text] of sources) {
      for (const m of text.matchAll(/(--[a-z0-9-]+)['"]?\s*:/g)) defined.add(m[1])
    }
    const missing = []
    for (const [file, text] of sources) {
      for (const m of text.matchAll(/var\((--[a-z0-9-]+)\s*\)/g)) {
        if (!defined.has(m[1])) missing.push(`${m[1]} in ${file.slice(root.length)}`)
      }
    }
    expect(missing).toEqual([])
  })
})
