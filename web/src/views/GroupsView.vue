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
const form = ref({ name: '', days: 30, quotaGB: '' })
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
function menuFor(g) {
  const empty = g.clients === 0
  return [
    { key: 'extend', label: t('group.extend'), icon: 'clock', disabled: empty },
    { key: 'quota', label: t('group.setQuota'), icon: 'database', disabled: empty },
    { key: 'reset', label: t('action.resetTraffic'), icon: 'refresh', disabled: empty },
    { key: 'enable', label: t('action.enable'), icon: 'power', disabled: empty },
    { key: 'disable', label: t('action.disable'), icon: 'power', disabled: empty },
    { key: 'rename', label: t('group.rename'), icon: 'edit' },
    { key: 'clear', label: t('group.dissolve'), icon: 'close', danger: true },
    { key: 'deleteClients', label: t('group.deleteClients'), icon: 'trash', danger: true, disabled: empty },
  ]
}

function pick(g, key) {
  closeMenu()
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
      <span class="strip-label"><Icon name="users" :size="14" />{{ t('group.ungrouped') }}</span>
      <span class="strip-value num">{{ nf(totals.ungrouped) }}</span>
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
            <!-- The group's name leads the row, as on every other table here:
                 a control column in front of the identity makes a list
                 something to decode rather than to scan. -->
            <th>{{ t('group.name') }}</th>
            <th>{{ t('nav.clients') }}</th>
            <th>{{ t('status.active') }}</th>
            <th>{{ t('device.title') }}</th>
            <th>{{ t('overview.upload') }}</th>
            <th>{{ t('overview.download') }}</th>
            <th>{{ t('client.traffic') }}</th>
            <th class="right">{{ t('table.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="g in items" :key="g.name">
            <td>
              <button class="tag geekblue name" @click="viewMembers(g)">{{ g.name }}</button>
            </td>

            <td class="num">{{ nf(g.clients) }}</td>
            <td class="num" :style="g.active ? 'color: var(--ok)' : ''">{{ nf(g.active) }}</td>
            <td class="num">{{ nf(g.devices) }}</td>
            <td class="muted"><span class="num ltr">{{ bytes(g.upBytes, store.locale) }}</span></td>
            <td class="muted"><span class="num ltr">{{ bytes(g.downBytes, store.locale) }}</span></td>
            <td><span class="num ltr">{{ bytes(g.usedBytes, store.locale) }}</span></td>

            <td class="right">
              <button
                class="act"
                :title="t('table.actions')"
                :aria-expanded="menu?.group?.name === g.name"
                @click="openMenuFor(g, $event)"
              >
                <Icon name="more" :size="16" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <div
    v-if="menu"
    class="rowmenu"
    role="menu"
    :style="{ top: menu.y + 'px', left: menu.x + 'px' }"
  >
    <button
      v-for="m in menuFor(menu.group)"
      :key="m.key"
      class="menu-item"
      :class="{ danger: m.danger }"
      :disabled="m.disabled"
      role="menuitem"
      @click="pick(menu.group, m.key)"
    >
      <Icon :name="m.icon" :size="14" />{{ m.label }}
    </button>
  </div>

  <!-- One dialog serves rename, extend and set-quota; they differ only in the
       single field they collect. -->
  <div v-if="dialog" class="modal-backdrop" @click.self="dialog = null">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="g-title">
      <div class="card-head">
        <h2 id="g-title">
          {{ dialog.kind === 'rename' ? t('group.rename')
            : dialog.kind === 'extend' ? t('group.extend') : t('group.setQuota') }}
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="dialog = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="g-form" class="card-body" @submit.prevent="submitDialog">
        <p class="target">
          <span class="tag geekblue">{{ dialog.group.name }}</span>
          <span class="muted small">{{ nf(dialog.group.clients) }} {{ t('nav.clients') }}</span>
        </p>

        <div v-if="dialog.kind === 'rename'" class="field">
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
  border: 1px solid #263a91;
  cursor: pointer;
  font: inherit;
  font-family: var(--mono);
  font-size: var(--t-xs);
  font-weight: 600;
}
.name:hover {
  background: #263a91;
  color: #cdd8ff;
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
