package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// What only an administrator may do is refused to a machine token, and the
// list of such routes covers what a leaked token could otherwise use to
// keep the door open: the administrator's own account, further tokens,
// backups, the node registry, the panel's settings.
func TestOperatorRoutesRefuseAMachineToken(t *testing.T) {
	s := &Server{}
	called := false
	h := s.requireOperator(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/tokens", nil))
	if called || rec.Code != http.StatusForbidden {
		t.Fatalf("a request without an administrator got through: called=%v code=%d", called, rec.Code)
	}
	rec = httptest.NewRecorder()
	ctx := context.WithValue(context.Background(), ctxAdmin, &model.Admin{ID: 1, Username: "admin"})
	h(rec, httptest.NewRequest(http.MethodGet, "/api/tokens", nil).WithContext(ctx))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("an administrator was refused: called=%v code=%d", called, rec.Code)
	}

	must := map[string]bool{
		"GET /api/auth/me": true, "PATCH /api/auth/me": true, "POST /api/auth/password": true,
		"POST /api/auth/totp/start": true, "POST /api/auth/totp/confirm": true, "POST /api/auth/totp/disable": true,
		"GET /api/tokens": true, "POST /api/tokens": true, "PATCH /api/tokens/{id}": true, "DELETE /api/tokens/{id}": true,
		"GET /api/backups": true, "POST /api/backups": true, "GET /api/backups/{name}": true,
		"DELETE /api/backups/{name}": true, "POST /api/backups/upload": true, "POST /api/backups/{name}/restore": true,
		"POST /api/nodes": true, "PATCH /api/nodes/{id}": true, "DELETE /api/nodes/{id}": true,
		"POST /api/nodes/mtls/trust": true, "PUT /api/settings": true, "POST /api/panel/restart": true,
		"GET /api/template": true, "PUT /api/template": true,
	}
	for _, r := range s.routes() {
		key := r.Method + " " + r.Path
		if must[key] && !r.Operator {
			t.Errorf("%s is open to a machine token", key)
		}
		if r.Operator && !r.Auth {
			t.Errorf("%s is operator-only but not authenticated", key)
		}
	}
}
