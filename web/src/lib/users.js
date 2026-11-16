// A user in a plan for several is a file name: "user-2" is that user's file
// on every tunnel the plan reaches. Their number is the place of that name
// among the customer's, in the order the files were issued -- the counting
// the panel's server uses for the customer's page, each user's own link and
// key rotation, so "User 2" is the same person everywhere.

// userNumbers maps each file's id to its user's number.
export function userNumbers(accounts) {
  const place = new Map()
  const byIssue = [...(accounts || [])].sort((a, b) => a.id - b.id)
  for (const a of byIssue) {
    const k = String(a.deviceName || '').trim().toLowerCase()
    if (!place.has(k)) place.set(k, place.size + 1)
  }
  const out = {}
  for (const a of byIssue) out[a.id] = place.get(String(a.deviceName || '').trim().toLowerCase())
  return out
}
