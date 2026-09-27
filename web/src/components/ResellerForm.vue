<script setup>
// The reseller dialog, laid out as the customer dialog is: the same modal,
// the same 24-column rows, the same controls, a question mark where a field
// needs a word of explanation rather than a paragraph under every one of
// them. What a reseller is sold -- how many customers, how much traffic
// between them, until when, on which servers -- is one screen.
//
// The sign-in is drawn for them. Nobody picks a good password for somebody
// else by hand, and the owner has to hand both halves over anyway: the copy
// button puts the address, the username and the password on the clipboard in
// one go, ready for a message.
import { computed, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import { quotaToUnit, unitToBytes } from '../lib/format.js'
import AntIcon from './AntIcon.vue'
import Toggle from './Toggle.vue'
import MultiSelect from './MultiSelect.vue'
import AutoComplete from './AutoComplete.vue'
import HelpTip from './HelpTip.vue'
import DateField from './DateField.vue'

const props = defineProps({
  // The owner's servers, every one of them: a reseller is given a subset.
  interfaces: { type: Array, required: true },
  // Present to edit that operator; absent to add one.
  admin: { type: Object, default: null },
  // Group names already on the panel, offered for the label.
  groupNames: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'saved'])

const editing = computed(() => !!props.admin)

// The same alphabets the customer dialog draws from. A username a reseller
// can read over the phone; a password nobody has to.
function draw(n, alphabet) {
  const bytes = new Uint8Array(n)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}
const newUsername = () => draw(10, 'abcdefghijklmnopqrstuvwxyz0123456789')
const newPassword = () => draw(16, 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789')

// A timestamp as the local day it falls on, for the calendar.
function localDay(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}
function todayLocal() {
  return localDay(new Date().toISOString())
}
function monthFromToday() {
  const d = new Date()
  d.setMonth(d.getMonth() + 1)
  return localDay(d.toISOString())
}

const a = props.admin
const q = quotaToUnit(a?.quotaBytes)
const form = ref({
  username: a ? a.username : newUsername(),
  // Never prefilled on an edit: the stored one cannot be read back, and a
  // box that looked full would be saved unchanged and read as "the
  // password still works" when it had just been cleared.
  password: a ? '' : newPassword(),
  role: a ? a.role : 'reseller',
  enabled: a ? !!a.enabled : true,
  clientLimit: a?.clientLimit || '',
  quota: q.value,
  quotaUnit: q.unit,
  onHold: !!(a && !a.expiresAt && a.durationDays > 0),
  durationDays: a?.durationDays || 30,
  // A new reseller is offered a month on the calendar: a plan with no end
  // is something an owner should have to choose, not fall into.
  expiresOn: a ? localDay(a.expiresAt) : monthFromToday(),
  groupName: a?.groupName || '',
  note: a?.note || '',
  interfaceIds: [...(a?.interfaceIds || (props.interfaces[0] ? [props.interfaces[0].id] : []))],
})

const capped = computed(() => form.value.role === 'reseller')

const serverOptions = computed(() =>
  props.interfaces.map((i) => ({
    value: i.id,
    label: i.name,
    tags: [
      { text: t(`protocol.${i.protocol}`), kind: 'proto' },
      ...(i.mode === 'amnezia' ? [{ text: 'AmneziaWG' }] : []),
    ],
  })),
)
const allChosen = computed(
  () => props.interfaces.length > 0 && form.value.interfaceIds.length === props.interfaces.length,
)
function chooseAll() {
  form.value.interfaceIds = props.interfaces.map((i) => i.id)
}
function chooseNone() {
  form.value.interfaceIds = []
}

// Reseller plans people actually sell, in the order they are asked for.
const presets = [
  { days: 30, gb: 500, customers: 20 },
  { days: 30, gb: 1000, customers: 50 },
  { days: 90, gb: 3000, customers: 100 },
]
function applyPreset(p) {
  form.value.durationDays = p.days
  form.value.onHold = true
  form.value.quota = p.gb >= 1000 && p.gb % 1000 === 0 ? p.gb / 1000 : p.gb
  form.value.quotaUnit = p.gb >= 1000 && p.gb % 1000 === 0 ? 'TB' : 'GB'
  form.value.clientLimit = p.customers
}

// Everything needed to sign in, in one paste.
const signInURL = new URL('login', document.baseURI).href
async function copyLogin() {
  const lines = [
    `${t('admins.copy.panel')}: ${signInURL}`,
    `${t('admins.username')}: ${form.value.username.trim()}`,
  ]
  if (form.value.password) lines.push(`${t('admins.password')}: ${form.value.password}`)
  try {
    await navigator.clipboard.writeText(lines.join('\n'))
    notify(t('common.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

const fieldError = ref({})
watch(form, () => { fieldError.value = {} }, { deep: true })

function validate() {
  const e = {}
  const u = form.value.username.trim()
  if (u.length < 3 || u.length > 64 || !/^[A-Za-z0-9._-]+$/.test(u)) e.username = t('admins.usernameRule')
  if ((!editing.value || form.value.password) && form.value.password.length < 8) e.password = t('admins.passwordRule')
  if (capped.value && form.value.onHold) {
    const d = Number(form.value.durationDays)
    if (!Number.isInteger(d) || d < 1 || d > 3650) e.term = t('admins.daysRequired')
  }
  fieldError.value = e
  return !Object.keys(e).length
}

const busy = ref(false)
async function submit() {
  if (!validate()) return
  const f = form.value
  const body = {
    username: f.username.trim(),
    note: f.note.trim(),
    role: f.role,
    enabled: f.enabled,
  }
  if (f.password) body.password = f.password
  if (capped.value) {
    body.interfaceIds = f.interfaceIds
    body.clientLimit = Math.max(0, Math.floor(Number(f.clientLimit) || 0))
    body.quotaBytes = unitToBytes(f.quota, f.quotaUnit)
    if (f.onHold) {
      body.durationDays = Number(f.durationDays)
      body.expiresAt = null
    } else {
      body.durationDays = 0
      // The day chosen is theirs to the end of it, in the owner's own time.
      body.expiresAt = f.expiresOn ? new Date(`${f.expiresOn}T23:59:59`).toISOString() : null
    }
    const label = f.groupName.trim()
    if (label && label !== (props.admin?.groupName || '')) body.groupName = label
  }
  busy.value = true
  try {
    if (editing.value) await api.updateAdmin(props.admin.id, body)
    else await api.createAdmin(body)
    emit('saved')
  } catch (err) {
    const where = { interfaceIds: 'servers', durationDays: 'term', expiresAt: 'term' }[err.field] || err.field
    if (where && ['username', 'password', 'servers', 'term', 'groupName', 'role'].includes(where)) {
      fieldError.value = { [where]: err.message }
    } else {
      notify(err.message, 'error')
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w720" role="dialog" aria-modal="true" aria-labelledby="rf-title">
      <div class="amodal-head">
        <h2 id="rf-title" class="amodal-title">{{ editing ? t('admins.edit') : t('admins.add') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body rf-body">
        <form id="reseller-form" @submit.prevent="submit">
          <div v-if="!editing" class="presets">
            <span class="presets-label">{{ t('client.presets') }}</span>
            <button v-for="p in presets" :key="`${p.days}-${p.gb}`" type="button" class="abtn small" @click="applyPreset(p)">
              <span class="ltr">{{ p.days }}d · {{ p.gb >= 1000 ? p.gb / 1000 + 'TB' : p.gb + 'GB' }} · {{ p.customers }}<AntIcon name="TeamOutlined" /></span>
            </button>
          </div>

          <!-- The sign-in, on one line: drawn for them, and drawn again with
               the button beside each. -->
          <div class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="rf-user">{{ t('admins.username') }}</label>
                <div class="acompact">
                  <label class="ainput block" :class="{ invalid: fieldError.username }"><input id="rf-user" v-model="form.username" class="ltr" autocomplete="off" spellcheck="false" maxlength="64" /></label>
                  <button type="button" class="abtn icon" :title="t('client.generate')" :aria-label="t('client.generate')" @click="form.username = newUsername()"><AntIcon name="ReloadOutlined" /></button>
                </div>
                <p v-if="fieldError.username" class="field-error">{{ fieldError.username }}</p>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" :class="{ required: !editing }" for="rf-pass">
                  {{ editing ? t('admins.newPassword') : t('admins.password') }}
                  <HelpTip :text="editing ? t('admins.passwordHintEdit') : t('admins.passwordHint')" />
                </label>
                <div class="acompact">
                  <label class="ainput block" :class="{ invalid: fieldError.password }"><input id="rf-pass" v-model="form.password" class="ltr" type="text" autocomplete="new-password" spellcheck="false" maxlength="72" :placeholder="editing ? t('admins.passwordKeep') : ''" /></label>
                  <button type="button" class="abtn icon" :title="t('client.generate')" :aria-label="t('client.generate')" @click="form.password = newPassword()"><AntIcon name="ReloadOutlined" /></button>
                  <button type="button" class="abtn icon" :title="t('admins.copyLogin')" :aria-label="t('admins.copyLogin')" @click="copyLogin"><AntIcon name="CopyOutlined" /></button>
                </div>
                <p v-if="fieldError.password" class="field-error">{{ fieldError.password }}</p>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div class="acol8">
              <div class="aform-item">
                <label class="aform-label" for="rf-role">{{ t('admins.roleColumn') }} <HelpTip :text="capped ? t('admins.role.resellerHint') : t('admins.role.adminHint')" /></label>
                <div class="aselect"><select id="rf-role" v-model="form.role">
                  <option value="reseller">{{ t('admins.role.reseller') }}</option>
                  <option value="admin">{{ t('admins.role.admin') }}</option>
                </select></div>
              </div>
            </div>
            <template v-if="capped">
              <div class="acol8">
                <div class="aform-item">
                  <label class="aform-label" for="rf-limit">{{ t('admins.clientLimit') }} <HelpTip :text="t('admins.limitHint')" /></label>
                  <label class="ainput number"><input id="rf-limit" v-model="form.clientLimit" type="number" min="0" step="1" class="ltr" :placeholder="t('client.unlimited')" /></label>
                </div>
              </div>
              <div class="acol8">
                <div class="aform-item">
                  <label class="aform-label" for="rf-quota">{{ t('admins.traffic') }} <HelpTip :text="t('admins.quotaHint')" /></label>
                  <div class="acompact">
                    <label class="ainput number"><input id="rf-quota" v-model="form.quota" type="number" min="0" step="any" inputmode="decimal" class="ltr" :placeholder="t('client.unlimited')" /></label>
                    <div class="aselect unit"><select v-model="form.quotaUnit" :aria-label="t('client.quotaUnit')"><option value="GB">GB</option><option value="TB">TB</option></select></div>
                  </div>
                </div>
              </div>
            </template>
          </div>

          <div v-if="capped" class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="rf-term">
                  {{ form.onHold ? t('admins.termDays') : t('admins.until') }}
                  <HelpTip :text="form.onHold ? t('admins.onHoldHint') : t('admins.untilHint')" />
                </label>
                <label v-if="form.onHold" class="ainput number" :class="{ invalid: fieldError.term }">
                  <input id="rf-term" v-model="form.durationDays" type="number" min="1" max="3650" step="1" class="ltr" placeholder="30" />
                  <span class="ainput-suffix">{{ t('unit.days') }}</span>
                </label>
                <DateField v-else id="rf-term" v-model="form.expiresOn" :min="todayLocal()" :invalid="!!fieldError.term" :placeholder="t('admins.noEnd')" />
                <p v-if="fieldError.term" class="field-error">{{ fieldError.term }}</p>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label">{{ t('admins.onHold') }} <HelpTip :text="t('admins.onHoldHint')" /></label>
                <div class="switch-line"><Toggle v-model="form.onHold" :label="t('admins.onHold')" /></div>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label">{{ t('admins.enabled') }} <HelpTip :text="t('admins.enabledHint')" /></label>
                <div class="switch-line"><Toggle v-model="form.enabled" :label="t('admins.enabled')" /></div>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div v-if="capped" class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="rf-group">{{ t('admins.group') }} <HelpTip :text="t('admins.groupHint')" /></label>
                <AutoComplete id="rf-group" v-model="form.groupName" :options="groupNames" :placeholder="editing ? '' : `reseller:${form.username.trim() || '…'}`" :empty="t('admins.noGroupsYet')" />
                <p v-if="fieldError.groupName" class="field-error">{{ fieldError.groupName }}</p>
              </div>
            </div>
            <div :class="capped ? 'acol12' : 'acol24'">
              <div class="aform-item">
                <label class="aform-label" for="rf-note">{{ t('admins.note') }}</label>
                <label class="ainput block"><input id="rf-note" v-model="form.note" maxlength="256" :placeholder="t('client.notePlaceholder')" /></label>
              </div>
            </div>
          </div>

          <!-- The servers they may sell, picked as a customer's are. What is
               chosen here is exactly what their own customer form offers:
               nothing more, and nothing missing. -->
          <div v-if="capped" class="aform-item">
            <label class="aform-label">{{ t('admins.servers') }} <HelpTip :text="t('admins.serversHint')" /></label>
            <div class="bulk">
              <button type="button" class="abtn small" :disabled="allChosen" @click="chooseAll">{{ t('client.selectAll') }}</button>
              <button type="button" class="abtn small" :disabled="!form.interfaceIds.length" @click="chooseNone">{{ t('client.clearAll') }}</button>
            </div>
            <MultiSelect v-model="form.interfaceIds" :options="serverOptions" :placeholder="t('client.selectServers')" :invalid="!!fieldError.servers" />
            <p v-if="fieldError.servers" class="field-error">{{ fieldError.servers }}</p>
            <p v-else-if="!form.interfaceIds.length" class="hint warn-hint">{{ t('admins.noServersWarn') }}</p>
          </div>

          <div v-if="!capped" class="aform-item enabled-line">
            <Toggle v-model="form.enabled" :label="t('admins.enabled')" />
            <span class="enabled-text">{{ t('admins.enabled') }}</span>
          </div>
        </form>
      </div>

      <div class="amodal-foot">
        <button class="abtn" type="button" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="submit" form="reseller-form" :disabled="busy">
          <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
          <template v-else>{{ editing ? t('action.save') : t('action.create') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w720 { width: min(720px, calc(100vw - 32px)); }
.rf-body { overflow-x: hidden; padding-inline: 8px; margin-inline: -8px; padding-top: 4px; }

.arow16 { display: grid; grid-template-columns: repeat(24, minmax(0, 1fr)); margin-inline: -8px; }
.arow16 > [class^='acol'] { padding-inline: 8px; min-width: 0; }
.acol24 { grid-column: 1 / -1; }
.acol12 { grid-column: span 12; }
.acol8 { grid-column: span 8; }
.acol6 { grid-column: span 6; }
@media (max-width: 768px) {
  .acol12 { grid-column: 1 / -1; }
  .acol8 { grid-column: 1 / -1; }
  .acol6 { grid-column: span 12; }
  .amodal.w720 { width: calc(100vw - 16px); padding: 16px; max-height: calc(100dvh - 32px); }
  .amodal-backdrop { padding: 16px 8px; align-items: flex-start; }
}

.aform-label.required::before { content: '*'; margin-inline-end: 4px; color: var(--bad); }
.ainput.invalid, .ainput.invalid:hover { border-color: var(--bad); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 14px; white-space: nowrap; }
.acompact .aselect.unit { flex: 0 0 auto; width: auto; min-width: 72px; }
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
.warn-hint { color: var(--warn); }
.field-error { margin: 4px 0 0; font-size: 12px; color: var(--bad); }
.anticon.spin { animation: aspin 1s linear infinite; }
@keyframes aspin { to { transform: rotate(360deg); } }
</style>
