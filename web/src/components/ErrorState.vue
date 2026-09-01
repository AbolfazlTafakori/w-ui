<script setup>
import { computed } from 'vue'
import { t } from '../lib/store.js'
import Icon from './Icon.vue'

// What a page shows when it could not load.
//
// The alternative — an empty card, or a spinner that never stops — leaves an
// operator unable to tell a server that is down from one with nothing to show.
// This says which, and offers the one action that might help.
const props = defineProps({
  error: { type: [Object, String], default: null },
  // Whether the caller can retry. A page that has a load function passes true.
  canRetry: { type: Boolean, default: true },
})
const emit = defineEmits(['retry'])

const message = computed(() => {
  if (!props.error) return t('error.unknown')
  return typeof props.error === 'string' ? props.error : props.error.message
})

// A failure the panel caused reads differently from one the network caused, and
// the difference decides whether retrying is worth anything.
const kind = computed(() => (typeof props.error === 'object' && props.error?.kind) || 'server')
const status = computed(() => (typeof props.error === 'object' && props.error?.status) || 0)

const title = computed(() => {
  if (kind.value === 'network') return t('error.unreachable')
  if (kind.value === 'timeout') return t('error.timeout')
  if (status.value === 403) return t('error.forbidden')
  if (status.value === 404) return t('error.notFound')
  if (status.value >= 500) return t('error.serverSide')
  return t('error.failed')
})

const retryable = computed(() => {
  if (!props.canRetry) return false
  if (typeof props.error === 'object' && props.error && 'retryable' in props.error) {
    return props.error.retryable
  }
  return true
})
</script>

<template>
  <section class="card error-state" role="alert">
    <span class="error-mark"><Icon name="alert" :size="20" /></span>
    <h2>{{ title }}</h2>
    <p class="error-detail">{{ message }}</p>
    <button v-if="retryable" class="btn" @click="emit('retry')">
      <Icon name="refresh" :size="15" />
      <span>{{ t('error.tryAgain') }}</span>
    </button>
  </section>
</template>
