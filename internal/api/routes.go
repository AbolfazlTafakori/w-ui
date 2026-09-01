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
			Body: `{"name":"Ali","interfaceId":1,"quotaBytes":10737418240,` +
				`"deviceLimit":2,"resetCycle":"none","deviceNames":["Phone","Laptop"]}`,
			Note:    "Set startOnFirstUse with durationDays to begin the clock on first connection.",
			handler: s.handleCreateClient},
		{Method: "GET", Path: "/api/clients/{id}", Group: "Customers", Auth: true,
			Summary: "One customer with their devices.", handler: s.handleGetClient},
		{Method: "PATCH", Path: "/api/clients/{id}", Group: "Customers", Auth: true,
			Summary: "Change a customer's plan or status.",
			Body:    `{"quotaBytes":21474836480,"status":"active"}`,
			handler: s.handleUpdateClient},
		{Method: "DELETE", Path: "/api/clients/{id}", Group: "Customers", Auth: true,
			Summary: "Delete a customer and release their addresses.",
			handler: s.handleDeleteClient},
		{Method: "POST", Path: "/api/clients/{id}/devices", Group: "Customers", Auth: true,
			Summary: "Issue another device.", Body: `{"name":"Tablet"}`,
			handler: s.handleAddDevice},
		{Method: "POST", Path: "/api/clients/{id}/reset", Group: "Customers", Auth: true,
			Summary: "Set one customer's usage back to zero.", handler: s.handleResetTraffic},

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
				`"subnet":"10.66.0.0/16","endpointHost":"vpn.example.com","natInterface":"eth0"}`,
			handler: s.handleCreateInterface},
		{Method: "PATCH", Path: "/api/interfaces/{id}", Group: "Interfaces", Auth: true,
			Summary: "Change a tunnel.", Body: `{"dns":"1.1.1.1","mtu":1420}`,
			handler: s.handleUpdateInterface},
		{Method: "DELETE", Path: "/api/interfaces/{id}", Group: "Interfaces", Auth: true,
			Summary: "Delete a tunnel and everything on it.",
			handler: s.handleDeleteInterface},

		// ── Groups ──
		{Method: "GET", Path: "/api/groups", Group: "Groups", Auth: true,
			Summary: "Groups with their totals.", handler: s.handleListGroups},
		{Method: "GET", Path: "/api/groups/names", Group: "Groups", Auth: true,
			Summary: "Just the names, for a picker.", handler: s.handleGroupNames},
		{Method: "POST", Path: "/api/groups/rename", Group: "Groups", Auth: true,
			Summary: "Rename a group.", Body: `{"from":"reseller-a","to":"reseller-north"}`,
			handler: s.handleRenameGroup},
		{Method: "POST", Path: "/api/groups/assign", Group: "Groups", Auth: true,
			Summary: "Put customers into a group.", Body: `{"group":"reseller-a","ids":[1,2]}`,
			handler: s.handleAssignGroup},
		{Method: "POST", Path: "/api/groups/action", Group: "Groups", Auth: true,
			Summary: "Act on every member of a group.",
			Body:    `{"group":"reseller-a","action":"disable"}`, handler: s.handleGroupAction},

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
		"Groups", "Server", "Settings"}
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
