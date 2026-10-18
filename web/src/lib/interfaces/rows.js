// How a tunnel reads in its list: its protocol and transport tags, what it is
// moving, the order the headers sort it in, the totals over the list, its row
// menu, and the shape it is exported in. Pure functions of the tunnels.
import { bytes } from '../format.js'
import { store, t } from '../store.js'

// What a tunnel carries its traffic over, as the table tags it: OpenVPN's
// choice, PPTP's TCP (its data is GRE), OpenConnect's TLS on TCP with DTLS
// on UDP beside it, and UDP for the rest. A 3x-ui inbound shows what it is
// there: VLESS over TCP with Reality, say.
export function protoTag(i) {
  return i.protocol === 'xray' ? (i.xui?.kind || 'xray') : i.protocol
}
export function transportTag(i) {
  if (i.protocol === 'xray') {
    const net = (i.xui?.network || 'tcp').toUpperCase()
    const sec = i.xui?.security && i.xui.security !== 'none' ? ' · ' + i.xui.security : ''
    return net + sec
  }
  if (i.protocol === 'openvpn') return (i.openvpn?.transport || 'udp').toUpperCase()
  if (i.protocol === 'pptp') return 'TCP'
  if (i.protocol === 'openconnect') return i.openconnect?.dtls ? 'TCP + UDP' : 'TCP'
  return 'UDP'
}

// What a tunnel's files are moving right now, each way; the server leaves
// it out while they move nothing.
export function speedOf(i) {
  return i.speed ? (i.speed.up || 0) + (i.speed.down || 0) : 0
}
export const speedActive = (i) => speedOf(i) > 0
export function speedText(i) {
  const rate = (v) => `${bytes(v || 0, store.locale)}/s`
  return `↑ ${rate(i.speed.up)} / ↓ ${rate(i.speed.down)}`
}

export const cmpText = (a, b) => String(a || '').localeCompare(String(b || ''), undefined, { numeric: true, sensitivity: 'base' })
const sorters = {
  id: (a, b) => a.id - b.id,
  remark: (a, b) => cmpText(a.name, b.name),
  node: (a, b) => cmpText(a.nodeName, b.nodeName),
  port: (a, b) => a.listenPort - b.listenPort,
  protocol: (a, b) => cmpText(a.protocol, b.protocol),
  clients: (a, b) => (a.clients || 0) - (b.clients || 0),
  traffic: (a, b) => (a.usedBytes || 0) - (b.usedBytes || 0),
  speed: (a, b) => speedOf(a) - speedOf(b),
  expiry: () => 0,
}

// Their sortable headers. A click goes ascending, another descending, a
// third back to the order the server gave, exactly as Ant's Table cycles.
export function nextSort(cur, key) {
  if (cur.key !== key) return { key, dir: 'asc' }
  if (cur.dir === 'asc') return { key, dir: 'desc' }
  return { key: '', dir: '' }
}
export function sortRows(rows, { key, dir }) {
  if (!key || !sorters[key]) return rows
  const out = [...rows].sort(sorters[key])
  return dir === 'desc' ? out.reverse() : out
}

// The strip the classic panel puts above its inbound table: what the whole
// set is carrying.
export function totals(list) {
  const sum = (f) => list.reduce((a, i) => a + (i[f] || 0), 0)
  return {
    used: sum('usedBytes'), up: sum('upBytes'), down: sum('downBytes'), clients: sum('clients'),
    devices: sum('devices'), allocated: sum('allocated'), capacity: sum('capacity'), count: list.length,
  }
}

// Their row menu, item for item: what the classic panel shows for an inbound
// that carries many customers (every one of ours does). Restart is the one
// addition; a kernel tunnel can be bounced, an Xray inbound cannot.
export function rowItems(i) {
  const items = [
    { key: 'urls', icon: 'ExportOutlined', label: t('iface.menu.exportUrls') },
    { key: 'export', icon: 'CopyOutlined', label: t('iface.menu.exportInbound') },
    { key: 'reset', icon: 'RetweetOutlined', label: t('outbound.resetTraffic') },
    { key: 'clone', icon: 'BlockOutlined', label: t('interface.clone') },
    { key: 'restart', icon: 'ReloadOutlined', label: t('interface.restart') },
    { key: 'attachExisting', icon: 'UsergroupAddOutlined', label: t('iface.menu.attachExisting') },
  ]
  if (i.protocol === 'openvpn') items.splice(2, 0, { key: 'profile', icon: 'ExportOutlined', label: t('interface.downloadProfile') })
  if (i.clients > 0) {
    items.push(
      { key: 'attachTo', icon: 'UsergroupAddOutlined', label: t('iface.menu.attachTo') },
      { key: 'detach', icon: 'UsergroupDeleteOutlined', label: t('iface.menu.detachClients') },
      { key: 'group', icon: 'TagsOutlined', label: t('iface.menu.addToGroup') },
      { divider: true },
      { key: 'delAll', icon: 'UsergroupDeleteOutlined', label: t('iface.menu.delAllClients'), danger: true },
    )
  } else {
    items.push({ divider: true })
  }
  items.push({ key: 'delete', icon: 'DeleteOutlined', label: t('action.delete'), danger: true })
  // A 3x-ui inbound is changed on its own panel: here it only holds
  // customers, and is deleted only once it is gone there.
  if (i.protocol === 'xray') {
    const keep = new Set(['urls', 'attachExisting', 'attachTo', 'detach', 'group', 'delAll'])
    if (i.xui?.gone) keep.add('delete')
    return items.filter((m) => m.divider || keep.has(m.key))
  }
  return items
}

// The General Actions menu over the list.
export function generalItems() {
  return [
    { key: 'import', icon: 'ImportOutlined', label: t('iface.menu.import') },
    { key: 'export', icon: 'ExportOutlined', label: t('iface.menu.exportUrls') },
    { key: 'exportAll', icon: 'CopyOutlined', label: t('iface.menu.exportAll') },
    { key: 'resetAll', icon: 'ReloadOutlined', label: t('iface.menu.resetAll') },
  ]
}

// Export: the tunnel as the JSON the API creates one from -- what "Import an
// Interface" reads back, field for field. The stored AmneziaWG and OpenVPN
// blocks are not part of it: the create call takes neither, refuses a field
// it does not know, and makes both afresh; the transport, the one part of
// them it does take, travels on its own.
export function exportable(i) {
  return {
    name: i.name, protocol: i.protocol, enabled: i.enabled, listenPort: i.listenPort, subnet: i.subnet,
    endpointHost: i.endpointHost, mtu: i.mtu, dns: i.dns, natInterface: i.natInterface, mode: i.mode,
    transport: i.protocol === 'openvpn' && i.openvpn?.transport ? i.openvpn.transport : undefined,
    // Top level, as the API takes it: a tunnel made from this keeps the key
    // every device already has.
    psk: i.protocol === 'l2tp' ? i.l2tp?.psk : undefined,
    l2tpPlain: i.protocol === 'l2tp' && i.l2tp?.allowPlain ? true : undefined,
  }
}

// What an OpenConnect tunnel's form sends: a secret left empty is drawn by
// the server.
export function ocPayload(oc) {
  if (!oc) return undefined
  const out = { dtls: !!oc.dtls, camouflage: !!oc.camouflage, usePanelCert: !!oc.usePanelCert }
  if ((oc.camouflageSecret || '').trim()) out.camouflageSecret = oc.camouflageSecret.trim()
  return out
}
