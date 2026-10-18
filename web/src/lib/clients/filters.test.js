import { describe, expect, it } from 'vitest'
import { emptyFilters } from '../../components/FilterDrawer.vue'
import { filterChips, filterParams, filtersFromLink, listQuery, withoutChip } from './filters.js'

const t = (k) => k

describe('the customer list’s filters', () => {
  it('open on what a link names, and nothing else', () => {
    expect(filtersFromLink({ status: 'depleting' }).buckets).toEqual(['depleting'])
    expect(filtersFromLink({ status: 'whatever' }).buckets).toEqual([])
    expect(filtersFromLink({ group: 'trial' }).groups).toEqual(['trial'])
  })

  it('send only what is set', () => {
    expect(filterParams(emptyFilters())).toEqual({})
    const f = { ...emptyFilters(), protocols: ['wireguard', 'l2tp'], usedFromGB: 0 }
    expect(filterParams(f)).toEqual({ protocols: 'wireguard,l2tp', usedFromGB: 0 })
  })

  it('ask for the ended as both kinds of ended', () => {
    const q = listQuery({ search: 'ali', status: 'depleted', group: '', sort: 'oldest', filters: emptyFilters() })
    expect(q).toMatchObject({ search: 'ali', status: '', buckets: 'exhausted,expired' })
    expect(listQuery({ status: 'active', filters: emptyFilters() }).status).toBe('active')
  })

  it('show a chip per narrowing, and closing one takes only it off', () => {
    const f = { ...emptyFilters(), protocols: ['wireguard', 'l2tp'], interfaceIds: [3], expiryFrom: '2026-01-01' }
    const chips = filterChips(f, [{ id: 3, name: 'wg0' }], t)
    expect(chips.map((c) => c.key)).toEqual(['protocols', 'protocols', 'interfaceIds', 'expiry'])
    expect(chips[2].text).toBe('wg0')
    const g = withoutChip(f, chips[0])
    expect(g.protocols).toEqual(['l2tp'])
    expect(withoutChip(f, chips[3]).expiryFrom).toBe('')
    expect(f.protocols).toEqual(['wireguard', 'l2tp'])
  })
})
