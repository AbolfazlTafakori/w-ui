// Keeping a floating menu on the screen.
//
// Every row and card menu in the panel is a fixed-position box opened at
// its button. Opened near the bottom of a phone, or at the end of a wide
// table, it used to run off the edge and lose half its items. This
// directive measures the box once it exists and moves it back inside the
// viewport, with an 8px margin; on a phone it does something better than
// nudging, and turns the box into a sheet along the bottom edge, where a
// thumb reaches and every item has room.
//
//   <div v-fit class="rowmenu" :style="{ top, left }">…</div>
//
// The sheet is a class the stylesheet draws; the directive only decides.
import { MOBILE_BREAKPOINT_PX } from './mobile.js'

const margin = 8

function fit(el) {
  if (window.innerWidth <= MOBILE_BREAKPOINT_PX) {
    el.classList.add('sheet')
    return
  }
  el.classList.remove('sheet')
  const r = el.getBoundingClientRect()
  const vw = window.innerWidth
  const vh = window.innerHeight
  let left = r.left
  let top = r.top
  if (left + r.width > vw - margin) left = vw - margin - r.width
  if (left < margin) left = margin
  if (top + r.height > vh - margin) top = vh - margin - r.height
  if (top < margin) top = margin
  el.style.left = left + 'px'
  el.style.top = top + 'px'
}

export const vFit = {
  mounted(el) {
    // After layout, so the box has its size.
    requestAnimationFrame(() => fit(el))
  },
  updated(el) {
    requestAnimationFrame(() => fit(el))
  },
}
