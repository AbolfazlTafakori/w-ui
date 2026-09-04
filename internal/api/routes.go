package api

import (
	"net/http"
	"sort"
	"strings"
)

// The route table is data, not a sequence of registration calls.
//
// The panel documents its own API, and hand-written documentation for an API
// rots the first time an endpoint moves. Registering and documenting from one
// table means the page can only ever describe routes that exist, and a new
// endpoint is documented by being added.

// Route is one endpoint.
type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`

	// Group is the heading it appears under.
	Group string `json:"group"`

	// Summary says what it does, in one line, from the caller's point of view.
	Summary string `json:"summary"`

	// Auth reports whether a bearer token is required.
	Auth bool `json:"auth"`

	// Body is an example request body, if it takes one.
	Body string `json:"body,omitempty"`

	// Note carries anything a caller would otherwise learn the hard way.
	Note string `json:"note,omitempty"`

	handler http.HandlerFunc
}

// routes builds the table. It is a method because the handlers are bound to
// the server.
func (s *Server) routes() []Route {
	return []Route{
		// ── Signing in ──
		{Method: "POST", Path: "/api/auth/login", Group: "Authentication",
			Summary: "Exchange a username and password for a token.",
			Body:    `{"username":"admin","password":"…","code":"123456"}`,
			Note: "The code is only needed when two-factor is on. Without it the " +
				"reply is {\"needCode\":true} and no session is created.",
			handler: s.handleLogin},
		{Method: "GET", Path: "/api/meta", Group: "Authentication",
			Summary: "What this server supports, for the sign-in page.",
			handler: s.handleMeta},
		{Method: "GET", Path: "/api/i18n/{locale}", Group: "Authentication",
			Summary: "Interface strings for a language.",
			handler: s.handleMessages},

		{Method: "GET", Path: "/api/auth/me", Group: "Authentication", Auth: true,
			Summary: "The signed-in administrator.", handler: s.handleMe},
		{Method: "PATCH", Path: "/api/auth/me", Group: "Authentication", Auth: true,
			Summary: "Change your own preferences.", Body: `{"locale":"fa"}`,
			handler: s.handleUpdateMe},
		{Method: "POST", Path: "/api/auth/password", Group: "Authentication", Auth: true,
			Summary: "Change your password.",
			Body:    `{"currentPassword":"…","newPassword":"…"}`,
			handler: s.handleChangePassword},
		{Method: "POST", Path: "/api/auth/totp/start", Group: "Authentication", Auth: true,
			Summary: "Begin enrolling a second factor. Returns a secret and an otpauth URI.",
			Note:    "Nothing is stored until the code is confirmed.",
			handler: s.handleTOTPStart},
		{Method: "POST", Path: "/api/auth/totp/confirm", Group: "Authentication", Auth: true,
			Summary: "Prove the code works and store the secret.",
			Body:    `{"secret":"…","code":"123456"}`, handler: s.handleTOTPConfirm},
		{Method: "POST", Path: "/api/auth/totp/disable", Group: "Authentication", Auth: true,
			Summary: "Turn the second factor off.", Body: `{"password":"…"}`,
			Note:    "The password is required again, so a borrowed session cannot remove it.",
			handler: s.handleTOTPDisable},

		// ── Customers ──
		{Method: "GET", Path: "/api/clients", Group: "Customers", Auth: true,
			Summary: "List customers. Takes page, perPage, search, status, group and sort.",
			handler: s.handleListClients},
		{Method: "POST", Path: "/api/clients", Group: "Customers", Auth: true,
			Summary: "Create a customer and their devices.",
			Body: `{"name":"Ali","interfaceIds":[1,2],"quotaBytes":10737418240,` +
				`"deviceLimit":2,"resetCycle":"none","deviceNames":["Phone","Laptop"]}`,
			Note: "interfaceIds is every server this customer may use; their allowance, " +
				"expiry and device limit are shared across all of them, so one being " +
				"blocked leaves the rest working on the same purchase. interfaceId still " +
				"works for one server. Set startOnFirstUse with durationDays to begin " +
				"the clock on first connection.",
			handler: s.handleCreateClient},
		{Method: "GET", Path: "/api/clients/{id}", Group: "Customers", Auth: true,
			Summary: "One customer with their devices.", handler: s.handleGetClient},
		{Method: "PATCH", Path: "/api/clients/{id}", Group: "Customers", Auth: true,
			Summary: "Change a customer's plan, status, or which servers they reach.",
			Body:    `{"quotaBytes":21474836480,"status":"active","interfaceIds":[1,3]}`,
			Note: "Sending interfaceIds replaces the set of servers: one added issues " +
				"credentials there for every device they hold, one removed deletes " +
				"those credentials. Their usage and expiry are untouched.",
			handler: s.handleUpdateClient},
		{Method: "DELETE", Path: "/api/clients/{id}", Group: "Customers", Auth: true,
			Summary: "Delete a customer and release their addresses.",
			handler: s.handleDeleteClient},
		{Method: "POST", Path: "/api/clients/{id}/devices", Group: "Customers", Auth: true,
			Summary: "Issue another device, on every server this customer reaches.",
			Body:    `{"name":"Tablet"}`,
			Note:    "Returns one account per server. The device limit counts devices, not accounts.",
			handler: s.handleAddDevice},
		{Method: "POST", Path: "/api/clients/{id}/reset", Group: "Customers", Auth: true,
			Summary: "Set one customer's usage back to zero.", handler: s.handleResetTraffic},
		{Method: "GET", Path: "/api/clients/{id}/configs", Group: "Customers", Auth: true,
			Summary: "Every device's configuration, as a zip. Add ?format=base64 or " +
				"?format=plain for the subscription formats instead.",
			handler: s.handleClientConfigs},

		{Method: "POST", Path: "/api/clients/bulk", Group: "Customers", Auth: true,
			Summary: "Enable, disable, reset or delete many at once.",
			Body:    `{"action":"disable","ids":[1,2,3]}`, handler: s.handleBulk},
		{Method: "POST", Path: "/api/clients/adjust", Group: "Customers", Auth: true,
			Summary: "Extend or change the plan of many at once.",
			Body:    `{"ids":[1,2],"addDays":30,"quotaBytes":53687091200}`,
			Note:    "addDays on an expired customer counts from now, not from their old date.",
			handler: s.handleAdjust},
		{Method: "POST", Path: "/api/clients/reset-all", Group: "Customers", Auth: true,
			Summary: "Set every customer's usage back to zero.", handler: s.handleResetAll},
		{Method: "POST", Path: "/api/clients/purge", Group: "Customers", Auth: true,
			Summary: "Delete every customer with a given status.",
			Body:    `{"status":"expired"}`, handler: s.handlePurge},
		{Method: "POST", Path: "/api/clients/batch", Group: "Customers", Auth: true,
			Summary: "Create many customers from one template.",
			Body:    `{"prefix":"user","count":25,"interfaceId":1,"quotaBytes":10737418240}`,
			handler: s.handleCreateBatch},
		{Method: "GET", Path: "/api/clients/export", Group: "Customers", Auth: true,
			Summary: "Download the customer list.",
			Note:    "Carries no keys or usage counters: it records who was sold what.",
			handler: s.handleExport},
		{Method: "POST", Path: "/api/clients/import", Group: "Customers", Auth: true,
			Summary: "Load a customer list back.",
			Body:    `{"interfaceId":1,"onConflict":"skip","clients":[…]}`,
			Note:    "onConflict is skip, rename or replace. Skip is the default and never overwrites.",
			handler: s.handleImport},

		// ── Devices ──
		{Method: "GET", Path: "/api/devices/{id}/profile", Group: "Devices", Auth: true,
			Summary: "The configuration for one device. Add ?download=1 for the file itself.",
			handler: s.handleProfile},
		{Method: "DELETE", Path: "/api/devices/{id}", Group: "Devices", Auth: true,
			Summary: "Remove a device and free its address.", handler: s.handleRemoveDevice},

		// ── Interfaces ──
		{Method: "GET", Path: "/api/interfaces", Group: "Interfaces", Auth: true,
			Summary: "List the tunnels on this server.", handler: s.handleListInterfaces},
		{Method: "POST", Path: "/api/interfaces", Group: "Interfaces", Auth: true,
			Summary: "Create a tunnel. Keys and certificates are generated here.",
			Body: `{"name":"wg0","protocol":"wireguard","listenPort":51820,` +
				`"subnet":"10.66.0.0/16","endpointHost":"vpn.example.com","natInterface":"eth0",` +
				`"transport":"udp"}`,
			handler: s.handleCreateInterface},
		{Method: "PATCH", Path: "/api/interfaces/{id}", Group: "Interfaces", Auth: true,
			Summary: "Change a tunnel. transport is OpenVPN only and moves every " +
				"customer on it, who then need their configuration again.",
			Body:    `{"dns":"1.1.1.1","mtu":1420,"transport":"tcp"}`,
			handler: s.handleUpdateInterface},
		{Method: "GET", Path: "/api/interfaces/{id}/profile", Group: "Interfaces", Auth: true,
			Summary: "The one OpenVPN file for this tunnel, the same for every customer on it.",
			Note: "OpenVPN only. Hand it out once; sell access by creating credentials. " +
				"A WireGuard profile is per device and comes from /api/devices/{id}/profile.",
			handler: s.handleInterfaceProfile},
		{Method: "POST", Path: "/api/interfaces/{id}/restart", Group: "Interfaces", Auth: true,
			Summary: "Reopen a tunnel's driver without restarting the panel.",
			Note: "For a tunnel that failed to come up — a taken port, a missing tool. " +
				"Restarting the panel instead would disconnect every customer on every " +
				"other interface to fix one.",
			handler: s.handleRestartInterface},
		{Method: "DELETE", Path: "/api/interfaces/{id}", Group: "Interfaces", Auth: true,
			Summary: "Delete a tunnel and everything on it.",
			handler: s.handleDeleteInterface},

		// ── Groups ──
		{Method: "GET", Path: "/api/groups", Group: "Groups", Auth: true,
			Summary: "Groups with their totals.", handler: s.handleListGroups},
		{Method: "GET", Path: "/api/groups/names", Group: "Groups", Auth: true,
			Summary: "Just the names, for a picker.", handler: s.handleGroupNames},
		{Method: "POST", Path: "/api/groups", Group: "Groups", Auth: true,
			Summary: "Create a group, with or without anybody in it.",
			Body:    `{"name":"reseller-a","note":"north region"}`,
			handler: s.handleCreateGroup},
		{Method: "POST", Path: "/api/groups/delete", Group: "Groups", Auth: true,
			Summary: "Delete a group. Its customers are ungrouped, not deleted.",
			Body:    `{"name":"reseller-a"}`,
			Note:    "Deleting the customers as well is a separate action on the clients page.",
			handler: s.handleDeleteGroup},
		{Method: "POST", Path: "/api/groups/rename", Group: "Groups", Auth: true,
			Summary: "Rename a group.", Body: `{"from":"reseller-a","to":"reseller-north"}`,
			handler: s.handleRenameGroup},
		{Method: "POST", Path: "/api/groups/assign", Group: "Groups", Auth: true,
			Summary: "Put customers into a group.", Body: `{"group":"reseller-a","ids":[1,2]}`,
			handler: s.handleAssignGroup},
		{Method: "POST", Path: "/api/groups/action", Group: "Groups", Auth: true,
			Summary: "Act on every member of a group.",
			Body:    `{"group":"reseller-a","action":"disable"}`, handler: s.handleGroupAction},

		// ── Outbounds ──
		{Method: "GET", Path: "/api/outbounds", Group: "Outbounds", Auth: true,
			Summary: "Every way out of this server, in the order they are tried.",
			handler: s.handleListOutbounds},
		{Method: "POST", Path: "/api/outbounds", Group: "Outbounds", Auth: true,
			Summary: "Add a way out: another WireGuard server, or a SOCKS or HTTP proxy.",
			Body:    `{"tag":"frankfurt","kind":"wireguard","address":"de.example.com:51820","privateKey":"…","peerPubKey":"…","hopAddress":"10.9.0.2/32"}`,
			Note:    "The two built-in outbounds, direct and blocked, cannot be created or removed.",
			handler: s.handleCreateOutbound},
		{Method: "PATCH", Path: "/api/outbounds/{id}", Group: "Outbounds", Auth: true,
			Summary: "Change an outbound. Renaming it moves every rule that points at it.",
			Body:    `{"tag":"frankfurt","kind":"wireguard","address":"de.example.com:51820","enabled":true}`,
			Note:    "Blank secrets leave the stored ones alone.",
			handler: s.handleUpdateOutbound},
		{Method: "DELETE", Path: "/api/outbounds/{id}", Group: "Outbounds", Auth: true,
			Summary: "Remove an outbound. Refused while routing rules still point at it.",
			handler: s.handleDeleteOutbound},
		{Method: "POST", Path: "/api/outbounds/order", Group: "Outbounds", Auth: true,
			Summary: "Set the order outbounds are listed in.",
			Body:    `{"ids":[1,4,2,3]}`,
			handler: s.handleReorderOutbounds},
		{Method: "POST", Path: "/api/outbounds/{id}/check", Group: "Outbounds", Auth: true,
			Summary: "Measure how long one outbound takes to answer.",
			Note:    "A WireGuard endpoint is UDP; what is measured is whether the host is reachable, not whether the tunnel is up.",
			handler: s.handleCheckOutbound},
		{Method: "POST", Path: "/api/outbounds/check", Group: "Outbounds", Auth: true,
			Summary: "Measure every outbound at once.",
			handler: s.handleCheckAllOutbounds},

		// ── Routing ──
		{Method: "GET", Path: "/api/routing", Group: "Routing", Auth: true,
			Summary: "The whole policy: the switches, the lists, and the rules in evaluation order.",
			handler: s.handleGetRouting},
		{Method: "PUT", Path: "/api/routing", Group: "Routing", Auth: true,
			Summary: "Save the switch-and-list routing.",
			Body:    `{"blockBitTorrent":true,"blockIps":["private"],"blockDomains":["ads.example.com"],"directIps":["1.1.1.1"],"defaultOutbound":"direct"}`,
			Note:    "Addresses accept a CIDR, a bare address, or a named group such as private or bogon.",
			handler: s.handleSaveRouting},
		{Method: "POST", Path: "/api/routing/rules", Group: "Routing", Auth: true,
			Summary: "Add a rule. Rules are evaluated in order and the first match decides.",
			Body:    `{"name":"stream via de","match":"domain","value":"netflix.com","outboundTag":"frankfurt"}`,
			handler: s.handleCreateRoutingRule},
		{Method: "PATCH", Path: "/api/routing/rules/{id}", Group: "Routing", Auth: true,
			Summary: "Change a rule.",
			Body:    `{"name":"stream via de","match":"domain","value":"netflix.com","outboundTag":"frankfurt","enabled":true}`,
			handler: s.handleUpdateRoutingRule},
		{Method: "DELETE", Path: "/api/routing/rules/{id}", Group: "Routing", Auth: true,
			Summary: "Remove a rule.",
			handler: s.handleDeleteRoutingRule},
		{Method: "POST", Path: "/api/routing/rules/order", Group: "Routing", Auth: true,
			Summary: "Set evaluation order. First match wins, so this changes behaviour.",
			Body:    `{"ids":[3,1,2]}`,
			handler: s.handleReorderRoutingRules},
		{Method: "POST", Path: "/api/routing/test", Group: "Routing", Auth: true,
			Summary: "Ask which outbound would carry a connection, and why. Nothing is sent.",
			Body:    `{"target":"netflix.com","port":443,"protocol":"tcp"}`,
			handler: s.handleTestRoute},

		// ── Hosts ──
		{Method: "GET", Path: "/api/hosts", Group: "Hosts", Auth: true,
			Summary: "The public addresses customers are handed, by interface.",
			handler: s.handleListHosts},
		{Method: "POST", Path: "/api/hosts", Group: "Hosts", Auth: true,
			Summary: "Add a public address for an interface.",
			Body:    `{"interfaceId":1,"name":"cdn","address":"edge.example.com","port":443,"priority":10}`,
			Note:    "With no hosts configured an interface keeps using its own endpoint, as before.",
			handler: s.handleCreateHost},
		{Method: "PATCH", Path: "/api/hosts/{id}", Group: "Hosts", Auth: true,
			Summary: "Change a host.",
			Body:    `{"name":"cdn","address":"edge.example.com","port":443,"enabled":true}`,
			handler: s.handleUpdateHost},
		{Method: "DELETE", Path: "/api/hosts/{id}", Group: "Hosts", Auth: true,
			Summary: "Remove a host.",
			handler: s.handleDeleteHost},
		{Method: "POST", Path: "/api/hosts/{id}/check", Group: "Hosts", Auth: true,
			Summary: "Try to reach a host the way a customer would.",
			handler: s.handleCheckHost},

		{Method: "GET", Path: "/api/security/warnings", Group: "Server", Auth: true,
			Summary: "What an attacker would notice about this installation.",
			Note:    "Read-only. Nothing here changes anything; each warning says where to fix it.",
			handler: s.handleSecurityWarnings},

		// ── Subscriptions ──
		{Method: "GET", Path: "/api/subscription", Group: "Subscriptions", Auth: true,
			Summary: "Whether the subscription service is on, and where it answers.",
			handler: s.handleGetSubSettings},
		{Method: "PUT", Path: "/api/subscription", Group: "Subscriptions", Auth: true,
			Summary: "Turn the subscription service on and choose its path.",
			Body:    `{"enabled":true,"path":"/subscribe/","title":"My VPN","updateHours":12}`,
			Note:    "The path takes effect on the next request; no restart is needed.",
			handler: s.handleSaveSubSettings},
		{Method: "GET", Path: "/api/clients/{id}/subscription", Group: "Subscriptions", Auth: true,
			Summary: "The link for one customer, creating its token if it has none.",
			handler: s.handleClientSubscription},
		{Method: "POST", Path: "/api/clients/{id}/subscription/rotate", Group: "Subscriptions", Auth: true,
			Summary: "Issue a new link. Every copy of the old one stops working at once.",
			handler: s.handleRotateSubscription},

		// ── Nodes ──
		{Method: "GET", Path: "/api/nodes", Group: "Nodes", Auth: true,
			Summary: "Every server this panel watches, with what the last probe found.",
			handler: s.handleListNodes},
		{Method: "POST", Path: "/api/nodes", Group: "Nodes", Auth: true,
			Summary: "Register another W-UI panel as a node.",
			Body:    `{"name":"frankfurt","address":"https://vpn2.example.com:2096","token":"wui_…"}`,
			Note:    "The token is issued on that panel, under Settings. It is probed immediately.",
			handler: s.handleCreateNode},
		{Method: "PATCH", Path: "/api/nodes/{id}", Group: "Nodes", Auth: true,
			Summary: "Change a node.", Body: `{"name":"frankfurt","address":"https://…","enabled":true}`,
			Note:    "An empty token leaves the stored one alone.",
			handler: s.handleUpdateNode},
		{Method: "DELETE", Path: "/api/nodes/{id}", Group: "Nodes", Auth: true,
			Summary: "Remove a node. Refused while it still carries interfaces.",
			handler: s.handleDeleteNode},
		{Method: "POST", Path: "/api/nodes/{id}/probe", Group: "Nodes", Auth: true,
			Summary: "Ask one node right now instead of waiting for the schedule.",
			handler: s.handleProbeNode},
		{Method: "GET", Path: "/api/tokens", Group: "Nodes", Auth: true,
			Summary: "Access tokens issued for machine use.",
			handler: s.handleListTokens},
		{Method: "POST", Path: "/api/tokens", Group: "Nodes", Auth: true,
			Summary: "Issue a token another panel can use against this one.",
			Body:    `{"name":"frankfurt panel"}`,
			Note:    "The secret is returned once and stored only as a hash.",
			handler: s.handleIssueToken},
		{Method: "DELETE", Path: "/api/tokens/{id}", Group: "Nodes", Auth: true,
			Summary: "Revoke a token.", handler: s.handleRevokeToken},

		// ── The server ──
		{Method: "GET", Path: "/api/overview", Group: "Server", Auth: true,
			Summary: "Host telemetry.", handler: s.handleOverview},
		{Method: "GET", Path: "/api/overview/full", Group: "Server", Auth: true,
			Summary: "Telemetry, panel state and inventory in one call.",
			Note:    "One request so the page cannot show three readings from three moments.",
			handler: s.handleFullOverview},
		{Method: "GET", Path: "/api/system", Group: "Server", Auth: true,
			Summary: "Versions, configuration and whether limits are being enforced.",
			handler: s.handleSystemInfo},
		{Method: "GET", Path: "/api/configs", Group: "Server", Auth: true,
			Summary: "The programs this panel is asking the kernel to run, as text.",
			Note:    "Read-only. The reconciler rewrites these from the database every tick.",
			handler: s.handleGeneratedConfigs},
		{Method: "GET", Path: "/api/logs", Group: "Server", Auth: true,
			Summary: "Recent log lines. Takes limit and level.", handler: s.handleLogs},
		{Method: "GET", Path: "/api/sharing", Group: "Server", Auth: true,
			Summary: "Credentials seen from several places at once.",
			Note:    "Evidence, not proof. Nothing is disconnected automatically.",
			handler: s.handleSharing},

		// ── Settings and backups ──
		{Method: "GET", Path: "/api/settings", Group: "Settings", Auth: true,
			Summary: "Panel settings and the shipped defaults.",
			Note:    "The bot token comes back as a placeholder and is never returned in full.",
			handler: s.handleGetSettings},
		{Method: "PUT", Path: "/api/settings", Group: "Settings", Auth: true,
			Summary: "Save panel settings.",
			Body:    `{"sessionHours":12,"defaultLocale":"en","defaultDeviceLimit":1,"defaultResetCycle":"none"}`,
			Note:    "Sending the token placeholder back leaves the stored token alone.",
			handler: s.handleSaveSettings},
		{Method: "POST", Path: "/api/settings/notify/test", Group: "Settings", Auth: true,
			Summary: "Send one test notification and report what happened.",
			handler: s.handleTestNotification},
		{Method: "GET", Path: "/api/backups", Group: "Settings", Auth: true,
			Summary: "Backups on disk, newest first.", handler: s.handleListBackups},
		{Method: "POST", Path: "/api/backups", Group: "Settings", Auth: true,
			Summary: "Take a backup now.", handler: s.handleCreateBackup},
		{Method: "GET", Path: "/api/backups/{name}", Group: "Settings", Auth: true,
			Summary: "Download an archive.",
			Note:    "It holds every key and credential. Treat it like a password file.",
			handler: s.handleDownloadBackup},
		{Method: "DELETE", Path: "/api/backups/{name}", Group: "Settings", Auth: true,
			Summary: "Delete an archive.", handler: s.handleDeleteBackup},
	}
}

// register wires the table into a mux.
func (s *Server) register(mux *http.ServeMux) {
	for _, r := range s.routes() {
		h := r.handler
		if r.Auth {
			h = s.requireAuth(h)
		}
		mux.HandleFunc(r.Method+" "+r.Path, h)
	}
}

// APIGroup is one heading in the documentation.
type APIGroup struct {
	Name   string  `json:"name"`
	Routes []Route `json:"routes"`
}

// handleAPIDocs describes the API from the same table that serves it.
func (s *Server) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	order := []string{"Authentication", "Customers", "Devices", "Interfaces",
		"Groups", "Nodes", "Server", "Settings"}
	rank := map[string]int{}
	for i, g := range order {
		rank[g] = i
	}

	byGroup := map[string][]Route{}
	for _, rt := range s.routes() {
		byGroup[rt.Group] = append(byGroup[rt.Group], rt)
	}

	groups := make([]APIGroup, 0, len(byGroup))
	for name, rs := range byGroup {
		groups = append(groups, APIGroup{Name: name, Routes: rs})
	}
	sort.Slice(groups, func(i, j int) bool {
		ri, oki := rank[groups[i].Name]
		rj, okj := rank[groups[j].Name]
		if oki && okj {
			return ri < rj
		}
		return groups[i].Name < groups[j].Name
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": baseURL(r),
		"groups":  groups,
		"auth": "Send the token from POST /api/auth/login as " +
			"Authorization: Bearer <token> on every other request.",
	})
}

// baseURL reconstructs the address the caller reached this panel on, so the
// examples are ones they can paste rather than ones with a placeholder host.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}
