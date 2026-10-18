import { describe, expect, it } from 'vitest'
import { checkedAgo, countryFlag, countryName, exportText, menuFor, modeLabel, parseImport } from './rows.js'

const NOW = Date.parse('2026-10-18T12:00:00Z')

describe('an outbound’s row', () => {
  it('names the test modes, and any other in capitals', () => {
    expect(modeLabel('tcp')).toBe('TCP')
    expect(modeLabel('quic')).toBe('QUIC')
  })

  it('says when a figure was taken, and nothing when it never was', () => {
    expect(checkedAgo({}, NOW)).toBe('')
    expect(checkedAgo({ lastCheckAt: new Date(NOW - 10e3).toISOString() }, NOW)).toMatch(/now/i)
    expect(checkedAgo({ lastCheckAt: new Date(NOW - 5 * 60e3).toISOString() }, NOW)).toMatch(/minute/i)
    expect(checkedAgo({ lastCheckAt: new Date(NOW - 2 * 3600e3).toISOString() }, NOW)).toMatch(/hour/i)
  })

  it('draws a flag and names the country only for a two-letter code', () => {
    expect(countryFlag('de')).toBe('🇩🇪')
    expect(countryFlag('DEU')).toBe('')
    expect(countryName('DE', 'en')).toBe('Germany')
    expect(countryName('', 'en')).toBe('')
  })

  it('offers moves that make sense where the row is, and nothing destructive on a built-in', () => {
    const keys = (items) => items.filter((m) => !m.divider).map((m) => (m.disabled ? `${m.key}-` : m.key))
    expect(keys(menuFor({ enabled: true }, 0, 3))).toEqual(['up-', 'down', 'reset', 'toggle', 'del'])
    expect(keys(menuFor({ enabled: true }, 2, 3))).toEqual(['top', 'up', 'down-', 'reset', 'toggle', 'del'])
    expect(keys(menuFor({ enabled: true, builtin: true }, 1, 3))).toEqual(['top', 'up', 'down', 'reset', 'toggle-', 'del-'])
  })
})

describe('export and import', () => {
  it('leaves out the built-ins and empty fields', () => {
    const out = JSON.parse(exportText([
      { tag: 'direct', builtin: true },
      { tag: 'wg', kind: 'wireguard', enabled: true, address: '1.2.3.4:51820', note: '' },
    ]))
    expect(out).toEqual([{ tag: 'wg', kind: 'wireguard', enabled: true, address: '1.2.3.4:51820' }])
  })

  it('takes an array or an object holding one, and nothing else', () => {
    expect(parseImport('[{"tag":"a"}, 3, null]')).toEqual([{ tag: 'a' }])
    expect(parseImport('{"outbounds":[{"tag":"b"}]}')).toEqual([{ tag: 'b' }])
    expect(parseImport('{"tag":"c"}')).toBeNull()
    expect(parseImport('not json')).toBeNull()
  })
})
