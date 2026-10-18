// @vitest-environment node
// Every Ant icon a page names is one the panel carries. A name it does not
// carry draws nothing: a menu entry with a gap where its icon should be,
// and nothing anywhere says so.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { antIcons } from './anticons.js'

const root = fileURLToPath(new URL('..', import.meta.url))

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return files(path)
    return /\.(vue|js)$/.test(name) && !name.endsWith('.test.js') && name !== 'anticons.js' ? [path] : []
  })
}

describe('Ant icons', () => {
  it('are all ones the panel carries', () => {
    const missing = []
    for (const file of files(root)) {
      const text = readFileSync(file, 'utf8')
      for (const m of text.matchAll(/['"]([A-Z][A-Za-z]+(?:Outlined|Filled|TwoTone))['"]/g)) {
        if (!antIcons[m[1]]) missing.push(`${m[1]} in ${file.slice(root.length)}`)
      }
    }
    expect([...new Set(missing)]).toEqual([])
  })
})
