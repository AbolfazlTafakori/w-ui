// What an operator does to tunnels from their list: switch one on or off,
// restart, reset or empty it, delete it or a selection, download its
// profile, export it, and everything its row menu and the General Actions
// menu offer. The page hands in its state, how to reload, and what to open.
import { ref } from 'vue'
import { api, saveFile } from '../api.js'
import { t, tn, notify } from '../store.js'
import { exportable } from './rows.js'

// interfaces: every tunnel; selected: the chosen ids; busy: the page's busy
// flag; load(quiet): reload; open: what the menus open on the page --
// { text({title,text,file}), importer(), clone(i), attach(i, list), move(i, kind), group(i, names), edit(i) }.
export function useInterfaceActions({ interfaces, selected, busy, load, open }) {
  async function guard(fn, successKey) {
    try {
      await fn()
      if (successKey) notify(t(successKey), 'success')
      await load()
    } catch (err) {
      notify(err.message, 'error')
    }
  }

  // Rows that are mid-request. Per-row rather than one flag for the page:
  // taking one tunnel down should not freeze the controls on the other five.
  const pending = ref(new Set())
  function hold(id) {
    pending.value = new Set(pending.value).add(id)
  }
  function release(id) {
    const next = new Set(pending.value)
    next.delete(id)
    pending.value = next
  }

  // The switch moves on click and the knob spins until the server agrees.
  // Bringing an interface up is slower than a client toggle -- a device is
  // created, addresses assigned, a driver opened -- so the old behaviour,
  // where nothing moved until a full reload came back, read as a switch that
  // did not work and invited a second click.
  async function setEnabled(iface, on) {
    const was = iface.enabled
    if (was === on) return
    iface.enabled = on
    hold(iface.id)
    try {
      Object.assign(iface, await api.updateInterface(iface.id, { enabled: on }))
      notify(t('interface.updated'), 'success')
    } catch (err) {
      iface.enabled = was
      notify(err.message, 'error')
    } finally {
      release(iface.id)
      // The strip above the table counts what is enabled and what it carries.
      load(true)
    }
  }

  // Asked first: { title, body, subject, consequences, confirmLabel, requireText, run }.
  const ask = ref(null)
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

  // Zeroing what everyone on a tunnel has used. A customer on three tunnels
  // has one total, so this clears all of it for them -- which is on the
  // confirmation rather than discovered from a customer who suddenly has
  // their whole allowance back.
  function resetUsage(iface) {
    ask.value = {
      title: t('interface.resetUsageTitle'),
      subject: iface.name,
      body: t('interface.resetUsageBody'),
      confirmLabel: t('action.resetTraffic'),
      run: async () => {
        const res = await api.post(`/api/interfaces/${iface.id}/reset-usage`)
        notify(`${t('interface.resetUsageDone')} — ${res?.customers ?? 0}`, 'success')
        await load()
      },
    }
  }
  function resetAllUsage() {
    ask.value = {
      title: t('iface.menu.resetAll'),
      body: t('interface.resetUsageBody'),
      subject: tn('interface.nTunnels', interfaces.value.length),
      confirmLabel: t('outbound.reset'),
      run: async () => {
        for (const i of interfaces.value) await api.post(`/api/interfaces/${i.id}/reset-usage`)
        await load()
      },
    }
  }

  // Emptying a tunnel, which is what has to happen before it can be removed.
  function clearTunnel(iface) {
    ask.value = {
      title: t('interface.clearTitle'),
      subject: iface.name,
      body: t('interface.clearBody'),
      confirmLabel: t('interface.clear'),
      danger: true,
      run: async () => {
        const res = await api.post(`/api/interfaces/${iface.id}/clear`)
        const failed = Object.entries(res?.failures || {})
        if (failed.length) notify(failed.map(([name, why]) => `${name}: ${why}`).join('\n'), 'error')
        else notify(`${t('interface.clearDone')} — ${res?.changed ?? 0}`, 'success')
        await load()
      },
    }
  }

  // Reopening one tunnel's driver. The reconciler heals most things, but not
  // a driver whose Open failed at startup -- a port that was taken, a tool not
  // installed yet. Without this the only way out is restarting the panel,
  // which disconnects every customer on every other interface to fix one.
  async function restart(iface) {
    hold(iface.id)
    try {
      const res = await api.post(`/api/interfaces/${iface.id}/restart`)
      // The reason it will not come up is the whole point of asking.
      if (res.ok) notify(t('interface.restarted'), 'success')
      else notify(t('interface.restartFailed').replace('{error}', res.error), 'error')
      await load()
    } catch (e) {
      notify(e.message, 'error')
    } finally {
      release(iface.id)
    }
  }

  // Deleting a tunnel destroys its keys, and there is no undo. The server
  // refuses while any device is still on it -- customers are taken off first,
  // with the row menu's Take All Clients Off -- so a tunnel that still has
  // customers is told so at once, rather than after a confirmation that could
  // only fail.
  const stillHas = (i) => tn('interface.stillHasClients', i.clients).replace('{name}', i.name)
  function removeOne(iface) {
    if ((iface.clients ?? 0) > 0) {
      notify(stillHas(iface), 'error')
      return
    }
    ask.value = {
      title: t('interface.confirmDeleteTitle'),
      body: t('interface.confirmDeleteBody'),
      subject: iface.name,
      consequences: [t('interface.consequenceKeys')],
      confirmLabel: t('action.delete'),
      run: () => guard(() => api.deleteInterface(iface.id), 'interface.deleted'),
    }
  }
  function bulkDelete() {
    const ids = [...selected.value]
    if (!ids.length) return
    const busyOnes = interfaces.value.filter((i) => selected.value.has(i.id) && (i.clients ?? 0) > 0)
    if (busyOnes.length) {
      notify(busyOnes.map(stillHas).join('\n'), 'error')
      return
    }
    ask.value = {
      title: t('interface.confirmDeleteManyTitle'),
      body: t('interface.confirmDeleteBody'),
      subject: tn('interface.nTunnels', ids.length),
      consequences: [t('interface.consequenceKeys')],
      confirmLabel: t('action.delete'),
      run: () => guard(async () => {
        for (const id of ids) await api.deleteInterface(id)
        selected.value = new Set()
      }, 'interface.deleted'),
    }
  }

  // The tunnel's own OpenVPN profile, which every customer on it uses. Saved
  // with the session rather than linked: it carries the certificate authority.
  async function downloadProfile(iface) {
    try {
      await saveFile(`/api/interfaces/${iface.id}/profile`, `${iface.name}.ovpn`)
      notify(t('interface.profileDownloaded'), 'success')
    } catch (err) {
      notify(err.message, 'error')
    }
  }

  // The customers on one tunnel, for the actions that take all of them: every
  // one of them, however many pages the list runs to.
  const clientsOn = (i) => api.allClients({ interfaceIds: i.id })
  // The subscription links of these customers, one per line, each once.
  async function linksOf(list) {
    const links = await api.subscriptionLinks(list.map((c) => c.id))
    return [...new Set(links.filter(Boolean))]
  }
  // Export All URLs: every customer's subscription link, one per line, the
  // way the classic panel hands over every client's share link at once --
  // for one tunnel, or for everyone on any tunnel, fetched once each.
  async function exportUrls(i) {
    try {
      if (i) {
        const links = await linksOf(await clientsOn(i))
        open.text({ title: `${t('iface.menu.exportUrls')} — ${i.name}`, text: links.join('\n'), file: `${i.name}-links.txt` })
      } else {
        const all = await api.allClients()
        const links = await linksOf(all.filter((c) => (c.accounts || []).length))
        open.text({ title: t('iface.menu.exportUrls'), text: links.join('\n'), file: 'subscription-links.txt' })
      }
    } catch (err) {
      notify(err.message, 'error')
    }
  }

  // Attach Existing Clients: the customers not yet on this tunnel, to pick from.
  async function openAttach(i) {
    try {
      const all = await api.allClients()
      open.attach(i, all.filter((c) => !(c.accounts || []).some((a) => a.interfaceId === i.id)))
    } catch (err) {
      notify(err.message, 'error')
    }
  }
  async function openGroup(i) {
    let names = []
    try { names = await api.groupNames() } catch { /* the box still takes a new name */ }
    open.group(i, names)
  }

  function pickRow(i, key) {
    switch (key) {
      case 'urls': return exportUrls(i)
      case 'export': return open.text({ title: `${t('iface.menu.exportInbound')} — ${i.name}`, text: JSON.stringify(exportable(i), null, 2), file: `${i.name}.json` })
      case 'profile': return downloadProfile(i)
      case 'reset': return resetUsage(i)
      case 'clone': return open.clone(i)
      case 'restart': return restart(i)
      case 'attachExisting': return openAttach(i)
      case 'attachTo': return open.move(i, 'attach')
      case 'detach': return open.move(i, 'detach')
      case 'group': return openGroup(i)
      case 'delAll': return clearTunnel(i)
      case 'delete': return removeOne(i)
    }
  }
  function pickGeneral(key) {
    switch (key) {
      case 'import': return open.importer()
      case 'export': return exportUrls(null)
      case 'exportAll': return open.text({ title: t('iface.menu.exportAll'), text: JSON.stringify(interfaces.value.map(exportable), null, 2), file: 'interfaces.json' })
      case 'resetAll': return resetAllUsage()
    }
  }

  return { pending, setEnabled, ask, runConfirmed, bulkDelete, pickRow, pickGeneral, clientsOn }
}
