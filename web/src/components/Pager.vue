<script setup>
// A list's pages, as Ant draws them: the total, the pages -- every one up to
// seven, else the ends and a window around the current one with jumps between
// -- and, past ten rows, a size changer.
import { computed } from 'vue'
import { store, t } from '../lib/store.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  total: { type: Number, required: true },
  page: { type: Number, required: true },
  pageSize: { type: Number, required: true },
  // The sizes offered; none hides the changer.
  sizes: { type: Array, default: () => [] },
  small: { type: Boolean, default: false },
})
const emit = defineEmits(['go', 'size'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const totalPages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const items = computed(() => {
  const total = totalPages.value
  const cur = props.page
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1)
  const out = [1]
  const from = Math.max(2, cur - 2)
  const to = Math.min(total - 1, cur + 2)
  if (from > 2) out.push('prev')
  for (let i = from; i <= to; i++) out.push(i)
  if (to < total - 1) out.push('next')
  out.push(total)
  return out
})
// "25 / page", number first, in every language. Persian digits are
// right-to-left characters, so "۲۵ / صفحه" would turn round as one run; a
// left-to-right mark after the number keeps it in front.
const LRM = String.fromCharCode(0x200e)
const sizeLabel = (n) => `${nf(n)}${LRM} / ${t('client.perPage')}`
const jump = (it) => emit('go', it === 'prev' ? Math.max(1, props.page - 5) : Math.min(totalPages.value, props.page + 5))
</script>

<template>
  <ul class="apagination" :class="{ small }">
    <li class="apagination-total">{{ nf(total) }}</li>
    <li><button class="apage" :disabled="page <= 1" :aria-label="t('action.prev')" @click="emit('go', page - 1)"><AntIcon name="LeftOutlined" /></button></li>
    <li v-for="(it, i) in items" :key="i">
      <button v-if="typeof it === 'number'" class="apage" :class="{ active: it === page }" :aria-current="it === page ? 'page' : null" @click="emit('go', it)">{{ nf(it) }}</button>
      <button v-else class="apage jump" :aria-label="it === 'prev' ? t('action.prev') : t('action.next')" @click="jump(it)">•••</button>
    </li>
    <li><button class="apage" :disabled="page >= totalPages" :aria-label="t('action.next')" @click="emit('go', page + 1)"><AntIcon name="RightOutlined" /></button></li>
    <!-- The label is drawn here and the select lies invisibly over it, its
         menu and keyboard still the browser's: Chromium draws a closed
         select's text right to left once it holds a Persian word, whatever
         its direction and marks say. -->
    <li v-if="sizes.length && total > 10" class="aselect apage-size"><span class="apage-size-label" dir="ltr" aria-hidden="true">{{ sizeLabel(pageSize) }}</span><select :value="pageSize" :aria-label="t('client.pageSize')" @change="emit('size', Number($event.target.value))">
      <option v-for="n in sizes" :key="n" :value="n">{{ sizeLabel(n) }}</option>
    </select></li>
  </ul>
</template>
