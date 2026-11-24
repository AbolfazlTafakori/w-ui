// The options a typed search keeps: matched, without regard to case, on the
// name, the note and every tag -- so "reseller" finds them all. Nothing
// typed keeps every option.
export function searchOptions(options, query) {
  const q = String(query || '').trim().toLowerCase()
  if (!q) return options
  return options.filter((o) =>
    [o.label, o.note, ...(o.tags || []).map((x) => x.text)].some((s) => String(s || '').toLowerCase().includes(q)),
  )
}
