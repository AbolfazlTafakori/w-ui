// Which customers on a list are chosen: ticked one by one, a whole page, or
// -- offered once a page is ticked, as a mail client offers it -- every
// customer the filter matches, across the pages. Selecting a page is not
// selecting a list of two thousand.
import { computed, ref, watch } from 'vue'
import { api } from '../api.js'
import { notify } from '../store.js'

// page: the page of customers; selected: the chosen ids; query(): what the
// list is asked for; filtered(): whether anything narrows it; filterKey():
// what changes when the filter does.
export function useSelection({ page, selected, query, filtered, filterKey }) {
  const allSelected = computed(
    () => !!page.value?.items.length && page.value.items.every((c) => selected.value.has(c.id)),
  )
  function toggleAll(checked) {
    allMatching.value = false
    selected.value = checked ? new Set(page.value.items.map((c) => c.id)) : new Set()
  }

  // Every customer the filter matches, across every page -- or every customer,
  // with no filter on. Offered once the whole page is ticked, as a mail client
  // offers it: selecting a page is not selecting a list of two thousand.
  const allMatching = ref(false)
  const matchingBusy = ref(false)
  const filtering = computed(filtered)
  const offerAllMatching = computed(
    () => !allMatching.value && allSelected.value && (page.value?.total || 0) > page.value.items.length,
  )
  async function selectAllMatching() {
    matchingBusy.value = true
    try {
      const res = await api.matchingClientIds(query())
      selected.value = new Set(res.ids || [])
      allMatching.value = true
    } catch (err) {
      notify(err.message, 'error')
    } finally {
      matchingBusy.value = false
    }
  }
  function clearSelection() {
    allMatching.value = false
    selected.value = new Set()
  }
  // A different filter is a different list: a selection of every match of the
  // old one would reach customers no longer shown.
  watch(
    filterKey,
    () => { if (allMatching.value) clearSelection() },
  )
  function toggleOne(id, checked) {
    // Unticking one of every match is a hand-picked selection again.
    if (!checked) allMatching.value = false
    const next = new Set(selected.value)
    checked ? next.add(id) : next.delete(id)
    selected.value = next
  }

  return { allSelected, toggleAll, toggleOne, allMatching, matchingBusy, filtering, offerAllMatching, selectAllMatching, clearSelection }
}
