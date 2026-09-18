---
description: "A named bucket of customers : a reseller, a plan tier, a batch : used for filtering and bulk actions. A group can carry defaults  that a new customer in it inherits, and a whole group can be…"
---

# Groups

A named bucket of customers — a reseller, a plan tier, a batch — used for filtering and bulk actions. A group can carry defaults (quota, days, device limit) that a new customer in it inherits, and a whole group can be enabled or disabled as one.

The Clients page filters by group with one click on the group tag.

## Several at once

A customer can be in **several groups** — a reseller's and a region's and a plan tier's. The Groups box on the client dialog is a picker: choose from the groups that exist, or type a new name and press Enter (or pick *Add “name”*) to start one on the spot. Each group is a chip; the cross takes the customer out of that one group and leaves the rest.

On the groups page, *add members* puts customers into that group and *remove members* takes them out of that group only. Renaming a group onto another merges them. Because customers can overlap, the groups' counts can add up to more than the customer count.

The API carries `groups` (a list) on a customer; the older `group` field still works and means a list of one. `POST /api/groups/assign` takes `{group, ids, remove}`.
