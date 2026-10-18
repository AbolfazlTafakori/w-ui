<script setup>
// Issuing a token a node signs in with: a name for it, then the token
// itself, shown once -- it is stored hashed, and nothing can show it again.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'

defineEmits(['close'])

const name = ref('')
const busy = ref(false)
const issued = ref(null)

async function issue() {
  const n = name.value.trim()
  if (!n) return
  busy.value = true
  try {
    issued.value = await api.post('/api/tokens', { name: n })
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}

async function copy() {
  try {
    await navigator.clipboard.writeText(issued.value.token)
    notify(t('node.tokenCopied'), 'success')
  } catch {
    notify(t('api.copyFailed'), 'error')
  }
}
</script>

<template>
  <div v-if="!issued" class="amodal-backdrop" @click.self="$emit('close')">
    <div class="amodal w520" role="dialog" aria-modal="true" aria-labelledby="tk-title">
      <div class="amodal-head">
        <h2 id="tk-title" class="amodal-title">{{ t('node.issueToken') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="$emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <form id="tk-form" class="hform" @submit.prevent="issue">
          <div class="hrow">
            <label class="req" for="tk-name">{{ t('node.tokenName') }}</label>
            <div class="hctl">
              <label class="ainput block"><input id="tk-name" v-model="name" required autofocus maxlength="64" :placeholder="t('node.tokenNamePlaceholder')" /></label>
              <p class="hint">{{ t('node.tokenNameHint') }}</p>
            </div>
          </div>
        </form>
      </div>
      <div class="amodal-foot">
        <button type="button" class="abtn" @click="$emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="tk-form" class="abtn primary" :disabled="busy">
          <span v-if="busy" class="spin sm"></span>
          <template v-else>{{ t('node.issueToken') }}</template>
        </button>
      </div>
    </div>
  </div>

  <!-- A freshly issued token, shown once. -->
  <div v-else class="amodal-backdrop" @click.self="$emit('close')">
    <div class="amodal w520" role="dialog" aria-modal="true">
      <div class="amodal-head"><h2 class="amodal-title">{{ t('node.tokenIssued') }}</h2></div>
      <div class="amodal-body">
        <div class="nf-notice"><AntIcon name="ExclamationCircleOutlined" /><span>{{ t('node.tokenOnce') }}</span></div>
        <pre class="api-code ltr nf-token"><code>{{ issued.token }}</code></pre>
      </div>
      <div class="amodal-foot">
        <button class="abtn" @click="$emit('close')">{{ t('common.close') }}</button>
        <button class="abtn primary" @click="copy"><AntIcon name="CopyOutlined" /><span>{{ t('api.copy') }}</span></button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.nf-notice {
  display: flex; align-items: flex-start; gap: 10px;
  padding: 10px 12px; margin-bottom: 12px;
  border: 1px solid var(--warn-line, var(--line)); border-radius: 6px;
  background: var(--warn-soft); color: var(--ink); font-size: 13px; line-height: 1.5;
}
.nf-notice .anticon { color: var(--warn); margin-top: 2px; }
.nf-token { margin: 0; user-select: all; }
@media (max-width: 640px) {
  .hrow { grid-template-columns: 1fr; }
  .hrow > label { text-align: start; padding-top: 0; }
}
</style>
