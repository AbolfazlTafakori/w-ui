// Whether the viewport is a phone, by the classic panel's one breakpoint (768px).
//
// matchMedia's change event rather than resize: it fires only when the
// answer flips, not on every pixel of a window drag.
import { onUnmounted, ref } from 'vue'

export const MOBILE_BREAKPOINT_PX = 768

export function useIsMobile(breakpoint = MOBILE_BREAKPOINT_PX) {
  const mql = window.matchMedia(`(max-width: ${breakpoint}px)`)
  const isMobile = ref(mql.matches)
  const onChange = (e) => { isMobile.value = e.matches }
  mql.addEventListener('change', onChange)
  onUnmounted(() => mql.removeEventListener('change', onChange))
  return isMobile
}
