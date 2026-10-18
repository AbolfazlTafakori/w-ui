// The engine page's state, shared with its tabs: the settings form and what
// it was saved as, the defaults, the inner tab each page is on, and where the
// DNS resolver is listening. Provided by the page, injected by each tab.
import { inject, provide } from 'vue'

const KEY = Symbol('engine')

export function provideEngine(state) {
  provide(KEY, state)
}
export function useEngine() {
  return inject(KEY)
}
