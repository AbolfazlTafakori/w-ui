<script setup>
// The classic panel's client dialog, control for control: a wide modal with
// three tabs -- Basics, Credentials, Links -- over Ant's 24-column grid at
// gutter 16, 14px labels 8px above 32px controls, 24px between rows, a
// question mark beside every label that needs a word of explanation, and the
// Enabled switch at the foot of the basics. Ours has more to say than theirs
// (a speed limit, a plan that starts on first use, OpenVPN's own username and
// password, several servers per customer), so the rows are ours; the shape
// and the sizes are theirs.
import { ref, computed, onMounted, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import { quotaToUnit, unitToBytes, durationToUnit, unitToHours } from '../lib/format.js'
import AntIcon from './AntIcon.vue'
import Toggle from './Toggle.vue'
import MultiSelect from './MultiSelect.vue'
import HelpTip from './HelpTip.vue'
import AutoComplete from './AutoComplete.vue'

const props = defineProps({
  interfaces: { type: Array, required: true },
  // When present the dialog edits that client instead of creating one.
  client: { type: Object, default: null },
})
const emit = defineEmits(['close', 'submit'])

const editing = computed(() => !!props.client)
const tab = ref('basics')

// Their email is a random ten-character handle with a button to draw another;
// a customer's name here is free text, and the same button fills one in for
// a reseller who names customers by their order number anyway.
function randomHandle(n = 10) {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = new Uint8Array(n)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}
function randomSecret(n = 16) {
  const alphabet = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
  const bytes = new Uint8Array(n)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}

// The usernames the customer's OpenVPN users have now, in the plan's
// order (user 1 first), from the first OpenVPN tunnel they are on.
function currentOpenVPNUsernames() {
  const ovpn = (props.interfaces || []).filter((i) => i.protocol === 'openvpn').map((i) => i.id)
  if (!ovpn.length) return []
  const on = (props.client?.accounts || []).filter((a) => a.interfaceId === ovpn[0]).sort((a, b) => a.id - b.id)
  return on.map((a) => a.username || '')
}
function hoursLeft(iso) {
  if (!iso) return 0
  const h = (new Date(iso).getTime() - Date.now()) / 3600e3
  return h > 0 ? Math.round(h * 100) / 100 : 0
}

const form = ref(
  editing.value
    ? {
        name: props.client.name,
        note: props.client.note || '',
        group: props.client.group || '',
        telegramId: props.client.telegramId || 0,
        // Every server this customer already reaches, from their accounts.
        interfaceIds: [...new Set((props.client.accounts || []).map((a) => a.interfaceId))],
        quota: quotaToUnit(props.client.quotaBytes).value,
        quotaUnit: quotaToUnit(props.client.quotaBytes).unit,
        expiresIn: props.client.startOnFirstUse
          ? props.client.durationDays || ''
          : durationToUnit(hoursLeft(props.client.expiresAt)).value,
        expiresUnit: props.client.startOnFirstUse ? 'days' : durationToUnit(hoursLeft(props.client.expiresAt)).unit,
        // 0 is unlimited and shown as an empty box, as the quota is.
        deviceLimit: props.client.deviceLimit || '',
        rateMbit: props.client.rateBitsPerSec ? props.client.rateBitsPerSec / 1e6 : '',
        startOnFirstUse: !!props.client.startOnFirstUse,
        resetCycle: props.client.resetCycle || 'none',
        enabled: props.client.status !== 'disabled',
        // The name their first OpenVPN device logs in with, so it can be read
        // and changed here rather than looked up on the devices page.
        openvpnUsers: currentOpenVPNUsernames().map((u) => ({ username: u, password: '' })),
        deviceNames: [],
        subId: props.client.subId || '',
      }
    : {
        name: '',
        note: '',
        group: '',
        telegramId: '',
        interfaceIds: props.interfaces[0] ? [props.interfaces[0].id] : [],
        quota: '',
        quotaUnit: 'GB',
        expiresIn: '',
        expiresUnit: 'days',
        deviceLimit: 1,
        rateMbit: '',
        startOnFirstUse: false,
        resetCycle: 'none',
        enabled: true,
        openvpnUsers: [],
        deviceNames: [],
        // Drawn now, as theirs is, so the operator sees the link's secret
        // before the customer exists and can replace it with one of their own.
        subId: randomHandle(16),
      },
)
const busy = ref(false)

// A new customer starts from the defaults on the settings page, so a reseller
// selling one plan does not retype it for every customer. Editing an existing
// one leaves their values alone.
onMounted(async () => {
  if (editing.value) return
  try {
    const cfg = await api.get('/api/settings')
    const d = cfg.settings
    if (d.defaultQuotaBytes) {
      const q = quotaToUnit(d.defaultQuotaBytes)
      form.value.quota = q.value
      form.value.quotaUnit = q.unit
    }
    if (d.defaultExpiryDays) {
      form.value.expiresIn = d.defaultExpiryDays
      form.value.expiresUnit = 'days'
    }
    if (d.defaultDeviceLimit) form.value.deviceLimit = d.defaultDeviceLimit
    if (d.defaultRateBitsPerSec) form.value.rateMbit = d.defaultRateBitsPerSec / 1e6
    if (d.defaultResetCycle) form.value.resetCycle = d.defaultResetCycle
  } catch {
    // The form is perfectly usable without them; failing to load a convenience
    // must not stop a customer being created.
  }
})

// Existing names are offered as suggestions rather than a fixed list: a group
// comes into being by being typed, so the field must stay free text.
const groupNames = ref([])
onMounted(async () => {
  try {
    groupNames.value = await api.groupNames()
  } catch {
    /* suggestions are optional; the field works without them */
  }
})

// The subscription, for the Links tab of an existing customer.
const sub = ref(null)
const subEnabled = ref(false)
async function loadSub() {
  if (!editing.value) return
  try {
    const s = await api.get('/api/subscription', { background: true })
    subEnabled.value = !!s?.enabled
    if (s?.enabled) sub.value = await api.get(`/api/clients/${props.client.id}/subscription`, { background: true })
  } catch {
    sub.value = null
  }
}
onMounted(loadSub)

async function copy(text) {
  try {
    await navigator.clipboard.writeText(String(text))
    notify(t('common.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

// The plan length in days while On hold is on: the expiry box, whatever
// unit it is in, rounded up to whole days.
function planDays() {
  const h = unitToHours(form.value.expiresIn, form.value.expiresUnit)
  return h > 0 ? Math.ceil(h / 24) : 0
}

// One login per user: the rows follow the Users count. On an edit a row
// starts with the user's current name; a blank password keeps theirs.
const openvpnRows = computed(() => {
  const n = Math.max(1, Number(form.value.deviceLimit) || 1)
  while (form.value.openvpnUsers.length < n) form.value.openvpnUsers.push({ username: '', password: '' })
  return form.value.openvpnUsers.slice(0, n)
})
// What to send: only what differs from what the user has now, by position,
// with blanks for the rest so positions line up.
function openvpnChanges() {
  const now = currentOpenVPNUsernames()
  const rows = openvpnRows.value.map((r, i) => ({
    username: r.username.trim() && r.username.trim() !== (now[i] || '') ? r.username.trim() : '',
    password: r.password || '',
  }))
  while (rows.length && !rows[rows.length - 1].username && !rows[rows.length - 1].password) rows.pop()
  return rows
}

const chosen = computed(() => props.interfaces.filter((i) => form.value.interfaceIds.includes(i.id)))
// Whether any tunnel picked is OpenVPN: those log in with a username and
// password, and a reseller may want to choose them rather than be handed
// generated ones.
const hasOpenVPN = computed(() => chosen.value.some((i) => i.protocol === 'openvpn'))

const allChosen = computed(
  () => props.interfaces.length > 0 && form.value.interfaceIds.length === props.interfaces.length,
)
function chooseAll() {
  form.value.interfaceIds = [...new Set([...form.value.interfaceIds, ...props.interfaces.map((i) => i.id)])]
}
function chooseNone() {
  form.value.interfaceIds = []
}

const serverOptions = computed(() =>
  props.interfaces.map((i) => ({
    value: i.id,
    label: i.name,
    tags: [
      { text: t(`protocol.${i.protocol}`), kind: 'proto' },
      ...(i.mode === 'amnezia' ? [{ text: 'AmneziaWG' }] : []),
    ],
    note: i.capacity ? (i.capacity - i.allocated).toLocaleString() : '',
  })),
)

// The tightest pool among the chosen servers, because that is the one that
// runs out first and stops the whole customer being created.
const poolLeft = computed(() => {
  const left = chosen.value.filter((i) => i.capacity).map((i) => i.capacity - i.allocated)
  return left.length ? Math.min(...left) : null
})

const presets = [
  { gb: 30, days: 30, devices: 1 },
  { gb: 50, days: 30, devices: 2 },
  { gb: 100, days: 30, devices: 3 },
  { gb: 200, days: 60, devices: 3 },
]
function applyPreset(p) {
  form.value.quota = p.gb
  form.value.quotaUnit = 'GB'
  form.value.expiresIn = p.days
  form.value.expiresUnit = 'days'
  form.value.deviceLimit = p.devices
}

// The devices of an existing customer, read-only here: they are issued and
// removed on the customer's own page, where each has its files.
const devices = computed(() => {
  const seen = new Map()
  for (const a of props.client?.accounts || []) {
    if (!seen.has(a.deviceName)) seen.set(a.deviceName, a)
  }
  return [...seen.values()]
})

const fieldError = ref({})
watch(form, () => { fieldError.value = {} }, { deep: true })

function validate() {
  const e = {}
  if (!form.value.name.trim()) e.name = t('client.nameRequired')
  if (!form.value.interfaceIds.length) e.servers = t('client.chooseAtLeastOne')
  if (form.value.startOnFirstUse && !(planDays() > 0)) e.expiresIn = t('client.durationRequired')
  const sid = form.value.subId.trim()
  if (sid && !/^[A-Za-z0-9_-]{8,64}$/.test(sid)) e.subId = t('client.subIdInvalid')
  fieldError.value = e
  if (Object.keys(e).length) {
    tab.value = e.name || e.servers || e.expiresIn ? 'basics' : 'credentials'
    return false
  }
  return true
}

async function submit() {
  if (!validate()) return
  busy.value = true
  try {
    // Sold in whatever unit was chosen -- half a gigabyte, thirty-six hours --
    // and stored in bytes and a timestamp, which is what is enforced.
    // Empty or 0 is no expiry, on creation and on an edit alike: null on the
    // wire clears a date the customer had. While On hold is on the same box
    // is the plan length, counted from their first connection instead.
    const hours = form.value.startOnFirstUse ? 0 : unitToHours(form.value.expiresIn, form.value.expiresUnit)
    const expiresAt = hours > 0 ? new Date(Date.now() + hours * 3600e3).toISOString() : null

    await emit('submit', {
      name: form.value.name.trim(),
      note: form.value.note.trim(),
      group: form.value.group.trim(),
      telegramId: Number(form.value.telegramId) || 0,
      interfaceIds: form.value.interfaceIds,
      quotaBytes: unitToBytes(form.value.quota, form.value.quotaUnit),
      expiresAt,
      deviceLimit: Number(form.value.deviceLimit) || 0,
      rateBitsPerSec: Math.max(0, Math.round(Number(form.value.rateMbit) * 1e6)) || 0,
      startOnFirstUse: form.value.startOnFirstUse,
      durationDays: form.value.startOnFirstUse ? planDays() : 0,
      resetCycle: form.value.resetCycle,
      ...(editing.value ? { status: form.value.enabled ? 'active' : 'disabled' } : { enabled: form.value.enabled }),
      ...(hasOpenVPN.value && openvpnChanges().length ? { openvpnUsers: openvpnChanges() } : {}),
      deviceNames: form.value.deviceNames.map((d) => d.trim()).filter(Boolean),
      ...(form.value.subId.trim() && form.value.subId.trim() !== (props.client?.subId || '') ? { subId: form.value.subId.trim() } : {}),
    })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w720" role="dialog" aria-modal="true" aria-labelledby="cf-title">
      <div class="amodal-head">
        <h2 id="cf-title" class="amodal-title">{{ editing ? t('client.edit') : t('client.create') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body cf-body">
        <div class="atabs-nav"><div class="atabs-list">
          <button type="button" class="atab" :class="{ active: tab === 'basics' }" @click="tab = 'basics'"><span>{{ t('client.tabBasics') }}</span></button>
          <button type="button" class="atab" :class="{ active: tab === 'credentials' }" @click="tab = 'credentials'"><span>{{ t('client.tabCredentials') }}</span></button>
          <button type="button" class="atab" :class="{ active: tab === 'links' }" @click="tab = 'links'"><span>{{ t('client.tabLinks') }}</span></button>
        </div></div>

        <form id="client-form" class="cf-form" @submit.prevent="submit">
          <!-- ══ Basics ══ -->
          <div v-show="tab === 'basics'">
            <div v-if="!editing" class="presets">
              <span class="presets-label">{{ t('client.presets') }}</span>
              <button v-for="p in presets" :key="`${p.gb}-${p.days}`" type="button" class="abtn small" @click="applyPreset(p)">
                <span class="ltr">{{ p.gb }}GB · {{ p.days }}d · {{ p.devices }}<AntIcon name="TeamOutlined" /></span>
              </button>
            </div>

            <div class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label required" for="cf-name">{{ t('client.name') }}</label>
                  <div class="acompact">
                    <label class="ainput block" :class="{ invalid: fieldError.name }"><input id="cf-name" v-model="form.name" maxlength="64" autofocus /></label>
                    <button type="button" class="abtn icon" :title="t('client.randomName')" :aria-label="t('client.randomName')" @click="form.name = randomHandle()"><AntIcon name="ReloadOutlined" /></button>
                  </div>
                  <p v-if="fieldError.name" class="field-error">{{ fieldError.name }}</p>
                </div>
              </div>
              <div class="acol6">
                <div class="aform-item">
                  <label class="aform-label" for="cf-quota">{{ t('client.quota') }} <HelpTip :text="t('client.quotaHint')" /></label>
                  <div class="acompact">
                    <label class="ainput number"><input id="cf-quota" v-model="form.quota" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="t('client.unlimited')" /></label>
                    <div class="aselect unit"><select v-model="form.quotaUnit" :aria-label="t('client.quotaUnit')"><option value="MB">MB</option><option value="GB">GB</option><option value="TB">TB</option></select></div>
                  </div>
                </div>
              </div>
              <div class="acol6">
                <div class="aform-item">
                  <label class="aform-label" for="cf-devices">{{ t('client.deviceLimit') }} <HelpTip :text="t('client.deviceLimitHint')" /></label>
                  <label class="ainput number"><input id="cf-devices" v-model="form.deviceLimit" type="number" min="0" max="50" class="ltr" :placeholder="t('client.unlimited')" /></label>
                </div>
              </div>
            </div>

            <div class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="cf-expires">{{ form.startOnFirstUse ? t('client.expireDays') : t('client.expiresIn') }} <HelpTip :text="form.startOnFirstUse ? t('client.durationHint') : t('client.expiresHint')" /></label>
                  <div class="acompact">
                    <label class="ainput number" :class="{ invalid: fieldError.expiresIn }"><input id="cf-expires" v-model="form.expiresIn" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="form.startOnFirstUse ? '30' : t('client.neverExpires')" /></label>
                    <div class="aselect unit"><select v-model="form.expiresUnit" :aria-label="t('client.expiresUnit')"><option value="hours">{{ t('unit.hours') }}</option><option value="days">{{ t('unit.days') }}</option><option value="months">{{ t('unit.months') }}</option></select></div>
                  </div>
                  <p v-if="fieldError.expiresIn" class="field-error">{{ fieldError.expiresIn }}</p>
                </div>
              </div>
              <div class="acol6">
                <div class="aform-item">
                  <label class="aform-label">{{ t('client.delayedStart') }} <HelpTip :text="t('client.startOnFirstUseHint')" /></label>
                  <div class="switch-line"><Toggle v-model="form.startOnFirstUse" :label="t('client.startOnFirstUse')" /></div>
                </div>
              </div>
            </div>

            <div class="arow16">
              <div class="acol6">
                <div class="aform-item">
                  <label class="aform-label" for="cf-rate">{{ t('client.rate') }} <HelpTip :text="t('client.rateHint')" /></label>
                  <label class="ainput number"><input id="cf-rate" v-model="form.rateMbit" type="number" min="0" step="1" class="ltr" placeholder="0" /><span class="ainput-suffix">Mbit/s</span></label>
                </div>
              </div>
              <div class="acol6">
                <div class="aform-item">
                  <label class="aform-label" for="cf-reset">{{ t('client.resetCycle') }}</label>
                  <div class="aselect"><select id="cf-reset" v-model="form.resetCycle"><option value="none">{{ t('reset.none') }}</option><option value="daily">{{ t('reset.daily') }}</option><option value="weekly">{{ t('reset.weekly') }}</option><option value="monthly">{{ t('reset.monthly') }}</option></select></div>
                </div>
              </div>
              <div class="acol6">
                <div class="aform-item">
                  <label class="aform-label" for="cf-tgid">{{ t('client.telegramId') }} <HelpTip :text="t('client.telegramIdHint')" /></label>
                  <label class="ainput number"><input id="cf-tgid" v-model.number="form.telegramId" type="number" min="0" class="ltr" :placeholder="t('client.telegramIdPlaceholder')" /></label>
                </div>
              </div>
            </div>

            <div class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="cf-note">{{ t('client.note') }}</label>
                  <label class="ainput block"><input id="cf-note" v-model="form.note" maxlength="256" :placeholder="t('client.notePlaceholder')" /></label>
                </div>
              </div>
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="cf-group">{{ t('client.group') }} <HelpTip :text="t('client.groupHint')" /></label>
                  <AutoComplete id="cf-group" v-model="form.group" :options="groupNames" :placeholder="t('client.groupPlaceholder')" />
                </div>
              </div>
            </div>

            <!-- Their Attached inbounds: Select all / Clear all above a
                 multiple select whose chosen items are removable tags. -->
            <div class="aform-item">
              <label class="aform-label required">{{ t('client.chooseServers') }}</label>
              <div class="bulk">
                <button type="button" class="abtn small" :disabled="allChosen" @click="chooseAll">{{ t('client.selectAll') }}</button>
                <button type="button" class="abtn small" :disabled="!form.interfaceIds.length" @click="chooseNone">{{ t('client.clearAll') }}</button>
              </div>
              <MultiSelect v-model="form.interfaceIds" :options="serverOptions" :placeholder="t('client.selectServers')" :invalid="!!fieldError.servers" />
              <p v-if="fieldError.servers" class="field-error">{{ fieldError.servers }}</p>
              <p v-else-if="poolLeft !== null" class="hint">{{ t('interface.addressesLeft') }}: <span class="ltr">{{ poolLeft.toLocaleString() }}</span></p>
            </div>

            <div class="aform-item enabled-line">
              <Toggle v-model="form.enabled" :label="t('client.enabled')" />
              <span class="enabled-text">{{ t('client.enabled') }}</span>
            </div>
            <p v-if="editing" class="hint foot-hint">{{ t('client.expiryResetHint') }}</p>
          </div>

          <!-- ══ Credentials ══ -->
          <div v-show="tab === 'credentials'">
            <template v-if="hasOpenVPN">
              <!-- One login per user, in the plan's order. A plan for
                   several is several people, each logging in as
                   themselves. -->
              <div v-for="(row, i) in openvpnRows" :key="i" class="aform-item ovpn-user">
                <label class="aform-label">
                  <template v-if="openvpnRows.length > 1">{{ t('client.userN', { n: i + 1 }) }}</template>
                  <template v-else>{{ t('client.openvpnLogin') }}</template>
                  <HelpTip v-if="i === 0" :text="t('client.openvpnHint')" />
                </label>
                <div class="arow16">
                  <div class="acol12">
                    <div class="acompact">
                      <label class="ainput block"><input v-model="row.username" class="ltr" autocomplete="off" maxlength="48" :placeholder="editing && row.username === '' ? t('client.openvpnKeep') : t('client.openvpnUsername') + ' — ' + t('client.openvpnGenerated')" /></label>
                      <button type="button" class="abtn icon" :title="t('client.generate')" :aria-label="t('client.generate')" @click="row.username = randomHandle(12)"><AntIcon name="ReloadOutlined" /></button>
                    </div>
                  </div>
                  <div class="acol12">
                    <div class="acompact">
                      <label class="ainput block"><input v-model="row.password" class="ltr" type="text" autocomplete="off" maxlength="64" :placeholder="editing ? t('client.openvpnPassword') + ' — ' + t('client.openvpnKeep') : t('client.openvpnPassword') + ' — ' + t('client.openvpnGenerated')" /></label>
                      <button type="button" class="abtn icon" :title="t('client.generate')" :aria-label="t('client.generate')" @click="row.password = randomSecret()"><AntIcon name="ReloadOutlined" /></button>
                    </div>
                  </div>
                </div>
              </div>
            </template>
            <div v-else class="aalert info"><AntIcon name="InfoCircleOutlined" /><span>{{ t('client.credsNoOpenVPN') }}</span></div>

            <div class="aform-item">
              <label class="aform-label" for="cf-subid">{{ t('client.subscriptionId') }} <HelpTip :text="t('client.subIdHint')" /></label>
              <div class="acompact">
                <label class="ainput block" :class="{ invalid: fieldError.subId }"><input id="cf-subid" v-model="form.subId" class="ltr" autocomplete="off" maxlength="64" /></label>
                <button type="button" class="abtn icon" :title="t('client.rotateSub')" :aria-label="t('client.rotateSub')" @click="form.subId = randomHandle(16)"><AntIcon name="ReloadOutlined" /></button>
              </div>
              <p v-if="fieldError.subId" class="field-error">{{ fieldError.subId }}</p>
              <p v-else-if="editing && form.subId.trim() !== (props.client.subId || '')" class="hint">{{ t('client.subIdChangeHint') }}</p>
            </div>

            <!-- Files follow the Users count: one per user, issued with the
                 plan. Nothing to type here on creation. -->
            <template v-if="editing">
              <div class="aform-item">
                <label class="aform-label">{{ t('client.devices') }} <HelpTip :text="t('client.devicesOnPage')" /></label>
                <div class="device-list">
                  <span v-for="d in devices" :key="d.id" class="atag">{{ d.deviceName }}</span>
                  <span v-if="!devices.length" class="hint">—</span>
                </div>
                <p class="hint">{{ t('client.devicesOnPage') }}</p>
              </div>
            </template>
          </div>

          <!-- ══ Links ══ -->
          <div v-show="tab === 'links'">
            <p class="tab-lead">{{ t('client.linksLead') }}</p>
            <div v-if="!editing" class="aalert info"><AntIcon name="InfoCircleOutlined" /><span>{{ t('client.linksAfterCreate') }}</span></div>
            <template v-else>
              <div class="aform-item">
                <label class="aform-label">{{ t('client.subscriptionTitle') }}</label>
                <div v-if="sub?.link" class="acompact">
                  <label class="ainput block disabled"><input class="ltr" :value="sub.link" readonly /></label>
                  <button type="button" class="abtn icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(sub.link)"><AntIcon name="CopyOutlined" /></button>
                  <a class="abtn icon" :href="sub.link" target="_blank" rel="noopener noreferrer" :title="t('client.openSubPage')"><AntIcon name="LinkOutlined" /></a>
                </div>
                <p v-else class="hint">{{ subEnabled ? t('client.linksAfterCreate') : t('client.subDisabled') }}</p>
              </div>
              <div class="aform-item">
                <label class="aform-label">{{ t('client.deviceFiles') }}</label>
                <p class="hint">{{ t('client.deviceFilesHint') }}</p>
              </div>
            </template>
          </div>
        </form>
      </div>

      <div class="amodal-foot">
        <button class="abtn" type="button" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="submit" form="client-form" :disabled="busy">
          <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
          <template v-else>{{ editing ? t('action.save') : t('action.create') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w720 { width: min(720px, calc(100vw - 32px)); }
.cf-body { max-height: 72vh; overflow-y: auto; overflow-x: hidden; }
.cf-form { padding-top: 4px; }

/* Their Row gutter={16}: 8px each side, pulled off the row so the outer
   columns line up with everything else in the dialog. Grid tracks divide
   the row exactly; flex percentages round up and drop the third column. */
.arow16 { display: grid; grid-template-columns: repeat(24, minmax(0, 1fr)); margin-inline: -8px; }
.arow16 > [class^='acol'] { padding-inline: 8px; min-width: 0; }
.acol12 { grid-column: span 12; }
.acol6 { grid-column: span 6; }
/* Their xs={24} for the wide fields and xs={12} for the small numbers: on
   a phone the numbers sit two to a row rather than one under another. And
   as Ant's modal does on a phone, the dialog takes the width less 8px a
   side and the page scrolls rather than a box inside the dialog, so no
   scrollbar sits on top of the controls. */
@media (max-width: 768px) {
  .acol12 { grid-column: 1 / -1; }
  .acol6 { grid-column: span 12; }
  .amodal.w720 { width: calc(100vw - 16px); padding: 16px; }
  .amodal-backdrop { padding: 16px 8px; align-items: flex-start; }
  .cf-body { max-height: none; overflow: visible; }
}

.aform-label.required::before { content: '*'; margin-inline-end: 4px; color: var(--bad); }

.ainput.invalid, .ainput.invalid:hover { border-color: var(--bad); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.acompact .aselect.unit { flex: 0 0 auto; width: auto; min-width: 84px; }
.acompact .abtn.icon { width: 32px; padding: 0; flex: none; }
.abtn.icon { display: inline-flex; align-items: center; justify-content: center; height: 32px; }

.switch-line { display: flex; align-items: center; height: 32px; }
.enabled-line { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.enabled-text { font-size: 14px; color: var(--ink); }

.bulk { display: flex; gap: 8px; margin-bottom: 8px; }
.presets { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
.presets-label { font-size: 12px; color: var(--faint); }
.presets .anticon { margin-inline-start: 3px; font-size: 12px; }

.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.foot-hint { margin-top: 0; }
.field-error { margin: 4px 0 0; font-size: 12px; color: var(--bad); }
.tab-lead { margin: 0 0 16px; font-size: 14px; color: var(--muted); }
.device-list { display: flex; flex-wrap: wrap; gap: 6px; padding: 4px 0; }
.aalert { margin-bottom: 24px; }
.anticon.spin { animation: aspin 1s linear infinite; }
@keyframes aspin { to { transform: rotate(360deg); } }
</style>
