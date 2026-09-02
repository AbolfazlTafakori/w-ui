<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { t } from '../lib/store.js'
import Icon from './Icon.vue'

// Asking before something cannot be undone.
//
// The browser's own confirm() cannot say how many customers a click is about to
// delete, cannot be read right-to-left, and on some browsers can be switched off
// entirely — at which point the panel destroys things without asking. It also
// gave both "delete exhausted" and "delete expired" the same sentence, so an
// operator could not tell from the dialog which one they had chosen.
//
// This says what will happen, to how many, and for the worst of them asks the
// operator to type the name. That last part is not friction for its own sake:
// it is the difference between losing a group and losing every customer in it.
const props = defineProps({
  open: { type: Boolean, default: false },
  title: { type: String, required: true },
  // What will happen, in a sentence. Shown as the body.
  body: { type: String, default: '' },
  // The concrete thing at stake — a name, or "38 customers". Shown as a chip so
  // it cannot be skimmed past.
  subject: { type: String, default: '' },
  // A list of consequences, when there is more than one worth naming.
  consequences: { type: Array, default: () => [] },
  confirmLabel: { type: String, default: '' },
  danger: { type: Boolean, default: true },
  // When set, the operator must type this exactly. Reserved for actions that
  // destroy customer records rather than configuration.
  requireText: { type: String, default: '' },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['confirm', 'cancel'])

const typed = ref('')
const input = ref(null)

const ready = computed(() => !props.requireText || typed.value.trim() === props.requireText)

watch(
  () => props.open,
  async (open) => {
    if (!open) return
    typed.value = ''
    await nextTick()
    // Focus the field when there is one to type in, and otherwise leave focus
    // on the dialog rather than on the destructive button: a stray Enter should
    // not be what confirms this.
    input.value?.focus()
  },
)

function onKey(e) {
  if (e.key === 'Escape') emit('cancel')
}
</script>

<template>
  <div v-if="open" class="modal-backdrop" @click.self="emit('cancel')" @keydown="onKey">
    <div class="modal narrow confirm" role="alertdialog" aria-modal="true" aria-labelledby="cd-title">
      <div class="card-body confirm-body">
        <span class="confirm-mark" :class="{ danger }">
          <Icon :name="danger ? 'alert' : 'info'" :size="20" />
        </span>

        <h2 id="cd-title">{{ title }}</h2>
        <p v-if="body" class="confirm-text">{{ body }}</p>

        <p v-if="subject" class="confirm-subject">
          <span class="tag" :class="danger ? 'red' : 'geekblue'">{{ subject }}</span>
        </p>

        <!-- Spelled out when more than one thing follows from this. An operator
             who reads only the title should still not be surprised. -->
        <ul v-if="consequences.length" class="confirm-list">
          <li v-for="(c, i) in consequences" :key="i">{{ c }}</li>
        </ul>

        <div v-if="requireText" class="field confirm-type">
          <label for="cd-type">
            {{ t('confirm.typeToConfirm').replace('{text}', requireText) }}
          </label>
          <input
            id="cd-type"
            ref="input"
            v-model="typed"
            autocomplete="off"
            spellcheck="false"
            :placeholder="requireText"
          />
        </div>
      </div>

      <div class="modal-foot">
        <button type="button" class="btn ghost" @click="emit('cancel')">
          {{ t('action.cancel') }}
        </button>
        <button
          type="button"
          class="btn"
          :class="danger ? 'danger' : 'primary'"
          :disabled="!ready || busy"
          @click="emit('confirm')"
        >
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ confirmLabel || t('action.confirm') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>
