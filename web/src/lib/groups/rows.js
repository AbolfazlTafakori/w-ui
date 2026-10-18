// How a group reads in its list: its row menu, and the customers the members
// picker offers. Pure functions of the group and the customers.
import { t } from '../store.js'

// Every entry in the row menu. The order is the classic panel's: what you do
// to the members, then to the group, then everything that destroys
// something. Entries needing members are disabled rather than hidden, so the
// menu keeps its shape on an empty group and an operator learns one layout
// instead of two; the rule before the destructive half means a slip of the
// pointer lands on a separator rather than on delete.
export function menuFor(g) {
  const empty = g.clients === 0
  return [
    { key: 'extend', label: t('group.extend'), icon: 'ClockCircleOutlined', disabled: empty },
    { key: 'quota', label: t('group.setQuota'), icon: 'DatabaseOutlined', disabled: empty },
    { key: 'reset', label: t('action.resetTraffic'), icon: 'RetweetOutlined', disabled: empty },
    { key: 'enable', label: t('action.enable'), icon: 'CheckCircleOutlined', disabled: empty },
    { key: 'disable', label: t('action.disable'), icon: 'StopOutlined', disabled: empty },
    { key: 'addClients', label: t('group.addClients'), icon: 'UsergroupAddOutlined' },
    { key: 'rename', label: t('group.rename'), icon: 'EditOutlined' },
    { divider: true },
    { key: 'removeClients', label: t('group.removeClients'), icon: 'UsergroupDeleteOutlined', danger: true, disabled: empty },
    { key: 'clear', label: t('group.dissolve'), icon: 'UngroupOutlined', danger: true, disabled: empty },
    { key: 'deleteClients', label: t('group.deleteClients'), icon: 'DeleteOutlined', danger: true, disabled: empty },
    { key: 'delete', label: t('group.delete'), icon: 'DeleteOutlined', danger: true },
  ]
}

// The customers the members picker lists: those not in the group to add,
// those in it to take out, narrowed by a search on the name.
export function memberChoices(clients, group, mode, search = '') {
  const q = search.trim().toLowerCase()
  return clients
    .filter((c) => (mode === 'add' ? !(c.groups || []).includes(group) : (c.groups || []).includes(group)))
    .filter((c) => !q || (c.name || '').toLowerCase().includes(q))
}
