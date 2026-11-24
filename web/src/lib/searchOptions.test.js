import { describe, it, expect } from 'vitest'
import { searchOptions } from './searchOptions.js'

const opts = [
  { value: 0, label: 'you', tags: [{ text: 'Owner' }] },
  { value: 3, label: 'ui-admin', tags: [{ text: 'Panel administrator' }] },
  { value: 7, label: 'Res13', tags: [{ text: 'Reseller' }], note: 'Disabled' },
]

describe('searchOptions', () => {
  it('keeps everything for nothing typed', () => {
    expect(searchOptions(opts, '  ')).toBe(opts)
  })
  it('matches the name without regard to case', () => {
    expect(searchOptions(opts, 'res1').map((o) => o.value)).toEqual([7])
  })
  it('matches a role tag and a note', () => {
    expect(searchOptions(opts, 'administrator').map((o) => o.value)).toEqual([3])
    expect(searchOptions(opts, 'disabled').map((o) => o.value)).toEqual([7])
  })
  it('keeps nothing that does not match', () => {
    expect(searchOptions(opts, 'zzz')).toEqual([])
  })
})
