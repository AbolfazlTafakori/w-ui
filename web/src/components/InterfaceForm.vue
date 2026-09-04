<script setup>
import { computed, nextTick, ref } from 'vue'
import { store, t } from '../lib/store.js'
import Icon from './Icon.vue'
import Toggle from './Toggle.vue'

const props = defineProps({
  // When present the dialog edits that interface instead of creating one.
  iface: { type: Object, default: null },
})
const emit = defineEmits(['close', 'submit'])

const editing = computed(() => !!props.iface)

const defaults = {
  wireguard: { name: 'wg0', listenPort: 51820, subnet: '10.66.0.0/16', mtu: 1420 },
  openvpn: { name: 'ovpn0', listenPort: 1194, subnet: '10.8.0.0/16', mtu: 1500 },
}

const form = ref(
  editing.value
    ? {
        name: props.iface.name,
        protocol: props.iface.protocol,
        listenPort: props.iface.listenPort,
        subnet: props.iface.subnet,
        endpointHost: props.iface.endpointHost,
        mtu: props.iface.mtu,
        dns: props.iface.dns || '',
        natInterface: props.iface.natInterface || '',
        mode: props.iface.mode,
        // Nested under openvpn on the way out of the API, because that is
        // where it is stored; the scrubbed view keeps it and drops the keys.
        transport: props.iface.openvpn?.transport || 'udp',
        enabled: props.iface.enabled,
      }
    : {
        ...defaults.wireguard,
        protocol: 'wireguard',
        endpointHost: '',
        dns: '1.1.1.1',
        natInterface: 'eth0',
        mode: 'standard',
        transport: 'udp',
        enabled: true,
      },
)
const busy = ref(false)

const protocols = computed(() => store.meta?.protocols || ['wireguard'])
const isWireGuard = computed(() => form.value.protocol === 'wireguard')

// Switching protocol swaps in that protocol's conventional port and subnet, so
// the operator is not left with a WireGuard port on an OpenVPN interface.
function onProtocolChange() {
  Object.assign(form.value, defaults[form.value.protocol])
  if (form.value.protocol === 'openvpn') form.value.mode = 'standard'
}

const fieldError = ref('')
const fieldName = ref('')

function clearError(name) {
  if (fieldName.value === name) {
    fieldName.value = ''
    fieldError.value = ''
  }
}

async function submit() {
  busy.value = true
  fieldName.value = ''
  fieldError.value = ''
  try {
    await emit('submit', { ...form.value })
  } finally {
    busy.value = false
  }
}

// Called by the page when the server refused the submission.
function showError(err) {
  if (err?.field) {
    fieldName.value = err.field
    fieldError.value = err.message
    // Put the operator in the field being complained about, so the fix is one
    // keystroke away rather than a hunt down the form.
    nextTick(() => {
      const el = document.querySelector(`[data-field="${err.field}"]`)
      el?.focus()
      el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
  }
}

defineExpose({ showError })
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="if-title">
      <div class="card-head">
        <Icon :name="editing ? 'edit' : 'plus'" :size="17" />
        <h2 id="if-title">{{ editing ? t('interface.edit') : t('interface.create') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="iface-form" class="card-body form" @submit.prevent="submit">
        <p v-if="fieldError && !fieldName" class="field-error" role="alert">{{ fieldError }}</p>
        <div v-if="editing" class="enable-row">
          <div>
            <div class="enable-label">{{ t('table.enabled') }}</div>
            <div class="hint">{{ t('interface.enabledHint') }}</div>
          </div>
          <Toggle v-model="form.enabled" :label="t('table.enabled')" class="spacer" />
        </div>

        <div class="grid-2">
          <div class="field">
            <label for="if-proto"><span class="req">*</span>{{ t('client.protocol') }}</label>
            <select id="if-proto" data-field="protocol" :class="{ bad: fieldName === 'protocol' }" @input="clearError('protocol')" v-model="form.protocol" :disabled="editing" @change="onProtocolChange">
              <option v-for="p in protocols" :key="p" :value="p">{{ t(`protocol.${p}`) }}</option>
            </select>
            <span v-if="fieldName === 'protocol'" class="field-error" role="alert">{{ fieldError }}</span>
          </div>

          <div class="field">
            <label for="if-name"><span class="req">*</span>{{ t('interface.name') }}</label>
            <input id="if-name" data-field="name" :class="{ bad: fieldName === 'name' }" @input="clearError('name')" v-model="form.name" :disabled="editing" required />
            <span v-if="fieldName === 'name'" class="field-error" role="alert">{{ fieldError }}</span>
          </div>

          <div class="field">
            <label for="if-host"><span class="req">*</span>{{ t('interface.endpoint') }}</label>
            <input
              id="if-host"
              data-field="endpointHost"
              :class="{ bad: fieldName === 'endpointHost' }"
              v-model="form.endpointHost"
              placeholder="vpn.example.com"
              required
              @input="clearError('endpointHost')"
            />
            <span v-if="fieldName === 'endpointHost'" class="field-error" role="alert">{{ fieldError }}</span>
            <span class="hint">{{ t('interface.endpointHint') }}</span>
          </div>

          <div class="field">
            <label for="if-port"><span class="req">*</span>{{ t('interface.port') }}</label>
            <input
              id="if-port"
              data-field="listenPort"
              :class="{ bad: fieldName === 'listenPort' }"
              @input="clearError('listenPort')"
              v-model="form.listenPort"
              type="number"
              min="1"
              max="65535"
              :disabled="editing"
              required
            />
            <span v-if="fieldName === 'listenPort'" class="field-error" role="alert">{{ fieldError }}</span>
          </div>

          <!-- OpenVPN only: WireGuard is UDP and has no other option, so
               offering the choice there would be offering a setting that does
               nothing. -->
          <div v-if="!isWireGuard" class="field">
            <label for="if-transport">{{ t('interface.transport') }}</label>
            <select
              id="if-transport"
              data-field="transport"
              :class="{ bad: fieldName === 'transport' }"
              @input="clearError('transport')"
              v-model="form.transport"
            >
              <option value="udp">{{ t('interface.transportUdp') }}</option>
              <option value="tcp">{{ t('interface.transportTcp') }}</option>
            </select>
            <span v-if="fieldName === 'transport'" class="field-error" role="alert">{{ fieldError }}</span>
            <span v-else-if="form.transport === 'tcp'" class="hint">{{ t('interface.transportTcpHint') }}</span>
            <span v-else class="hint">{{ t('interface.transportUdpHint') }}</span>
          </div>

          <div class="field">
            <label for="if-subnet"><span class="req">*</span>{{ t('interface.subnet') }}</label>
            <input id="if-subnet" data-field="subnet" :class="{ bad: fieldName === 'subnet' }" @input="clearError('subnet')" v-model="form.subnet" :disabled="editing" required />
            <span v-if="fieldName === 'subnet'" class="field-error" role="alert">{{ fieldError }}</span>
            <span class="hint">{{ t('interface.subnetHint') }}</span>
          </div>

          <div class="field">
            <label for="if-mtu">MTU</label>
            <input id="if-mtu" data-field="mtu" :class="{ bad: fieldName === 'mtu' }" @input="clearError('mtu')" v-model="form.mtu" type="number" min="576" max="9000" />
            <span v-if="fieldName === 'mtu'" class="field-error" role="alert">{{ fieldError }}</span>
          </div>

          <div class="field">
            <label for="if-dns">DNS</label>
            <input id="if-dns" v-model="form.dns" />
          </div>

          <div class="field">
            <label for="if-nat">{{ t('interface.natInterface') }}</label>
            <input id="if-nat" v-model="form.natInterface" />
          </div>
        </div>

        <div v-if="isWireGuard" class="field">
          <label for="if-mode">{{ t('interface.mode') }}</label>
          <select id="if-mode" data-field="mode" :class="{ bad: fieldName === 'mode' }" @input="clearError('mode')" v-model="form.mode" :disabled="editing">
            <option value="standard">{{ t('interface.mode.standard') }}</option>
            <option value="amnezia">{{ t('interface.mode.amnezia') }}</option>
          </select>
            <span v-if="fieldName === 'mode'" class="field-error" role="alert">{{ fieldError }}</span>
          <span class="hint">{{ t('interface.modeHint') }}</span>
        </div>

        <!-- Port, subnet, protocol and mode are baked into every config already
             handed out, so changing them would break live customers. -->
        <p v-if="editing" class="locked">
          <Icon name="lock" :size="13" />{{ t('interface.lockedHint') }}
        </p>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('close')">
          {{ t('action.cancel') }}
        </button>
        <button type="submit" form="iface-form" class="btn primary" :disabled="busy">
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
.enable-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--line-soft);
}
.enable-label {
  font-size: var(--t-sm);
  font-weight: 600;
}
.locked {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 0;
  padding: 10px 13px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  border: 1px solid var(--line);
  font-size: var(--t-xs);
  color: var(--muted);
  line-height: 1.55;
}
.locked svg {
  flex-shrink: 0;
  margin-top: 2px;
}
</style>
