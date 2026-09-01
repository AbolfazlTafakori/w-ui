<script setup>
import { computed } from 'vue'
import { useRouter, RouterLink, RouterView } from 'vue-router'
import { store, t, signOut, loadMessages } from './lib/store.js'
import Icon from './components/Icon.vue'

const router = useRouter()

const signedIn = computed(() => !!store.admin)

const nav = [
  { to: '/', key: 'nav.overview', icon: 'dashboard', exact: true },
  { to: '/clients', key: 'nav.clients', icon: 'users' },
  { to: '/groups', key: 'nav.groups', icon: 'tag' },
  { to: '/interfaces', key: 'nav.interfaces', icon: 'server' },
  { to: '/settings', key: 'nav.settings', icon: 'settings' },
]

function isActive(item) {
  const path = router.currentRoute.value.path
  return item.exact ? path === item.to : path.startsWith(item.to)
}

async function switchLocale(event) {
  await loadMessages(event.target.value)
}

function handleSignOut() {
  signOut()
  router.replace({ name: 'login' })
}
</script>

<template>
  <div v-if="!signedIn" class="auth-shell">
    <RouterView />
  </div>

  <div v-else class="shell">
    <aside class="sidebar">
      <div class="brand">
        <span class="brand-mark">W</span>
        <span>
          <span class="brand-name">{{ t('app.name') }}</span>
          <span class="brand-ver">{{ store.meta?.version || 'dev' }}</span>
        </span>
      </div>

      <RouterLink
        v-for="item in nav"
        :key="item.to"
        :to="item.to"
        class="navlink"
        :class="{ active: isActive(item) }"
        :title="t(item.key)"
      >
        <Icon :name="item.icon" :size="17" />
        <span class="label">{{ t(item.key) }}</span>
      </RouterLink>

      <div class="sidebar-foot">
        <select :value="store.locale" aria-label="Language" @change="switchLocale">
          <option v-for="l in store.meta?.locales || ['en']" :key="l" :value="l">
            {{ l === 'fa' ? 'فارسی' : 'English' }}
          </option>
        </select>
        <button class="btn sm ghost" :title="t('auth.signOut')" @click="handleSignOut">
          <Icon name="logout" :size="15" />
          <span class="label">{{ store.admin?.username }}</span>
        </button>
      </div>
    </aside>

    <main class="main">
      <div
        v-if="store.meta && !store.meta.enforcementActive"
        class="banner warn"
        role="status"
      >
        <Icon name="alert" :size="15" />
        <span>{{ t('enforcement.unavailable') }}</span>
      </div>

      <RouterView />
    </main>
  </div>

  <Transition name="fade">
    <div v-if="store.toast" class="toast" :class="store.toast.kind" role="status">
      {{ store.toast.message }}
    </div>
  </Transition>
</template>

<style scoped>
/* The sign-in screen paints its own full-viewport background, so the shell
   only has to stay out of its way. */
.auth-shell {
  min-height: 100%;
}

.brand span span {
  display: block;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
