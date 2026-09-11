<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Icon from './Icon.vue'
import ConfirmDialog from './ConfirmDialog.vue'

// The WARP dialog, in 3x-ui's order: create the account; then the account's
// four values and Delete account; Settings with the WARP+ licence key; Account
// info with Refresh and Change IP over the device record; and Outbound Status,
// where the hop is added or reset.
//
// Their "Auto Update IP Address" interval is not here: it is a scheduled
// re-registration, and a schedule this panel does not run is a setting that
// would sit there doing nothing.

const emit = defineEmits(['close', 'changed'])

const state = ref(null)
const loading = ref(true)
const busy = ref(false)
const license = ref('')
const licenseError = ref('')
const ask = ref(null)
const open = ref({ license: false })

const config = computed(() => state.value?.config || null)
const account = computed(() => config.value?.account || null)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    state.value = await api.get('/api/providers/warp')
    license.value = state.value?.account?.licenseKey || ''
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function run(fn, okMsg) {
  busy.value = true
  try {
    const st = await fn()
    if (st && typeof st === 'object' && 'registered' in st) state.value = st
    else await load(true)
    if (okMsg) notify(okMsg, 'success')
    emit('changed')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

const register = () => run(() => api.post('/api/providers/warp/register'))
const changeIP = () => run(() => api.post('/api/providers/warp/change-ip'), t('outbound.warp.changeIpSuccess'))
const addOutbound = () => run(() => api.post('/api/providers/warp/outbound'), t('outbound.created'))

async function setLicense() {
  licenseError.value = ''
  if (!license.value.trim()) return
  busy.value = true
  try {
    state.value = await api.post('/api/providers/warp/license', { license: license.value.trim() })
    notify(t('outbound.updated'), 'success')
  } catch (err) {
    licenseError.value = err.message || t('outbound.warp.licenseError')
  } finally {
    busy.value = false
  }
}

function deleteAccount() {
  ask.value = {
    title: t('outbound.warp.deleteAccount'),
    body: t('outbound.warp.deleteAccountBody'),
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.del('/api/providers/warp')
      await load()
      emit('changed')
    },
  }
}

async function runConfirmed() {
  if (!ask.value) return
  busy.value = true
  try {
    await ask.value.run()
    ask.value = null
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

function yesNo(v) {
  return v ? t('common.yes') : t('common.no')
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="warp-title">
      <div class="card-head">
        <h2 id="warp-title">WARP</h2>
        <button class="act" :title="t('common.close')" @click="emit('close')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="card-body">
        <div v-if="loading" class="empty"><span class="spin"></span></div>

        <template v-else-if="!state?.registered">
          <button class="btn primary" :disabled="busy" @click="register">
            <span v-if="busy" class="spin sm"></span>
            <Icon v-else name="link" :size="14" />
            <span>{{ t('outbound.warp.createAccount') }}</span>
          </button>
        </template>

        <template v-else>
          <table class="info">
            <tbody>
              <tr><td>{{ t('outbound.warp.accessToken') }}</td><td class="ltr mono">{{ state.account.accessToken }}</td></tr>
              <tr><td>{{ t('outbound.warp.deviceId') }}</td><td class="ltr mono">{{ state.account.deviceId }}</td></tr>
              <tr><td>{{ t('outbound.warp.licenseKey') }}</td><td class="ltr mono">{{ state.account.licenseKey }}</td></tr>
              <tr><td>{{ t('outbound.warp.privateKey') }}</td><td class="ltr mono">{{ state.account.privateKey }}</td></tr>
            </tbody>
          </table>
          <button class="btn danger" :disabled="busy" @click="deleteAccount">
            <Icon name="trash" :size="14" />
            <span>{{ t('outbound.warp.deleteAccount') }}</span>
          </button>

          <div class="divider"><span>{{ t('outbound.warp.settings') }}</span></div>

          <div class="collapse">
            <button type="button" class="collapse-head" :aria-expanded="open.license" @click="open.license = !open.license">
              <Icon name="chevronDown" :size="14" :class="{ flip: open.license }" />
              <span>{{ t('outbound.warp.licenseKeyLabel') }}</span>
            </button>
            <div v-if="open.license" class="collapse-body">
              <div class="field">
                <label for="warp-key">{{ t('outbound.warp.key') }}</label>
                <div class="inline">
                  <input id="warp-key" v-model="license" class="ltr" :placeholder="t('outbound.warp.keyPlaceholder')" spellcheck="false" />
                  <button class="btn primary" :disabled="busy || !license.trim()" @click="setLicense">
                    {{ t('action.update') }}
                  </button>
                </div>
                <span v-if="licenseError" class="form-error">{{ licenseError }}</span>
              </div>
            </div>
          </div>

          <div class="divider"><span>{{ t('outbound.warp.accountInfo') }}</span></div>

          <div class="btn-row">
            <button class="btn" :disabled="busy" @click="load(true)">
              <Icon name="refresh" :size="14" />
              <span>{{ t('action.refresh') }}</span>
            </button>
            <button class="btn" :disabled="busy" @click="changeIP">
              <Icon name="swap" :size="14" />
              <span>{{ t('outbound.warp.changeIp') }}</span>
            </button>
          </div>

          <table v-if="config" class="info">
            <tbody>
              <tr><td>{{ t('outbound.warp.deviceName') }}</td><td>{{ config.name || '—' }}</td></tr>
              <tr><td>{{ t('outbound.warp.deviceModel') }}</td><td>{{ config.model || '—' }}</td></tr>
              <tr><td>{{ t('outbound.warp.deviceEnabled') }}</td><td>{{ yesNo(config.enabled) }}</td></tr>
              <template v-if="account">
                <tr><td>{{ t('outbound.warp.accountType') }}</td><td>{{ account.account_type || '—' }}</td></tr>
                <tr><td>{{ t('outbound.warp.role') }}</td><td>{{ account.role || '—' }}</td></tr>
                <tr><td>{{ t('outbound.warp.warpPlusData') }}</td><td class="ltr">{{ bytes(account.warp_plus || 0) }}</td></tr>
                <tr><td>{{ t('outbound.warp.quota') }}</td><td class="ltr">{{ bytes(account.quota || 0) }}</td></tr>
                <tr v-if="account.usage != null"><td>{{ t('outbound.warp.usage') }}</td><td class="ltr">{{ bytes(account.usage || 0) }}</td></tr>
              </template>
            </tbody>
          </table>
          <p v-else class="muted small">{{ t('outbound.warp.fetchFirst') }}</p>

          <div class="divider"><span>{{ t('outbound.outboundStatus') }}</span></div>

          <div class="status-row">
            <template v-if="state.outbound">
              <span class="tag green">{{ t('status.enabled') }}</span>
              <span class="mono ltr">{{ state.outbound.tag }}</span>
              <button class="btn sm" :disabled="busy" @click="addOutbound">
                <Icon name="refresh" :size="13" />
                <span>{{ t('outbound.reset') }}</span>
              </button>
            </template>
            <template v-else>
              <span class="tag orange">{{ t('status.disabled') }}</span>
              <button class="btn sm primary" :disabled="busy" @click="addOutbound">
                <Icon name="plus" :size="13" />
                <span>{{ t('outbound.warp.addOutbound') }}</span>
              </button>
            </template>
          </div>
        </template>
      </div>
    </div>

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :confirm-label="ask?.confirmLabel || ''"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </div>
</template>

<style scoped>
.card-body {
  padding: 18px;
}
.info {
  table-layout: fixed;
  width: 100%;
  margin: 0 0 12px;
  border-collapse: collapse;
  font-size: var(--t-sm);
}
.info td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--line-soft);
  overflow-wrap: anywhere;
  word-break: break-all;
  vertical-align: top;
}
.info td:first-child {
  width: 34%;
  color: var(--muted);
}
.info td.mono {
  overflow-wrap: anywhere;
}
.divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 18px 0 12px;
  color: var(--ink);
  font-weight: 600;
  font-size: var(--t-sm);
}
.divider::before,
.divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--line);
}
.collapse {
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.collapse-head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  border: 0;
  background: var(--surface-2);
  color: var(--ink);
  font: inherit;
  text-align: start;
  cursor: pointer;
}
.collapse-body {
  padding: 12px;
}
.inline {
  display: flex;
  gap: 8px;
}
.inline input {
  flex: 1;
}
.btn-row {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.status-row {
  display: flex;
  align-items: center;
  gap: 10px;
}
.form-error {
  display: block;
  margin-top: 6px;
  color: var(--bad);
  font-size: var(--t-sm);
}
</style>
