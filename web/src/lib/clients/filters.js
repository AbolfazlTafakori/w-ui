// What the customer list is narrowed by, as the page sends it and shows it.
// Pure functions of the filter state, so the list, the "select every match"
// call and the chips all read it the same way.
import { emptyFilters } from '../../components/FilterDrawer.vue'

// A link from elsewhere may name what to open the list on: a status, from
// the overview's tiles (/clients?status=depleting), or a group, from the
// groups page (/clients?group=trial). Each becomes that one filter, which
// shows as a chip and is cleared like any other.
export const STATUS_LINKS = ['active', 'depleting', 'exhausted', 'expired', 'disabled', 'online']

export function filtersFromLink(query = {}) {
  const f = emptyFilters()
  const s = String(query.status || '')
  if (STATUS_LINKS.includes(s)) f.buckets = [s]
  if (query.group) f.groups = [String(query.group)]
  return f
}

// filterParams is the drawer's filters as query parameters. Sent only when
// set, so an untouched filter leaves the query string as short as it was
// before any of this existed.
export function filterParams(f) {
  const out = {}
  if (f.buckets.length) out.buckets = f.buckets.join(',')
  if (f.protocols.length) out.protocols = f.protocols.join(',')
  if (f.interfaceIds.length) out.interfaceIds = f.interfaceIds.join(',')
  if (f.groups.length) out.groups = f.groups.join(',')
  if (f.expiryFrom) out.expiryFrom = f.expiryFrom
  if (f.expiryTo) out.expiryTo = f.expiryTo
  if (f.usedFromGB !== '') out.usedFromGB = f.usedFromGB
  if (f.usedToGB !== '') out.usedToGB = f.usedToGB
  if (f.renews) out.renews = f.renews
  if (f.hasNote) out.hasNote = f.hasNote
  return out
}

// listQuery is everything the list is asked for by, but the page: the search
// box, the status and group pickers, the sort, and the drawer.
export function listQuery({ search, status, group, sort, filters }) {
  return {
    search,
    status: ['active', 'disabled', 'expired', 'exhausted'].includes(status) ? status : '',
    buckets: status === 'depleted' ? 'exhausted,expired' : undefined,
    group,
    sort,
    ...filterParams(filters),
  }
}

// filterChips is one chip per active filter, each closable on its own. t is
// the translator; interfaces names the tunnels a filter is on.
export function filterChips(f, interfaces, t) {
  const out = []
  for (const b of f.buckets || []) out.push({ key: 'buckets', value: b, color: '', text: t(`filter.bucket.${b}`) })
  for (const p of f.protocols || []) out.push({ key: 'protocols', value: p, color: 'geekblue', text: t(`protocol.${p}`) })
  for (const id of f.interfaceIds || []) out.push({ key: 'interfaceIds', value: id, color: 'green', text: interfaces.find((i) => i.id === id)?.name || `#${id}` })
  for (const g of f.groups || []) out.push({ key: 'groups', value: g, color: 'geekblue', text: `${t('client.group')}: ${g}` })
  if (f.expiryFrom || f.expiryTo) out.push({ key: 'expiry', color: 'orange', text: `${t('client.expires')}: ${f.expiryFrom || '…'} → ${f.expiryTo || '…'}` })
  if (f.usedFromGB !== '' || f.usedToGB !== '') out.push({ key: 'usage', color: 'purple', text: `${t('client.traffic')}: ${f.usedFromGB || 0} → ${f.usedToGB || '∞'} GB` })
  if (f.renews) out.push({ key: 'renews', color: 'gold', text: `${t('client.resetCycle')}: ${f.renews === 'on' ? t('status.enabled') : t('status.disabled')}` })
  if (f.hasNote) out.push({ key: 'hasNote', color: '', text: `${t('client.note')}: ${f.hasNote === 'yes' ? t('filter.has') : t('filter.hasNot')}` })
  return out
}

// withoutChip is the filters with one chip's narrowing taken off.
export function withoutChip(filters, chip) {
  const f = { ...filters }
  switch (chip.key) {
    case 'buckets':
    case 'protocols':
    case 'interfaceIds':
    case 'groups':
      f[chip.key] = f[chip.key].filter((v) => v !== chip.value)
      break
    case 'expiry':
      f.expiryFrom = ''
      f.expiryTo = ''
      break
    case 'usage':
      f.usedFromGB = ''
      f.usedToGB = ''
      break
    default:
      f[chip.key] = ''
  }
  return f
}
