// Keeping a floating menu on the screen, the way the classic panel's
// dropdown does: opened at its button, below it when there is room and
// above it when there is not, and never past the side of the window.
//
//   <div v-fit="anchorRect" class="rowmenu" :style="{ top, left }">…</div>
//
// The value is the button's bounding rect, for the flip; without one the
// box is only nudged back inside the viewport. The directive measures the
// box once it exists, after layout, so the decision is made on its real
// height rather than a guess at the number of items.

const margin = 8
const gap = 4

function fit(el, anchor) {
  const r = el.getBoundingClientRect()
  const vw = window.innerWidth
  const vh = window.innerHeight
  let left = r.left
  let top = r.top
  if (anchor) {
    const fitsBelow = anchor.bottom + gap + r.height <= vh - margin
    const fitsAbove = anchor.top - gap - r.height >= margin
    top = fitsBelow || !fitsAbove ? anchor.bottom + gap : anchor.top - gap - r.height
  }
  if (left + r.width > vw - margin) left = vw - margin - r.width
  if (left < margin) left = margin
  if (top + r.height > vh - margin) top = vh - margin - r.height
  if (top < margin) top = margin
  el.style.left = left + 'px'
  el.style.top = top + 'px'
}

export const vFit = {
  // Synchronously: the box is in the document by now, and reading its
  // rect forces the layout, so there is nothing to wait a frame for -- and
  // a frame may not come while the tab is hidden.
  mounted(el, binding) {
    fit(el, binding.value)
  },
  updated(el, binding) {
    fit(el, binding.value)
  },
}
