<script setup>
// The engine's DNS: whether it answers, what it listens on and how; the
// hosts it answers itself; and the servers it asks, with the public ones
// offered as presets.
import { ref } from 'vue'
import { t } from '../../lib/store.js'
import { PRESETS, presetServers, serverRow } from '../../lib/engine/dns.js'
import { useEngine } from './context.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import TagInput from '../TagInput.vue'

const { form, inner, dnsListening } = useEngine()

const presetsOpen = ref(false)
function installPreset(data) {
  form.value.dns.servers = [...form.value.dns.servers, ...presetServers(data)]
  presetsOpen.value = false
}
const serverModal = ref(null) // { index?, address, port, domains, skipFallback }
function openAddServer() {
  serverModal.value = { address: '', port: 0, domains: [], skipFallback: false }
}
function openEditServer(i) {
  const s = form.value.dns.servers[i]
  serverModal.value = { index: i, address: s.address, port: s.port || 0, domains: [...(s.domains || [])], skipFallback: !!s.skipFallback }
}
function submitServer() {
  const m = serverModal.value
  const row = serverRow(m)
  if (!row) return
  const list = [...form.value.dns.servers]
  if (m.index == null) list.push(row)
  else list[m.index] = row
  form.value.dns.servers = list
  serverModal.value = null
}
function removeServer(i) {
  form.value.dns.servers = form.value.dns.servers.filter((_, k) => k !== i)
}
function addHost() {
  form.value.dns.hosts = [...form.value.dns.hosts, { domain: '', values: [] }]
}
function removeHost(i) {
  form.value.dns.hosts = form.value.dns.hosts.filter((_, k) => k !== i)
}
</script>

<template>
<div class="atabs-nav"><div class="atabs-list">
  <button class="atab" :class="{ active: inner.dns === '1' }" @click="inner.dns = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('eng.generalConfigs') }}</span></button>
  <template v-if="form.dns.enabled">
    <button class="atab" :class="{ active: inner.dns === 'hosts' }" @click="inner.dns = 'hosts'"><AntIcon name="ProfileOutlined" /><span>{{ t('eng.dns.hosts') }}</span></button>
    <button class="atab" :class="{ active: inner.dns === '2' }" @click="inner.dns = '2'"><AntIcon name="DatabaseOutlined" /><span>DNS</span></button>
  </template>
</div></div>

<template v-if="inner.dns === '1' || !form.dns.enabled">
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.enable') }}</div><div class="setting-list-description">{{ t('eng.dns.enableDesc') }}</div></div></div>
    <div class="acol"><Toggle v-model="form.dns.enabled" :label="t('eng.dns.enable')" /></div>
  </div></div>
  <template v-if="form.dns.enabled">
    <div class="aalert warning" style="margin-bottom: 12px">
      <AntIcon name="ExclamationCircleFilled" />
      <div class="aalert-body"><span class="aalert-title">{{ t('eng.dns.dnsLeakWarning') }}</span></div>
    </div>
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.listening') }}</div><div class="setting-list-description">{{ t('eng.dns.listeningDesc') }}</div></div></div>
      <div class="acol">
        <span v-for="a in dnsListening" :key="a" class="atag green ltr">{{ a }}:53</span>
        <span v-if="!dnsListening.length" class="atag">{{ t('eng.dns.notListening') }}</span>
      </div>
    </div></div>
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.strategy') }}</div><div class="setting-list-description">{{ t('eng.dns.strategyDesc') }}</div></div></div>
      <div class="acol"><div class="aselect"><select v-model="form.dns.queryStrategy">
        <option value="UseIP">UseIP</option>
        <option value="UseIPv4">UseIPv4</option>
        <option value="UseIPv6">UseIPv6</option>
      </select></div></div>
    </div></div>
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.disableCache') }}</div><div class="setting-list-description">{{ t('eng.dns.disableCacheDesc') }}</div></div></div>
      <div class="acol"><Toggle v-model="form.dns.disableCache" :label="t('eng.dns.disableCache')" /></div>
    </div></div>
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.dns.serveExpiredTTL') }}</div><div class="setting-list-description">{{ t('eng.dns.serveExpiredTTLDesc') }}</div></div></div>
      <div class="acol"><label class="ainput number"><input v-model.number="form.dns.serveExpiredTTL" type="number" min="0" step="60" class="ltr" /></label></div>
    </div></div>
  </template>
</template>

<template v-else-if="inner.dns === 'hosts'">
  <div v-if="!form.dns.hosts.length" class="aempty">
    <div class="aempty-desc">{{ t('eng.dns.hostsEmpty') }}</div>
    <button class="abtn primary" style="margin-top: 16px" @click="addHost"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.hostsAdd') }}</span></button>
  </div>
  <div v-else class="aspace-v gap-16">
    <div><button class="abtn primary" @click="addHost"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.hostsAdd') }}</span></button></div>
    <div v-for="(h, i) in form.dns.hosts" :key="i" class="hosts-row">
      <label class="ainput"><input v-model="h.domain" class="ltr" :placeholder="t('eng.dns.hostsDomain')" :aria-label="t('eng.dns.hostsDomain')" /></label>
      <div class="tagbox"><TagInput v-model="h.values" :placeholder="t('eng.dns.hostsValues')" /></div>
      <button class="abtn danger" :aria-label="t('eng.delete')" @click="removeHost(i)"><AntIcon name="DeleteOutlined" /></button>
    </div>
  </div>
</template>

<template v-else>
  <div v-if="!form.dns.servers.length" class="aempty">
    <div class="aempty-desc">{{ t('eng.emptyDnsDesc') }}</div>
    <div class="aspace" style="margin-top: 16px">
      <button class="abtn primary" @click="openAddServer"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.add') }}</span></button>
      <button class="abtn" @click="presetsOpen = true"><AntIcon name="MenuOutlined" /><span>{{ t('eng.dns.usePreset') }}</span></button>
    </div>
  </div>
  <div v-else class="aspace-v gap-16">
    <div class="aspace">
      <button class="abtn primary" @click="openAddServer"><AntIcon name="PlusOutlined" /><span>{{ t('eng.dns.add') }}</span></button>
      <button class="abtn" @click="presetsOpen = true"><AntIcon name="MenuOutlined" /><span>{{ t('eng.dns.usePreset') }}</span></button>
      <button class="abtn danger" @click="form.dns.servers = []"><AntIcon name="DeleteOutlined" /><span>{{ t('eng.dns.clearAll') }}</span></button>
    </div>
    <div class="atable-wrap" style="margin-top: 0">
      <table class="atable small bordered">
        <thead>
          <tr>
            <th class="center" style="width: 60px">#</th>
            <th>{{ t('eng.address') }}</th>
            <th>{{ t('eng.dns.domains') }}</th>
            <th class="center" style="width: 120px">{{ t('eng.dns.skipFallback') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(s, i) in form.dns.servers" :key="i">
            <td class="center">
              <div class="action-cell">
                <span class="row-index">{{ i + 1 }}</span>
                <div class="action-buttons">
                  <button class="abtn circle" :aria-label="t('eng.edit')" @click="openEditServer(i)"><AntIcon name="EditOutlined" /></button>
                  <button class="abtn circle" :aria-label="t('eng.delete')" @click="removeServer(i)"><AntIcon name="DeleteOutlined" /></button>
                </div>
              </div>
            </td>
            <td class="ltr muted-break">{{ s.address }}<span v-if="s.port">:{{ s.port }}</span></td>
            <td><span v-for="d in s.domains" :key="d" class="atag ltr">{{ d }}</span><span v-if="!s.domains?.length" class="muted">{{ t('eng.dns.allDomains') }}</span></td>
            <td class="center"><span class="atag" :class="s.skipFallback ? 'green' : ''" style="margin: 0">{{ s.skipFallback ? t('eng.yes') : t('eng.no') }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<!-- ── the DNS server form ── -->
<div v-if="serverModal" class="amodal-backdrop" @click.self="serverModal = null">
  <div class="amodal" role="dialog" aria-modal="true">
    <div class="amodal-head">
      <h2 class="amodal-title">{{ serverModal.index == null ? t('eng.dns.add') : `${t('eng.edit')} #${serverModal.index + 1}` }}</h2>
      <button class="amodal-close" :aria-label="t('common.close')" @click="serverModal = null"><AntIcon name="CloseOutlined" /></button>
    </div>
    <div class="amodal-body">
      <div class="aform-item"><label class="aform-label required">{{ t('eng.address') }}</label><label class="ainput block"><input v-model="serverModal.address" class="ltr" placeholder="1.1.1.1, tcp://1.1.1.1, https://dns.google/dns-query" /></label></div>
      <div class="aform-item"><label class="aform-label">{{ t('eng.port') }}</label><label class="ainput number"><input v-model.number="serverModal.port" type="number" min="0" max="65535" class="ltr" placeholder="53" /></label></div>
      <div class="aform-item"><label class="aform-label">{{ t('eng.dns.domains') }}</label><TagInput v-model="serverModal.domains" placeholder="example.com, domain:example.com, full:x.y" /></div>
      <div class="aform-item"><label class="aform-label">{{ t('eng.dns.skipFallback') }}</label><Toggle v-model="serverModal.skipFallback" :label="t('eng.dns.skipFallback')" /></div>
    </div>
    <div class="amodal-foot">
      <button class="abtn" @click="serverModal = null">{{ t('eng.cancel') }}</button>
      <button class="abtn primary" @click="submitServer">{{ t('eng.confirm') }}</button>
    </div>
  </div>
</div>

<!-- ── the presets ── -->
<div v-if="presetsOpen" class="amodal-backdrop" @click.self="presetsOpen = false">
  <div class="amodal" role="dialog" aria-modal="true">
    <div class="amodal-head">
      <h2 class="amodal-title">{{ t('eng.dns.presetTitle') }}</h2>
      <button class="amodal-close" :aria-label="t('common.close')" @click="presetsOpen = false"><AntIcon name="CloseOutlined" /></button>
    </div>
    <div class="amodal-body">
      <div class="aalert warning preset-warning">
        <AntIcon name="ExclamationCircleFilled" />
        <div class="aalert-body"><span class="aalert-title">{{ t('eng.dns.dnsLeakWarning') }}</span></div>
      </div>
      <div class="preset-list">
        <div v-for="p in PRESETS" :key="p.name" class="preset-row">
          <div class="aspace">
            <span v-for="tag in p.tags" :key="tag" class="atag" :class="tag === 'Family' ? 'purple' : tag === 'UDP' ? 'orange' : 'green'" style="margin: 0">{{ tag === 'Family' ? t('eng.dns.presetFamily') : tag }}</span>
            <span class="preset-name">{{ p.name }}</span>
          </div>
          <button class="abtn primary small" @click="installPreset(p.data)">{{ t('eng.install') }}</button>
        </div>
      </div>
    </div>
  </div>
</div>
</template>
