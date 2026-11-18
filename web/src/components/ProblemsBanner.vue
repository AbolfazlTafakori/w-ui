<script setup>
// What is going wrong now, at the top of every page: the firewall table
// that could not be written, usage not being stored, a held device that was
// not disconnected, a node out of reach or not counted, a tunnel down. Each
// by its fixed code, in the reader's language. The owner also sees what
// failed; an operator only what touches their own customers and servers,
// and the server already left the detail out for them.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { store, t } from '../lib/store.js'
import Icon from './Icon.vue'

// Every label by its own key, so the check that each exists in every
// language can see them.
const LABEL = {
  'loop.failed': 'problem.loopFailed',
  'enforce.failed': 'problem.enforceFailed',
  'usage.notWritten': 'problem.usageNotWritten',
  'limit.disconnectFailed': 'problem.disconnectFailed',
  'node.unreachable': 'problem.nodeUnreachable',
  'node.limitPartial': 'problem.nodeLimitPartial',
  'tunnel.down': 'problem.tunnelDown',
}

const items = ref([])
let timer = null

async function load() {
  try {
    items.value = (await api.get('/api/problems', { background: true })).items || []
  } catch {
    // The list of problems failing is not itself shown as one: the page
    // the operator is on says what failed for it.
  }
}
onMounted(() => {
  load()
  timer = setInterval(load, 30_000)
})
onBeforeUnmount(() => clearInterval(timer))

const owner = computed(() => !!store.admin?.managesPanel)
const lines = computed(() =>
  items.value.map((p) => ({
    key: p.code + (p.node || '') + (p.tunnel || '') + (p.clientId || '') + (p.device || ''),
    text: t(LABEL[p.code] || 'problem.other', { node: p.node || '', tunnel: p.tunnel || '', name: p.name || '', device: p.device || '' }),
    detail: owner.value ? p.detail || '' : '',
  })),
)
</script>

<template>
  <div v-if="lines.length" class="banner warn problems" role="status">
    <Icon name="alert" :size="15" />
    <ul>
      <li v-for="l in lines" :key="l.key">
        <span>{{ l.text }}</span>
        <code v-if="l.detail" class="ltr problem-detail">{{ l.detail }}</code>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.problems { align-items: flex-start; }
.problems ul { margin: 0; padding: 0; list-style: none; display: grid; gap: 4px; }
.problem-detail { display: block; margin-top: 2px; font-size: 12px; opacity: 0.85; word-break: break-word; }
</style>
