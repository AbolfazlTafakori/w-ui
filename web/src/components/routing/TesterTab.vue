<script setup>
// Route Tester: where a flow would go -- an address or name, a port, a
// protocol, and the tunnel it comes in on -- and which rule says so.
import { ref } from 'vue'
import { api } from '../../lib/api.js'
import { t } from '../../lib/store.js'
import Icon from '../Icon.vue'

defineProps({ interfaces: { type: Array, default: () => [] } })
// decided(ruleId): the rule the answer named; show-rule: go and see it.
const emit = defineEmits(['decided', 'show-rule'])

const probe = ref({ target: '', port: 443, protocol: 'tcp', clientId: 0, interfaceId: 0 })
const answer = ref(null)
const testing = ref(false)
const testError = ref('')


async function testRoute() {
  testing.value = true
  testError.value = ''
  answer.value = null
  try {
    answer.value = await api.post('/api/routing/test', probe.value)
    emit('decided', answer.value.ruleId || null)
  } catch (err) {
    testError.value = err.message
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <div class="alert info block mb-12">
    <Icon name="info" :size="14" />
    <span>{{ t('routing.testerNotice') }}</span>
  </div>

  <form class="tester-row" @submit.prevent="testRoute">
    <input v-model="probe.target" class="ltr grow" :placeholder="t('routing.testTarget')" :aria-label="t('routing.testTarget')" />
    <input v-model.number="probe.port" class="ltr port" type="number" min="1" max="65535" :placeholder="t('routing.testPort')" :aria-label="t('routing.testPort')" />
    <select v-model="probe.protocol" class="net" :aria-label="t('routing.testProtocol')">
      <option value="tcp">TCP</option>
      <option value="udp">UDP</option>
      <option value="icmp">ICMP</option>
    </select>
    <select v-model.number="probe.interfaceId" class="inb" :aria-label="t('routing.testInbound')">
      <option :value="0">{{ t('routing.testInbound') }}</option>
      <option v-for="i in interfaces" :key="i.id" :value="i.id">{{ i.name }}</option>
    </select>
    <button class="btn primary" type="submit" :disabled="testing || !probe.target">
      <span v-if="testing" class="spin sm"></span>
      <Icon v-else name="zap" :size="14" />
      <span>{{ t('routing.testRoute') }}</span>
    </button>
  </form>

  <p v-if="testError" class="field-error">{{ testError }}</p>

  <div v-if="answer" class="answer">
    <div v-if="answer.ruleId || answer.blocked" class="answer-head">
      <span>{{ t('routing.matchedOutbound') }}:</span>
      <span class="tag lg" :class="answer.blocked ? 'red' : 'green'">{{ answer.outbound || '—' }}</span>
      <template v-if="answer.balancer">
        <span class="muted">{{ t('routing.viaBalancer') }}:</span>
        <span class="tag purple">{{ answer.balancer }}</span>
      </template>
      <button v-if="answer.ruleId" type="button" class="btn sm" @click="emit('show-rule')">{{ t('routing.showRule') }}</button>
    </div>
    <div v-else class="alert warning block">
      <Icon name="alert" :size="14" />
      <span>{{ t('routing.noRuleMatched') }} <span class="tag green">{{ answer.outbound }}</span></span>
    </div>
    <p class="muted small">{{ answer.reason }}</p>
    <ul v-if="answer.steps?.length" class="steps">
      <li v-for="(s, i) in answer.steps" :key="i">{{ s }}</li>
    </ul>
  </div>
</template>
