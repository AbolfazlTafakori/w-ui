<script setup>
// Copying a tunnel. The port and the address range have to be new -- two
// tunnels sharing either would collide -- so they are suggested rather than
// assumed, moved one along from the original where that is free.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { freeName, nextSubnet } from '../../lib/suggest.js'
import { t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const props = defineProps({
  from: { type: Object, required: true },
  // Every tunnel, for a name none of them has.
  interfaces: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'cloned'])
// Stepped past names already in use, so the suggestion is one that will
// actually be accepted rather than one the operator has to correct.
const cloning = ref({
  from: props.from,
  name: freeName(props.from.name, props.interfaces.map((i) => i.name)),
  listenPort: (props.from.listenPort || 51820) + 1,
  subnet: nextSubnet(props.from.subnet),
  endpointHost: props.from.endpointHost || '',
})
const cloneBusy = ref(false)

async function submitClone() {
  cloneBusy.value = true
  try {
    const c = cloning.value
    await api.post(`/api/interfaces/${c.from.id}/clone`, {
      name: c.name.trim(),
      listenPort: Number(c.listenPort),
      subnet: c.subnet.trim(),
      endpointHost: c.endpointHost.trim(),
    })
    notify(t('interface.cloned'), 'success')
    emit('cloned')
    emit('close')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    cloneBusy.value = false
  }
}
</script>

<template>
  <!-- Copying a tunnel. Its own name, port, range and keys; no customers. -->
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="cl-title">
      <div class="card-head">
        <h2 id="cl-title">{{ t('interface.cloneTitle') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <form id="cl-form" class="card-body" @submit.prevent="submitClone">
        <p class="muted small">{{ cloning.from.name }} → {{ cloning.name }}</p>

        <div class="field">
          <label for="cl-name">{{ t('interface.name') }}</label>
          <input id="cl-name" v-model="cloning.name" required autofocus maxlength="32" class="ltr" />
        </div>

        <div class="grid-2">
          <div class="field">
            <label for="cl-port">{{ t('interface.port') }}</label>
            <input id="cl-port" v-model="cloning.listenPort" type="number" min="1" max="65535" required class="ltr" />
          </div>
          <div class="field">
            <label for="cl-subnet">{{ t('interface.subnet') }}</label>
            <input id="cl-subnet" v-model="cloning.subnet" required class="ltr" />
          </div>
        </div>

        <div class="field">
          <span class="hint">{{ t('interface.cloneHint') }}</span>
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="cl-form" class="btn primary" :disabled="cloneBusy">
          <span v-if="cloneBusy" class="spin"></span>
          <span v-else>{{ t('interface.clone') }}</span>
        </button>
      </div>
    </div>
  </div>
</template>
