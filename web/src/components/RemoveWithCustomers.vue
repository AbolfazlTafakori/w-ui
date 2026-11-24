<script setup>
// Removing a tunnel, or a whole server, with the customers on it -- one
// step instead of taking everyone off first by hand.
//
// It reads first what the removal would do (a dry run) and says it plainly:
// who keeps another server and only loses this one, and who is on it and
// nothing else. Those last are the only decision: moved to a tunnel the
// operator picks, or removed. Removing customers deletes records, so that
// choice asks for the name to be typed, as the panel's other such actions do.
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, tn, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  // 'tunnel' or 'node'.
  kind: { type: String, required: true },
  // The tunnel or the node: { id, name }.
  target: { type: Object, required: true },
})
const emit = defineEmits(['close', 'done'])

const isNode = computed(() => props.kind === 'node')
const base = computed(() => (isNode.value ? `/api/nodes/${props.target.id}/remove` : `/api/interfaces/${props.target.id}/remove`))

const plan = ref(null) // what the dry run said
const loadError = ref('')
const tunnels = ref([]) // where the customers on it alone can go
const choice = ref('move')
const moveTo = ref(0)
const typed = ref('')
const busy = ref(false)
const runError = ref('')

onMounted(async () => {
  try {
    const [res, list] = await Promise.all([
      api.post(base.value, { dryRun: true }),
      api.interfaces({ background: true }).catch(() => []),
    ])
    plan.value = isNode.value ? res.result : res
    // Somewhere real to go: not this tunnel, not this server, not another
    // panel's inbound.
    tunnels.value = (list || []).filter((i) =>
      i.protocol !== 'xray' && (isNode.value ? i.nodeId !== props.target.id : i.id !== props.target.id))
    moveTo.value = tunnels.value[0]?.id || 0
    if (!tunnels.value.length) choice.value = 'delete'
  } catch (e) {
    loadError.value = e.message
  }
})

const alone = computed(() => plan.value?.alone || 0)
const names = computed(() => {
  const shown = plan.value?.aloneNames || []
  const more = alone.value - shown.length
  return more > 0 ? `${shown.join(', ')} … ${tn('remove.andMore', more)}` : shown.join(', ')
})
const deleting = computed(() => alone.value > 0 && choice.value === 'delete')
const ready = computed(() => {
  if (!plan.value || busy.value) return false
  if (alone.value > 0 && choice.value === 'move' && !moveTo.value) return false
  if (deleting.value && typed.value.trim() !== props.target.name) return false
  return true
})
watch(choice, () => { typed.value = ''; runError.value = '' })

async function run() {
  if (!ready.value) return
  busy.value = true
  runError.value = ''
  try {
    const body = alone.value > 0 ? { alone: choice.value, moveTo: Number(moveTo.value) || 0 } : {}
    const res = await api.post(base.value, body)
    const done = isNode.value ? res.result : res
    notify(t(isNode.value ? 'remove.nodeDone' : 'remove.tunnelDone').replace('{name}', props.target.name) +
      (done.moved ? ` — ${tn('remove.moved', done.moved)}` : '') +
      (done.deleted ? ` — ${tn('remove.deleted', done.deleted)}` : ''), 'success')
    if (res.warning) notify(res.warning, 'warning')
    emit('done')
    emit('close')
  } catch (e) {
    runError.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w560" role="dialog" aria-modal="true" aria-labelledby="rm-title">
      <div class="amodal-head">
        <h2 id="rm-title" class="amodal-title">{{ t(isNode ? 'remove.nodeTitle' : 'remove.tunnelTitle') }} — {{ target.name }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body">
        <div v-if="loadError" class="aalert error with-desc" role="alert">
          <AntIcon name="CloseCircleFilled" /><div class="aalert-body"><span class="aalert-desc">{{ loadError }}</span></div>
        </div>
        <div v-else-if="!plan" class="rm-wait"><span class="spin"></span></div>
        <template v-else>
          <p v-if="isNode && plan.tunnels.length" class="rm-line">
            <AntIcon name="ClusterOutlined" />{{ tn('remove.nodeTunnels', plan.tunnels.length) }}: <b class="ltr">{{ plan.tunnels.join(', ') }}</b>
          </p>
          <p v-if="!plan.customers && !(isNode && !plan.tunnels.length)" class="rm-line"><AntIcon name="CheckCircleOutlined" />{{ t('remove.nobody') }}</p>
          <template v-else-if="plan.customers">
            <p class="rm-line"><AntIcon name="TeamOutlined" />{{ tn('remove.customers', plan.customers) }}</p>
            <p v-if="plan.others" class="rm-line ok"><AntIcon name="SwapOutlined" />{{ tn(isNode ? 'remove.othersNode' : 'remove.others', plan.others) }}</p>
          </template>

          <!-- The one decision: those on it and nothing else. -->
          <div v-if="alone" class="rm-alone">
            <p class="rm-line warn"><AntIcon name="ExclamationCircleOutlined" />{{ tn(isNode ? 'remove.aloneNode' : 'remove.alone', alone) }}</p>
            <p class="rm-names ltr">{{ names }}</p>
            <label class="rm-choice" :class="{ on: choice === 'move', off: !tunnels.length }">
              <input v-model="choice" type="radio" value="move" :disabled="!tunnels.length" />
              <span class="rm-choice-text">{{ t('remove.moveThem') }}</span>
              <div class="aselect rm-select"><select v-model="moveTo" :disabled="choice !== 'move' || !tunnels.length" :aria-label="t('remove.moveThem')">
                <option v-for="i in tunnels" :key="i.id" :value="i.id">{{ i.name }} · {{ t(`protocol.${i.protocol}`) }}</option>
              </select></div>
            </label>
            <p v-if="!tunnels.length" class="rm-hint">{{ t('remove.noOther') }}</p>
            <label class="rm-choice" :class="{ on: choice === 'delete' }">
              <input v-model="choice" type="radio" value="delete" />
              <span class="rm-choice-text">{{ tn('remove.deleteThem', alone) }}</span>
            </label>
            <div v-if="deleting" class="aform-item rm-type">
              <label class="aform-label" for="rm-typed">{{ t('remove.typeName').replace('{name}', target.name) }}</label>
              <label class="ainput block"><input id="rm-typed" v-model="typed" class="ltr" autocomplete="off" spellcheck="false" /></label>
            </div>
          </div>

          <p v-if="isNode && !plan.tunnels.length" class="rm-line"><AntIcon name="InfoCircleOutlined" />{{ t('remove.nodeEmpty') }}</p>
          <ul v-else class="rm-after">
            <li>{{ t(isNode ? 'remove.nodeKeys' : 'remove.tunnelKeys') }}</li>
            <li v-if="isNode">{{ t('remove.nodeTold') }}</li>
          </ul>
          <div v-if="runError" class="aalert error with-desc" role="alert">
            <AntIcon name="CloseCircleFilled" /><div class="aalert-body"><span class="aalert-desc">{{ runError }}</span></div>
          </div>
        </template>
      </div>

      <div class="amodal-foot">
        <button class="abtn" type="button" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn danger-primary" type="button" :disabled="!ready" @click="run">
          <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
          <span>{{ t(isNode ? 'remove.nodeConfirm' : 'remove.tunnelConfirm') }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w560 { width: min(560px, calc(100vw - 32px)); }
.rm-wait { display: flex; justify-content: center; padding: 24px; }
.rm-line { display: flex; align-items: center; gap: 8px; margin: 0 0 8px; font-size: 14px; color: var(--ink); }
.rm-line .anticon { color: var(--muted); flex: none; }
.rm-line.ok .anticon { color: var(--ok); }
.rm-line.warn .anticon { color: var(--warn); }
.rm-alone { margin: 12px 0; padding: 12px 14px; border: 1px solid var(--line-soft); border-radius: 8px; background: var(--surface-2, transparent); }
.rm-names { margin: 0 0 10px; font-size: 12px; color: var(--muted); line-height: 1.6; word-break: break-word; }
.rm-choice { display: flex; align-items: center; gap: 10px; padding: 8px 10px; border: 1px solid var(--line-soft); border-radius: 8px; margin-top: 8px; cursor: pointer; flex-wrap: wrap; }
.rm-choice.on { border-color: color-mix(in srgb, var(--accent) 55%, var(--line-soft)); }
.rm-choice.off { opacity: 0.6; cursor: default; }
.rm-choice-text { font-size: 14px; color: var(--ink); }
.rm-select { flex: 1 1 200px; min-width: 0; }
.rm-hint { margin: 6px 0 0; font-size: 12px; color: var(--faint); }
.rm-type { margin-top: 12px; margin-bottom: 0; }
.rm-after { margin: 12px 0 0; padding-inline-start: 18px; font-size: 12px; color: var(--muted); line-height: 1.7; }
.anticon.spin { animation: aspin 1s linear infinite; }
@keyframes aspin { to { transform: rotate(360deg); } }
</style>
