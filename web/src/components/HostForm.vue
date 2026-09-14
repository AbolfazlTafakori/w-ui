<script setup>
// the classic panel's HostFormModal: a 760px modal, a horizontal form (labels 8/24,
// controls 14/24), tabs for the basics and the advanced page. Their
// Security and Clash tabs are Xray's TLS and Mihomo settings and have no
// meaning for a WireGuard or OpenVPN endpoint, so they are not here.
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import Toggle from './Toggle.vue'
import MultiSelect from './MultiSelect.vue'
import TagInput from './TagInput.vue'

const props = defineProps({
  group: { type: Object, default: null },
  interfaces: { type: Array, default: () => [] },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.group)
const tab = ref('basic')
const busy = ref(false)
const fieldError = ref({})
const formError = ref('')
const knownTags = ref([])

const form = ref({
  remark: '', description: '', interfaceIds: [], hosts: [], port: 0, tags: [],
  enabled: true, excludeFormats: [], shuffle: false,
})
const FORMATS = ['conf', 'base64', 'zip', 'page']

onMounted(async () => {
  if (props.group) {
    const g = props.group
    form.value = {
      remark: g.remark, description: g.description || '', interfaceIds: [...(g.interfaceIds || [])],
      hosts: [...(g.hosts || [])], port: g.port || 0, tags: [...(g.tags || [])], enabled: !!g.enabled,
      excludeFormats: [...(g.excludeFormats || [])], shuffle: !!g.shuffle,
    }
  }
  try {
    knownTags.value = await api.get('/api/hosts/tags', { background: true })
  } catch {
    knownTags.value = []
  }
})
watch(form, () => { fieldError.value = {}; formError.value = '' }, { deep: true })

const inboundOptions = computed(() =>
  props.interfaces.map((i) => ({
    value: i.id,
    label: `${i.name} (${i.protocol}@${i.listenPort})`,
    tags: [{ text: i.protocol, kind: i.protocol === 'openvpn' ? 'orange' : 'green' }],
  })),
)

async function submit() {
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  try {
    const body = { ...form.value, port: Number(form.value.port) || 0 }
    const saved = editing.value
      ? await api.put(`/api/hosts/groups/${encodeURIComponent(props.group.groupId)}`, body)
      : await api.post('/api/hosts/groups', body)
    notify(editing.value ? t('hosts.updated') : t('hosts.created'), 'success')
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
  <div class="amodal-backdrop" @click.self="emit('cancel')">
    <div class="amodal w760" role="dialog" aria-modal="true" aria-labelledby="hf-title">
      <div class="amodal-head">
        <h2 id="hf-title" class="amodal-title">{{ editing ? t('hosts.editHost') : t('hosts.addHost') }}</h2>
        <button class="amodal-close" :aria-label="t('common.close')" @click="emit('cancel')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body hf-body">
        <div class="atabs-nav"><div class="atabs-list">
          <button class="atab" :class="{ active: tab === 'basic' }" @click="tab = 'basic'"><AntIcon name="ProfileOutlined" /><span>{{ t('hosts.sections.basic') }}</span></button>
          <button class="atab" :class="{ active: tab === 'advanced' }" @click="tab = 'advanced'"><AntIcon name="SettingOutlined" /><span>{{ t('hosts.sections.advanced') }}</span></button>
        </div></div>

        <form class="hform" @submit.prevent="submit">
          <template v-if="tab === 'basic'">
            <div class="hrow">
              <label class="req" for="hf-remark" :title="t('hosts.hints.remark')">{{ t('hosts.fields.remark') }}</label>
              <div class="hctl"><label class="ainput block"><input id="hf-remark" v-model="form.remark" maxlength="256" required /></label><p v-if="fieldError.remark" class="field-error">{{ fieldError.remark }}</p></div>
            </div>
            <div class="hrow">
              <label for="hf-desc" :title="t('hosts.hints.serverDescription')">{{ t('hosts.fields.serverDescription') }}</label>
              <div class="hctl"><label class="ainput block"><input id="hf-desc" v-model="form.description" maxlength="64" /></label></div>
            </div>
            <div class="hrow">
              <label class="req">{{ t('hosts.fields.inbound') }}</label>
              <div class="hctl"><MultiSelect v-model="form.interfaceIds" :options="inboundOptions" :placeholder="t('hosts.selectInbound')" /><p v-if="fieldError.interfaceIds" class="field-error">{{ fieldError.interfaceIds }}</p></div>
            </div>
            <div class="hrow">
              <label :title="t('hosts.hints.address')">{{ t('hosts.fields.address') }}</label>
              <div class="hctl"><TagInput v-model="form.hosts" placeholder="cdn.example.com, cdn2.example.com:443" /><p class="hint">{{ t('hosts.hints.address') }}</p><p v-if="fieldError.hosts" class="field-error">{{ fieldError.hosts }}</p></div>
            </div>
            <div class="hrow">
              <label for="hf-port" :title="t('hosts.hints.port')">{{ t('hosts.fields.port') }}</label>
              <div class="hctl"><label class="ainput number" style="width: 120px"><input id="hf-port" v-model.number="form.port" type="number" min="0" max="65535" class="ltr" /></label><p class="hint">{{ t('hosts.hints.port') }}</p></div>
            </div>
            <div class="hrow">
              <label :title="t('hosts.hints.tags')">{{ t('hosts.fields.tags') }}</label>
              <div class="hctl"><TagInput v-model="form.tags" :suggestions="knownTags" /><p class="hint">{{ t('hosts.hints.tags') }}</p><p v-if="fieldError.tags" class="field-error">{{ fieldError.tags }}</p></div>
            </div>
            <div class="hrow">
              <label>{{ t('hosts.fields.enable') }}</label>
              <div class="hctl"><Toggle v-model="form.enabled" :label="t('hosts.fields.enable')" /></div>
            </div>
          </template>

          <template v-else>
            <div class="hrow">
              <label>{{ t('hosts.fields.excludeFromSubTypes') }}</label>
              <div class="hctl">
                <div class="acheck-group">
                  <label v-for="f in FORMATS" :key="f" class="acheckbox"><input v-model="form.excludeFormats" type="checkbox" :value="f" /><span>{{ t(`hosts.formats.${f}`) }}</span></label>
                </div>
                <p class="hint">{{ t('hosts.hints.excludeFormats') }}</p>
              </div>
            </div>
            <div class="hrow">
              <label>{{ t('hosts.fields.shuffleHost') }}</label>
              <div class="hctl"><Toggle v-model="form.shuffle" :label="t('hosts.fields.shuffleHost')" /><p class="hint">{{ t('hosts.hints.shuffle') }}</p></div>
            </div>
          </template>
          <p v-if="formError" class="field-error">{{ formError }}</p>
        </form>
      </div>
      <div class="amodal-foot">
        <button class="abtn" type="button" @click="emit('cancel')">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="button" :disabled="busy" @click="submit">{{ t('action.confirm') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w760 { width: 760px; }
.hf-body { max-height: 70vh; overflow-y: auto; overflow-x: hidden; }
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.field-error { margin: 4px 0 0; font-size: 12px; color: var(--bad); }
.acheck-group { display: flex; flex-wrap: wrap; gap: 8px 16px; padding-top: 5px; }
</style>
