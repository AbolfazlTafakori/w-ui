import { describe, expect, it } from 'vitest'
import { ago, latencyTone, nodeBody, nodeForm, takesImport, totals, uptime } from './rows.js'

const NOW = Date.parse('2026-10-18T12:00:00Z')

describe('the figures over the nodes', () => {
  it('counts this panel as online and averages only the nodes that answered', () => {
    const s = totals([
      { kind: 'local' },
      { kind: 'remote', reachable: true, latencyMs: 100 },
      { kind: 'remote', reachable: true, latencyMs: 201 },
      { kind: 'xui', reachable: false, latencyMs: 5000 },
    ])
    expect(s).toEqual({ all: 4, online: 3, offline: 1, latency: 151 })
  })

  it('has no latency when no node answered', () => {
    expect(totals([{ kind: 'local' }]).latency).toBeNull()
  })
})

describe('a node’s row', () => {
  it('says how long ago in the largest whole unit', () => {
    expect(ago(null, NOW)).toBe(ago(undefined, NOW))
    expect(ago(new Date(NOW - 30e3).toISOString(), NOW)).toBe(ago(new Date(NOW).toISOString(), NOW))
    expect(ago(new Date(NOW - 5 * 60e3).toISOString(), NOW)).toMatch(/minute/i)
    expect(ago(new Date(NOW - 3 * 3600e3).toISOString(), NOW)).toMatch(/hour/i)
    expect(ago(new Date(NOW - 2 * 86400e3).toISOString(), NOW)).toMatch(/day/i)
  })

  it('writes uptime in days and hours, or hours and minutes', () => {
    expect(uptime(0)).toBe('—')
    expect(uptime(90061)).toBe('1d 1h')
    expect(uptime(3720)).toBe('1h 2m')
  })

  it('judges latency as a person would', () => {
    expect(latencyTone(0)).toBe('grey')
    expect(latencyTone(80)).toBe('green')
    expect(latencyTone(250)).toBe('orange')
    expect(latencyTone(900)).toBe('red')
  })

  it('offers an import only for a panel with users of its own', () => {
    expect(takesImport({ kind: 'xui' })).toBe(true)
    expect(takesImport({ kind: 'pasarguard' })).toBe(true)
    expect(takesImport({ kind: 'remote' })).toBe(true)
    expect(takesImport({ kind: 'local' })).toBe(false)
  })
})

describe('the node form', () => {
  it('never carries a stored secret into an edit', () => {
    const f = nodeForm({ kind: 'xui', name: 'n', address: 'https://a', token: 'secret', password: 'p', dataLimitBytes: 2 * 1024 ** 3 })
    expect(f.token).toBe('')
    expect(f.password).toBe('')
    expect(f.dataLimitGB).toBe(2)
  })

  it('sends numbers, trimmed text and the allowance in bytes', () => {
    const body = nodeBody({ ...nodeForm(), usageCoefficient: '1.5', tlsPin: ' sha256/x ', dataLimitGB: '1', resetDay: '3' })
    expect(body.usageCoefficient).toBe(1.5)
    expect(body.tlsPin).toBe('sha256/x')
    expect(body.dataLimitBytes).toBe(1024 ** 3)
    expect(body.resetDay).toBe(3)
    expect(body.dataLimitGB).toBeUndefined()
  })
})
