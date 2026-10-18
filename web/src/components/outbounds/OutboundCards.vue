<script setup>
// On a phone the table becomes the classic panel's outbound card list: the
// same figures, a card each, with the row menu in the card's corner.
import { ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { countryFlag, countryName, menuFor } from '../../lib/outbounds/rows.js'
import AntIcon from '../AntIcon.vue'
import MoreMenu from '../MoreMenu.vue'

defineProps({
  outbounds: { type: Array, required: true },
  pending: { type: Set, required: true },
})
// pick(o, idx, key): a row menu choice.
defineEmits(['pick', 'check'])

// The cards whose egress address is shown; hidden until asked, as theirs.
const egressShown = ref(new Set())
function toggleEgress(id) {
  const next = new Set(egressShown.value)
  next.has(id) ? next.delete(id) : next.add(id)
  egressShown.value = next
}
const kindLabel = (o) => t(`outbound.kind.${o.kind}`)
// The table has an edit button beside the menu; a card has only the menu,
// so editing leads it.
const cardMenu = (o, idx, count) => [{ key: 'edit', icon: 'EditOutlined', label: t('action.edit') }, ...menuFor(o, idx, count)]
</script>

<template>
  <div class="outbound-cards">
    <div v-if="!outbounds.length" class="card-empty">
      <AntIcon name="ExportOutlined" :size="32" />
      <div>{{ t('common.nothingYet') }}</div>
    </div>
    <div v-for="(o, idx) in outbounds" :key="o.id" class="outbound-card" :class="{ off: !o.enabled }">
      <div class="card-head">
        <div class="card-identity">
          <span class="card-num">{{ idx + 1 }}</span>
          <span class="tag-name" :title="o.tag">{{ o.tag }}</span>
          <span class="tag green">{{ kindLabel(o) }}</span>
        </div>
        <MoreMenu :items="cardMenu(o, idx, outbounds.length)" compact button-class="act round sm" :label="t('action.more')" @pick="(key) => $emit('pick', o, idx, key)" />
      </div>
      <div v-if="o.address" class="address-list">
        <span class="address-pill" :title="o.address">{{ o.address }}</span>
      </div>
      <div v-if="o.egressIp || o.egressCountry" class="card-egress">
        <div class="card-egress-row">
          <span>{{ t('outbound.egress') }}:</span>
          <button type="button" class="ip-toggle" :aria-label="t('outbound.toggleIpVisibility')" @click="toggleEgress(o.id)">
            <AntIcon :name="egressShown.has(o.id) ? 'EyeOutlined' : 'EyeInvisibleOutlined'" />
          </button>
          <span v-if="o.egressCountry" class="country-pill">
            <span>{{ countryFlag(o.egressCountry) }}</span>
            <span>{{ countryName(o.egressCountry, store.locale) || o.egressCountry }}</span>
          </span>
        </div>
        <div v-if="o.egressIp" class="card-egress-row" :title="o.egressIp">
          <span class="egress-family">{{ o.egressIp.includes(':') ? 'v6' : 'v4' }}:</span>
          <span class="egress-ip" :class="egressShown.has(o.id) ? 'address-visible' : 'address-hidden'">{{ o.egressIp }}</span>
        </div>
      </div>
      <div class="card-foot">
        <span class="traffic-up">↑ {{ bytes(o.txBytes || 0) }}</span>
        <span class="traffic-sep"></span>
        <span class="traffic-down">↓ {{ bytes(o.rxBytes || 0) }}</span>
        <span class="card-test">
          <span v-if="o.lastError" class="tag red" :title="o.lastError">{{ t('outbound.failed') }}</span>
          <span v-else-if="o.latencyMs" class="tag green">{{ o.latencyMs }} ms</span>
          <AntIcon v-else-if="pending.has(o.id)" name="LoadingOutlined" class="anticon-spin" />
          <button
            class="act round sm primary"
            :aria-label="t('outbound.check')"
            :disabled="pending.has(o.id) || o.kind === 'block'"
            @click="$emit('check', o)"
          >
            <AntIcon v-if="pending.has(o.id)" name="LoadingOutlined" class="anticon-spin" />
            <AntIcon v-else name="ThunderboltOutlined" />
          </button>
        </span>
      </div>
    </div>
  </div>
</template>
