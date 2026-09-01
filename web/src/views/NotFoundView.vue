<script setup>
import { useRoute } from 'vue-router'
import { RouterLink } from 'vue-router'
import { t } from '../lib/store.js'
import Icon from '../components/Icon.vue'

// Unknown addresses used to be redirected to the overview, which quietly
// swallowed a typo and left the operator wondering whether they had clicked
// something wrong or the page had moved.
const route = useRoute()
</script>

<template>
  <section class="card error-state">
    <span class="error-mark"><Icon name="search" :size="20" /></span>
    <h2>{{ t('error.noSuchPage') }}</h2>
    <p class="error-detail ltr">{{ route.fullPath }}</p>
    <div class="error-actions">
      <RouterLink to="/" class="btn">{{ t('nav.overview') }}</RouterLink>
      <RouterLink to="/clients" class="btn ghost">{{ t('nav.clients') }}</RouterLink>
    </div>
  </section>
</template>
