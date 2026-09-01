<script setup>
// The switch 3x-ui puts in its Enabled column. It is a real checkbox
// underneath, so it reaches the keyboard and screen readers the way a styled
// div never would.
defineProps({
  modelValue: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  label: { type: String, default: '' },
})
defineEmits(['update:modelValue'])
</script>

<template>
  <label class="toggle" :class="{ on: modelValue, disabled }">
    <input
      type="checkbox"
      :checked="modelValue"
      :disabled="disabled"
      :aria-label="label"
      @change="$emit('update:modelValue', $event.target.checked)"
    />
    <span class="track"><span class="knob"></span></span>
  </label>
</template>

<style scoped>
.toggle {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  flex-shrink: 0;
}
.toggle.disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
input {
  position: absolute;
  opacity: 0;
  width: 0;
  height: 0;
  margin: 0;
}
.track {
  width: 40px;
  height: 22px;
  border-radius: 100px;
  background: var(--surface-3);
  border: 1px solid var(--line);
  position: relative;
  transition: background 0.16s, border-color 0.16s;
}
.knob {
  position: absolute;
  top: 2px;
  inset-inline-start: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--muted);
  transition: transform 0.16s, background 0.16s;
}
.toggle.on .track {
  background: var(--accent);
  border-color: var(--accent);
}
.toggle.on .knob {
  background: #fff;
  transform: translateX(18px);
}
/* The knob travels the other way when the page does. */
[dir='rtl'] .toggle.on .knob {
  transform: translateX(-18px);
}
input:focus-visible + .track {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
</style>
