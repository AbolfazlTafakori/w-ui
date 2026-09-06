<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/store.js'
import { bytesToGigabytes } from '../lib/format.js'
import Icon from './Icon.vue'
import Toggle from './Toggle.vue'

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
    <div class="modal cf-modal" role="dialog" aria-modal="true" aria-labelledby="cf-title">
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

        <!-- A half-width field leading a row of quarters, which is the shape
             3x-ui uses: the one thing you type sits beside the numbers that
             qualify it, and the fourth number wraps to its own line. -->
        <div class="row">
          <div class="col-12">
            <div class="field">
              <label for="cf-name"><span class="req">*</span>{{ t('client.name') }}</label>
              <input id="cf-name" v-model="form.name" required autofocus />
            </div>
          </div>

          <div class="col-6">
            <div class="field">
              <!-- The unit sits in the field rather than the label. "Data
                   allowance (GB)" wrapped to two lines in a quarter column and
                   pushed its own input out of the row's alignment. -->
              <label for="cf-quota">
                {{ t('client.quota') }}
                <Icon name="info" :size="12" class="help" :title="t('client.quotaHint')" />
              </label>
              <div class="unit-field">
                <input
                  id="cf-quota"
                  v-model="form.quotaGB"
                  type="number"
                  min="0"
                  step="0.5"
                  :placeholder="t('client.unlimited')"
                />
                <span class="unit">GB</span>
              </div>
            </div>
          </div>

          <div class="col-6">
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
          </div>

          <div class="col-6">
            <div class="field">
              <label for="cf-devices">
                <span class="req">*</span>{{ t('client.deviceLimit') }}
                <Icon name="info" :size="12" class="help" :title="t('client.deviceLimitHint')" />
              </label>
              <input id="cf-devices" v-model="form.deviceLimit" type="number" min="1" max="50" required />
            </div>
          </div>
        </div>

        <div class="row">
          <div class="col-6">
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
          </div>

          <div class="col-6">
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

          <!-- A switch under its own label, the way 3x-ui puts "Start After
               First Use" in the row rather than as a stray tickbox below it. -->
          <div class="col-6">
            <div class="field">
              <label>
                {{ t('client.delayedStart') }}
                <Icon name="info" :size="12" class="help" :title="t('client.startOnFirstUseHint')" />
              </label>
              <Toggle v-model="form.startOnFirstUse" :label="t('client.startOnFirstUse')" />
            </div>
          </div>

          <div v-if="form.startOnFirstUse" class="col-6">
            <div class="field">
              <label for="cf-duration">{{ t('client.durationDays') }}</label>
              <div class="unit-field">
                <input id="cf-duration" v-model="form.durationDays" type="number" min="1" max="3650" step="1" />
                <span class="unit">{{ t('settings.days') }}</span>
              </div>
            </div>
          </div>
        </div>

        <div class="row">
          <div class="col-12">
            <div class="field">
              <label for="cf-note">{{ t('client.note') }}</label>
              <input id="cf-note" v-model="form.note" :placeholder="t('client.notePlaceholder')" />
            </div>
          </div>

          <div class="col-12">
            <div class="field">
              <label for="cf-group">
                {{ t('client.group') }}
                <Icon name="info" :size="12" class="help" :title="t('client.groupHint')" />
              </label>
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
          </div>
        </div>

        <!-- Full width, with the two bulk buttons directly above the control,
             which is where 3x-ui puts Select all / Clear all. -->
        <div class="field">
          <label><span class="req">*</span>{{ t('client.chooseServers') }}</label>
          <div class="bulk">
            <button type="button" class="btn sm" :disabled="allChosen" @click="chooseAll">
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
            <span class="bulk-count">{{ form.interfaceIds.length }} / {{ interfaces.length }}</span>
          </div>

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
        </div>

        <span v-if="editing" class="hint">{{ t('client.expiryResetHint') }}</span>
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
/* The geometry is 3x-ui's, read from its source rather than from a screenshot:
   a 720px dialog, Ant Design's 24-column grid at gutter 16, a label above its
   control with 8px between them, and 24px between rows. Ant Design's own
   defaults supply the rest, because 3x-ui overrides only colour tokens and
   leaves sizing alone. */

.card-body {
  display: flex;
  flex-direction: column;
  gap: 0;
}
.card-head svg {
  color: var(--muted);
}
.presets {
  display: flex;
  align-items: center;
  gap: 7px;
  flex-wrap: wrap;
  padding-bottom: 16px;
  margin-bottom: 24px;
  border-bottom: 1px solid var(--line-soft);
}

/* gutter={16}: 8px of padding on each column, pulled back off the row so the
   first and last columns still line up with everything else in the dialog. */
/* Their 24-column grid, as a grid rather than as flex percentages. Flex bases
   of 50% and 25% resolve to fractions on an odd row width, round up, overflow
   by less than a pixel, and drop the third column onto its own line -- which
   is exactly what happened: 342 + 171 + 171 into 683. Grid tracks divide the
   row exactly and cannot. */
.row {
  display: grid;
  grid-template-columns: repeat(24, minmax(0, 1fr));
  margin-inline: -8px;
}
.row > [class^='col-'] {
  /* gutter={16}: 8px each side, pulled back off the row so the outer columns
     still line up with everything else in the dialog. */
  padding-inline: 8px;
  /* Form.Item's own margin-bottom, which is the gap between rows as much as
     between fields. */
  margin-bottom: 24px;
  min-width: 0;
}
.col-12 {
  grid-column: span 12;
}
.col-6 {
  grid-column: span 6;
}

/* xs={24}: everything is full width on a phone, which is what their Col does
   below the md breakpoint. */
@media (max-width: 768px) {
  .col-12,
  .col-6 {
    grid-column: 1 / -1;
  }
}

/* Their label: normal weight at body size, sitting 8px above its control,
   rather than the small bold caps this panel uses elsewhere. */
.field > label {
  font-size: var(--t-base);
  font-weight: 400;
  color: var(--ink);
  margin-bottom: 2px;
}
.field {
  gap: 6px;
}

/* The question mark beside a label, which is where their tooltips live. It
   replaced two paragraphs that made the dialog a screen taller. */
.help {
  color: var(--faint);
  cursor: help;
  vertical-align: -1px;
}
.help:hover {
  color: var(--ink-2);
}

/* Select all / Clear all sit directly above the control they act on. */
.bulk {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.bulk-count {
  margin-inline-start: auto;
  font-size: var(--t-sm);
  font-variant-numeric: tabular-nums;
  color: var(--muted);
}

/* The tunnels wrap and the group grows with them. It was a box with its own
   scrollbar inside a dialog that also scrolled, so choosing the fourth tunnel
   meant finding the right scrollbar first. */
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
/* The tags give way, not the name: truncating the name first left a tunnel
   showing one character, and the name is the part an operator recognises. */
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

.cf-grid .unit-field,
.cf-grid .unit-field input,
.unit-field {
  min-width: 0;
}
.unit-field input {
  min-width: 0;
}
</style>
