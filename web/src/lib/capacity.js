// Capacity, as the operator's pages show it: their card, the units a change
// will take, and the server's "not enough" answers in the operator's language.

// The server's capacity refusals, in English with their numbers. Each is
// matched where it stands in the message (a prefix such as "No users left:"
// may come first) and said again through the catalog.
const REFUSALS = [
  {
    re: /this needs (\d+) users and you have (\d+) left/i,
    key: 'cap.err.consumable',
    vars: (m) => ({ need: Number(m[1]), left: Number(m[2]) }),
  },
  {
    re: /this needs (\d+) users at once and (\d+) of your (\d+) are free/i,
    key: 'cap.err.open',
    vars: (m) => ({ need: Number(m[1]), free: Number(m[2]), total: Number(m[3]) }),
  },
  { re: /you have no tokens left to issue/i, key: 'cap.err.tokens' },
  { re: /a reseller's API tokens are issued by their admin/i, key: 'cap.err.resellerTokens' },
  { re: /you have no API tokens/i, key: 'cap.err.noTokens' },
]

// The codes the server gives these refusals, for those whose sentence has
// no numbers to read back.
const BY_CODE = {
  'tokens.none': 'cap.err.noTokens',
  'tokens.insufficient': 'cap.err.tokens',
  'tokens.resellerCannotIssue': 'cap.err.resellerTokens',
}

// capacityMessage is the server's message said in the operator's language
// when it is a capacity refusal, or the message as it came.
export function capacityMessage(message, t, code = '') {
  const text = String(message || '')
  if (BY_CODE[code]) return t(BY_CODE[code])
  for (const r of REFUSALS) {
    const m = text.match(r.re)
    if (m) return t(r.key, r.vars ? r.vars(m) : undefined)
  }
  return text
}

// cardOf is an operator's capacity card: users and tokens, each as the line
// the card reads and how full it is (0..1), or null where they have none.
//
//   open:       "62 of 100 users"     -- seats held now, out of what was given
//   consumable: "38 units left"       -- what can still be spent
//   tokens:     "5 of 20 tokens"      -- issued, out of what was given
export function cardOf(me) {
  const out = { users: null, tokens: null }
  const u = me?.userCapacity
  if (u) {
    const given = Number(u.granted) || 0
    const busy = (Number(u.used) || 0) + (Number(u.handed) || 0)
    out.users = {
      mode: u.mode,
      given,
      used: Number(u.used) || 0,
      handed: Number(u.handed) || 0,
      free: Math.max(Number(u.free) || 0, 0),
      fill: given > 0 ? Math.min(busy / given, 1) : 1,
    }
  }
  const tk = me?.tokenCapacity
  if (tk && (Number(tk.granted) > 0 || Number(tk.held) > 0)) {
    const given = Number(tk.granted) || 0
    const used = Number(tk.used) || 0
    out.tokens = { given, used, held: Number(tk.held) || 0, fill: given > 0 ? Math.min(used / given, 1) : 1 }
  }
  return out
}

// unitsFor is what a change will take from the operator's users, for the
// line under the form's button; 0 when it takes nothing.
//
//   create:  consumable spends a unit per user; open holds a seat per user
//   extend:  consumable spends a unit per user again; open is free while the
//            customer is still on, and takes the seats back when they ended
//   grow:    the users added, either way
export function unitsFor(me, change) {
  const u = me?.userCapacity
  if (!u) return 0
  const users = Math.max(Number(change.users) || 0, 0)
  switch (change.kind) {
    case 'create':
      return users
    case 'extend':
      if (u.mode === 'consumable') return users
      return change.ended ? users : 0
    case 'grow':
      return Math.max(users - (Number(change.from) || 0), 0)
  }
  return 0
}
