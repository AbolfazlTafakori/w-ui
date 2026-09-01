<script setup>
import { nextTick, ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { store, t, signIn, loadMessages } from '../lib/store.js'
import Icon from '../components/Icon.vue'

const router = useRouter()
const route = useRoute()

const username = ref('admin')
const password = ref('')
const error = ref('')
const code = ref('')
const needCode = ref(false)
const busy = ref(false)
const langOpen = ref(false)

// The headline cycles a word at a time, the way 3x-ui's does. It is the one
// piece of motion on an otherwise still screen, so it carries the page.
const words = computed(() => t('login.headlineWords').split('|'))
const wordIndex = ref(0)
const word = computed(() => words.value[wordIndex.value % words.value.length])
let cycle = null

onMounted(() => {
  const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  if (!reduced) cycle = setInterval(() => wordIndex.value++, 2600)
})
onUnmounted(() => clearInterval(cycle))

async function submit() {
  error.value = ''
  busy.value = true
  try {
    const res = await signIn(username.value, password.value, code.value)
    if (res && res.needCode) {
      needCode.value = true
      // Focus lands on the code box so the operator can type straight away
      // rather than hunting for a field that has just appeared. nextTick
      // rather than an animation frame: a frame never arrives in a tab the
      // browser has backgrounded, and the field would stay unfocused.
      await nextTick()
      document.getElementById('login-code')?.focus()
      return
    }
    router.replace(route.query.next || '/')
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function pickLocale(l) {
  langOpen.value = false
  await loadMessages(l)
}

const locales = computed(() => store.meta?.locales || ['en'])
const localeName = (l) => (l === 'fa' ? 'فارسی' : 'English')
</script>

<template>
  <div class="login-app">
    <!-- Five blurred blobs drifting behind the glass, the depth 3x-ui builds
         its sign-in screen out of. -->
    <div class="blobs" aria-hidden="true">
      <span class="blob a"></span>
      <span class="blob b"></span>
      <span class="blob c"></span>
      <span class="blob d"></span>
      <span class="blob e"></span>
    </div>

    <div class="toolbar">
      <div class="lang">
        <button
          class="round"
          :aria-label="t('settings.language')"
          :aria-expanded="langOpen"
          @click="langOpen = !langOpen"
        >
          <Icon name="translate" :size="17" />
        </button>
        <div v-if="langOpen" class="menu" role="menu">
          <button
            v-for="l in locales"
            :key="l"
            class="menu-item"
            :class="{ on: l === store.locale }"
            role="menuitem"
            @click="pickLocale(l)"
          >
            {{ localeName(l) }}
          </button>
        </div>
      </div>
    </div>

    <div class="login-wrapper">
      <div class="login-card">
        <div class="brand">
          <div class="brand-name">W<span>-UI</span></div>
          <div class="brand-accent"></div>
        </div>

        <h1 class="welcome">
          {{ t('login.welcome') }}
          <b :key="word">{{ word }}</b>
        </h1>

        <form @submit.prevent="submit">
          <label class="field">
            <span class="label">{{ t('auth.username') }}</span>
            <span class="control">
              <Icon name="user" :size="16" />
              <input v-model="username" autocomplete="username" required />
            </span>
          </label>

          <label class="field">
            <span class="label">{{ t('auth.password') }}</span>
            <span class="control">
              <Icon name="lock" :size="16" />
              <input
                v-model="password"
                type="password"
                autocomplete="current-password"
                required
              />
            </span>
          </label>

          <!-- Only after the password was accepted, so this reveals nothing
               to someone guessing at the username. -->
          <label v-if="needCode" class="field">
            <span class="label">{{ t('auth.code') }}</span>
            <span class="control">
              <Icon name="shield" :size="16" />
              <input
                id="login-code"
                v-model="code"
                inputmode="numeric"
                autocomplete="one-time-code"
                maxlength="6"
                placeholder="000000"
                class="ltr code-input"
                required
              />
            </span>
            <span class="hint">{{ t('auth.codeHint') }}</span>
          </label>

          <p v-if="error" class="error" role="alert">{{ error }}</p>

          <button class="submit" type="submit" :disabled="busy">
            <span v-if="busy" class="spin"></span>
            <template v-else>{{ t('auth.signIn') }}</template>
          </button>
        </form>

        <p class="hint">{{ t('auth.firstRunHint') }}</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-app {
  position: fixed;
  inset: 0;
  overflow: hidden;
  /* A dark ground with a faint warm centre, so the blobs have something to
     sit on rather than floating in flat black. */
  background:
    radial-gradient(1200px 700px at 50% -10%, #1a0d11 0%, transparent 60%),
    var(--ground);
}

/* ---------- drifting blobs ---------- */
.blobs {
  position: absolute;
  inset: -10%;
  filter: blur(100px);
  pointer-events: none;
}
.blob {
  position: absolute;
  display: block;
  border-radius: 50%;
}
.blob.a {
  width: 46vw;
  height: 46vw;
  top: -6%;
  left: -8%;
  background: radial-gradient(circle, rgba(224, 46, 61, 0.5), transparent 68%);
  animation: drift-a 24s ease-in-out infinite alternate;
}
.blob.b {
  width: 40vw;
  height: 40vw;
  bottom: -12%;
  right: -6%;
  background: radial-gradient(circle, rgba(160, 26, 42, 0.45), transparent 68%);
  animation: drift-b 30s ease-in-out infinite alternate;
}
.blob.c {
  width: 34vw;
  height: 34vw;
  top: 28%;
  left: 38%;
  background: radial-gradient(circle, rgba(242, 64, 79, 0.28), transparent 70%);
  animation: drift-c 36s ease-in-out infinite alternate;
}
.blob.d {
  width: 26vw;
  height: 26vw;
  top: 6%;
  right: 18%;
  background: radial-gradient(circle, rgba(120, 18, 30, 0.5), transparent 70%);
  animation: drift-d 28s ease-in-out infinite alternate;
}
.blob.e {
  width: 30vw;
  height: 30vw;
  bottom: 4%;
  left: 14%;
  background: radial-gradient(circle, rgba(224, 46, 61, 0.22), transparent 72%);
  animation: drift-e 32s ease-in-out infinite alternate;
}

@keyframes drift-a {
  to {
    transform: translateX(34vw) translateY(6vh);
  }
}
@keyframes drift-b {
  to {
    transform: translateX(-30vw) translateY(-8vh);
  }
}
@keyframes drift-c {
  to {
    transform: translate(-22%, 18%);
  }
}
@keyframes drift-d {
  to {
    transform: translateY(14vh) scale(1.1);
  }
}
@keyframes drift-e {
  to {
    transform: translate(18vw, -10vh) scale(0.9);
  }
}

/* ---------- grid overlay ---------- */
.login-wrapper {
  position: relative;
  height: 100%;
  display: grid;
  place-items: center;
  padding: 24px;
}
.login-wrapper::after {
  content: '';
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(to right, rgba(255, 255, 255, 0.045) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(255, 255, 255, 0.045) 1px, transparent 1px);
  background-size: 48px 48px;
  -webkit-mask-image: radial-gradient(ellipse 70% 60% at 50% 45%, #000 10%, transparent 75%);
  mask-image: radial-gradient(ellipse 70% 60% at 50% 45%, #000 10%, transparent 75%);
  pointer-events: none;
}

/* ---------- card ---------- */
.login-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 400px;
  padding: 40px 32px 28px;
  border-radius: 20px;
  background: rgba(20, 18, 21, 0.72);
  backdrop-filter: blur(24px);
  -webkit-backdrop-filter: blur(24px);
  box-shadow:
    0 2px 8px rgba(0, 0, 0, 0.4),
    0 24px 50px -12px rgba(0, 0, 0, 0.75);
}
/* A one-pixel gradient border, drawn as a masked ring so the card keeps its
   translucency instead of sitting on an opaque frame. */
.login-card::before {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  padding: 1px;
  background: linear-gradient(
    145deg,
    rgba(242, 64, 79, 0.55),
    rgba(255, 255, 255, 0.08) 40%,
    rgba(255, 255, 255, 0.03) 70%,
    rgba(242, 64, 79, 0.3)
  );
  -webkit-mask:
    linear-gradient(#000 0 0) content-box,
    linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
  mask-composite: exclude;
  pointer-events: none;
}

.brand-name {
  font-size: 28px;
  font-weight: 700;
  line-height: 1.1;
  letter-spacing: -0.01em;
  background: linear-gradient(135deg, #ffffff 0%, #f2404f 55%, #a01a2a 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  width: max-content;
}
.brand-accent {
  width: 40px;
  height: 3px;
  border-radius: 2px;
  margin-top: 10px;
  background: linear-gradient(90deg, var(--accent), rgba(242, 64, 79, 0.15));
}

.welcome {
  margin: 22px 0 26px;
  font-size: 15px;
  font-weight: 500;
  color: var(--muted);
  line-height: 1.5;
}
.welcome b {
  display: inline-block;
  color: var(--ink);
  font-weight: 700;
  animation: headline-in 280ms ease both;
}
@keyframes headline-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
}

form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.field .label {
  font-size: var(--t-xs);
  font-weight: 600;
  color: var(--ink-2);
}
.control {
  position: relative;
  display: flex;
  align-items: center;
}
.control svg {
  position: absolute;
  inset-inline-start: 13px;
  color: var(--faint);
  pointer-events: none;
  transition: color 0.15s;
}
.control input {
  width: 100%;
  padding-inline-start: 40px;
  min-height: 44px;
  background: rgba(10, 9, 10, 0.6);
  border-color: rgba(255, 255, 255, 0.09);
}
.control:focus-within svg {
  color: var(--accent);
}

/* Chrome paints its own autofill background; this keeps the field on-theme.
   The absurd delay is the standard trick to stop the flash before it repaints. */
.control input:-webkit-autofill,
.control input:-webkit-autofill:hover,
.control input:-webkit-autofill:focus {
  -webkit-box-shadow: 0 0 0 1000px #161417 inset;
  -webkit-text-fill-color: var(--ink);
  transition: background-color 9999s ease-in-out 0s;
}

.submit {
  margin-top: 4px;
  min-height: 44px;
  width: 100%;
  border: none;
  border-radius: var(--radius-sm);
  background: linear-gradient(135deg, var(--accent), #b8202e);
  color: #fff;
  font: inherit;
  font-size: var(--t-base);
  font-weight: 600;
  cursor: pointer;
  display: grid;
  place-items: center;
  transition: filter 0.15s, transform 0.08s;
  box-shadow: 0 6px 20px -8px rgba(224, 46, 61, 0.8);
}
.submit:hover:not(:disabled) {
  filter: brightness(1.1);
}
.submit:active:not(:disabled) {
  transform: translateY(1px);
}
.submit:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.error {
  margin: 0;
  padding: 9px 12px;
  border-radius: var(--radius-sm);
  font-size: var(--t-xs);
  color: var(--bad);
  background: rgba(244, 101, 95, 0.1);
  border: 1px solid rgba(244, 101, 95, 0.3);
}

.hint {
  margin: 22px 0 0;
  padding-top: 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.07);
  font-size: var(--t-xs);
  color: var(--faint);
  text-align: center;
  line-height: 1.55;
}

/* ---------- toolbar ---------- */
.toolbar {
  position: absolute;
  top: 20px;
  inset-inline-end: 20px;
  z-index: 2;
}
.lang {
  position: relative;
}
.round {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: rgba(20, 18, 21, 0.7);
  backdrop-filter: blur(12px);
  color: var(--ink-2);
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}
.round:hover {
  background: rgba(40, 34, 42, 0.8);
  color: var(--ink);
}
.menu {
  position: absolute;
  top: 46px;
  inset-inline-end: 0;
  min-width: 140px;
  padding: 5px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: rgba(20, 18, 21, 0.94);
  backdrop-filter: blur(16px);
  box-shadow: var(--shadow);
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.menu-item {
  padding: 8px 12px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--ink-2);
  font: inherit;
  font-size: var(--t-sm);
  text-align: start;
  cursor: pointer;
}
.menu-item:hover {
  background: var(--surface-3);
  color: var(--ink);
}
.menu-item.on {
  background: var(--accent-soft);
  color: var(--accent-hover);
  font-weight: 600;
}

@media (max-width: 480px) {
  .login-card {
    padding: 32px 20px 24px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .blob {
    animation: none;
  }
  .welcome b {
    animation: none;
  }
}
</style>
