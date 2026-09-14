<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'
import Toggle from './Toggle.vue'
import MultiSelect from './MultiSelect.vue'

// The rule dialog, laid out the way the classic panel's is: a 780px horizontal form
// with Enabled, Comment, then the criteria in their order -- Source IPs,
// Source Port, Network, IPs, Domains, User, Port, Inbound tags -- and where
// it goes: Outbound tag or Balancer. Every criterion filled in has to
// match, which is how an Xray rule reads.
//
// Their VLESS Route, sniffed Protocol and Attributes are not here: those
// read fields inside the connection that a kernel router never sees.

const props = defineProps({
  rule: { type: Object, default: null },
  outbounds: { type: Array, default: () => [] },
  balancers: { type: Array, default: () => [] },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.rule)
const busy = ref(false)
const fieldError = ref({})
const formError = ref('')
const groups = ref([])
const clients = ref([])
const interfaces = ref([])

const form = ref({
  name: '',
  enabled: true,
  sourceIps: '',
  sourcePorts: '',
  network: '',
  destIps: '',
  domains: '',
  ports: '',
  clients: [],
  groups: [],
  interfaces: [],
  outboundTag: '',
  balancerTag: '',
  note: '',
})

const NETWORKS = ['', 'tcp', 'udp', 'icmp']

const split = (s) => (s || '').split(',').map((x) => x.trim()).filter(Boolean)

onMounted(async () => {
  if (props.rule) {
    const r = props.rule
    const isBalancer = props.balancers.some((b) => b.tag === r.outboundTag)
    Object.assign(form.value, {
      name: r.name,
      enabled: r.enabled,
      sourceIps: r.sourceIps || '',
      sourcePorts: r.sourcePorts || '',
      network: r.network || '',
      destIps: r.destIps || '',
      domains: r.domains || '',
      ports: r.ports || '',
      clients: split(r.clients),
      groups: split(r.groups),
      interfaces: split(r.interfaces),
      outboundTag: isBalancer ? '' : r.outboundTag,
      balancerTag: isBalancer ? r.outboundTag : '',
      note: r.note || '',
    })
  } else if (props.outbounds.length) {
    form.value.outboundTag = props.outbounds[0].tag
  }
  try {
    const [g, c, i] = await Promise.all([
      api.get('/api/groups', { background: true }).catch(() => []),
      api.get('/api/clients?perPage=500', { background: true }).catch(() => ({ items: [] })),
      api.get('/api/interfaces', { background: true }).catch(() => []),
    ])
    groups.value = Array.isArray(g) ? g : g.items || []
    clients.value = Array.isArray(c) ? c : c.items || []
    interfaces.value = Array.isArray(i) ? i : i.items || []
  } catch {
    /* the lists are a convenience; typing still works */
  }
})

watch(
  () => ({ ...form.value }),
  () => {
    fieldError.value = {}
    formError.value = ''
  },
  { deep: true },
)

const clientOptions = computed(() => clients.value.map((c) => ({ value: String(c.id), label: c.name })))
const groupOptions = computed(() => groups.value.map((g) => ({ value: g.name, label: g.name })))
const interfaceOptions = computed(() =>
  interfaces.value.map((i) => ({ value: String(i.id), label: i.name, tags: [{ text: t(`protocol.${i.protocol}`), kind: 'proto' }] })),
)

// Outbound and balancer are one target: picking one clears the other, the
// way theirs treats them.
watch(() => form.value.balancerTag, (v) => { if (v) form.value.outboundTag = '' })
watch(() => form.value.outboundTag, (v) => { if (v) form.value.balancerTag = '' })

async function submit() {
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  const f = form.value
  const body = {
    name: f.name,
    enabled: f.enabled,
    sourceIps: f.sourceIps,
    sourcePorts: f.sourcePorts,
    network: f.network,
    destIps: f.destIps,
    domains: f.domains,
    ports: f.ports,
    clients: f.clients.join(', '),
    groups: f.groups.join(', '),
    interfaces: f.interfaces.join(', '),
    outboundTag: f.balancerTag || f.outboundTag,
    note: f.note,
  }
  try {
    const saved = props.rule
      ? await api.patch(`/api/routing/rules/${props.rule.id}`, body)
      : await api.post('/api/routing/rules', body)
    notify(editing.value ? t('routing.ruleUpdated') : t('routing.ruleCreated'), 'success')
    emit('saved', saved)
  } catch (err) {
    if (err.field) fieldError.value = { [err.field]: err.message }
    else formError.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal rr-modal" role="dialog" aria-modal="true" aria-labelledby="rr-title">
      <div class="card-head">
        <h2 id="rr-title">{{ editing ? `${t('action.edit')} ${t('routing.tab.rules')}` : `+ ${t('routing.tab.rules')}` }}</h2>
        <button class="act" :title="t('common.close')" @click="emit('cancel')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="card-body">
        <p v-if="formError" class="form-error">{{ formError }}</p>

        <form class="hform" @submit.prevent="submit">
          <div class="hrow">
            <label>{{ t('table.enabled') }}</label>
            <div class="hctl"><Toggle v-model="form.enabled" :label="t('table.enabled')" /></div>
          </div>
          <div class="hrow">
            <label for="rr-name">{{ t('routing.col.comment') }}</label>
            <div class="hctl">
              <input id="rr-name" v-model="form.name" maxlength="200" autocomplete="off" :placeholder="t('routing.col.comment')" />
              <p v-if="fieldError.name" class="field-error">{{ fieldError.name }}</p>
            </div>
          </div>

          <div class="hrow">
            <label for="rr-src" :title="t('routing.rule.useComma')">{{ t('routing.rule.sourceIps') }} <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <input id="rr-src" v-model="form.sourceIps" class="ltr" placeholder="0.0.0.0/8, fc00::/7, geoip:ir" autocomplete="off" spellcheck="false" />
              <p v-if="fieldError.sourceIps" class="field-error">{{ fieldError.sourceIps }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-sport" :title="t('routing.rule.useComma')">{{ t('routing.rule.sourcePort') }} <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <input id="rr-sport" v-model="form.sourcePorts" class="ltr" placeholder="53,443,1000-2000" autocomplete="off" spellcheck="false" />
              <p v-if="fieldError.sourcePorts" class="field-error">{{ fieldError.sourcePorts }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-net">{{ t('routing.col.network') }}</label>
            <div class="hctl">
              <select id="rr-net" v-model="form.network">
                <option v-for="n in NETWORKS" :key="n" :value="n">{{ n || '(any)' }}</option>
              </select>
              <p v-if="fieldError.network" class="field-error">{{ fieldError.network }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-dest" :title="t('routing.rule.useComma')">IP <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <input id="rr-dest" v-model="form.destIps" class="ltr" placeholder="0.0.0.0/8, fc00::/7, geoip:ir" autocomplete="off" spellcheck="false" />
              <p v-if="fieldError.destIps" class="field-error">{{ fieldError.destIps }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-dom" :title="t('routing.rule.useComma')">{{ t('routing.rule.domain') }} <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <input id="rr-dom" v-model="form.domains" class="ltr" placeholder="google.com, example.org" autocomplete="off" spellcheck="false" />
              <p v-if="fieldError.domains" class="field-error">{{ fieldError.domains }}</p>
            </div>
          </div>
          <div class="hrow">
            <label :title="t('routing.rule.useComma')">{{ t('routing.rule.user') }} <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <MultiSelect v-model="form.clients" :options="clientOptions" :placeholder="t('routing.rule.userPlaceholder')" />
              <p v-if="fieldError.clients" class="field-error">{{ fieldError.clients }}</p>
            </div>
          </div>
          <div class="hrow">
            <label>{{ t('client.group') }}</label>
            <div class="hctl">
              <MultiSelect v-model="form.groups" :options="groupOptions" :placeholder="t('client.group')" />
              <p v-if="fieldError.groups" class="field-error">{{ fieldError.groups }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-port" :title="t('routing.rule.useComma')">{{ t('outbound.form.port') }} <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <input id="rr-port" v-model="form.ports" class="ltr" placeholder="53,443,1000-2000" autocomplete="off" spellcheck="false" />
              <p v-if="fieldError.ports" class="field-error">{{ fieldError.ports }}</p>
            </div>
          </div>
          <div class="hrow">
            <label>{{ t('routing.rule.inboundTags') }}</label>
            <div class="hctl">
              <MultiSelect v-model="form.interfaces" :options="interfaceOptions" :placeholder="t('routing.rule.inboundTags')" />
              <p v-if="fieldError.interfaces" class="field-error">{{ fieldError.interfaces }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-ob">{{ t('routing.rule.outboundTag') }}</label>
            <div class="hctl">
              <select id="rr-ob" v-model="form.outboundTag">
                <option value="">(none)</option>
                <option v-for="o in outbounds" :key="o.id" :value="o.tag" :disabled="!o.enabled">
                  {{ o.tag }}<template v-if="!o.enabled"> — {{ t('routing.disabled') }}</template>
                </option>
              </select>
              <p v-if="fieldError.outboundTag" class="field-error">{{ fieldError.outboundTag }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-bal" :title="t('routing.rule.balancerTagTooltip')">{{ t('routing.rule.balancer') }} <Icon name="info" :size="12" /></label>
            <div class="hctl">
              <select id="rr-bal" v-model="form.balancerTag">
                <option value="">(none)</option>
                <option v-for="b in balancers" :key="b.id" :value="b.tag" :disabled="!b.enabled">{{ b.tag }}</option>
              </select>
            </div>
          </div>
          <div class="hrow">
            <label for="rr-note">{{ t('routing.rule.note') }}</label>
            <div class="hctl"><input id="rr-note" v-model="form.note" autocomplete="off" /></div>
          </div>
        </form>
      </div>

      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('cancel')">{{ t('common.close') }}</button>
        <button type="button" class="btn primary" :disabled="busy" @click="submit">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ editing ? t('outbound.form.saveChanges') : t('outbound.form.create') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rr-modal {
  max-width: 780px;
}
.card-body {
  padding: 24px 24px 8px;
}
.hrow > label svg {
  vertical-align: -2px;
  opacity: 0.6;
}
.field-error {
  margin: 4px 0 0;
  color: var(--bad);
  font-size: var(--t-sm);
}
.form-error {
  margin: 0 0 12px;
  color: var(--bad);
  font-size: var(--t-sm);
}
</style>
