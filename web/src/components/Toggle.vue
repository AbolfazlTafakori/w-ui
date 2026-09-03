<script setup>
// The switch 3x-ui puts in its Enabled column. It is a real checkbox
// underneath, so it reaches the keyboard and screen readers the way a styled
// div never would.
defineProps({
  modelValue: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  label: { type: String, default: '' },
  // While a change is in flight. The switch moves to where it was asked to go
  // and spins, rather than sitting still until the server answers — a control
  // that does not move when clicked reads as broken, and gets clicked again.
  loading: { type: Boolean, default: false },
})
defineEmits(['update:modelValue'])
</script>

<template>
  <label class="toggle" :class="{ on: modelValue, disabled: disabled || loading, busy: loading }">
    <input
      type="checkbox"
      :checked="modelValue"
      :disabled="disabled || loading"
      :aria-label="label"
      :aria-busy="loading"
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
/* Waiting is not the same as unavailable. A pending switch keeps its colour so
   the state it is moving to stays readable; it simply cannot be clicked again. */
.toggle.busy {
  cursor: progress;
  opacity: 1;
}
/* The real checkbox, kept reachable by keyboard and screen reader but out of
   the layout. Scoped to .toggle so a global rule for form inputs cannot win on
   specificity and give it a size again -- which it did, and in right-to-left
   the stray 180px box hung off the edge and scrolled every page sideways.
   Clipped rather than merely sized to zero, so a width from anywhere still
   cannot affect layout. */
.toggle > input {
  position: absolute;
  width: 1px;
  height: 1px;
  /* Stated as well as the width, because a minimum from a surrounding form
     layout would otherwise put the box back. */
  min-width: 0;
  max-width: none;
  margin: -1px;
  padding: 0;
  border: 0;
  opacity: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.track {
  width: 40px;
  height: 22px;
  border-radius: 100px;
  background: var(--surface-3);
  border: 1px solid var(--line);
  position: relative;
  /* Recessed when off, so the knob reads as sitting in a channel. */
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.3);
  transition:
    background var(--t-quick, 0.12s) linear,
    border-color var(--t-quick, 0.12s) linear,
    box-shadow var(--t-quick, 0.12s) linear;
}
.knob {
  position: absolute;
  top: 2px;
  inset-inline-start: 2px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--muted);
  /* The knob is the only part that moves, so it is the only part that carries
     a shadow -- it lifts out of the channel it slides in. */
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.4);
  /* Decelerating hard rather than easing evenly: the knob should feel like it
     arrives at the far end, not like it drifts there. */
  transition:
    transform var(--t-move, 0.18s) var(--ease, cubic-bezier(0.2, 0.8, 0.2, 1)),
    background var(--t-quick, 0.12s) linear;
}
.toggle.on .track {
  background: var(--accent);
  border-color: var(--accent);
  /* On: the channel fills and lifts, so the state reads from the form of the
     control and not from its colour alone -- which is what makes it legible to
     someone who cannot separate the red from the grey. */
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.16), 0 0 0 3px var(--accent-ring);
}
.toggle.on .knob {
  background: #fff;
  transform: translateX(18px);
}

/* The knob becomes the spinner rather than gaining one beside it: the switch
   keeps its size, so nothing in the row shifts while a change is in flight. */
.toggle.busy .knob {
  border: 2px solid transparent;
  border-top-color: currentColor;
  background: transparent;
  color: var(--muted);
  animation: toggle-spin 0.6s linear infinite;
}
.toggle.busy.on .knob {
  color: #fff;
}
@keyframes toggle-spin {
  to { transform: translateX(0) rotate(360deg); }
}
.toggle.busy.on .knob {
  animation-name: toggle-spin-on;
}
@keyframes toggle-spin-on {
  to { transform: translateX(18px) rotate(360deg); }
}
[dir='rtl'] .toggle.busy.on .knob {
  animation-name: toggle-spin-on-rtl;
}
@keyframes toggle-spin-on-rtl {
  to { transform: translateX(-18px) rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  /* Still shows it is waiting, without the rotation. */
  .toggle.busy .knob {
    animation: none;
    opacity: 0.55;
  }
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
