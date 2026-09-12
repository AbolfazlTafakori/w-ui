<script setup>
// The page-loading state 3x-ui shows: Ant's large Spin, with "loading"
// under it, over a spacer the height of the screen.
import { t } from '../lib/store.js'
</script>

<template>
  <div class="aspin-nested" aria-busy="true">
    <div class="aspin" role="status" :aria-label="t('common.loading')">
      <span class="aspin-dot"><i></i><i></i><i></i><i></i></span>
      <div class="aspin-text">{{ t('common.loading') }}</div>
    </div>
    <div class="loading-spacer"></div>
  </div>
</template>
