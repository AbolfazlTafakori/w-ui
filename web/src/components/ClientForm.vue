<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/store.js'
import { bytesToGigabytes } from '../lib/format.js'
import Icon from './Icon.vue'

const props = defineProps({
  interfaces: { type: Array, required: true },
  // When present the dialog edits that client instead of creating one.
  client: { type: Object, default: null },
})
const emit = defineEmits(['close', 'submit'])

const editing = computed(() => !!props.client)

function daysLeft(iso) {
  if (!iso) return ''
  const d = Math.ceil((new Date(iso) - Date.now()) / 86400e3)
  return d > 0 ? d : ''
}

const form = ref(
  editing.value
    ? {
        name: props.client.name,
        note: props.client.note || '',
        group: props.client.group || '',
        interfaceId: props.client.accounts?.[0]?.interfaceId ?? props.interfaces[0]?.id ?? null,
        quotaGB: bytesToGigabytes(props.client.quotaBytes),
        expiresInDays: daysLeft(props.client.expiresAt),
        deviceLimit: props.client.deviceLimit,
        rateMbit: props.client.rateBitsPerSec ? props.client.rateBitsPerSec / 1e6 : '',
        startOnFirstUse: !!props.client.startOnFirstUse,
        durationDays: props.client.durationDays || '',
        resetCycle: props.client.resetCycle || 'none',
      }
    : {
        name: '',
        note: '',
        group: '',
        interfaceId: props.interfaces[0]?.id ?? null,
        quotaGB: '',
        expiresInDays: '',
        deviceLimit: 1,
        rateMbit: '',
        startOnFirstUse: false,
        durationDays: '',
        resetCycle: 'none',
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
    if (d.defaultQuotaBytes) form.value.quotaGB = d.defaultQuotaBytes / 1024 ** 3
    if (d.defaultExpiryDays) form.value.expiresInDays = d.defaultExpiryDays
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

// The protocol is not a separate choice. An account lives on an interface and
// an interface serves one protocol, so picking the interface picks the
// protocol, and the two can never be made to disagree. Moving an existing
// client between interfaces would mean reissuing every device, so the field is
// locked once the client exists.
const selected = computed(() =>
  props.interfaces.find((i) => i.id === form.value.interfaceId),
)

const poolLeft = computed(() => {
  const i = selected.value
  return i && i.capacity ? i.capacity - i.allocated : null
})

const presets = [
  { gb: 30, days: 30, devices: 1 },
  { gb: 50, days: 30, devices: 2 },
  { gb: 100, days: 30, devices: 3 },
  { gb: 200, days: 60, devices: 3 },
]

function applyPreset(p) {
  form.value.quotaGB = p.gb
  form.value.expiresInDays = p.days
  form.value.deviceLimit = p.devices
}

async function submit() {
  busy.value = true
  try {
    const days = Number(form.value.expiresInDays)
    const hasExpiry = Number.isFinite(days) && days > 0
    const expiresAt = hasExpiry
      ? new Date(Date.now() + days * 86400e3).toISOString()
      : null

    await emit('submit', {
      name: form.value.name.trim(),
      note: form.value.note.trim(),
      group: form.value.group.trim(),
      interfaceId: form.value.interfaceId,
      quotaGB: form.value.quotaGB,
      expiresAt,
      deviceLimit: Number(form.value.deviceLimit) || 1,
      rateBitsPerSec: Math.max(0, Math.round(Number(form.value.rateMbit) * 1e6)) || 0,
      startOnFirstUse: form.value.startOnFirstUse,
      durationDays: Number(form.value.durationDays) || 0,
      resetCycle: form.value.resetCycle,
      deviceNames: [],
    })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="cf-title">
      <div class="card-head">
        <Icon :name="editing ? 'edit' : 'plus'" :size="17" />
        <h2 id="cf-title">{{ editing ? t('client.edit') : t('client.create') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="client-form" class="card-body" @submit.prevent="submit">
        <div v-if="!editing" class="presets">
          <span class="muted small">{{ t('client.presets') }}</span>
          <button
            v-for="p in presets"
            :key="`${p.gb}-${p.days}`"
            type="button"
            class="btn sm"
            @click="applyPreset(p)"
          >
            <span class="ltr num">{{ p.gb }}GB · {{ p.days }}d</span>
            <Icon name="users" :size="12" />{{ p.devices }}
          </button>
        </div>

        <div class="grid-2">
          <div class="field">
            <label for="cf-name"><span class="req">*</span>{{ t('client.name') }}</label>
            <input id="cf-name" v-model="form.name" required autofocus />
          </div>

          <div class="field">
            <label for="cf-iface"><span class="req">*</span>{{ t('client.chooseInterface') }}</label>
            <select id="cf-iface" v-model="form.interfaceId" :disabled="editing" required>
              <option v-for="i in interfaces" :key="i.id" :value="i.id">
                {{ i.name }} — {{ t(`protocol.${i.protocol}`) }}{{ i.mode === 'amnezia' ? ' · AmneziaWG' : '' }}
              </option>
            </select>
            <span v-if="editing" class="hint">{{ t('client.interfaceLocked') }}</span>
            <span v-else-if="poolLeft !== null" class="hint">
              {{ t('interface.addressesLeft') }}:
              <span class="num ltr">{{ poolLeft.toLocaleString() }}</span>
            </span>
          </div>

          <div class="field">
            <label for="cf-quota">{{ t('client.quota') }} (GB)</label>
            <input
              id="cf-quota"
              v-model="form.quotaGB"
              type="number"
              min="0"
              step="0.5"
              :placeholder="t('client.unlimited')"
            />
          </div>

          <div class="field">
            <label for="cf-days">{{ t('client.expiresInDays') }}</label>
            <input
              id="cf-days"
              v-model="form.expiresInDays"
              type="number"
              min="0"
              :placeholder="t('client.neverExpires')"
            />
            <span v-if="editing" class="hint">{{ t('client.expiryResetHint') }}</span>
          </div>

          <div class="field">
            <label for="cf-devices"><span class="req">*</span>{{ t('client.deviceLimit') }}</label>
            <input id="cf-devices" v-model="form.deviceLimit" type="number" min="1" max="50" required />
            <span class="hint">{{ t('client.deviceLimitHint') }}</span>
          </div>

          <div class="field span-2">
            <label class="check">
              <input v-model="form.startOnFirstUse" type="checkbox" />
              <span>{{ t('client.startOnFirstUse') }}</span>
            </label>
            <span class="hint">{{ t('client.startOnFirstUseHint') }}</span>
          </div>

          <div v-if="form.startOnFirstUse" class="field">
            <label for="cf-duration">{{ t('client.durationDays') }}</label>
            <div class="unit-field">
              <input id="cf-duration" v-model="form.durationDays" type="number" min="1" max="3650" step="1" />
              <span class="unit">{{ t('settings.days') }}</span>
            </div>
          </div>

          <div class="field">
            <label for="cf-rate">{{ t('client.rate') }}</label>
            <div class="unit-field">
              <input id="cf-rate" v-model="form.rateMbit" type="number" min="0" step="1" placeholder="0" />
              <span class="unit">Mbit/s</span>
            </div>
            <span class="hint">{{ t('client.rateHint') }}</span>
          </div>

          <div class="field">
            <label for="cf-reset">{{ t('client.resetCycle') }}</label>
            <select id="cf-reset" v-model="form.resetCycle">
              <option value="none">{{ t('reset.none') }}</option>
              <option value="daily">{{ t('reset.daily') }}</option>
              <option value="weekly">{{ t('reset.weekly') }}</option>
              <option value="monthly">{{ t('reset.monthly') }}</option>
            </select>
          </div>
        </div>

        <div class="grid-2">
          <div class="field">
            <label for="cf-group">{{ t('client.group') }}</label>
            <input
              id="cf-group"
              v-model="form.group"
              list="cf-groups"
              :placeholder="t('client.groupPlaceholder')"
            />
            <datalist id="cf-groups">
              <option v-for="g in groupNames" :key="g" :value="g" />
            </datalist>
            <span class="hint">{{ t('client.groupHint') }}</span>
          </div>

          <div class="field">
            <label for="cf-note">{{ t('client.note') }}</label>
            <input id="cf-note" v-model="form.note" :placeholder="t('client.notePlaceholder')" />
          </div>
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('close')">
          {{ t('action.cancel') }}
        </button>
        <button type="submit" form="client-form" class="btn primary" :disabled="busy">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ editing ? t('action.save') : t('action.create') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.card-head svg {
  color: var(--muted);
}
.presets {
  display: flex;
  align-items: center;
  gap: 7px;
  flex-wrap: wrap;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line-soft);
}
</style>
