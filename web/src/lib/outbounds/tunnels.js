import { api } from '../api.js'
import { t } from '../store.js'

// The tunnels an outbound's or a balancer's form can send through it, as
// MultiSelect options. Ids travel as strings in the select and as numbers
// to the API.

export async function tunnelOptions() {
  const res = await api.get('/api/interfaces', { background: true }).catch(() => [])
  const list = Array.isArray(res) ? res : res.items || []
  return list.map((i) => ({ value: String(i.id), label: i.name, tags: [{ text: t(`protocol.${i.protocol}`), kind: 'proto' }] }))
}

export function tunnelIds(values) {
  return values.map((v) => Number(v)).filter((n) => Number.isInteger(n) && n > 0)
}
