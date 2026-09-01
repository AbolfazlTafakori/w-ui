<script setup>
import { ref, computed } from 'vue'
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
        enabled: props.iface.enabled,
      }
    : {
        ...defaults.wireguard,
        protocol: 'wireguard',
        endpointHost: '',
        dns: '1.1.1.1',
        natInterface: 'eth0',
        mode: 'standard',
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

async function submit() {
  busy.value = true
  try {
    await emit('submit', { ...form.value })
  } finally {
    busy.value = false
  }
}
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
            <select id="if-proto" v-model="form.protocol" :disabled="editing" @change="onProtocolChange">
              <option v-for="p in protocols" :key="p" :value="p">{{ t(`protocol.${p}`) }}</option>
            </select>
          </div>

          <div class="field">
            <label for="if-name"><span class="req">*</span>{{ t('interface.name') }}</label>
            <input id="if-name" v-model="form.name" :disabled="editing" required />
          </div>

          <div class="field">
            <label for="if-host"><span class="req">*</span>{{ t('interface.endpoint') }}</label>
            <input id="if-host" v-model="form.endpointHost" placeholder="vpn.example.com" required />
            <span class="hint">{{ t('interface.endpointHint') }}</span>
          </div>

          <div class="field">
            <label for="if-port"><span class="req">*</span>{{ t('interface.port') }}</label>
            <input
              id="if-port"
              v-model="form.listenPort"
              type="number"
              min="1"
              max="65535"
              :disabled="editing"
              required
            />
          </div>

          <div class="field">
            <label for="if-subnet"><span class="req">*</span>{{ t('interface.subnet') }}</label>
            <input id="if-subnet" v-model="form.subnet" :disabled="editing" required />
            <span class="hint">{{ t('interface.subnetHint') }}</span>
          </div>

          <div class="field">
            <label for="if-mtu">MTU</label>
            <input id="if-mtu" v-model="form.mtu" type="number" min="576" max="9000" />
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
          <select id="if-mode" v-model="form.mode" :disabled="editing">
            <option value="standard">{{ t('interface.mode.standard') }}</option>
            <option value="amnezia">{{ t('interface.mode.amnezia') }}</option>
          </select>
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
