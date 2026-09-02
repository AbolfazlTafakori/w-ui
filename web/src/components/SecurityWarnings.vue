<script setup>
import { onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/store.js'
import Icon from './Icon.vue'

// What an attacker would notice about this installation.
//
// Shown at the top of Settings rather than on a page of its own, because a
// security page is a page nobody opens. Every one of these describes a server
// that is working perfectly and is one mistake away from being someone else's,
// so the point is that the operator sees it without going to look.
const warnings = ref([])
const loaded = ref(false)

onMounted(async () => {
  try {
    const res = await api.get('/api/security/warnings', { background: true })
    warnings.value = res.warnings || []
  } catch {
    // A panel that cannot audit itself should still let its settings be
    // changed, so this is deliberately silent.
  } finally {
    loaded.value = true
  }
})

const high = () => warnings.value.filter((w) => w.severity === 'high')
const rest = () => warnings.value.filter((w) => w.severity !== 'high')
</script>

<template>
  <section v-if="loaded && warnings.length" class="warnings" aria-live="polite">
    <div class="warnings-head">
      <Icon name="shield" :size="16" />
      <h2>{{ t('security.title') }}</h2>
      <span class="tag" :class="high().length ? 'red' : 'orange'">{{ warnings.length }}</span>
    </div>

    <ul class="warning-list">
      <li v-for="w in [...high(), ...rest()]" :key="w.id" :class="w.severity">
        <div class="warning-mark">
          <Icon :name="w.severity === 'high' ? 'alert' : 'info'" :size="15" />
        </div>
        <div class="warning-body">
          <strong>{{ w.title }}</strong>
          <p class="muted">{{ w.detail }}</p>
          <p class="fix">{{ w.fix }}</p>
        </div>
        <!-- One click from actionable. A warning that names a page and makes
             you go and find it is a warning that gets postponed. -->
        <RouterLink v-if="w.where" class="btn ghost sm" :to="w.where">
          {{ t('security.goFix') }}
        </RouterLink>
      </li>
    </ul>
  </section>
</template>
