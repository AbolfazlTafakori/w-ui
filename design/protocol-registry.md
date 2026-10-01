# Design: a protocol registry

Status: proposed. Not implemented. A prerequisite for IKEv2/IPsec
(strongSwan) and L2TP, PPTP and SSTP (accel-ppp) with a built-in RADIUS
server.

## Where the boundary is today

The driver boundary exists and is clean at its core:

- `internal/backend/backend.go:96` -- `Backend`: `Protocol`, `Open`, `Sync`
  (the desired accounts), `Stats`, `Kick`, `Render`; plus `Destroyer` and a
  remover registry.
- `internal/backend/registry.go` -- `Register(protocol, factory)`, `New`,
  `Registered`; `wgdriver` and `ovpndriver` register themselves.
- **Enforcement, quotas and billing are protocol-blind.** `internal/enforce`
  matches a customer by the address the tunnel gave them and counts in
  nftables per customer key; `internal/reconciler` mentions protocols only in
  comments. A driver that gives each account a stable address inside the
  interface's subnet, with its traffic forwarded through the host, is
  enforced and billed with no change there.

What leaks out of the drivers, and has to move behind them:

| Where | What is protocol-specific |
|---|---|
| `internal/database/model/models.go` -- `Interface` | `Mode`, `AWG` (AmneziaWG parameters), `OpenVPN` (PKI, transport, ciphers) as columns |
| `internal/database/model/models.go` -- `Account` | `PrivateKey`, `PublicKey`, `PresharedKey` (WireGuard); `Username`, `Secret` (OpenVPN) |
| `internal/service/interfaces.go` | validation and defaults per protocol (lines ~137-600) |
| `internal/service/clients.go` | `buildAccount` (a switch on the protocol), OpenVPN credentials (`checkOpenVPNCredentials`, `setOpenVPNCredentials`) |
| `internal/service/rotatekeys.go` | what "new credentials" means per protocol |
| `internal/api/subpage.go`, `subpreview.go` | which file formats a device offers |
| `internal/shaper` | limits are applied per tunnel device |
| `web/src` | `InterfaceForm.vue`, `ClientForm.vue`, `InterfaceDetail.vue`, `ClientInfoModal.vue`, `ClientQrModal.vue`, `InterfacesView.vue`, `OutboundForm.vue` |

## The design

### One spec per protocol

Each driver package registers, beside its `Backend`, a `Spec`:

```go
// Spec is everything the panel knows about a protocol outside the running
// tunnel: how an interface of it is described and checked, what a customer's
// credentials are, what files a device gets, and how its speed is limited.
type Spec struct {
	Protocol model.Protocol
	Modes    []model.InterfaceMode // "standard", "amnezia", ...

	// Interface settings: the fields the form shows, their defaults, and
	// the check on save. Params is the protocol's own settings, as JSON.
	Fields   []Field
	Defaults func(in *InterfaceInput)
	Validate func(in *InterfaceInput) error

	// Credentials: what a new device gets, what rotating it means, and
	// whether a customer chooses a login (OpenVPN, L2TP, SSTP, IKEv2-EAP)
	// or is handed keys (WireGuard).
	NewCredentials    func(iface *model.Interface, seat int) (Credentials, error)
	RotateCredentials func(acc *model.Account) (Credentials, error)
	UsesLogin         bool

	// Files a device offers, by format: "conf", "ovpn", "mobileconfig"
	// (IKEv2 on iOS and macOS), "sstp", ... -- each with a renderer.
	Formats []Format

	// How the speed limit is applied (below).
	Shaping ShapingMode

	// Whether the tunnel asks the panel to authenticate each session.
	RADIUS bool
}
```

`service`, `api` and the reconciler stop switching on the protocol. They ask
`backend.SpecFor(iface.Protocol)`. Adding a protocol is a new driver package
and its registration; nothing in `enforce`, `reconciler`, quotas or billing
changes.

### The data, additively

- Existing columns stay, with their names and their meaning: `Interface.Mode`,
  `Interface.AWG`, `Interface.OpenVPN`, and every `Account` column. The
  WireGuard and OpenVPN specs read and write them as they do today.
- New protocols keep their interface settings in one added column,
  `Interface.Params` (JSON, the spec's own shape), and log in with the
  existing `Account.Username` and `Account.Secret`.
- Nothing is renamed or moved; `TestMigrationsOnlyAdd` holds.

### The frontend

`GET /api/protocols` returns each spec's fields (name, type, default,
choices, the i18n key of its label and help) and formats. `InterfaceForm.vue`
draws the protocol's section from them; `ClientForm.vue` shows the login
fields when `UsesLogin`; the device dialogs list the formats. WireGuard,
AmneziaWG and OpenVPN keep their hand-made sections where those do more than
a field list can (AmneziaWG's generator, the OpenVPN transport) -- registered
the same way, so they are just the first three.

### Speed limits

Today `shaper.Apply(devices, clients)` puts an HTB tree on each tunnel device,
one class per customer key, packets classified by the mark nftables puts on
them. That works for a protocol with one device per interface (WireGuard,
OpenVPN's tun). Two new cases:

- **One device per session** (accel-ppp: `ppp0`, `ppp1`, ...): accel-ppp's
  own shaper limits each session, given the rate in the RADIUS answer
  (`Filter-Id` or its rate attribute). `Shaping: ShapingDelegated`: the panel
  hands each session its customer's rate when it authenticates it, and
  changes it with a RADIUS CoA when the customer's rate changes. No tc tree on
  `ppp*`.
- **No device at all** (IPsec in the kernel, policy-based): `ShapingEgress`:
  one HTB tree on the uplink for download and one on an `ifb` mirror of it
  for upload, classified by the same nftables mark. The shaper learns this
  mode as a second `Apply` target; WireGuard and OpenVPN keep the
  per-device tree they have.

### A RADIUS server in the panel

strongSwan (EAP-RADIUS) and accel-ppp both ask a RADIUS server. The panel
answers on `127.0.0.1:1812/1813` with a secret it generates per install:

- **Access-Request**: the customer's login and secret, checked as the
  OpenVPN login is today; refused for a customer who is off, ended or out of
  traffic, or over their connection limit. The answer carries the address the
  panel's address pool gave that account (`Framed-IP-Address`) -- so
  enforcement and billing find them exactly as they find a WireGuard peer --
  and, for accel-ppp, the rate.
- **Accounting**: sessions starting and stopping, for who is online and for
  the connection limit. Traffic is still counted in nftables, as for every
  other protocol: one meter, not two.
- **Kick** sends a Disconnect-Message; a changed rate sends a CoA.

The server listens on the loopback only. A node runs its own, for the tunnels
it runs.

### WireGuard, AmneziaWG and OpenVPN stay byte for byte

The first change moves the three existing protocols behind specs **without
changing what they produce**, before any new protocol lands:

- The code moves; it is not rewritten. `buildAccount`'s WireGuard branch
  becomes the WireGuard spec's `NewCredentials`, unchanged.
- **Proof**: the contract golden files (`cmd/wui/testdata/contract/`), which
  include every customer's configuration as their link serves it; the upgrade
  test from every release since v1.0.0, which compares each configuration
  connecting the same way; the route golden file (the API unchanged); and the
  migrations-are-additive test. All four pass unchanged on that commit, or it
  does not go in.

## Order

1. Specs for WireGuard (with the AmneziaWG mode) and OpenVPN, moving code
   only; the four proofs above unchanged. `GET /api/protocols`.
2. The frontend drawn from the specs, the existing three looking as they do.
3. The shaper's egress mode; the RADIUS server.
4. strongSwan (IKEv2, EAP-MSCHAPv2 and certificates; a `mobileconfig` file).
5. accel-ppp (L2TP/IPsec, SSTP; PPTP only behind a warning).

Each step ships on its own, with the upgrade and contract tests green and the
CI upgrade jobs green on Linux.
