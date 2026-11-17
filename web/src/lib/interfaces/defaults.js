// A new tunnel's suggested name, subnet and port, clear of the tunnels already
// on the panel. The form used to offer the same three every time -- wg0,
// 10.66.0.0/16, 51820 -- so the second tunnel of a kind was refused until
// each was changed by hand.

// The protocols whose port every built-in client dials: there is one such
// tunnel to a server, and its port is not to be moved.
const FIXED_PORT = new Set(['l2tp', 'pptp', 'ikev2'])

function ipv4(s) {
  const p = String(s).split('.').map(Number)
  if (p.length !== 4 || p.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) return null
  return ((p[0] << 24) >>> 0) + (p[1] << 16) + (p[2] << 8) + p[3]
}

// A subnet as its first and last address, or null.
function range(cidr) {
  const [addr, bits] = String(cidr || '').trim().split('/')
  const a = ipv4(addr)
  const b = Number(bits)
  if (a === null || !Number.isInteger(b) || b < 0 || b > 32) return null
  const size = 2 ** (32 - b)
  const start = Math.floor(a / size) * size
  return [start, start + size - 1]
}

const overlaps = (x, y) => x[0] <= y[1] && y[0] <= x[1]

function nextName(base, taken) {
  if (!taken.has(base)) return base
  const m = /^(.*?)(\d+)$/.exec(base)
  const stem = m ? m[1] : base
  for (let n = m ? Number(m[2]) + 1 : 1; ; n++) {
    if (!taken.has(stem + n)) return stem + n
  }
}

// The next /prefix of the same size after base, stepping the second octet for
// a /16 and the third for a /24, within 10.0.0.0/8: the private range every
// default is in.
function nextSubnet(base, used) {
  const r = range(base)
  if (!r) return base
  const bits = Number(base.split('/')[1])
  const size = r[1] - r[0] + 1
  const lo = ipv4('10.0.0.0')
  const hi = ipv4('10.255.255.255')
  for (let start = r[0]; start + size - 1 <= hi; start += size) {
    const cand = [start, start + size - 1]
    if (start >= lo && !used.some((u) => overlaps(u, cand))) {
      return [start >>> 24, (start >>> 16) & 255, (start >>> 8) & 255, start & 255].join('.') + '/' + bits
    }
  }
  return base
}

function nextPort(base, used) {
  for (let p = base; p <= 65535; p++) {
    if (!used.has(p)) return p
  }
  return base
}

// freeDefaults is base (a protocol's name, listenPort, subnet and mtu) moved
// clear of existing: a name no tunnel has, a subnet that overlaps none, and,
// for a protocol whose port may move, a port no tunnel listens on, neither
// as its own nor as a stealth transport's.
export function freeDefaults(protocol, base, existing) {
  const list = Array.isArray(existing) ? existing : []
  const names = new Set(list.map((i) => i.name))
  const subnets = list.map((i) => range(i.subnet)).filter(Boolean)
  const ports = new Set()
  for (const i of list) {
    if (i.listenPort) ports.add(Number(i.listenPort))
    if (i.stealth?.port) ports.add(Number(i.stealth.port))
  }
  return {
    ...base,
    name: nextName(base.name, names),
    subnet: nextSubnet(base.subnet, subnets),
    listenPort: FIXED_PORT.has(protocol) ? base.listenPort : nextPort(base.listenPort, ports),
  }
}
