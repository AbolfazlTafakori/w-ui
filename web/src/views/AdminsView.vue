<script setup>
// The people who sell this panel on.
//
// The owner's page, and only the owner's -- the route guard keeps it off
// everyone else's menu and every endpoint behind it refuses them again. The
// owner is not listed on it: this is a page about the people they sell to,
// and a row for themselves with every column empty is a row to skip past.
//
// Laid out as the customer list is, because a reseller is managed the way a
// customer is: a summary across the top, then a row each with how much of
// what they bought is left -- customers, traffic, days -- in the same colours
// the customer list uses, so "orange" means the same thing on both pages.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes, dateTime } from '../lib/format.js'
import { useIsMobile } from '../lib/mobile.js'
import AntIcon from '../components/AntIcon.vue'
import Toggle from '../components/Toggle.vue'
import ErrorState from '../components/ErrorState.vue'
import PageSpin from '../components/PageSpin.vue'
import ResellerForm from '../components/ResellerForm.vue'

const isMobile = useIsMobile()

const operators = ref([])
const servers = ref([])
const groupNames = ref([])
const loading = ref(true)
const loadError = ref(null)
const pending = ref(new Set())

const formFor = ref(null) // {} to add, { admin } to edit
const removing = ref(null)
const busy = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load({ quiet = false } = {}) {
  if (!quiet) loading.value = true
  loadError.value = null
  try {
    const [list, ifaces, names] = await Promise.all([
      api.admins({ background: quiet }),
      api.interfaces({ background: quiet }),
      api.groupNames().catch(() => []),
    ])
    operators.value = list.items || []
    servers.value = ifaces || []
    groupNames.value = names || []
  } catch (err) {
    loadError.value = err
  } finally {
    loading.value = false
  }
}
onMounted(load)

// Everybody but the owner.
const rows = computed(() => operators.value.filter((a) => a.role !== 'owner'))
const resellers = computed(() => rows.value.filter((a) => a.role === 'reseller'))

// --- What is left, per reseller. -----------------------------------------
const DAY = 86400e3
const WARN_DAYS = 7
const WARN_PERCENT = 85

function usedPercent(a) {
  return a.quotaBytes ? Math.min(100, Math.round((a.usedBytes / a.quotaBytes) * 100)) : null
}
function daysLeft(a) {
  if (!a.expiresAt) return null
  return Math.ceil((new Date(a.expiresAt).getTime() - Date.now()) / DAY)
}

// One word for where a reseller stands, first reason first: the owner's
// switch, then the date, then the traffic, then a term not yet started.
function standing(a) {
  if (!a.enabled) return { key: 'off', color: '', label: t('admins.switchedOff') }
  if (a.role !== 'reseller') return { key: 'active', color: 'green', label: t('status.active') }
  const d = daysLeft(a)
  if (d !== null && d <= 0) return { key: 'ended', color: 'red', label: t('admins.termEnded') }
  if (a.quotaBytes && a.usedBytes >= a.quotaBytes) return { key: 'spent', color: 'red', label: t('admins.allowanceSpent') }
  if (!a.expiresAt && a.durationDays > 0) return { key: 'hold', color: 'blue', label: t('status.onHold') }
  if ((d !== null && d <= WARN_DAYS) || (usedPercent(a) ?? 0) >= WARN_PERCENT) {
    return { key: 'soon', color: 'orange', label: t('admins.endingSoon') }
  }
  return { key: 'active', color: 'green', label: t('status.active') }
}

function trafficLeftTag(a) {
  if (!a.quotaBytes) return { color: 'purple', label: '∞' }
  const p = usedPercent(a)
  const label = bytes(Math.max(0, a.quotaBytes - a.usedBytes), store.locale)
  if (p >= 100) return { color: 'red', label }
  if (p >= WARN_PERCENT) return { color: 'orange', label }
  return { color: 'green', label }
}
function barColor(a) {
  if (!a.enabled) return 'var(--faint)'
  if (!a.quotaBytes) return 'rgba(114, 46, 209, 0.35)'
  const p = usedPercent(a) ?? 0
  if (p >= 100) return 'var(--bad)'
  if (p >= WARN_PERCENT) return 'var(--warn)'
  return 'var(--ok)'
}
function termTag(a) {
  if (!a.expiresAt && a.durationDays > 0) {
    return { color: 'blue', label: t('admins.daysOnHold', { n: nf(a.durationDays) }), title: t('admins.onHoldHint') }
  }
  const d = daysLeft(a)
  if (d === null) return { color: 'purple', label: '∞', title: t('admins.noEnd') }
  const title = dateTime(a.expiresAt, store.locale)
  if (d <= 0) return { color: 'red', label: t('admins.termEnded'), title }
  return { color: d <= WARN_DAYS ? 'orange' : 'green', label: t('admins.daysLeft', { n: nf(d) }), title }
}
function customersTag(a) {
  if (!a.clientLimit) return { color: '', label: `${nf(a.clients)} / ∞` }
  const full = a.clients >= a.clientLimit
  return { color: full ? 'orange' : '', label: `${nf(a.clients)} / ${nf(a.clientLimit)}` }
}

function serverName(id) {
  return servers.value.find((s) => s.id === id)?.name || `#${id}`
}
function serverProto(id) {
  return servers.value.find((s) => s.id === id)?.protocol
}
const CHIP_LIMIT = 2

// --- Across the top. -------------------------------------------------------
const summary = computed(() => {
  const list = resellers.value
  const by = (key) => list.filter((a) => standing(a).key === key).length
  return {
    total: rows.value.length,
    active: by('active') + by('hold'),
    soon: by('soon'),
    stopped: by('off') + by('ended') + by('spent'),
    customers: list.reduce((n, a) => n + (a.clients || 0), 0),
    traffic: list.reduce((n, a) => n + (a.usedBytes || 0), 0),
  }
})

// --- Doing things to one. ---------------------------------------------------
function markPending(id, on) {
  const next = new Set(pending.value)
  if (on) next.add(id)
  else next.delete(id)
  pending.value = next
}

async function setEnabled(a, on) {
  const was = a.enabled
  a.enabled = on
  markPending(a.id, true)
  try {
    await api.updateAdmin(a.id, { enabled: on })
  } catch (err) {
    a.enabled = was
    notify(err.message, 'error')
  } finally {
    markPending(a.id, false)
  }
}

async function resetUsage(a) {
  if (!window.confirm(t('admins.resetConfirm', { name: a.username }))) return
  markPending(a.id, true)
  try {
    await api.resetAdminUsage(a.id)
    a.usedBytes = 0
    notify(t('admins.usageReset'), 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    markPending(a.id, false)
  }
}

async function remove(mode) {
  const a = removing.value
  if (!a) return
  busy.value = true
  try {
    await api.deleteAdmin(a.id, mode)
    removing.value = null
    await load({ quiet: true })
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function saved() {
  formFor.value = null
  notify(t('admins.saved'), 'success')
  await load({ quiet: true })
}
</script>

<template>
  <div class="antpage resellers">
    <ErrorState v-if="loadError" :error="loadError" @retry="load" />
    <PageSpin v-else-if="loading" />

    <template v-else>
      <!-- The classic summary card, over resellers rather than customers. -->
      <div class="acard small summary-card">
        <div class="acard-body">
          <div class="arow six">
            <div class="acol">
              <div class="stat-title">{{ t('nav.admins') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="ShopOutlined" /></span><span>{{ nf(summary.total) }}</span></div>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('status.active') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-green"></i></span><span>{{ nf(summary.active) }}</span></div>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('admins.endingSoon') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-orange"></i></span><span>{{ nf(summary.soon) }}</span></div>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('admins.stopped') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><i class="dot dot-red"></i></span><span>{{ nf(summary.stopped) }}</span></div>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('admins.theirCustomers') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ nf(summary.customers) }}</span></div>
            </div>
            <div class="acol">
              <div class="stat-title">{{ t('admins.trafficUsed') }}</div>
              <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="BarChartOutlined" /></span><span>{{ bytes(summary.traffic, store.locale) }}</span></div>
            </div>
          </div>
        </div>
      </div>

      <div class="acard small">
        <div class="acard-head">
          <div class="card-toolbar">
            <button class="abtn primary" :disabled="!servers.length" :title="servers.length ? '' : t('interface.noneYet')" @click="formFor = {}">
              <AntIcon name="PlusOutlined" /><span>{{ t('admins.add') }}</span>
            </button>
          </div>
        </div>

        <div class="acard-body">
          <div v-if="!rows.length" class="resellers-empty">
            <AntIcon name="ShopOutlined" :size="32" />
            <div>{{ t('admins.none') }}</div>
          </div>

          <!-- A phone gets a card each: the same figures, stacked. -->
          <div v-else-if="isMobile" class="rcards">
            <div v-for="a in rows" :key="a.id" class="rcard" :class="{ off: !a.enabled }">
              <div class="rcard-head">
                <div class="rcard-name">
                  <span class="name">{{ a.username }}</span>
                  <span class="atag" :class="standing(a).color" style="margin: 0">{{ standing(a).label }}</span>
                </div>
                <Toggle :model-value="a.enabled" :label="a.username" small :loading="pending.has(a.id)" @update:model-value="(v) => setEnabled(a, v)" />
              </div>
              <div v-if="a.note" class="rcard-note">{{ a.note }}</div>
              <dl v-if="a.role === 'reseller'" class="rcard-stats">
                <div><dt>{{ t('admins.customers') }}</dt><dd><span class="atag ltr" :class="customersTag(a).color" style="margin: 0">{{ customersTag(a).label }}</span></dd></div>
                <div><dt>{{ t('admins.trafficLeft') }}</dt><dd><span class="atag ltr" :class="trafficLeftTag(a).color" style="margin: 0">{{ trafficLeftTag(a).label }}</span></dd></div>
                <div><dt>{{ t('admins.timeLeft') }}</dt><dd><span class="atag" :class="termTag(a).color" :title="termTag(a).title" style="margin: 0">{{ termTag(a).label }}</span></dd></div>
              </dl>
              <div v-else class="rcard-note"><span class="atag geekblue" style="margin: 0">{{ t('admins.role.admin') }}</span></div>
              <div class="rcard-actions">
                <button class="abtn text sm" :aria-label="t('action.edit')" @click="formFor = { admin: a }"><AntIcon name="EditOutlined" /><span>{{ t('action.edit') }}</span></button>
                <button v-if="a.role === 'reseller' && a.quotaBytes" class="abtn text sm" :disabled="pending.has(a.id)" @click="resetUsage(a)"><AntIcon name="RetweetOutlined" /><span>{{ t('admins.resetShort') }}</span></button>
                <button class="abtn text sm danger" @click="removing = a"><AntIcon name="DeleteOutlined" /><span>{{ t('action.delete') }}</span></button>
              </div>
            </div>
          </div>

          <div v-else class="atable-wrap" style="margin-top: 0">
            <table class="atable small" style="min-width: 1080px">
              <thead>
                <tr>
                  <th style="width: 110px">{{ t('table.actions') }}</th>
                  <th style="width: 70px">{{ t('table.enabled') }}</th>
                  <th style="width: 200px">{{ t('admins.reseller') }}</th>
                  <th style="width: 110px">{{ t('admins.status') }}</th>
                  <th style="width: 100px">{{ t('admins.customers') }}</th>
                  <th style="width: 260px">{{ t('client.traffic') }}</th>
                  <th style="width: 110px">{{ t('admins.trafficLeft') }}</th>
                  <th style="width: 110px">{{ t('admins.timeLeft') }}</th>
                  <th>{{ t('admins.servers') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="a in rows" :key="a.id" :class="{ off: !a.enabled }">
                  <td>
                    <div class="aspace" style="gap: 4px; flex-wrap: nowrap">
                      <button class="abtn text sm" :title="t('action.edit')" :aria-label="t('action.edit')" @click="formFor = { admin: a }"><AntIcon name="EditOutlined" /></button>
                      <button v-if="a.role === 'reseller'" class="abtn text sm" :title="t('admins.resetUsage')" :aria-label="t('admins.resetUsage')" :disabled="pending.has(a.id) || !a.quotaBytes" @click="resetUsage(a)"><AntIcon name="RetweetOutlined" /></button>
                      <button class="abtn text sm danger" :title="t('action.delete')" :aria-label="t('action.delete')" @click="removing = a"><AntIcon name="DeleteOutlined" /></button>
                    </div>
                  </td>
                  <td>
                    <Toggle :model-value="a.enabled" :label="a.username" small :loading="pending.has(a.id)" @update:model-value="(v) => setEnabled(a, v)" />
                  </td>
                  <td>
                    <div class="who">
                      <span class="name ltr">{{ a.username }}</span>
                      <span v-if="a.role === 'admin'" class="sub"><span class="atag geekblue" style="margin: 0">{{ t('admins.role.admin') }}</span></span>
                      <span v-if="a.note" class="sub" :title="a.note">{{ a.note }}</span>
                      <span v-if="a.groupName" class="sub" :title="t('admins.groupHint')"><AntIcon name="TagsOutlined" /> {{ a.groupName }}</span>
                    </div>
                  </td>
                  <td><span class="atag" :class="standing(a).color" style="margin: 0">{{ standing(a).label }}</span></td>
                  <template v-if="a.role === 'reseller'">
                    <td><span class="atag ltr" :class="customersTag(a).color" style="margin: 0">{{ customersTag(a).label }}</span></td>
                    <td>
                      <div class="traffic-cell" :class="{ 'is-unlimited': !a.quotaBytes }">
                        <span class="traffic-used ltr">{{ bytes(a.usedBytes, store.locale) }}</span>
                        <span class="aprogress traffic-bar"><span :style="{ width: (a.quotaBytes ? usedPercent(a) : 100) + '%', background: barColor(a) }"></span></span>
                        <span class="traffic-limit ltr">
                          <span v-if="!a.quotaBytes" class="traffic-infinity">∞</span>
                          <template v-else>{{ bytes(a.quotaBytes, store.locale) }}</template>
                        </span>
                      </div>
                    </td>
                    <td><span class="atag ltr" :class="trafficLeftTag(a).color" style="margin: 0">{{ trafficLeftTag(a).label }}</span></td>
                    <td><span class="atag" :class="termTag(a).color" :title="termTag(a).title" style="margin: 0">{{ termTag(a).label }}</span></td>
                    <td>
                      <template v-if="(a.interfaceIds || []).length">
                        <span v-for="id in a.interfaceIds.slice(0, CHIP_LIMIT)" :key="id" class="atag" :class="serverProto(id) === 'openvpn' ? 'orange' : 'gold'" style="margin: 2px">{{ serverName(id) }}</span>
                        <span v-if="a.interfaceIds.length > CHIP_LIMIT" class="atag default" style="margin: 2px" :title="a.interfaceIds.slice(CHIP_LIMIT).map(serverName).join(', ')">+{{ a.interfaceIds.length - CHIP_LIMIT }}</span>
                      </template>
                      <span v-else class="atag red" style="margin: 0" :title="t('admins.noServersWarn')">{{ t('admins.noServers') }}</span>
                    </td>
                  </template>
                  <template v-else>
                    <td colspan="5" class="cell-empty">{{ t('admins.role.adminHint') }}</td>
                  </template>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </template>
  </div>

  <ResellerForm
    v-if="formFor"
    :interfaces="servers"
    :admin="formFor.admin || null"
    :group-names="groupNames"
    @close="formFor = null"
    @saved="saved"
  />

  <!-- Removing one. What becomes of their customers is asked rather than
       assumed: one answer quietly stops people paying for a service and the
       other quietly leaves the owner administering customers they did not
       know they had. -->
  <div v-if="removing" class="amodal-backdrop centered" @click.self="removing = null">
    <div class="amodal" role="alertdialog" aria-modal="true" aria-labelledby="rm-title">
      <div class="amodal-head">
        <h2 id="rm-title" class="amodal-title">{{ t('admins.remove') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="removing = null"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <p class="rm-text">{{ t('admins.removeAsk', { name: removing.username }) }}</p>
        <span class="atag red" style="margin: 0">{{ t('admins.holdsCustomers', { n: nf(removing.clients || 0) }) }}</span>
      </div>
      <div class="amodal-foot">
        <button class="abtn" @click="removing = null">{{ t('action.cancel') }}</button>
        <button class="abtn" :disabled="busy" @click="remove('keep')">{{ t('admins.removeKeep') }}</button>
        <button class="abtn danger" :disabled="busy" @click="remove('delete')">{{ t('admins.removeWithClients') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.arow.six > .acol { flex: 0 0 16.6667%; max-width: 16.6667%; }
@media (max-width: 991px) { .arow.six > .acol { flex: 0 0 33.3333%; max-width: 33.3333%; } }
@media (max-width: 575px) { .arow.six > .acol { flex: 0 0 50%; max-width: 50%; } }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 4px; vertical-align: middle; }
.dot-green { background: var(--ok); }
.dot-red { background: var(--bad); }
.dot-orange { background: var(--warn); }
.summary-card { margin-bottom: 16px; }

.acard.small .acard-head { min-height: 38px; padding: 0 12px; }
.acard.small .acard-body { padding: 12px; }
.card-toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; width: 100%; padding: 6px 0; }

.resellers-empty { padding: 32px 0; text-align: center; color: var(--muted); }
.resellers-empty .anticon { display: block; margin: 0 auto 8px; opacity: 0.5; }

tr.off .who .name { opacity: 0.55; }
.who { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.who .name { font-weight: 500; }
.who .sub { font-size: 12px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 200px; }
.cell-empty { color: var(--faint); font-size: 12px; }

/* The customer list's traffic pill: the figures either side of a bar. */
.traffic-cell { display: flex; align-items: center; gap: 8px; width: 100%; min-width: 0; box-sizing: border-box; padding: 2px 10px; border-radius: 999px; background: var(--surface-2); }
.traffic-used, .traffic-limit { flex: 0 0 68px; min-width: 68px; font-size: 12px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.traffic-used { text-align: end; color: var(--ink); }
.traffic-limit { text-align: start; color: var(--muted); }
.traffic-bar { flex: 1 1 60px; min-width: 40px; }
.traffic-cell.is-unlimited .traffic-bar > span { border: 1px solid rgba(114, 46, 209, 0.55); }
.traffic-infinity { color: var(--tag-purple-ink); font-size: 14px; line-height: 1; }

/* Phone: a card each. */
.rcards { display: flex; flex-direction: column; gap: 10px; }
.rcard { padding: 12px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
.rcard.off { opacity: 0.7; }
.rcard-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.rcard-name { display: flex; align-items: center; gap: 8px; min-width: 0; flex-wrap: wrap; }
.rcard-name .name { font-weight: 600; }
.rcard-note { margin-top: 4px; font-size: 12px; color: var(--muted); }
.rcard-stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin: 10px 0 0; }
.rcard-stats dt { font-size: 11px; color: var(--faint); margin-bottom: 2px; }
.rcard-stats dd { margin: 0; }
.rcard-actions { display: flex; gap: 4px; flex-wrap: wrap; margin-top: 10px; padding-top: 8px; border-top: 1px solid var(--border); }
.rcard-actions .abtn span { margin-inline-start: 4px; }

.rm-text { margin: 0 0 12px; color: var(--ink); line-height: 1.6; }
</style>
