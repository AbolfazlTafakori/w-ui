import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'

import App from './App.vue'
import { store, bootstrap, signOut, notify, t } from './lib/store.js'
import { initTheme } from './lib/theme.js'

// Fonts are bundled rather than linked. The panel is often reached from
// networks where a font CDN is unreachable, and falling back to a system font
// mid-render is worse for Persian than for Latin.
import '@fontsource/vazirmatn/400.css'
import '@fontsource/vazirmatn/500.css'
import '@fontsource/vazirmatn/600.css'
import '@fontsource/vazirmatn/700.css'
import '@fontsource/vazirmatn/800.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'

import './style.css'

import LoginView from './views/LoginView.vue'
import OverviewView from './views/OverviewView.vue'
import ClientsView from './views/ClientsView.vue'
import ClientDetailView from './views/ClientDetailView.vue'
import InterfacesView from './views/InterfacesView.vue'
import GroupsView from './views/GroupsView.vue'
import SettingsView from './views/SettingsView.vue'
import SharingView from './views/SharingView.vue'
import ApiView from './views/ApiView.vue'
import NodesView from './views/NodesView.vue'
import NotFoundView from './views/NotFoundView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
    { path: '/', name: 'overview', component: OverviewView },
    { path: '/clients', name: 'clients', component: ClientsView },
    { path: '/clients/:id', name: 'client', component: ClientDetailView, props: true },
    { path: '/groups', name: 'groups', component: GroupsView },
    { path: '/interfaces', name: 'interfaces', component: InterfacesView },
    { path: '/nodes', name: 'nodes', component: NodesView },
    { path: '/sharing', name: 'sharing', component: SharingView },
    { path: '/api-docs', name: 'api', component: ApiView },
    { path: '/settings', name: 'settings', component: SettingsView },
    // Shown rather than redirected: silently swallowing a typo leaves the
    // operator unsure whether they mistyped or the page moved.
    { path: '/:pathMatch(.*)*', name: 'not-found', component: NotFoundView },
  ],
})

router.beforeEach((to) => {
  if (!store.ready) return true
  if (to.meta.public) {
    return store.admin ? { name: 'overview' } : true
  }
  return store.admin ? true : { name: 'login', query: { next: to.fullPath } }
})

// A token can expire between page loads. When any request comes back 401 the
// api layer raises this, and the app returns to the sign-in screen rather than
// leaving the operator on a page that silently stops updating.
window.addEventListener('wui:unauthorized', () => {
  signOut()
  if (router.currentRoute.value.name !== 'login') {
    // Said out loud. Being returned to a sign-in screen with no explanation
    // reads as the panel having lost the password, and the usual next move is
    // to start resetting things that were never wrong.
    notify(t('error.sessionEnded'), 'warn')
    router.replace({ name: 'login' })
  }
})

// Applied before anything renders, so the panel never shows one palette on
// its way to another.
initTheme()

bootstrap()
  .catch((err) => {
    console.error('startup failed', err)
  })
  .finally(() => {
    store.ready = true
    createApp(App).use(router).mount('#app')
  })
