// The engine's advanced page: the whole template as JSON, or one section of
// it, edited in place and applied; and the generated programs, read only.
// The page's header Save is this page's save while it is shown, so the state
// is held here and the page wires it to both.
import { ref, watch } from 'vue'
import { api } from '../api.js'
import { t, notify } from '../store.js'

export const ADV = ['complete', 'inbounds', 'outbounds', 'routing', 'programs']

// The part of the template a section shows.
export function sliceOf(doc, section) {
  if (!doc) return null
  switch (section) {
    case 'inbounds': return { inbounds: doc.inbounds }
    case 'outbounds': return { outbounds: doc.outbounds }
    case 'routing': return { routing: doc.routing, balancers: doc.balancers }
    default: return doc
  }
}

// The generated programs, one after another under a heading each.
export function programsText(p = {}) {
  return ['# enforcement', p.enforcement || '', '', '# routing', p.routing || '', '', '# shaping', p.shaping || ''].join('\n')
}

// onApplied: what else to reload once a section has been applied.
export function useTemplateDoc({ onApplied }) {
  const adv = ref('complete')
  const doc = ref(null)
  const text = ref('')
  const error = ref('')
  const dirty = ref(false)
  const programs = ref(null)
  const busy = ref(false)

  async function load() {
    try {
      doc.value = await api.get('/api/template', { background: true })
      programs.value = await api.get('/api/configs', { background: true })
      render()
    } catch (e) {
      notify(e.message, 'error')
    }
  }

  function render() {
    error.value = ''
    dirty.value = false
    text.value = adv.value === 'programs' ? programsText(programs.value || {}) : JSON.stringify(sliceOf(doc.value, adv.value), null, 2)
  }
  watch(adv, render)

  function onInput(v) {
    text.value = v
    dirty.value = true
    try {
      JSON.parse(v)
      error.value = ''
    } catch (e) {
      error.value = e.message
    }
  }

  async function save() {
    let parsed
    try {
      parsed = JSON.parse(text.value)
    } catch (e) {
      error.value = e.message
      return
    }
    busy.value = true
    try {
      const res = await api.put(`/api/template?section=${adv.value}`, parsed)
      const msg = `${t('eng.applied')} — ${res.created} ${t('eng.created')}, ${res.updated} ${t('eng.updated')}`
      if (res.skipped?.length) notify(`${msg}\n${res.skipped.join('\n')}`, 'error')
      else notify(msg, 'success')
      await load()
      await onApplied()
    } catch (e) {
      notify(e.message, 'error')
    } finally {
      busy.value = false
    }
  }

  // Nothing to apply on the programs, which are generated, nor while the
  // text is unchanged or not JSON.
  const canSave = () => dirty.value && !error.value && adv.value !== 'programs'

  return { adv, doc, text, error, busy, load, onInput, save, canSave }
}
