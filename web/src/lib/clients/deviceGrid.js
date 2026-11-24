// A customer's files as a grid: a row for each user, a column for each
// server they are on, as the subscription page shows usage by user and
// tunnel. A user is their name, without regard to case -- "Phone" and
// "phone" are one user, as the panel counts them against the plan.
//
// Rows keep the order users first appear in; columns the order servers do.
// A user's online state is any of their files' being connected now --
// account.online, which the panel fills by the reading its list counts
// with -- and when they were last seen the latest of them.
export function deviceGrid(accounts, interfaces = [], protocol = '') {
  const byId = new Map((interfaces || []).map((i) => [i.id, i]))
  const servers = []
  const seenServer = new Set()
  const users = []
  const byKey = new Map()
  for (const a of accounts || []) {
    if (!seenServer.has(a.interfaceId)) {
      seenServer.add(a.interfaceId)
      const i = byId.get(a.interfaceId)
      servers.push({ id: a.interfaceId, name: i?.name || `#${a.interfaceId}`, protocol: i?.protocol || protocol })
    }
    const key = (a.deviceName || '').toLowerCase()
    let u = byKey.get(key)
    if (!u) {
      u = { key, name: a.deviceName, cells: {}, accounts: [], online: false, seen: null }
      byKey.set(key, u)
      users.push(u)
    }
    // Two files of one user on one server is not a shape the panel makes;
    // were it there, the first is shown and both are removed with the row.
    if (!u.cells[a.interfaceId]) u.cells[a.interfaceId] = a
    u.accounts.push(a)
    if (a.online) u.online = true
    if (a.lastHandshake && (!u.seen || new Date(a.lastHandshake) > new Date(u.seen))) u.seen = a.lastHandshake
  }
  return { servers, users }
}
