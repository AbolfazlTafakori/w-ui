<script setup>
// The outbounds as a table: their columns, in their order and with their
// widths -- #, Tag, Address, Egress (with the eye that hides the address),
// Country, Traffic, Latency, Check.
import { ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { checkedAgo, countryFlag, countryName, menuFor, modeLabel } from '../../lib/outbounds/rows.js'
import Icon from '../Icon.vue'
import AntIcon from '../AntIcon.vue'
import MoreMenu from '../MoreMenu.vue'

defineProps({
  outbounds: { type: Array, required: true },
  pending: { type: Set, required: true },
  // The test mode, named on each row's Check button.
  mode: { type: String, required: true },
})
// pick(o, idx, key): a row menu choice.
defineEmits(['edit', 'pick', 'check'])

const showEgressIp = ref(false)
const kindLabel = (o) => t(`outbound.kind.${o.kind}`)
</script>

<template>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th class="w-num center">#</th>
          <th>{{ t('outbound.tag') }}</th>
          <th>{{ t('outbound.address') }}</th>
          <th class="w-egress">
            <span class="egress-header">
              {{ t('outbound.egress') }}
              <button
                type="button"
                class="ip-toggle"
                :aria-label="t('outbound.toggleIpVisibility')"
                :title="t('outbound.toggleIpVisibility')"
                @click="showEgressIp = !showEgressIp"
              >
                <AntIcon :name="showEgressIp ? 'EyeOutlined' : 'EyeInvisibleOutlined'" />
              </button>
            </span>
          </th>
          <th class="w-country">{{ t('outbound.country') }}</th>
          <th class="w-traffic">{{ t('outbound.traffic') }}</th>
          <th class="w-latency">{{ t('outbound.latency') }}</th>
          <th class="w-check center">{{ t('outbound.check') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!outbounds.length" class="empty-row">
          <td colspan="8">
            <div class="card-empty">
              <Icon name="upload" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="(o, idx) in outbounds" :key="o.id" :class="{ off: !o.enabled }">
          <!-- The number with the row's two circles beside it: edit, and
               the menu with everything else. -->
          <td class="w-num">
            <div class="rownum">
              <span class="num row-index">{{ idx + 1 }}</span>
              <div class="action-buttons">
                <button
                  class="act round sm"
                  :aria-label="t('action.edit')"
                  :title="t('action.edit')"
                  :disabled="pending.has(o.id)"
                  @click="$emit('edit', o)"
                >
                  <AntIcon name="EditOutlined" />
                </button>
                <MoreMenu :items="menuFor(o, idx, outbounds.length)" compact button-class="act round sm" :label="t('action.more')" @pick="(key) => $emit('pick', o, idx, key)" />
              </div>
            </div>
          </td>

          <td>
            <div class="identity-cell">
              <span class="tag-name" :title="o.tag">{{ o.tag }}</span>
              <div class="protocol-line">
                <span class="tag green">{{ kindLabel(o) }}</span>
              </div>
            </div>
            <div v-if="o.note" class="muted small">{{ o.note }}</div>
          </td>

          <td class="ltr">
            <div class="address-list">
              <span v-if="o.address" class="address-pill" :title="o.address">{{ o.address }}</span>
              <span v-else class="muted">—</span>
            </div>
          </td>

          <td class="ltr">
            <div v-if="o.egressIp" class="egress-stack">
              <span class="egress-address" :title="o.egressIp">
                <span class="egress-family">{{ o.egressIp.includes(':') ? 'v6' : 'v4' }}</span>
                <span class="egress-ip" :class="showEgressIp ? 'address-visible' : 'address-hidden'">{{ o.egressIp }}</span>
              </span>
            </div>
            <span v-else class="muted" :title="t('outbound.egressHint')">—</span>
          </td>

          <td>
            <span v-if="o.egressCountry" class="country-pill" title="Cloudflare trace">
              <span>{{ countryFlag(o.egressCountry) }}</span>
              <span>{{ countryName(o.egressCountry, store.locale) || o.egressCountry }}</span>
            </span>
            <span v-else class="muted" :title="t('outbound.egressHint')">—</span>
          </td>

          <td class="num ltr">
            <span class="traffic">
              <span class="up">↑ {{ bytes(o.txBytes || 0) }}</span>
              <span class="down">↓ {{ bytes(o.rxBytes || 0) }}</span>
            </span>
          </td>

          <td class="num ltr">
            <AntIcon v-if="pending.has(o.id)" name="LoadingOutlined" class="anticon-spin" />
            <template v-else-if="o.lastError">
              <span class="tag red" :title="o.lastError">{{ t('outbound.failed') }}</span>
            </template>
            <template v-else-if="o.latencyMs">
              <span class="tag green">{{ o.latencyMs }} ms</span>
              <div class="muted small">{{ checkedAgo(o) }}</div>
            </template>
            <span v-else class="muted">—</span>
          </td>

          <td class="center">
            <button
              class="act round primary check-btn"
              :aria-label="t('outbound.check')"
              :title="`${t('outbound.check')} (${modeLabel(mode)})`"
              :disabled="pending.has(o.id) || o.kind === 'block'"
              @click="$emit('check', o)"
            >
              <AntIcon v-if="pending.has(o.id)" name="LoadingOutlined" class="anticon-spin" />
              <AntIcon v-else name="ThunderboltOutlined" />
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
