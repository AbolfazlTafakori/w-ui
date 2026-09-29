import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'

import App from './App.vue'
import { store, bootstrap, signOut, notify, t, setNavigating } from './lib/store.js'
import { initTheme } from './lib/theme.js'
import { vFit } from './lib/fitmenu.js'

// Fonts are bundled rather than linked. The panel is often reached from
// networks where a font CDN is unreachable, and falling back to a system font
// mid-render is worse for Persian than for Latin.
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
// Vazirmatn for Persian: the system face has no Persian of its own on
// most machines and falls back glyph by glyph. Bundled like the mono, for
// the same reason.
import '@fontsource/vazirmatn/400.css'
import '@fontsource/vazirmatn/500.css'
import '@fontsource/vazirmatn/700.css'

import './style.css'
import './ant.css'
import './sidebar.css'

import LoginView from './views/LoginView.vue'

// Every other page is fetched when it is first opened. As one bundle the panel
// was over 800 kB of script before the first page could show -- the routing
// editor, the engine, the API reference and the rest, for a reseller who only
// ever opens two pages. The sign-in page stays in the bundle: it is the one
// every visit may start on.
const OverviewView = () => import('./views/OverviewView.vue')
const ClientsView = () => import('./views/ClientsView.vue')
const ClientDetailView = () => import('./views/ClientDetailView.vue')
const InterfacesView = () => import('./views/InterfacesView.vue')
const GroupsView = () => import('./views/GroupsView.vue')
const SettingsView = () => import('./views/SettingsView.vue')
const AdminsView = () => import('./views/AdminsView.vue')
const SharingView = () => import('./views/SharingView.vue')
const ApiView = () => import('./views/ApiView.vue')
const NodesView = () => import('./views/NodesView.vue')
const HostsView = () => import('./views/HostsView.vue')
const OutboundsView = () => import('./views/OutboundsView.vue')
const RoutingView = () => import('./views/RoutingView.vue')
const ConfigsView = () => import('./views/ConfigsView.vue')
const EngineView = () => import('./views/EngineView.vue')
const NotFoundView = () => import('./views/NotFoundView.vue')

// The router's own base, read from the <base> tag the server writes rather
// than compiled in. The panel may be mounted under a random path prefix, and
// a router that did not know about it would push /clients over the top of
// the prefix and navigate straight out of the panel.
const routerBase = new URL(document.baseURI).pathname

const router = createRouter({
  history: createWebHistory(routerBase),
  routes: [
    { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
    // meta.sells marks a page a reseller has; meta.everyone one a panel
    // administrator has on top of those. Everything unmarked is the
    // owner's. The guard below turns that into a redirect, and every
    // endpoint behind these pages checks again for itself -- a route table
    // is a convenience, never the thing that keeps anybody out.
    { path: '/', name: 'overview', component: OverviewView, meta: { everyone: true } },
    { path: '/clients', name: 'clients', component: ClientsView, meta: { sells: true } },
    { path: '/clients/:id', name: 'client', component: ClientDetailView, props: true, meta: { sells: true } },
    { path: '/groups', name: 'groups', component: GroupsView, meta: { sells: true } },
    { path: '/interfaces', name: 'interfaces', component: InterfacesView },
    { path: '/nodes', name: 'nodes', component: NodesView },
    { path: '/hosts', name: 'hosts', component: HostsView },
    { path: '/outbounds', name: 'outbounds', component: OutboundsView },
    { path: '/routing', name: 'routing', component: RoutingView },
    { path: '/sharing', name: 'sharing', component: SharingView },
    { path: '/api-docs', name: 'api', component: ApiView },
    { path: '/settings', name: 'settings', component: SettingsView },
    // Who sells on this panel, in the menu proper rather than under settings.
    { path: '/admins', name: 'admins', component: AdminsView },
    // Where it used to live. Kept so a bookmark or an open tab still lands on
    // the page rather than on a settings section that does not exist -- and
    // ahead of the :tab route below, which would otherwise swallow it.
    { path: '/settings/admins', redirect: '/admins' },
    // The menu links straight to a settings section. Each is the same page with
    // its tab already chosen, so a bookmark lands where it was taken from.
    { path: '/settings/:tab', name: 'settings-tab', component: SettingsView, props: true },
    { path: '/settings/notify', redirect: '/settings/telegram' },
    { path: '/configs/:tab', name: 'configs', component: ConfigsView, props: true },
    // The engine: what the classic panel keeps under Xray. A section per page, and the
    // hash form the classic panel's links use is honoured too.
    { path: '/engine', name: 'engine', component: EngineView },
    { path: '/engine/:tab', name: 'engine-tab', component: EngineView, props: true },
    { path: '/configs', redirect: '/configs/engine' },
    // Shown rather than redirected: silently swallowing a typo leaves the
    // operator unsure whether they mistyped or the page moved.
    { path: '/:pathMatch(.*)*', name: 'not-found', component: NotFoundView, meta: { sells: true, everyone: true } },
  ],
})

router.beforeEach((to) => {
  // Shown from the moment of the click. A page whose data takes half a second
  // otherwise leaves the previous one on screen, and the operator cannot tell
  // whether the click registered.
  setNavigating(true)

  if (!store.ready) return true
  if (to.meta.public) {
    return store.admin ? { name: 'overview' } : true
  }
  if (!store.admin) return { name: 'login', query: { next: to.fullPath } }
  if (!allowed(to)) {
    // Sent to the page they do have rather than shown a refusal. A
    // reseller following an old bookmark into the routing table has not
    // done anything wrong; there is simply nothing there for them.
    return { name: store.admin.managesPanel || store.admin.seesEveryone ? 'overview' : 'clients' }
  }
  return true
})

// allowed answers the question the sidebar answers for its own items.
function allowed(to) {
  const me = store.admin
  if (!me || me.managesPanel) return true
  if (me.seesEveryone) return !!(to.meta.everyone || to.meta.sells)
  return !!to.meta.sells
}

// A token can expire between page loads. When any request comes back 401 the
// api layer raises this, and the app returns to the sign-in screen rather than
// leaving the operator on a page that silently stops updating.
// Cleared as soon as the route resolves. The views that load data keep their own
// spinner from here, so the bar covers only the gap between the click and that.
//
// Not inside requestAnimationFrame: that does not fire while a tab is in the
// background, so a navigation started and then backgrounded would leave the bar
// running for as long as the tab stayed hidden.
router.afterEach(() => {
  setNavigating(false)
  // A page that arrived is proof the build is whole; the next failure may
  // reload again.
  try { sessionStorage.removeItem('wui.reloadedFor') } catch { /* private window */ }
})

// A navigation that is cancelled or fails never reaches afterEach, and the bar
// would run forever on a guard that redirects.
//
// And a page that cannot be fetched is, nearly always, a page from before an
// update: the tab still runs the old build, whose page files the updated panel
// no longer has. Loading the address afresh lands on the new build at the page
// that was asked for, instead of a click that does nothing. Once per address,
// so a page that is really missing does not reload for ever.
router.onError((err, to) => {
  setNavigating(false)
  const chunk = /dynamically imported module|Importing a module script failed|error loading dynamically|MIME type/i
  if (!chunk.test(String(err?.message || err))) return
  const target = router.resolve(to?.fullPath || '/').href
  let tried = ''
  try { tried = sessionStorage.getItem('wui.reloadedFor') || '' } catch { tried = '' }
  if (tried === target) return
  try { sessionStorage.setItem('wui.reloadedFor', target) } catch { /* private window */ }
  window.location.assign(target)
})

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
    createApp(App).use(router).directive('fit', vFit).mount('#app')
  })
