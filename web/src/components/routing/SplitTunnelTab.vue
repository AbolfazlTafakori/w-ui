<script setup>
// Split tunnel: a whole-panel switch that turns one list into a routing policy,
// either way round. Exclude sends the list direct and tunnels the rest; include
// tunnels only the list and drops the rest. It edits the same Basic routing
// model, so the page's one Save stores it -- this tab is only a clearer home for
// it than a row buried among the block and direct lists.
import { computed } from 'vue'
import { t } from '../../lib/store.js'
import Icon from '../Icon.vue'
import TagInput from '../TagInput.vue'

const basic = defineModel('basic', { type: Object, required: true })
const props = defineProps({
  outbounds: { type: Array, default: () => [] },
  // The panel's own named address groups, offered alongside the countries.
  groups: { type: Array, default: () => [] },
  fieldError: { type: Object, default: () => ({}) },
})

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

// The split list takes both addresses and names, so it offers both sets.
const splitSuggestions = computed(() => [
  ...COUNTRIES,
  ...props.groups.filter((g) => g !== 'private').map((g) => ({ value: g, label: g })),
  ...SERVICE_SUGGESTIONS,
])

// The line under the mode that spells out what the chosen direction does, so an
// operator is never guessing which way round "split" runs.
const splitHint = computed(() => {
  if (basic.value.splitMode === 'include') return t('routing.splitIncludeDesc')
  if (basic.value.splitMode === 'exclude') return t('routing.splitExcludeDesc')
  return t('routing.splitOffDesc')
})
</script>

<template>
  <div class="alert info block centered mb-12">
    <Icon name="route" :size="14" />
    <span>{{ t('routing.splitNotice') }}</span>
  </div>

  <div class="setting-list">
    <div class="setting-item">
      <div class="setting-meta">
        <div class="setting-title">{{ t('routing.splitMode') }}</div>
        <div class="setting-desc">{{ splitHint }}</div>
      </div>
      <div class="setting-ctl">
        <select id="split-mode" v-model="basic.splitMode">
          <option value="">{{ t('routing.splitOff') }}</option>
          <option value="exclude">{{ t('routing.splitExclude') }}</option>
          <option value="include">{{ t('routing.splitInclude') }}</option>
        </select>
        <p v-if="fieldError.splitMode" class="field-error">{{ fieldError.splitMode }}</p>
      </div>
    </div>

    <template v-if="basic.splitMode">
      <div class="setting-item">
        <div class="setting-meta">
          <div class="setting-title">{{ t('routing.splitOutbound') }}</div>
          <div class="setting-desc">{{ t('routing.splitOutboundHint') }}</div>
        </div>
        <div class="setting-ctl">
          <select id="split-ob" v-model="basic.splitOutbound">
            <option value="">{{ t('routing.splitOutboundDefault') }}</option>
            <option
              v-for="o in outbounds"
              :key="o.id"
              :value="o.tag"
              :disabled="!o.enabled || o.tag === 'blocked'"
            >
              {{ o.tag }}
            </option>
          </select>
          <p v-if="fieldError.splitOutbound" class="field-error">{{ fieldError.splitOutbound }}</p>
        </div>
      </div>

      <div class="setting-item">
        <div class="setting-meta">
          <div class="setting-title">{{ t('routing.splitList') }}</div>
          <div class="setting-desc">{{ t('routing.splitListHint') }}</div>
        </div>
        <div class="setting-ctl">
          <TagInput v-model="basic.splitList" :suggestions="splitSuggestions" />
          <p v-if="fieldError.splitList" class="field-error">{{ fieldError.splitList }}</p>
        </div>
      </div>
    </template>
  </div>
</template>
