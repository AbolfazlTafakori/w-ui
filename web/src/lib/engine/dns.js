// The engine's DNS: the public resolvers offered as presets, and how a
// server row is made from the form and from a preset. Pure functions.

export const PRESETS = [
  { name: 'Google DNS', tags: ['UDP'], data: ['8.8.8.8', '8.8.4.4', '2001:4860:4860::8888', '2001:4860:4860::8844'] },
  { name: 'Cloudflare DNS', tags: ['UDP'], data: ['1.1.1.1', '1.0.0.1', '2606:4700:4700::1111', '2606:4700:4700::1001'] },
  { name: 'AdGuard DNS', tags: ['UDP'], data: ['94.140.14.14', '94.140.15.15', '2a10:50c0::ad1:ff', '2a10:50c0::ad2:ff'] },
  { name: 'AdGuard Family DNS', tags: ['UDP', 'Family'], data: ['94.140.14.15', '94.140.15.16', '2a10:50c0::bad1:ff', '2a10:50c0::bad2:ff'] },
  { name: 'Cloudflare Family DNS', tags: ['UDP', 'Family'], data: ['1.1.1.3', '1.0.0.3', '2606:4700:4700::1113', '2606:4700:4700::1003'] },
  { name: 'Cloudflare DoH', tags: ['DoH'], data: ['https://cloudflare-dns.com/dns-query'] },
  { name: 'Google DoH', tags: ['DoH'], data: ['https://dns.google/dns-query'] },
  { name: 'Quad9 Secure DoH', tags: ['DoH', 'Malware'], data: ['https://dns.quad9.net/dns-query'] },
  { name: 'AdGuard DoH', tags: ['DoH', 'Ads'], data: ['https://dns.adguard-dns.com/dns-query'] },
  { name: 'Control D Ads DoH', tags: ['DoH', 'Ads'], data: ['https://freedns.controld.com/p2'] },
  { name: 'Control D Family DoH', tags: ['DoH', 'Family'], data: ['https://freedns.controld.com/p4'] },
]

// A preset's addresses as server rows: every domain, the default port.
export function presetServers(data) {
  return data.map((address) => ({ address, port: 0, domains: [], skipFallback: false }))
}

// The server form as a row; null while it has no address.
export function serverRow(m) {
  const address = (m.address || '').trim()
  if (!address) return null
  return { address, port: Number(m.port) || 0, domains: m.domains || [], skipFallback: !!m.skipFallback }
}
