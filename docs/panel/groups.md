---
description: "Labels on customers — a reseller, a region, a plan, a batch — for filtering the customer list and acting on everyone with a label at once."
---

# Groups

A group is a label on customers: a reseller's batch, a region, a plan tier, a trial. It is used to filter the customer list and to act on everyone carrying the label at once — extend them, top them up, reset them, switch them on or off.

A group holds a name and a note, nothing else: it gives its members no plan of its own, and a customer put into a group keeps their own allowance, date and users.

A customer can be in **several groups** — a reseller's, a region's and a plan tier's — so the groups' counts can add up to more than the number of customers.

## The strip

| Figure | What it is |
|--------|------------|
| **Groups** | How many groups exist. |
| **Grouped clients** | How many customers are in at least one group. |
| **Up / Down** | What the grouped customers have sent and received. |
| **Traffic** | The two together. |

## The table

**Add group** creates an empty group with a **Group** name and an optional **Note**. A group can exist before anyone is in it.

| Column | What it shows |
|--------|---------------|
| **Actions** | ☰ opens the group's menu; ✎ renames it. |
| **Group** | The name. Click it to open the Clients page filtered to this group. |
| **Customers** | How many customers carry it. |
| **Upload** / **Download** | What its members have sent and received. |
| **Traffic used** | The two together. |

## The group menu

Items that act on members are greyed out while the group is empty. The ones below the line cannot be undone.

| Item | What it does |
|------|--------------|
| **Extend validity** | Adds **Days to add** to each member's own end date, so anyone with time left keeps it. Anyone already past their date is counted from today and is back on. Members with no end date — a plan that never ends, or one waiting for its first connection — are left alone. A negative number takes days away. |
| **Set allowance** | Gives every member the same allowance, in GB; empty means unlimited. Anyone who was out of data and now has room is back on. |
| **Reset usage** | Sets every member's used traffic back to zero. Anyone out of data is back on. |
| **Enable** / **Disable** | Switches every member on or off. A plan that has ended is cut off again within a tick: it needs more traffic or time, not the switch. |
| **Add customers to group** | A list of customers not in the group, with search; tick and **Save**. |
| **Rename group** | A **New name**. Renaming onto a group that already exists merges the two. |
| **Take customers out** | A list of the members, with search; tick and **Save**. They leave this group only and keep the others. |
| **Dissolve group** | Takes everyone out of the group and leaves it empty. The customers are kept. |
| **Delete all clients** | Deletes every customer in the group, with all their files. This deletes the people, not just the label. |
| **Delete group** | Removes the group. Its customers are kept and simply lose this label. |

## Groups on the Clients page

- The **Groups** column shows a customer's first group, and *+N* for the rest; click one to filter the list to it.
- The client form's **Groups** box is a picker: choose groups that exist, or type a new name and press Enter (or pick *Add "name"*) to start one on the spot. The × on a chip takes the customer out of that group only.
- **more → Add to group** and **more → Ungroup** act on the selected customers; the filter panel's **Groups** narrows the list to any of the chosen groups.

## Resellers

A [reseller](/panel/operators) has groups of their own: names are unique per operator, so two resellers can each have a "trial" group without seeing each other's. Every customer a reseller creates is also put in the group the owner named for that reseller, which the reseller never sees and cannot rename, delete or take a customer out of — so filtering by it tells the owner whose customers these are.

## From the API

A customer carries `groups` (a list); the older `group` field still works and means a list of one. `POST /api/groups/assign` takes `{group, ids, remove}`.
