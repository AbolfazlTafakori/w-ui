// What an operator does to customers from their list: switch one on or off,
// reset or rekey one, delete one, and act on a selection -- each asked for
// first when it cannot be walked back. The list page hands in its state and
// how to reload; this keeps the rules.
import { computed, ref } from 'vue'
import { api } from '../api.js'
import { t, tn, notify } from '../store.js'

// The page's two menus: one for the page, one for a selection. Same items,
// same order, same dividers.
export function moreItems(selectedCount) {
  return selectedCount
    ? [
        { key: 'attach', label: t('client.menu.attach'), icon: 'UsergroupAddOutlined' },
        { key: 'detach', label: t('client.menu.detach'), icon: 'UsergroupDeleteOutlined', danger: true },
        { key: 'group', label: t('client.addToGroup'), icon: 'TagsOutlined' },
        { key: 'ungroup', label: t('client.ungroup'), icon: 'UngroupOutlined', danger: true },
        { divider: true },
        { key: 'enable', label: t('action.enable'), icon: 'CheckCircleOutlined' },
        { key: 'disable', label: t('action.disable'), icon: 'StopOutlined', danger: true },
        { key: 'time', label: t('client.menu.time'), icon: 'FieldTimeOutlined' },
        { key: 'traffic', label: t('client.menu.traffic'), icon: 'DatabaseOutlined' },
        { key: 'adjust', label: t('client.adjust'), icon: 'ClockCircleOutlined' },
        { key: 'reset', label: t('outbound.resetTraffic'), icon: 'RetweetOutlined' },
        { key: 'rotate', label: t('client.rotateKeys'), icon: 'KeyOutlined', danger: true },
        { key: 'subLinks', label: t('client.menu.subLinks'), icon: 'LinkOutlined' },
      ]
    : [
        { key: 'batch', label: t('client.menu.bulk'), icon: 'UsergroupAddOutlined' },
        { key: 'export', label: t('client.export'), icon: 'DownloadOutlined' },
        { key: 'import', label: t('client.menu.import'), icon: 'UploadOutlined' },
        { key: 'resetAll', label: t('client.resetAll'), icon: 'RetweetOutlined' },
        // Here too, and off: with nothing selected nothing is changed, and
        // the item says what to do instead.
        { key: 'time', label: t('client.menu.time'), icon: 'FieldTimeOutlined', disabled: true, title: t('xt.selectFirst') },
        { key: 'traffic', label: t('client.menu.traffic'), icon: 'DatabaseOutlined', disabled: true, title: t('xt.selectFirst') },
        { divider: true },
        { key: 'purgeDepleted', label: t('client.menu.delDepleted'), icon: 'RestOutlined', danger: true },
        { key: 'purgeUnattached', label: t('client.menu.delOrphans'), icon: 'DisconnectOutlined', danger: true },
      ]
}

// useClientActions binds the actions to a list page.
//
// page: the page of customers; stats: the counts above it; selected: the
// chosen ids; busy: the page's busy flag; load, loadGroups: reload; settled:
// reload quietly after a change; open: what the menu opens on the page --
// { dialog(kind), subLinks(), importer(), batch(), extend(kind) }.
export function useClientActions({ page, stats, selected, busy, load, loadGroups, settled, open }) {
  const ids = () => [...selected.value]
  const clear = () => { selected.value = new Set() }

  async function guard(fn, successKey) {
    try {
      await fn()
      if (successKey) notify(t(successKey), 'success')
      await Promise.all([load(), loadGroups()])
    } catch (err) {
      notify(err.message, 'error')
    }
  }

  // Rows with a change in flight, so the switch can spin on that row alone.
  const pending = ref(new Set())
  function hold(id) {
    pending.value = new Set(pending.value).add(id)
  }
  function release(id) {
    const next = new Set(pending.value)
    next.delete(id)
    pending.value = next
  }

  // Toggling one customer moves the switch immediately and patches that row
  // from the reply. It used to await the request and then refetch the whole
  // list and the group list, which meant the switch stayed where it was for
  // the entire round trip -- a control that does not move when clicked reads
  // as broken, and the operator clicks it again.
  async function setEnabled(c, on) {
    const was = c.status
    const next = on ? 'active' : 'disabled'
    if (was === next) return
    c.status = next // optimistic: the switch moves now
    hold(c.id)
    try {
      // Patched in place rather than reloading: refetching would also reorder
      // or re-page the list under the operator's cursor.
      Object.assign(c, await api.updateClient(c.id, { status: next }))
    } catch (err) {
      c.status = was // put it back where it was
      notify(err.message, 'error')
    } finally {
      release(c.id)
      // The row is right the moment the switch moves; the counts above it
      // are not, and they are what an operator checks to see the change took.
      settled()
    }
  }

  // Asked first: { title, body, subject, consequences, confirmLabel, requireText, run }.
  const ask = ref(null)
  const confirmAnd = (spec) => { ask.value = spec }
  async function runConfirmed() {
    const spec = ask.value
    if (!spec) return
    busy.value = true
    try {
      await spec.run()
    } finally {
      busy.value = false
      ask.value = null
    }
  }

  // Fresh credentials for every file this customer holds. Asked for plainly:
  // what they are holding stops working, and they have to be told.
  function rotateKeysFor(c) {
    confirmAnd({
      title: t('client.rotateKeys'),
      subject: c.name,
      body: t('client.rotateAllConfirm', { name: c.name }),
      consequences: [t('client.rotateAllHint')],
      confirmLabel: t('client.rotateKeys'),
      run: async () => {
        // Everything this customer holds: every file, and the link they
        // fetch them from.
        const res = await api.rotateKeys(c.id, { subToken: true })
        notify(t('client.rotateKeysDone', { n: res?.rotated || 0 }), 'success')
        await load()
      },
    })
  }

  // Zeroing one customer's counters. Fast on a small panel and not fast on a
  // large one, and until it came back the button gave nothing at all -- so
  // the operator's second click reset the traffic they had just reset.
  async function resetOne(c) {
    hold(c.id)
    try {
      await api.resetTraffic(c.id)
      c.rxBytes = 0
      c.txBytes = 0
      notify(t('client.trafficReset'), 'success')
      settled()
    } catch (err) {
      notify(err.message, 'error')
      await load()
    } finally {
      release(c.id)
    }
  }

  const removeOne = (c) =>
    confirmAnd({
      title: t('client.confirmDeleteTitle'),
      body: t('client.confirmDeleteBody'),
      subject: c.name,
      // Said because it is what an operator forgets: the customer's config
      // stops working the moment this runs, and reissuing it does not bring
      // the old one back.
      consequences: [
        tn('client.consequenceDevices', c.accounts?.length ?? 0),
        t('client.consequenceConfigs'),
        t('client.consequenceUsage'),
      ],
      confirmLabel: t('action.delete'),
      run: () => guard(() => api.deleteClient(c.id), 'client.deleted'),
    })

  const bulkRun = (action) => guard(async () => {
    await api.bulkClients(action, ids())
    clear()
  }, 'client.bulkDone')

  function bulk(action) {
    if (!selected.value.size) return
    const n = selected.value.size
    if (action !== 'delete') return bulkRun(action)
    return confirmAnd({
      title: t('client.confirmDeleteManyTitle'),
      body: t('client.confirmDeleteBody'),
      subject: tn('client.nCustomers', n),
      consequences: [t('client.consequenceConfigs'), t('client.consequenceUsage')],
      confirmLabel: t('action.delete'),
      // Typed out, because a selection of forty is not something the eye
      // checks and this is the one action that cannot be walked back.
      requireText: n >= 5 ? String(n) : '',
      run: () => bulkRun(action),
    })
  }

  // Every file of every selected customer, one customer at a time through the
  // same call a row's key button makes, so each is rotated whole -- files and
  // link together -- and one failing leaves the rest done rather than none.
  function rotateSelected() {
    const n = selected.value.size
    confirmAnd({
      title: t('client.rotateKeys'),
      body: t('client.bulkRotateBody'),
      subject: tn('client.nCustomers', n),
      consequences: [t('client.rotateAllHint')],
      confirmLabel: t('client.rotateKeys'),
      // Typed out past a handful, as a bulk delete is.
      requireText: n >= 5 ? String(n) : '',
      run: async () => {
        const failed = []
        let rotated = 0
        for (const id of ids()) {
          try {
            const res = await api.rotateKeys(id, { subToken: true })
            rotated += res?.rotated || 0
          } catch (err) {
            const c = page.value?.items.find((x) => x.id === id)
            failed.push(`${c?.name || id}: ${err.message}`)
          }
        }
        clear()
        if (failed.length) notify(`${t('client.rotateKeysDone', { n: rotated })}\n${failed.join('\n')}`, 'error')
        else notify(t('client.rotateKeysDone', { n: rotated }), 'success')
        await load()
      },
    })
  }

  // Deleting a whole class of customers: the ended ones, or those on no
  // tunnel. Separate dialogs naming separate counts.
  function purge(key) {
    const status = key === 'purgeDepleted' ? 'depleted' : 'unattached'
    const count = key === 'purgeDepleted' ? (stats.value?.exhausted ?? 0) + (stats.value?.expired ?? 0) : null
    confirmAnd({
      title: t(key === 'purgeDepleted' ? 'client.menu.delDepletedTitle' : 'client.menu.delOrphansTitle'),
      body: t(key === 'purgeDepleted' ? 'client.menu.delDepletedBody' : 'client.menu.delOrphansBody'),
      subject: count == null ? '' : tn('client.nCustomers', count),
      consequences: [t('client.consequenceConfigs'), t('client.consequenceUsage')],
      confirmLabel: t('action.delete'),
      run: () => guard(() => api.purgeClients(status), 'client.deleted'),
    })
  }

  function pickMore(key) {
    switch (key) {
      case 'time':
      case 'traffic':
        if (selected.value.size) open.extend(key)
        return
      case 'attach':
      case 'detach':
      case 'group':
      case 'adjust':
        return open.dialog(key)
      case 'ungroup':
        return guard(async () => {
          await api.assignGroup('', ids())
          clear()
        }, 'client.bulkDone')
      case 'enable':
      case 'disable':
        return bulk(key)
      case 'reset':
        return confirmAnd({
          title: t('outbound.resetTraffic'),
          body: t('client.bulkResetBody'),
          subject: tn('client.nCustomers', selected.value.size),
          confirmLabel: t('outbound.resetTraffic'),
          run: () => bulkRun('reset'),
        })
      case 'rotate':
        return rotateSelected()
      case 'subLinks':
        return open.subLinks()
      case 'import':
        return open.importer()
      case 'batch':
        return open.batch()
      case 'purgeDepleted':
      case 'purgeUnattached':
        return purge(key)
      case 'export':
        api.downloadClients().catch((e) => notify(e.message, 'error'))
        return
      case 'resetAll':
        return confirmAnd({
          title: t('client.confirmResetAllTitle'),
          body: t('client.confirmResetAllBody'),
          subject: tn('client.nCustomers', stats.value?.clients ?? 0),
          consequences: [t('client.consequenceReset')],
          confirmLabel: t('action.resetTraffic'),
          run: () => guard(() => api.resetAllTraffic(), 'client.bulkDone'),
        })
    }
  }

  return {
    ids, pending, setEnabled, ask, runConfirmed, rotateKeysFor, resetOne, removeOne, bulk, pickMore,
    menu: computed(() => moreItems(selected.value.size)),
  }
}
