<script setup>
import { computed, nextTick, ref } from 'vue'
import { t } from '../lib/store.js'
import Icon from './Icon.vue'

// A list of short values, entered one at a time.
//
// A textarea would be simpler and worse: an operator cannot tell whether a
// forty-line list contains a duplicate, cannot remove one entry without
// selecting exactly the right characters, and gets no confirmation that what
// they typed was understood as one item rather than two.
const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  // Offered under the field, for the named groups an operator would otherwise
  // have to know exist.
  suggestions: { type: Array, default: () => [] },
  placeholder: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

const draft = ref('')
const input = ref(null)

const unused = computed(() =>
  props.suggestions.filter((s) => !props.modelValue.includes(s)),
)

function add(value) {
  const v = String(value ?? draft.value).trim()
  if (!v) return
  // Pasting a comma-separated list is the common case, so it is split rather
  // than stored as one nonsensical entry.
  const parts = v
    .split(/[,\s]+/)
    .map((p) => p.trim())
    .filter(Boolean)

  const next = [...props.modelValue]
  for (const p of parts) {
    if (!next.includes(p)) next.push(p)
  }
  emit('update:modelValue', next)
  draft.value = ''
}

function remove(i) {
  const next = [...props.modelValue]
  next.splice(i, 1)
  emit('update:modelValue', next)
}

function onKeydown(e) {
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    add()
    return
  }
  // Backspace on an empty field removes the last chip, which is what every
  // other tag field does and what the hand expects.
  if (e.key === 'Backspace' && draft.value === '' && props.modelValue.length) {
    remove(props.modelValue.length - 1)
  }
}

async function focusInput() {
  await nextTick()
  input.value?.focus()
}
</script>

<template>
  <div class="taginput" @click="focusInput">
    <div class="chips">
      <span v-for="(v, i) in modelValue" :key="v" class="chip">
        <span class="ltr">{{ v }}</span>
        <button
          type="button"
          class="chip-x"
          :title="t('action.remove')"
          :aria-label="`${t('action.remove')} ${v}`"
          @click.stop="remove(i)"
        >
          <Icon name="close" :size="11" />
        </button>
      </span>

      <input
        ref="input"
        v-model="draft"
        class="chip-input ltr"
        autocomplete="off"
        spellcheck="false"
        :placeholder="modelValue.length ? '' : placeholder || t('form.addEntry')"
        @keydown="onKeydown"
        @blur="add()"
      />
    </div>

    <div v-if="unused.length" class="suggestions">
      <span class="muted small">{{ t('form.orPick') }}</span>
      <button
        v-for="s in unused"
        :key="s"
        type="button"
        class="chip ghost"
        @click.stop="add(s)"
      >
        <Icon name="plus" :size="11" /> {{ s }}
      </button>
    </div>
  </div>
</template>
