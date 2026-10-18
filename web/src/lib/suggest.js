// Suggestions for a copy of a tunnel: the next name, the next range. Only
// suggestions; the operator can type whatever they like over them.

// wg0 -> wg1, ovpn443 -> ovpn444, anything else -> name-2.
export function nextName(name) {
  const m = String(name || '').match(/^(.*?)(\d+)$/)
  if (!m) return `${name}-2`
  return m[1] + String(Number(m[2]) + 1)
}

// freeName steps nextName along until it finds one none of taken is.
export function freeName(name, taken = []) {
  taken = new Set(taken)
  let candidate = nextName(name)
  for (let i = 0; i < 100 && taken.has(candidate); i++) {
    candidate = nextName(candidate)
  }
  return candidate
}

// 10.66.0.0/24 -> 10.66.1.0/24. Only the third octet moves, which is the one
// that is free to move in every range this panel hands out.
export function nextSubnet(cidr) {
  const m = String(cidr || '').match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)(\/\d+)$/)
  if (!m) return cidr || ''
  const third = (Number(m[3]) + 1) % 256
  return `${m[1]}.${m[2]}.${third}.${m[4]}${m[5]}`
}
