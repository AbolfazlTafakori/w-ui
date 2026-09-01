<script setup>
import { computed } from 'vue'
import { store, t, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Icon from './Icon.vue'

const props = defineProps({
  iface: { type: Object, required: true },
})
const emit = defineEmits(['close'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const i = computed(() => props.iface)
const awg = computed(() => i.value.awg || {})

// The server-side config, rendered here rather than only inside the binary.
// Until the kernel driver lands the operator still has to place this file on
// the box by hand, and guessing at it from the fields would be worse.
const serverConfig = computed(() => {
  const f = i.value
  if (f.protocol !== 'wireguard') {
    return [
      `port ${f.listenPort}`,
      `proto ${f.openvpn?.transport || 'udp'}`,
      'dev tun',
      `server ${f.subnet.split('/')[0]} 255.255.0.0`,
      `cipher ${f.openvpn?.cipherSuite || 'AES-256-GCM'}`,
      `auth ${f.openvpn?.auth || 'SHA256'}`,
      'verify-client-cert none',
      'auth-user-pass-verify /etc/wui/verify.sh via-file',
      'script-security 2',
      'tls-crypt /etc/wui/ta.key',
      f.openvpn?.duplicateCN ? 'duplicate-cn' : '# duplicate-cn off: one session per credential',
      `push "dhcp-option DNS ${f.dns || '1.1.1.1'}"`,
      'push "redirect-gateway def1 bypass-dhcp"',
    ].join('\n')
  }

  const gw = f.subnet.replace(/\.0\/\d+$/, '.1')
  const lines = [
    '[Interface]',
    `Address = ${gw}/${f.subnet.split('/')[1]}`,
    `ListenPort = ${f.listenPort}`,
    'PrivateKey = <stored in the panel database>',
    `MTU = ${f.mtu}`,
  ]
  if (f.mode === 'amnezia') {
    const p = awg.value
    lines.push(
      '',
      '# AmneziaWG — these must match every client of this interface',
      `Jc = ${p.jc}`,
      `Jmin = ${p.jmin}`,
      `Jmax = ${p.jmax}`,
      `S1 = ${p.s1}`,
      `S2 = ${p.s2}`,
      `S3 = ${p.s3}`,
      `S4 = ${p.s4}`,
      `H1 = ${p.h1}`,
      `H2 = ${p.h2}`,
      `H3 = ${p.h3}`,
      `H4 = ${p.h4}`,
    )
  }
  lines.push(
    '',
    'PostUp = sysctl -w net.ipv4.ip_forward=1',
    `PostUp = iptables -t nat -A POSTROUTING -s ${f.subnet} -o ${f.natInterface} -j MASQUERADE`,
    `PostUp = iptables -A FORWARD -i ${f.name} -j ACCEPT`,
    `PostDown = iptables -t nat -D POSTROUTING -s ${f.subnet} -o ${f.natInterface} -j MASQUERADE`,
    `PostDown = iptables -D FORWARD -i ${f.name} -j ACCEPT`,
    '',
    '# One [Peer] block per device is written by the panel.',
  )
  return lines.join('\n')
})

async function copy(text) {
  try {
    await navigator.clipboard.writeText(text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

const poolPercent = computed(() =>
  i.value.capacity ? (i.value.allocated / i.value.capacity) * 100 : 0,
)
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal wide" role="dialog" aria-modal="true" aria-labelledby="id-title">
      <div class="card-head">
        <Icon name="server" :size="17" />
        <h2 id="id-title" class="mono">{{ i.name }}</h2>
        <span class="tag proto">{{ i.protocol }}</span>
        <span v-if="i.mode === 'amnezia'" class="tag active">
          <Icon name="shield" :size="11" />AmneziaWG
        </span>
        <span class="tag" :class="i.enabled ? 'active' : 'disabled'">
          <i v-if="i.enabled" class="dot"></i>
          {{ i.enabled ? t('status.active') : t('status.disabled') }}
        </span>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <dl class="metrics">
          <div class="metric">
            <dt>{{ t('interface.endpoint') }}</dt>
            <dd class="mono sm">{{ i.endpointHost }}:{{ i.listenPort }}</dd>
          </div>
          <div class="metric">
            <dt>{{ t('interface.subnet') }}</dt>
            <dd class="mono sm">{{ i.subnet }}</dd>
          </div>
          <div class="metric">
            <dt>{{ t('nav.clients') }}</dt>
            <dd class="ltr">{{ nf(i.clients) }} · {{ nf(i.devices) }}</dd>
          </div>
          <div class="metric">
            <dt>{{ t('client.traffic') }}</dt>
            <dd>{{ bytes(i.usedBytes, store.locale) }}</dd>
          </div>
        </dl>

        <div class="pool">
          <div class="row">
            <span class="small muted">{{ t('interface.capacity') }}</span>
            <span class="spacer num small muted ltr">
              {{ nf(i.allocated) }} / {{ nf(i.capacity) }}
            </span>
          </div>
          <div class="meter">
            <span
              :class="poolPercent > 90 ? 'bad' : poolPercent > 75 ? 'warn' : ''"
              :style="{ width: Math.max(poolPercent, 1) + '%' }"
            ></span>
          </div>
        </div>

        <div v-if="i.publicKey" class="field">
          <label>{{ t('interface.publicKey') }}</label>
          <div class="cred">
            <code>{{ i.publicKey }}</code>
            <button class="btn sm icon" :aria-label="t('action.copy')" @click="copy(i.publicKey)">
              <Icon name="copy" :size="13" />
            </button>
          </div>
        </div>

        <div v-if="i.mode === 'amnezia'" class="field">
          <label>{{ t('interface.awgParams') }}</label>
          <div class="awg">
            <span v-for="(v, k) in { Jc: awg.jc, Jmin: awg.jmin, Jmax: awg.jmax, S1: awg.s1, S2: awg.s2, S3: awg.s3, S4: awg.s4 }" :key="k" class="awg-chip">
              <b>{{ k }}</b>{{ v }}
            </span>
          </div>
          <span class="hint">{{ t('interface.awgHint') }}</span>
        </div>

        <div class="field">
          <label>{{ t('interface.serverConfig') }}</label>
          <pre class="config">{{ serverConfig }}</pre>
          <span class="hint">{{ t('interface.serverConfigHint') }}</span>
        </div>
      </div>

      <div class="modal-foot">
        <button class="btn ghost" @click="copy(serverConfig)">
          <Icon name="copy" :size="14" />{{ t('action.copy') }}
        </button>
        <button class="btn primary" @click="emit('close')">{{ t('action.cancel') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal.wide {
  max-width: 720px;
}
.card-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.card-head svg {
  color: var(--muted);
}
.metrics {
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.metric dd.sm {
  font-size: var(--t-sm);
  font-weight: 500;
}
.pool {
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.cred {
  display: flex;
  align-items: center;
  gap: 6px;
}
.cred code {
  flex: 1;
  min-width: 0;
  background: var(--surface-2);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  padding: 8px 11px;
  overflow-x: auto;
  white-space: nowrap;
}
.awg {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.awg-chip {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
  padding: 4px 10px;
  border-radius: 5px;
  background: var(--surface-2);
  border: 1px solid var(--line);
  font-family: var(--mono);
  font-size: var(--t-xs);
  direction: ltr;
}
.awg-chip b {
  color: var(--muted);
  font-weight: 500;
}
pre.config {
  max-height: 260px;
  overflow-y: auto;
}
</style>
