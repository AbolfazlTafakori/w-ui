<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

const props = defineProps({
  outbound: { type: Object, default: null },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.outbound)
const busy = ref(false)
// Which input a validation error belongs to, so the message lands next to the
// field that caused it rather than in a toast at the top of the page.
const fieldError = ref({})
const formError = ref('')

const form = ref({
  tag: '',
  kind: 'wireguard',
  address: '',
  note: '',
  username: '',
  password: '',
  privateKey: '',
  peerPubKey: '',
  presharedKey: '',
  hopAddress: '',
  hopDns: '',
  hopMtu: 1380,
})

const isWireGuard = computed(() => form.value.kind === 'wireguard')
const isProxy = computed(() => form.value.kind === 'socks' || form.value.kind === 'http')

onMounted(() => {
  if (props.outbound) {
    // Secrets are never sent back to the browser, so the fields start empty and
    // an empty one on save means "keep what is stored".
    Object.assign(form.value, {
      tag: props.outbound.tag,
      kind: props.outbound.kind,
      address: props.outbound.address || '',
      note: props.outbound.note || '',
      username: props.outbound.username || '',
      peerPubKey: props.outbound.peerPubKey || '',
      hopAddress: props.outbound.hopAddress || '',
      hopDns: props.outbound.hopDns || '',
      hopMtu: props.outbound.hopMtu || 1380,
    })
  }
})

// Clearing the error as soon as the field is touched: leaving it under an input
// the operator has already corrected makes a fixed form look broken.
watch(
  () => ({ ...form.value }),
  () => {
    fieldError.value = {}
    formError.value = ''
  },
  { deep: true },
)

async function submit() {
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  try {
    const body = { ...form.value }
    const saved = props.outbound
      ? await api.patch(`/api/outbounds/${props.outbound.id}`, body)
      : await api.post('/api/outbounds', body)
    notify(editing.value ? t('outbound.updated') : t('outbound.created'), 'success')
    emit('saved', saved)
  } catch (err) {
    if (err.field) fieldError.value = { [err.field]: err.message }
    else formError.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="ob-title">
      <div class="card-head">
        <h2 id="ob-title">{{ editing ? t('outbound.edit') : t('outbound.add') }}</h2>
        <button class="act" :title="t('action.cancel')" @click="emit('cancel')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <form class="card-body form" @submit.prevent="submit">
        <p v-if="formError" class="form-error">{{ formError }}</p>

        <div class="field">
          <label for="ob-tag">{{ t('outbound.tag') }}</label>
          <input id="ob-tag" v-model="form.tag" :disabled="outbound?.builtin" autocomplete="off" />
          <p class="hint">{{ t('outbound.tagHint') }}</p>
          <p v-if="fieldError.tag" class="field-error">{{ fieldError.tag }}</p>
        </div>

        <div class="field">
          <label for="ob-kind">{{ t('outbound.kind') }}</label>
          <select id="ob-kind" v-model="form.kind" :disabled="outbound?.builtin">
            <option value="wireguard">{{ t('outbound.kind.wireguard') }}</option>
            <option value="socks">{{ t('outbound.kind.socks') }}</option>
            <option value="http">{{ t('outbound.kind.http') }}</option>
          </select>
          <p v-if="fieldError.kind" class="field-error">{{ fieldError.kind }}</p>
        </div>

        <div class="field">
          <label for="ob-address">{{ t('outbound.address') }}</label>
          <input
            id="ob-address"
            v-model="form.address"
            class="ltr"
            autocomplete="off"
            :placeholder="isWireGuard ? 'de.example.com:51820' : '127.0.0.1:1080'"
          />
          <p class="hint">{{ t('outbound.addressHint') }}</p>
          <p v-if="fieldError.address" class="field-error">{{ fieldError.address }}</p>
        </div>

        <!-- Everything the upstream issued us. None of it is ours to generate,
             which is why there is no "create keys" button here as there is on
             an interface. -->
        <template v-if="isWireGuard">
          <div class="field">
            <label for="ob-priv">{{ t('outbound.privateKey') }}</label>
            <input
              id="ob-priv"
              v-model="form.privateKey"
              class="ltr"
              type="password"
              autocomplete="new-password"
              :placeholder="editing ? t('form.leaveBlankToKeep') : ''"
            />
            <p class="hint">{{ t('outbound.privateKeyHint') }}</p>
            <p v-if="fieldError.privateKey" class="field-error">{{ fieldError.privateKey }}</p>
          </div>

          <div class="field">
            <label for="ob-peer">{{ t('outbound.peerPubKey') }}</label>
            <input id="ob-peer" v-model="form.peerPubKey" class="ltr" autocomplete="off" />
            <p v-if="fieldError.peerPubKey" class="field-error">{{ fieldError.peerPubKey }}</p>
          </div>

          <div class="field">
            <label for="ob-psk">{{ t('outbound.presharedKey') }}</label>
            <input
              id="ob-psk"
              v-model="form.presharedKey"
              class="ltr"
              type="password"
              autocomplete="new-password"
              :placeholder="editing ? t('form.leaveBlankToKeep') : t('form.optional')"
            />
            <p v-if="fieldError.presharedKey" class="field-error">{{ fieldError.presharedKey }}</p>
          </div>

          <div class="field">
            <label for="ob-hopaddr">{{ t('outbound.hopAddress') }}</label>
            <input
              id="ob-hopaddr"
              v-model="form.hopAddress"
              class="ltr"
              autocomplete="off"
              placeholder="10.9.0.2/32"
            />
            <p class="hint">{{ t('outbound.hopAddressHint') }}</p>
            <p v-if="fieldError.hopAddress" class="field-error">{{ fieldError.hopAddress }}</p>
          </div>

          <div class="field">
            <label for="ob-mtu">{{ t('outbound.hopMtu') }}</label>
            <input id="ob-mtu" v-model.number="form.hopMtu" class="ltr" type="number" />
            <p class="hint">{{ t('outbound.hopMtuHint') }}</p>
            <p v-if="fieldError.hopMtu" class="field-error">{{ fieldError.hopMtu }}</p>
          </div>
        </template>

        <template v-if="isProxy">
          <div class="field">
            <label for="ob-user">{{ t('outbound.username') }}</label>
            <input
              id="ob-user"
              v-model="form.username"
              class="ltr"
              autocomplete="off"
              :placeholder="t('form.optional')"
            />
            <p v-if="fieldError.username" class="field-error">{{ fieldError.username }}</p>
          </div>

          <div class="field">
            <label for="ob-pass">{{ t('outbound.password') }}</label>
            <input
              id="ob-pass"
              v-model="form.password"
              class="ltr"
              type="password"
              autocomplete="new-password"
              :placeholder="editing ? t('form.leaveBlankToKeep') : t('form.optional')"
            />
            <p v-if="fieldError.password" class="field-error">{{ fieldError.password }}</p>
          </div>
        </template>

        <div class="field">
          <label for="ob-note">{{ t('outbound.note') }}</label>
          <input id="ob-note" v-model="form.note" autocomplete="off" />
        </div>
      </form>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('cancel')">
          {{ t('action.cancel') }}
        </button>
        <button type="button" class="btn primary" :disabled="busy" @click="submit">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
