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
        // Every server this customer already reaches, from their accounts.
        interfaceIds: [...new Set((props.client.accounts || []).map((a) => a.interfaceId))],
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
        interfaceIds: props.interfaces[0] ? [props.interfaces[0].id] : [],
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
const chosen = computed(() =>
  props.interfaces.filter((i) => form.value.interfaceIds.includes(i.id)),
)

function toggleInterface(id, on) {
  const next = new Set(form.value.interfaceIds)
  on ? next.add(id) : next.delete(id)
  form.value.interfaceIds = [...next]
}

// Taking all of them, or none. An operator selling access to every tunnel does
// it on almost every customer, and ticking six boxes by hand each time is the
// kind of small tax that a panel is supposed to remove.
const allChosen = computed(
  () =>
    props.interfaces.length > 0 &&
    form.value.interfaceIds.length === props.interfaces.length,
)

function chooseAll() {
  form.value.interfaceIds = props.interfaces.map((i) => i.id)
}

function chooseNone() {
  form.value.interfaceIds = []
}

// The tightest pool among the chosen servers, because that is the one that
// runs out first and stops the whole customer being created.
const poolLeft = computed(() => {
  const left = chosen.value
    .filter((i) => i.capacity)
    .map((i) => i.capacity - i.allocated)
  return left.length ? Math.min(...left) : null
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
      interfaceIds: form.value.interfaceIds,
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
    <div class="modal wide" role="dialog" aria-modal="true" aria-labelledby="cf-title">
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

        <!-- Who they are. Two short text fields and a note, on one line. -->
        <div class="cf-grid">
          <div class="field">
            <label for="cf-name"><span class="req">*</span>{{ t('client.name') }}</label>
            <input id="cf-name" v-model="form.name" required autofocus />
          </div>

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
          </div>

          <div class="field">
            <label for="cf-note">{{ t('client.note') }}</label>
            <input id="cf-note" v-model="form.note" :placeholder="t('client.notePlaceholder')" />
          </div>
        </div>

        <!-- Which servers. Every one this customer may use, on one plan: when
             one is blocked the others keep working on the same purchase, which
             is the whole reason to sell more than one.

             They wrap and the group grows. A box that scrolled inside a dialog
             which also scrolled meant an operator with six tunnels could not
             see what they had ticked without finding the right scrollbar. -->
        <fieldset class="cf-group">
          <legend>
            <span class="req">*</span>{{ t('client.chooseServers') }}
            <span class="cf-count" :class="{ none: !form.interfaceIds.length }">
              {{ form.interfaceIds.length }}/{{ interfaces.length }}
            </span>
            <span class="spacer cf-bulk">
              <button type="button" class="btn sm ghost" :disabled="allChosen" @click="chooseAll">
                {{ t('client.selectAll') }}
              </button>
              <button
                type="button"
                class="btn sm ghost"
                :disabled="!form.interfaceIds.length"
                @click="chooseNone"
              >
                {{ t('client.clearAll') }}
              </button>
            </span>
          </legend>

          <div class="server-grid">
            <label
              v-for="i in interfaces"
              :key="i.id"
              class="server"
              :class="{ on: form.interfaceIds.includes(i.id) }"
            >
              <input
                type="checkbox"
                :checked="form.interfaceIds.includes(i.id)"
                @change="toggleInterface(i.id, $event.target.checked)"
              />
              <span class="server-name">{{ i.name }}</span>
              <span class="tag proto">{{ t(`protocol.${i.protocol}`) }}</span>
              <span v-if="i.mode === 'amnezia'" class="tag">AmneziaWG</span>
              <span v-if="i.capacity" class="muted small num ltr spacer">
                {{ (i.capacity - i.allocated).toLocaleString() }}
              </span>
            </label>
          </div>

          <span v-if="!form.interfaceIds.length" class="field-error" role="alert">
            {{ t('client.chooseAtLeastOne') }}
          </span>
          <span v-else-if="poolLeft !== null" class="hint">
            {{ t('interface.addressesLeft') }}:
            <span class="num ltr">{{ poolLeft.toLocaleString() }}</span>
          </span>
        </fieldset>

        <!-- What they get. Five short numbers that belong to one decision, so
             they sit on one row rather than down a column. -->
        <fieldset class="cf-group">
          <legend>{{ t('client.planGroup') }}</legend>

          <div class="cf-grid tight">
            <div class="field">
              <label for="cf-quota">{{ t('client.quota') }} <span class="unit-note">GB</span></label>
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
            </div>

            <div class="field">
              <label for="cf-devices">
                <span class="req">*</span>{{ t('client.deviceLimit') }}
                <Icon name="info" :size="12" class="help" :title="t('client.deviceLimitHint')" />
              </label>
              <input id="cf-devices" v-model="form.deviceLimit" type="number" min="1" max="50" required />
            </div>

            <div class="field">
              <label for="cf-rate">
                {{ t('client.rate') }}
                <Icon name="info" :size="12" class="help" :title="t('client.rateHint')" />
              </label>
              <div class="unit-field">
                <input id="cf-rate" v-model="form.rateMbit" type="number" min="0" step="1" placeholder="0" />
                <span class="unit">Mbit/s</span>
              </div>
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

            <!-- Only meaningful once the plan is deferred, so it appears with
                 that choice rather than sitting empty beforehand. -->
            <div v-if="form.startOnFirstUse" class="field">
              <label for="cf-duration">{{ t('client.durationDays') }}</label>
              <div class="unit-field">
                <input id="cf-duration" v-model="form.durationDays" type="number" min="1" max="3650" step="1" />
                <span class="unit">{{ t('settings.days') }}</span>
              </div>
            </div>
          </div>

          <label class="check cf-defer">
            <input v-model="form.startOnFirstUse" type="checkbox" />
            <span>
              {{ t('client.startOnFirstUse') }}
              <span class="hint">{{ t('client.startOnFirstUseHint') }}</span>
            </span>
          </label>
          <span v-if="editing" class="hint">{{ t('client.expiryResetHint') }}</span>
        </fieldset>
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
  gap: 18px;
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

/* Fields sit side by side and wrap when there is no room, rather than stacking
   at every width. The plan numbers are short, so they get a smaller floor and
   five of them fit on one line. */
.cf-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 14px 16px;
}
.cf-grid.tight {
  grid-template-columns: repeat(auto-fit, minmax(152px, 1fr));
}

/* A named group rather than a card. Nesting a card inside a dialog would put
   three surfaces on top of each other to say one thing: these belong together.
   A rule and a label say it with none. */
.cf-group {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin: 0;
  padding: 0;
  border: 0;
  border-top: 1px solid var(--line-soft);
  padding-top: 16px;
}
.cf-group > legend {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 0;
  margin-bottom: 2px;
  font-size: var(--t-xs);
  font-weight: 600;
  color: var(--ink-2);
}
.cf-count {
  font-variant-numeric: tabular-nums;
  color: var(--muted);
  font-weight: 500;
}
.cf-count.none {
  color: var(--danger, var(--accent));
}
.cf-bulk {
  display: flex;
  gap: 6px;
  margin-inline-start: auto;
}

/* The servers wrap and the group grows with them. It used to be a box with its
   own scrollbar inside a dialog that also scrolled, so choosing the fourth
   tunnel meant finding the right scrollbar first. */
.server-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(268px, 1fr));
  gap: 8px;
}
.server {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  padding: 9px 11px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  cursor: pointer;
  transition:
    border-color 0.14s var(--ease),
    background-color 0.14s var(--ease);
}
.server:hover {
  border-color: color-mix(in srgb, var(--line) 50%, var(--accent) 30%);
}
/* Chosen is carried by the border and the ground, not by a coloured bar down
   one side. */
.server.on {
  border-color: var(--accent-line, var(--accent));
  background: var(--accent-soft);
}
.server input {
  width: auto;
  margin: 0;
  flex: none;
  cursor: pointer;
}
/* The tags give way, not the name. Truncating the name first left a tunnel
   showing one character, and the name is the only part an operator recognises;
   the protocol is already obvious from the tag's colour. */
.server-name {
  flex: 1 1 auto;
  min-width: 5ch;
  font-weight: 550;
}
.server .tag {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.server .spacer {
  margin-inline-start: auto;
}

/* The paragraph that explained deferred plans used to sit under the tickbox and
   make the dialog a screen taller. It reads beside its own control now. */
.cf-defer {
  align-items: flex-start;
  gap: 9px;
}
.cf-defer .hint {
  display: block;
  margin-top: 3px;
}
.cf-grid .unit-field {
  min-width: 0;
}
.cf-grid .unit-field input {
  min-width: 0;
}
.unit-note {
  color: var(--muted);
  font-weight: 500;
}
/* Held back until asked for, so five labels stay one line each. */
.help {
  color: var(--faint);
  cursor: help;
  vertical-align: -1px;
}
.help:hover {
  color: var(--ink-2);
}

@media (max-width: 640px) {
  .cf-bulk {
    width: 100%;
    margin-inline-start: 0;
  }
  .cf-group > legend {
    flex-wrap: wrap;
  }
}
</style>
