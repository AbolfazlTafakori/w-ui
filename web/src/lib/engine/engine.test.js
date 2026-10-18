import { describe, expect, it } from 'vitest'
import { PRESETS, presetServers, serverRow } from './dns.js'
import { programsText, sliceOf } from './template.js'

describe('the engine’s DNS', () => {
  it('turns a preset into servers for every domain on the default port', () => {
    const rows = presetServers(PRESETS.find((p) => p.name === 'Cloudflare DoH').data)
    expect(rows).toEqual([{ address: 'https://cloudflare-dns.com/dns-query', port: 0, domains: [], skipFallback: false }])
  })

  it('makes a server row only from an address, with the port as a number', () => {
    expect(serverRow({ address: '  ' })).toBeNull()
    expect(serverRow({ address: ' 1.1.1.1 ', port: '5353', domains: ['example.com'], skipFallback: 1 })).toEqual({
      address: '1.1.1.1', port: 5353, domains: ['example.com'], skipFallback: true,
    })
  })
})

describe('the engine’s template', () => {
  const doc = { inbounds: [1], outbounds: [2], routing: { rules: [] }, balancers: [3] }

  it('shows the part each section is about', () => {
    expect(sliceOf(doc, 'inbounds')).toEqual({ inbounds: [1] })
    expect(sliceOf(doc, 'routing')).toEqual({ routing: { rules: [] }, balancers: [3] })
    expect(sliceOf(doc, 'complete')).toBe(doc)
    expect(sliceOf(null, 'complete')).toBeNull()
  })

  it('lays the programs out under a heading each, empty ones included', () => {
    expect(programsText({ routing: 'r' }).split('\n')).toEqual(['# enforcement', '', '', '# routing', 'r', '', '# shaping', ''])
  })
})
