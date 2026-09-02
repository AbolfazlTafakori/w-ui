<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes, gigabytesToBytes } from '../lib/format.js'
import Icon from '../components/Icon.vue'

const router = useRouter()

const data = ref(null)
const loading = ref(true)
// The menu is positioned fixed and rendered outside the table. Inside it, the
// wrapper's overflow-x clips the dropdown to a couple of rows.
const menu = ref(null) // { group, x, y, up }

function openMenuFor(g, event) {
  if (menu.value?.group?.name === g.name) {
    menu.value = null
    return
  }
  const r = event.currentTarget.getBoundingClientRect()
  const height = 8 * 34 + 10
  const up = r.bottom + height > window.innerHeight && r.top > height
  menu.value = {
    group: g,
    x: r.left,
    y: up ? r.top - height - 4 : r.bottom + 4,
  }
}

function closeMenu() {
  menu.value = null
}

onMounted(() => {
  window.addEventListener('click', onDocClick, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('resize', closeMenu)
  window.addEventListener('scroll', closeMenu, true)
})
onUnmounted(() => {
  window.removeEventListener('click', onDocClick, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', closeMenu)
  window.removeEventListener('scroll', closeMenu, true)
})

function onDocClick(e) {
  if (!menu.value) return
  if (e.target.closest?.('.rowmenu') || e.target.closest?.('.act')) return
  closeMenu()
}
function onKey(e) {
  if (e.key === 'Escape') closeMenu()
}
const dialog = ref(null) // { kind, group }
const form = ref({ name: '', note: '', days: 30, quotaGB: '' })
const busy = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load() {
  loading.value = true
  try {
    data.value = await api.groups()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(load)

const items = computed(() => data.value?.items || [])
const totals = computed(() => data.value?.totals || {})

// Every entry in the row menu, with the ones that need members disabled rather
// than hidden, so the menu keeps the same shape on an empty group.
// The order is 3x-ui's: what you do to the members, then to the group, then a
// rule, then everything that destroys something. Entries needing members are
// disabled rather than hidden, so the menu keeps its shape on an empty group and
// an operator learns one layout instead of two.
function menuFor(g) {
  const empty = g.clients === 0
  return [
    { key: 'extend', label: t('group.extend'), icon: 'clock', disabled: empty },
    { key: 'quota', label: t('group.setQuota'), icon: 'database', disabled: empty },
    { key: 'reset', label: t('action.resetTraffic'), icon: 'refresh', disabled: empty },
    { key: 'enable', label: t('action.enable'), icon: 'power', disabled: empty },
    { key: 'disable', label: t('action.disable'), icon: 'power', disabled: empty },
    { key: 'addClients', label: t('group.addClients'), icon: 'users' },
    { key: 'rename', label: t('group.rename'), icon: 'edit' },
    { divider: true },
    { key: 'removeClients', label: t('group.removeClients'), icon: 'close', danger: true, disabled: empty },
    { key: 'clear', label: t('group.dissolve'), icon: 'close', danger: true, disabled: empty },
    { key: 'deleteClients', label: t('group.deleteClients'), icon: 'trash', danger: true, disabled: empty },
    { key: 'delete', label: t('group.delete'), icon: 'trash', danger: true },
  ]
}

function openCreate() {
  form.value.name = ''
  form.value.note = ''
  dialog.value = { kind: 'create' }
}

async function createGroup() {
  const name = (form.value.name || '').trim()
  if (!name) return
  busy.value = true
  try {
    await api.post('/api/groups', { name, note: (form.value.note || '').trim() })
    notify(t('group.created'), 'ok')
    dialog.value = null
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

async function deleteGroup(g) {
  // Deleting a group and deleting the people in it are different actions with
  // very different consequences, so they are different menu entries and this
  // one says which it is.
  if (!confirm(t('group.confirmDelete').replace('{name}', g.name))) return
  busy.value = true
  try {
    const res = await api.post('/api/groups/delete', { name: g.name })
    notify(t('group.deleted').replace('{n}', res.ungrouped ?? 0), 'ok')
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

function openRename(g) {
  closeMenu()
  form.value.name = g.name
  dialog.value = { kind: 'rename', group: g }
}

function pick(g, key) {
  closeMenu()
  if (key === 'addClients' || key === 'removeClients') {
    return openMembers(g, key === 'addClients' ? 'add' : 'remove')
  }
  if (key === 'rename') {
    form.value.name = g.name
    dialog.value = { kind: 'rename', group: g }
    return
  }
  if (key === 'extend') {
    form.value.days = 30
    dialog.value = { kind: 'extend', group: g }
    return
  }
  if (key === 'quota') {
    form.value.quotaGB = ''
    dialog.value = { kind: 'quota', group: g }
    return
  }
  // The two destructive ones ask before they run; the rest are reversible.
  if (key === 'delete') return deleteGroup(g)
  if (key === 'clear' && !confirm(t('group.confirmDissolve'))) return
  if (key === 'deleteClients' && !confirm(t('group.confirmDeleteClients'))) return
  run({ action: key, group: g.name })
}

async function run(op) {
  busy.value = true
  try {
    const res = await api.groupAction(op)
    notify(`${t('group.applied')} — ${nf(res.affected)}`, 'success')
    dialog.value = null
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function submitDialog() {
  if (dialog.value.kind === 'create') return createGroup()
  const d = dialog.value
  if (!d) return

  if (d.kind === 'rename') {
    busy.value = true
    try {
      const res = await api.renameGroup(d.group.name, form.value.name)
      notify(`${t('group.renamed')} — ${nf(res.affected)}`, 'success')
      dialog.value = null
      await load()
    } catch (err) {
      notify(err.message, 'error')
    } finally {
      busy.value = false
    }
    return
  }

  if (d.kind === 'extend') {
    return run({ action: 'extend', group: d.group.name, days: Number(form.value.days) })
  }
  if (d.kind === 'quota') {
    return run({
      action: 'quota',
      group: d.group.name,
      quotaBytes: gigabytesToBytes(form.value.quotaGB),
    })
  }
}

// Members picker, shared by "add clients" and "remove clients". They differ
// only in which customers are listed and what the confirm does, so one dialog
// with two modes beats two dialogs that drift apart.
const members = ref(null) // { group, mode, chosen:Set }
const allClients = ref([])
const memberSearch = ref('')

async function openMembers(g, mode) {
  closeMenu()
  members.value = { group: g, mode, chosen: new Set() }
  memberSearch.value = ''
  try {
    const res = await api.get('/api/clients?perPage=500')
    allClients.value = res.items || []
  } catch (e) {
    notify(e.message, 'error')
    members.value = null
  }
}

const memberChoices = computed(() => {
  if (!members.value) return []
  const { group, mode } = members.value
  const q = memberSearch.value.trim().toLowerCase()
  return allClients.value
    .filter((c) => (mode === 'add' ? c.group !== group.name : c.group === group.name))
    .filter((c) => !q || c.name.toLowerCase().includes(q))
})

function toggleMember(id) {
  const next = new Set(members.value.chosen)
  next.has(id) ? next.delete(id) : next.add(id)
  members.value = { ...members.value, chosen: next }
}

async function applyMembers() {
  const { group, mode, chosen } = members.value
  if (!chosen.size) return
  busy.value = true
  try {
    // One endpoint serves both: assigning to a group and assigning to nothing.
    await api.post('/api/groups/assign', {
      group: mode === 'add' ? group.name : '',
      ids: [...chosen],
    })
    notify(t(mode === 'add' ? 'group.clientsAdded' : 'group.clientsRemoved')
      .replace('{n}', chosen.size), 'ok')
    members.value = null
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

function viewMembers(g) {
  router.push({ path: '/clients', query: { search: g.name } })
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>{{ t('nav.groups') }}</h1>
      <p>{{ t('group.subtitle') }}</p>
    </div>
    <div class="page-actions">
      <button class="btn" @click="openCreate">
        <Icon name="plus" :size="15" />
        <span>{{ t('group.add') }}</span>
      </button>
    </div>
  </div>

  <div class="strip card">
    <div class="strip-item">
      <span class="strip-label"><Icon name="tag" :size="14" />{{ t('group.total') }}</span>
      <span class="strip-value num">{{ nf(totals.groups) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="users" :size="14" />{{ t('group.grouped') }}</span>
      <span class="strip-value num">{{ nf(totals.groupedClients) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="swap" :size="14" />{{ t('group.upDown') }}</span>
      <span class="strip-value num ltr small">
        {{ bytes(totals.upBytes, store.locale) }} / {{ bytes(totals.downBytes, store.locale) }}
      </span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="swap" :size="14" />{{ t('client.traffic') }}</span>
      <span class="strip-value num ltr">{{ bytes(totals.usedBytes, store.locale) }}</span>
    </div>
  </div>

  <div class="banner">
    <Icon name="info" :size="16" />
    <span>{{ t('group.howTo') }}</span>
  </div>

  <div class="card">
    <div v-if="loading" class="empty"><span class="spin"></span></div>

    <div v-else-if="!items.length" class="empty">
      <p>{{ t('group.none') }}</p>
      <p class="small">{{ t('group.noneHint') }}</p>
    </div>

    <div v-else class="table-wrap">
      <table>
        <thead>
          <tr>
            <th class="w-gact">{{ t('table.actions') }}</th>
            <th>{{ t('group.name') }}</th>
            <th class="w-gcount">{{ t('group.clientCount') }}</th>
            <th class="w-gsize">{{ t('overview.upload') }}</th>
            <th class="w-gsize">{{ t('overview.download') }}</th>
            <th class="w-gtraffic">{{ t('group.trafficUsed') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="g in items" :key="g.name">
            <!-- Two controls, as theirs has: the whole menu behind one
                 button, and rename lifted out of it because it is the one an
                 operator reaches for most. -->
            <td class="w-gact">
              <div class="actions">
                <button
                  class="act"
                  :title="t('table.actions')"
                  :aria-expanded="menu?.group?.name === g.name"
                  @click="openMenuFor(g, $event)"
                >
                  <Icon name="more" :size="16" />
                </button>
                <button class="act" :title="t('group.rename')" @click="openRename(g)">
                  <Icon name="edit" :size="16" />
                </button>
              </div>
            </td>

            <td>
              <button class="tag geekblue name" @click="viewMembers(g)">{{ g.name }}</button>
              <div v-if="g.note" class="sub muted small">{{ g.note }}</div>
            </td>

            <td class="num">{{ nf(g.clients) }}</td>
            <td class="muted"><span class="num ltr">{{ bytes(g.upBytes, store.locale) }}</span></td>
            <td class="muted"><span class="num ltr">{{ bytes(g.downBytes, store.locale) }}</span></td>
            <td><span class="num ltr">{{ bytes(g.usedBytes, store.locale) }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- Members picker -->
  <div v-if="members" class="modal-backdrop" @click.self="members = null">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="m-title">
      <div class="card-head">
        <h2 id="m-title">
          {{ members.mode === 'add' ? t('group.addClients') : t('group.removeClients') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="members = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <p class="target">
          <span class="tag geekblue">{{ members.group.name }}</span>
          <span class="muted small">{{ nf(members.chosen.size) }} {{ t('group.selected') }}</span>
        </p>

        <div class="field">
          <input v-model="memberSearch" type="search" :placeholder="t('client.searchHint')" />
        </div>

        <p v-if="!memberChoices.length" class="muted small">{{ t('group.noCandidates') }}</p>

        <ul v-else class="picklist">
          <li v-for="c in memberChoices" :key="c.id">
            <label class="pick">
              <input
                type="checkbox"
                :checked="members.chosen.has(c.id)"
                @change="toggleMember(c.id)"
              />
              <span class="pick-name">{{ c.name }}</span>
              <span v-if="c.group" class="tag geekblue">{{ c.group }}</span>
              <span class="muted small ltr">{{ bytes(c.usedBytes, store.locale) }}</span>
            </label>
          </li>
        </ul>
      </div>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="members = null">{{ t('action.cancel') }}</button>
        <button
          class="btn primary"
          :disabled="busy || !members.chosen.size"
          @click="applyMembers"
        >
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>

  <div
    v-if="menu"
    class="rowmenu"
    role="menu"
    :style="{ top: menu.y + 'px', left: menu.x + 'px' }"
  >
    <template v-for="(m, i) in menuFor(menu.group)" :key="m.key || `d${i}`">
      <!-- The rule before the destructive half, so a slip of the pointer lands
           on a separator rather than on delete. -->
      <hr v-if="m.divider" class="menu-divider" />
      <button
        v-else
        class="menu-item"
        :class="{ danger: m.danger }"
        :disabled="m.disabled"
        role="menuitem"
        @click="pick(menu.group, m.key)"
      >
        <Icon :name="m.icon" :size="14" />{{ m.label }}
      </button>
    </template>
  </div>

  <!-- One dialog serves rename, extend and set-quota; they differ only in the
       single field they collect. -->
  <div v-if="dialog" class="modal-backdrop" @click.self="dialog = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="g-title">
      <div class="card-head">
        <h2 id="g-title">
          {{ dialog.kind === 'create' ? t('group.add')
            : dialog.kind === 'rename' ? t('group.rename')
            : dialog.kind === 'extend' ? t('group.extend') : t('group.setQuota') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="dialog = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="g-form" class="card-body" @submit.prevent="submitDialog">
        <p v-if="dialog.group" class="target">
          <span class="tag geekblue">{{ dialog.group.name }}</span>
          <span class="muted small">{{ nf(dialog.group.clients) }} {{ t('nav.clients') }}</span>
        </p>

        <template v-if="dialog.kind === 'create'">
          <div class="field">
            <label for="g-new">{{ t('group.name') }}</label>
            <input id="g-new" v-model="form.name" required autofocus maxlength="64" />
            <span class="hint">{{ t('group.addHint') }}</span>
          </div>
          <div class="field">
            <label for="g-note">{{ t('group.note') }}</label>
            <input id="g-note" v-model="form.note" maxlength="256" />
          </div>
        </template>

        <div v-else-if="dialog.kind === 'rename'" class="field">
          <label for="g-name">{{ t('group.newName') }}</label>
          <input id="g-name" v-model="form.name" required autofocus />
          <span class="hint">{{ t('group.renameHint') }}</span>
        </div>

        <div v-else-if="dialog.kind === 'extend'" class="field">
          <label for="g-days">{{ t('group.extendDays') }}</label>
          <input id="g-days" v-model="form.days" type="number" required autofocus />
          <span class="hint">{{ t('group.extendHint') }}</span>
        </div>

        <div v-else class="field">
          <label for="g-quota">{{ t('client.quota') }} (GB)</label>
          <input
            id="g-quota"
            v-model="form.quotaGB"
            type="number"
            min="0"
            step="0.5"
            :placeholder="t('client.unlimited')"
            autofocus
          />
          <span class="hint">{{ t('group.quotaHint') }}</span>
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="dialog = null">{{ t('action.cancel') }}</button>
        <button type="submit" form="g-form" class="btn primary" :disabled="busy">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 1px;
  background: var(--line-soft);
  margin-bottom: 16px;
  overflow: hidden;
}
.strip-item {
  background: var(--surface);
  padding: 14px 18px;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.strip-label {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: var(--t-xs);
  color: var(--muted);
}
.strip-value {
  font-size: var(--t-lg);
  font-weight: 600;
  line-height: 1.15;
}

.act {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.act:hover {
  background: var(--surface-3);
  color: var(--ink);
}
.rowmenu {
  position: fixed;
  z-index: 40;
  min-width: 210px;
  padding: 5px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--surface-2);
  box-shadow: var(--shadow);
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.menu-item {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 11px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--ink-2);
  font: inherit;
  font-size: var(--t-sm);
  text-align: start;
  cursor: pointer;
  white-space: nowrap;
}
.menu-item:hover:not(:disabled) {
  background: var(--surface-3);
  color: var(--ink);
}
.menu-item:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.menu-item.danger {
  color: var(--bad);
}
.menu-item.danger:hover:not(:disabled) {
  background: var(--bad-soft);
}

.name {
  border: 1px solid var(--tag-geek-line);
  cursor: pointer;
  font: inherit;
  font-family: var(--mono);
  font-size: var(--t-xs);
  font-weight: 600;
}
.name:hover {
  background: var(--tag-geek-line);
  color: var(--tag-geek-ink);
}

.modal.narrow {
  max-width: 440px;
}
.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.target {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line-soft);
}
</style>
