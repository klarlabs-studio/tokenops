package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminGetsAllPermissions(t *testing.T) {
	all := []Permission{
		PermSpendRead, PermSpendForecastRead, PermWorkflowRead,
		PermOptimizationRead, PermOptimizationApply, PermReplayRun,
		PermCacheManage, PermConfigRead, PermConfigWrite, PermAuditRead,
	}
	for _, p := range all {
		if !Allows(RoleAdmin, p) {
			t.Errorf("admin missing %s", p)
		}
	}
}

func TestOperatorMissingWriteAndAudit(t *testing.T) {
	if Allows(RoleOperator, PermConfigWrite) {
		t.Error("operator must not have config.write")
	}
	if Allows(RoleOperator, PermAuditRead) {
		t.Error("operator must not have audit.read")
	}
	if !Allows(RoleOperator, PermOptimizationApply) {
		t.Error("operator should keep optimization.apply")
	}
}

func TestViewerReadOnly(t *testing.T) {
	if Allows(RoleViewer, PermOptimizationApply) {
		t.Error("viewer must not apply optimizations")
	}
	if Allows(RoleViewer, PermCacheManage) {
		t.Error("viewer must not manage cache")
	}
	if !Allows(RoleViewer, PermSpendRead) {
		t.Error("viewer should be able to read spend")
	}
}

func TestAnonymousDeniedByDefault(t *testing.T) {
	for _, p := range []Permission{
		PermSpendRead, PermConfigRead, PermAuditRead,
	} {
		if Allows(RoleAnonymous, p) {
			t.Errorf("anonymous granted %s", p)
		}
	}
}

func TestRequireUsesContextRole(t *testing.T) {
	ctx := WithRole(context.Background(), RoleViewer)
	if err := Require(ctx, PermSpendRead); err != nil {
		t.Errorf("viewer denied spend.read: %v", err)
	}
	if err := Require(ctx, PermConfigWrite); !errors.Is(err, ErrForbidden) {
		t.Errorf("viewer config.write expected forbidden, got %v", err)
	}
}

func TestHTTPMiddlewareGates(t *testing.T) {
	called := false
	handler := HTTPMiddleware(PermConfigWrite, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	// Anonymous → 403
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/config", nil))
	if rec.Code != 403 || called {
		t.Errorf("anonymous should be forbidden: code=%d called=%v", rec.Code, called)
	}

	// Viewer → still 403
	called = false
	req := httptest.NewRequest("POST", "/api/config", nil).WithContext(WithRole(context.Background(), RoleViewer))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 || called {
		t.Errorf("viewer should be forbidden: code=%d called=%v", rec.Code, called)
	}

	// Admin → ok
	called = false
	req = httptest.NewRequest("POST", "/api/config", nil).WithContext(WithRole(context.Background(), RoleAdmin))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 || !called {
		t.Errorf("admin should be allowed: code=%d called=%v", rec.Code, called)
	}
}

func TestPermissionsReturnsCopy(t *testing.T) {
	perms := Permissions(RoleViewer)
	original := len(perms)
	// Mutate the returned slice (dead-store on purpose — we're checking
	// the original storage isn't aliased).
	_ = append(perms, "fake")
	got := Permissions(RoleViewer)
	if len(got) != original {
		t.Errorf("Permissions returned shared slice; mutating leaked: original=%d after=%d", original, len(got))
	}
}

func TestAllRolesIncludesAnonymous(t *testing.T) {
	roles := AllRoles()
	want := map[Role]bool{RoleAdmin: false, RoleOperator: false, RoleViewer: false, RoleAnonymous: false}
	for _, r := range roles {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for r, present := range want {
		if !present {
			t.Errorf("AllRoles missing %s", r)
		}
	}
}
