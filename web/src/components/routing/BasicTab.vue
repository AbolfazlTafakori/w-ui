<script setup>
// Basic Routing: the switches and lists most panels need -- the default
// outbound, failing closed, what is blocked, what goes direct, what resolves
// IPv4 only. Their list of settings rows: title and description on the left,
// the control on the right. The page saves it with its one Save.
import { computed } from 'vue'
import { t } from '../../lib/store.js'
import Icon from '../Icon.vue'
import Toggle from '../Toggle.vue'
import TagInput from '../TagInput.vue'

const basic = defineModel('basic', { type: Object, required: true })
const props = defineProps({
  outbounds: { type: Array, default: () => [] },
  // The panel's own named address groups.
  groups: { type: Array, default: () => [] },
  // What the resolver has resolved, said under the lists.
  resolver: { type: Object, default: null },
  fieldError: { type: Object, default: () => ({}) },
})

// Their pick-lists. The countries are "geoip:xx", fetched as address lists
// the first time one is used; the rest are this panel's own named groups.
// Anything else -- an address, a range -- can still be typed.
const COUNTRIES = [
  { value: 'geoip:private', label: 'Private IPs' },
  { value: 'geoip:ir', label: '🇮🇷 Iran' },
  { value: 'geoip:cn', label: '🇨🇳 China' },
  { value: 'geoip:ru', label: '🇷🇺 Russia' },
  { value: 'geoip:vn', label: '🇻🇳 Vietnam' },
  { value: 'geoip:es', label: '🇪🇸 Spain' },
  { value: 'geoip:id', label: '🇮🇩 Indonesia' },
  { value: 'geoip:ua', label: '🇺🇦 Ukraine' },
  { value: 'geoip:tr', label: '🇹🇷 Türkiye' },
  { value: 'geoip:br', label: '🇧🇷 Brazil' },
]
const ipSuggestions = computed(() => [
  ...COUNTRIES,
  ...props.groups.filter((g) => g !== 'private').map((g) => ({ value: g, label: g })),
])
const SERVICE_SUGGESTIONS = [
  { value: 'apple.com', label: 'Apple' },
  { value: 'meta.com', label: 'Meta' },
  { value: 'google.com', label: 'Google' },
  { value: 'openai.com', label: 'OpenAI' },
  { value: 'spotify.com', label: 'Spotify' },
  { value: 'netflix.com', label: 'Netflix' },
  { value: 'reddit.com', label: 'Reddit' },
  { value: 'speedtest.net', label: 'Speedtest' },
]
</script>

<template>
  <div class="alert warning block centered mb-12">
    <Icon name="alert" :size="14" />
    <span>{{ t('routing.blockNotice') }}</span>
  </div>

  <div class="setting-list">
    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.defaultOutbound') }}</div>
        <div class="setting-desc">{{ t('routing.defaultOutboundHint') }}</div>
      </div>
      <div class="setting-ctl">
        <select id="def-ob" v-model="basic.defaultOutbound">
          <option v-for="o in outbounds" :key="o.id" :value="o.tag" :disabled="!o.enabled">{{ o.tag }}</option>
        </select>
        <p v-if="fieldError.defaultOutbound" class="field-error">{{ fieldError.defaultOutbound }}</p>
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.failClosed') }}</div>
        <div class="setting-desc">{{ t('routing.failClosedDesc') }}</div>
      </div>
      <div class="setting-ctl">
        <Toggle v-model="basic.failClosed" :label="t('routing.failClosed')" />
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.blockBitTorrent') }}</div>
      </div>
      <div class="setting-ctl">
        <Toggle v-model="basic.blockBitTorrent" :label="t('routing.blockBitTorrent')" />
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.blockIps') }}</div>
      </div>
      <div class="setting-ctl">
        <TagInput v-model="basic.blockIps" :suggestions="ipSuggestions" />
        <p v-if="fieldError.blockIps" class="field-error">{{ fieldError.blockIps }}</p>
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.blockDomains') }}</div>
      </div>
      <div class="setting-ctl">
        <TagInput v-model="basic.blockDomains" />
        <p v-if="fieldError.blockDomains" class="field-error">{{ fieldError.blockDomains }}</p>
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.blockPorts') }}</div>
        <div class="setting-desc">{{ t('routing.portHint') }}</div>
      </div>
      <div class="setting-ctl">
        <TagInput v-model="basic.blockPorts" />
        <p v-if="fieldError.blockPorts" class="field-error">{{ fieldError.blockPorts }}</p>
      </div>
    </div>
  </div>

  <div class="alert warning block centered mb-12">
    <Icon name="alert" :size="14" />
    <span>{{ t('routing.directNotice') }}</span>
  </div>

  <div class="setting-list">
    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.directIps') }}</div>
      </div>
      <div class="setting-ctl">
        <TagInput v-model="basic.directIps" :suggestions="ipSuggestions" />
        <p v-if="fieldError.directIps" class="field-error">{{ fieldError.directIps }}</p>
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.directDomains') }}</div>
      </div>
      <div class="setting-ctl">
        <TagInput v-model="basic.directDomains" />
        <p v-if="fieldError.directDomains" class="field-error">{{ fieldError.directDomains }}</p>
      </div>
    </div>

    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.ipv4Routing') }}</div>
        <div class="setting-desc">{{ t('routing.ipv4RoutingDesc') }}</div>
      </div>
      <div class="setting-ctl">
        <TagInput v-model="basic.ipv4Domains" :suggestions="SERVICE_SUGGESTIONS" />
        <p v-if="fieldError.ipv4Domains" class="field-error">{{ fieldError.ipv4Domains }}</p>
      </div>
    </div>
  </div>

  <p v-if="resolver" class="muted small resolver">
    {{ t('routing.resolverStatus').replace('{names}', resolver.names).replace('{addresses}', resolver.addresses) }}
  </p>
</template>
