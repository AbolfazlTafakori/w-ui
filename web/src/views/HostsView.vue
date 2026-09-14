<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { store, t, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import ErrorState from '../components/ErrorState.vue'
import HostForm from '../components/HostForm.vue'
import PageSpin from '../components/PageSpin.vue'
import { useIsMobile } from '../lib/mobile.js'
import Toggle from '../components/Toggle.vue'
// the classic panel's HostList drops the button's word on a phone.
const isMobile = useIsMobile()

// The hosts page, laid out as the classic panel's HostsPage: a summary of three
// figures, then a small card whose title is the toolbar and whose body is
// the table of host groups -- one row per name, however many addresses and
// inbounds sit behind it.

const groups = ref([])
const interfaces = ref([])
const loading = ref(true)
const loadError = ref(null)
const showWait = useDelayed(computed(() => loading.value && !groups.value.length))
const formFor = ref(null) // null | {} | { group }
const ask = ref(null)
const busy = ref(false)
const selected = ref(new Set())
const nf = (n) => Number(n || 0).toLocaleString(store.locale)

const summary = computed(() => ({
  total: groups.value.length,
  enabled: groups.value.filter((g) => g.enabled).length,
  disabled: groups.value.filter((g) => !g.enabled).length,
}))
const ifaceById = computed(() => Object.fromEntries(interfaces.value.map((i) => [i.id, i])))
const PROTO_COLOR = { wireguard: 'gold', openvpn: 'orange' }

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const [g, i] = await Promise.all([
      api.get('/api/hosts/groups', { background: quiet }),
      api.get('/api/interfaces', { background: quiet }),
    ])
    groups.value = g || []
    interfaces.value = i || []
    loadError.value = null
    const ids = new Set(groups.value.map((x) => x.groupId))
    selected.value = new Set([...selected.value].filter((id) => ids.has(id)))
  } catch (e) {
    loadError.value = e
  } finally {
    loading.value = false
  }
}
onMounted(load)

const allSelected = computed(() => !!groups.value.length && selected.value.size === groups.value.length)
function toggleAll(on) {
  selected.value = on ? new Set(groups.value.map((g) => g.groupId)) : new Set()
}
function toggleOne(id, on) {
  const next = new Set(selected.value)
  on ? next.add(id) : next.delete(id)
  selected.value = next
}

async function setEnabled(g, on) {
  try {
    await api.post('/api/hosts/groups/bulk', { action: on ? 'enable' : 'disable', groupIds: [g.groupId] })
    g.enabled = on
  } catch (e) {
    notify(e.message, 'error')
  }
}

async function bulk(action) {
  const ids = [...selected.value]
  if (!ids.length) return
  if (action === 'delete') {
    ask.value = {
      title: t('hosts.bulkDeleteConfirm').replace('{count}', nf(ids.length)),
      confirmLabel: t('hosts.bulkDelete'),
      danger: true,
      run: async () => {
        await api.post('/api/hosts/groups/bulk', { action, groupIds: ids })
        selected.value = new Set()
        await load(true)
      },
    }
    return
  }
  try {
    await api.post('/api/hosts/groups/bulk', { action, groupIds: ids })
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  }
}

function remove(g) {
  ask.value = {
    title: t('hosts.deleteConfirmTitle').replace('{name}', g.remark),
    confirmLabel: t('action.delete'),
    danger: true,
    run: async () => {
      await api.del(`/api/hosts/groups/${encodeURIComponent(g.groupId)}`)
      await load(true)
    },
  }
}

async function runConfirmed() {
  const a = ask.value
  busy.value = true
  try {
    await a.run()
    ask.value = null
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

// Their move up / move down: the whole order is sent, first first.
async function move(g, dir) {
  const ids = groups.value.map((x) => x.groupId)
  const i = ids.indexOf(g.groupId)
  const j = dir === 'up' ? i - 1 : i + 1
  if (j < 0 || j >= ids.length) return
  ;[ids[i], ids[j]] = [ids[j], ids[i]]
  try {
    await api.post('/api/hosts/groups/reorder', { groupIds: ids })
    await load(true)
  } catch (e) {
    notify(e.message, 'error')
  }
}

function onSaved() {
  formFor.value = null
  load(true)
}

const popover = ref(null) // { key, items, x, y }
function showMore(key, items, e) {
  if (popover.value?.key === key) {
    popover.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  popover.value = { key, items, x: r.right, y: r.bottom + 4 }
}
function ifaceLabel(id) {
  return ifaceById.value[id]?.name || `#${id}`
}
</script>

<template>
  <div class="antpage hosts">
    <!-- Their summary Card: size="small", three Statistics. -->
    <div class="acard small summary-card">
      <div class="acard-body">
        <div class="arow">
          <div class="acol">
            <div class="stat-title">{{ t('hosts.summary.total') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="GlobalOutlined" /></span><span>{{ nf(summary.total) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('hosts.summary.enabled') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="CheckCircleOutlined" /></span><span>{{ nf(summary.enabled) }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('hosts.summary.disabled') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="StopOutlined" /></span><span>{{ nf(summary.disabled) }}</span></div>
          </div>
        </div>
      </div>
    </div>

    <!-- Their list Card: size="small", the title a toolbar. -->
    <div class="acard small hosts-card">
      <div class="acard-head">
        <div class="card-toolbar">
          <template v-if="!selected.size">
            <button class="abtn primary" :disabled="!interfaces.length" @click="formFor = {}"><AntIcon name="PlusOutlined" /><span v-if="!isMobile">{{ t('hosts.addHost') }}</span></button>
          </template>
          <template v-else>
            <span class="atag blue closable" style="padding: 4px 8px; font-size: 13px">
              {{ t('hosts.selectedCount').replace('{count}', nf(selected.size)) }}
              <button type="button" class="atag-close" :aria-label="t('action.cancel')" @click="selected = new Set()"><AntIcon name="CloseOutlined" /></button>
            </span>
            <button class="abtn" @click="bulk('enable')">{{ t('hosts.bulkEnable') }}</button>
            <button class="abtn" @click="bulk('disable')">{{ t('hosts.bulkDisable') }}</button>
            <button class="abtn danger" @click="bulk('delete')"><AntIcon name="DeleteOutlined" /><span>{{ t('hosts.bulkDelete') }}</span></button>
          </template>
        </div>
      </div>
      <div class="acard-body">
        <ErrorState v-if="loadError && !groups.length" :error="loadError" @retry="load()" />
        <PageSpin v-else-if="showWait" />
        <div v-else-if="loading && !groups.length" class="empty"></div>
        <div v-else class="atable-wrap" style="margin-top: 0">
          <table class="atable small" style="min-width: 900px">
            <thead>
              <tr>
                <th class="sel"><input type="checkbox" class="acheck" :checked="allSelected" :aria-label="t('action.selectAll')" @change="toggleAll($event.target.checked)" /></th>
                <th style="width: 168px">{{ t('hosts.fields.actions') }}</th>
                <th style="width: 90px">{{ t('hosts.fields.enable') }}</th>
                <th>{{ t('hosts.fields.remark') }}</th>
                <th>{{ t('hosts.fields.endpoint') }}</th>
                <th>{{ t('hosts.fields.inbound') }}</th>
                <th>{{ t('hosts.fields.reachable') }}</th>
                <th>{{ t('hosts.fields.tags') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!groups.length">
                <td colspan="8">
                  <div class="card-empty">
                    <AntIcon name="GlobalOutlined" :size="32" />
                    <div>{{ t('common.nothingYet') }}</div>
                  </div>
                </td>
              </tr>
              <tr v-for="(g, idx) in groups" :key="g.groupId" :class="{ picked: selected.has(g.groupId) }">
                <td class="sel"><input type="checkbox" class="acheck" :checked="selected.has(g.groupId)" :aria-label="g.remark" @change="toggleOne(g.groupId, $event.target.checked)" /></td>
                <td>
                  <div class="aspace" style="gap: 2px; flex-wrap: nowrap">
                    <button class="abtn text sm" :title="t('hosts.moveUp')" :aria-label="t('hosts.moveUp')" :disabled="idx === 0" @click="move(g, 'up')"><AntIcon name="ArrowUpOutlined" /></button>
                    <button class="abtn text sm" :title="t('hosts.moveDown')" :aria-label="t('hosts.moveDown')" :disabled="idx >= groups.length - 1" @click="move(g, 'down')"><AntIcon name="ArrowDownOutlined" /></button>
                    <button class="abtn text sm" :title="t('action.edit')" :aria-label="t('action.edit')" @click="formFor = { group: g }"><AntIcon name="EditOutlined" /></button>
                    <button class="abtn text sm danger" :title="t('action.delete')" :aria-label="t('action.delete')" @click="remove(g)"><AntIcon name="DeleteOutlined" /></button>
                  </div>
                </td>
                <td><Toggle :model-value="g.enabled" :label="g.remark" small @update:model-value="(v) => setEnabled(g, v)" /></td>
                <td>
                  <div class="host-remark-cell">
                    <span class="host-remark">{{ g.remark }}</span>
                    <span v-if="g.description" class="host-desc">{{ g.description }}</span>
                  </div>
                </td>
                <td>
                  <span v-if="!g.hosts.length" class="atag orange">{{ t('hosts.fields.inheritAddress') }}</span>
                  <template v-else>
                    <span class="atag ltr host-endpoint">{{ g.hosts[0] }}</span>
                    <span v-if="g.hosts.length > 1" class="atag default" style="margin: 2px; cursor: pointer" @click="showMore(`h-${g.groupId}`, g.hosts, $event)">+{{ g.hosts.length - 1 }}</span>
                  </template>
                </td>
                <td>
                  <span v-if="!g.interfaceIds.length" class="host-muted">—</span>
                  <template v-else>
                    <span class="atag" :class="PROTO_COLOR[ifaceById[g.interfaceIds[0]]?.protocol] || 'default'" style="margin: 2px" :title="ifaceLabel(g.interfaceIds[0])">{{ ifaceLabel(g.interfaceIds[0]) }}</span>
                    <span v-if="g.interfaceIds.length > 1" class="atag default" style="margin: 2px; cursor: pointer" @click="showMore(`i-${g.groupId}`, g.interfaceIds.slice(1).map(ifaceLabel), $event)">+{{ g.interfaceIds.length - 1 }}</span>
                  </template>
                </td>
                <td>
                  <span class="atag" :class="g.reachable ? 'green' : 'red'" :title="g.lastError || ''">{{ g.reachable ? t('hosts.reachable') : t('hosts.unreachable') }}</span>
                </td>
                <td>
                  <template v-if="g.tags?.length"><span v-for="tag in g.tags" :key="tag" class="atag blue" style="margin: 0 4px 4px 0">{{ tag }}</span></template>
                  <span v-else class="host-muted">—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="popover" class="apopover" :style="{ top: popover.y + 'px', left: popover.x + 'px', transform: 'translateX(-100%)' }" @click.stop>
        <div class="chips-stack"><span v-for="it in popover.items.slice(1)" :key="it" class="atag ltr" style="margin: 0">{{ it }}</span></div>
      </div>
    </Teleport>

    <HostForm v-if="formFor" :group="formFor.group" :interfaces="interfaces" @saved="onSaved" @cancel="formFor = null" />

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :confirm-label="ask?.confirmLabel || ''"
      :danger="!!ask?.danger"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </div>
</template>

<style scoped>
/* the classic panel's HostList.css, as it is. */
.acard.small .acard-head { min-height: 38px; padding: 0 12px; }
.acard.small .acard-body { padding: 12px; }
.card-toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; width: 100%; padding: 6px 0; }
.host-remark-cell { display: flex; flex-direction: column; line-height: 1.3; }
.host-remark { font-weight: 500; }
.host-desc { font-size: 0.82em; color: var(--muted); }
.host-endpoint { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 0.92em; }
.host-muted { color: var(--faint); }
.card-empty { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 24px 12px; text-align: center; color: var(--muted); }
.card-empty .anticon { margin-bottom: 8px; }
.chips-stack { display: flex; flex-direction: column; gap: 4px; max-width: 280px; max-height: 280px; overflow-y: auto; }
.abtn.text.danger { color: var(--bad); }
</style>
