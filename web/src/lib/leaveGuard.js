// Leaving a page with an unsaved edit asks first, with three plain answers:
// stay, discard and leave, or save and leave. Staying is what closing the
// question does -- Escape, the backdrop, the Stay button -- because an
// edit should never be thrown away by a key pressed to dismiss a dialog.
import { ref } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'

// dirty: whether there is anything to lose. save: saves it; the route is
// only left if nothing is dirty afterwards, so a save that failed stays.
export function useLeaveGuard({ dirty, save }) {
  const router = useRouter()
  const pendingRoute = ref(null)
  let leaving = false

  onBeforeRouteLeave((to) => {
    if (!dirty.value || leaving) return true
    pendingRoute.value = to.fullPath
    return false
  })

  function stay() {
    pendingRoute.value = null
  }
  async function discard() {
    const to = pendingRoute.value
    pendingRoute.value = null
    if (!to) return
    leaving = true
    try {
      await router.push(to)
    } finally {
      leaving = false
    }
  }
  async function saveAndGo() {
    await save()
    if (!dirty.value) await discard()
    else stay()
  }

  return { pendingRoute, stay, discard, saveAndGo }
}
