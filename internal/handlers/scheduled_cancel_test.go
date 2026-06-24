package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"stellarbill-backend/internal/auth"
	"stellarbill-backend/internal/repository"
	"stellarbill-backend/internal/service"
)

func setupScheduleCancelRouter(svc service.SubscriptionService, tenantID, callerID string, roles []auth.Role) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tenantID != "" {
			c.Set("tenantID", tenantID)
		}
		if callerID != "" {
			c.Set("callerID", callerID)
		}
		if len(roles) > 0 {
			c.Set(auth.RolesContextKey, roles)
		}
		c.Next()
	})
	r.POST("/subscriptions/:id/cancel-schedule",
		auth.RequirePermission(auth.PermManageSubscriptions),
		NewScheduleCancelHandler(svc),
	)
	r.DELETE("/subscriptions/:id/cancel-schedule",
		auth.RequirePermission(auth.PermManageSubscriptions),
		NewUnscheduleCancelHandler(svc),
	)
	return r
}

func postScheduleCancel(t *testing.T, r *gin.Engine, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/subscriptions/"+id+"/cancel-schedule", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func deleteScheduleCancel(t *testing.T, r *gin.Engine, id string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/subscriptions/"+id+"/cancel-schedule", nil)
	r.ServeHTTP(rec, req)
	return rec
}

func TestScheduleCancelHandler_HappyPath(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-h1", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	cancelAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec := postScheduleCancel(t, r, "sub-h1", map[string]string{"cancel_at": cancelAt})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data service.ScheduledCancellationDetail `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.SubscriptionID != "sub-h1" {
		t.Errorf("unexpected subscription_id: %q", resp.Data.SubscriptionID)
	}
	if resp.Data.CancelAt == nil {
		t.Error("expected cancel_at to be populated")
	}
}

func TestScheduleCancelHandler_PastTimestampRejected(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-past", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := postScheduleCancel(t, r, "sub-past", map[string]string{"cancel_at": past})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCancelHandler_MissingCancelAt(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-missing", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	rec := postScheduleCancel(t, r, "sub-missing", map[string]string{})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCancelHandler_InvalidTimestamp(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-inv", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	rec := postScheduleCancel(t, r, "sub-inv", map[string]string{"cancel_at": "not-a-date"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCancelHandler_RequiresPermission(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-perm", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", nil) // no roles

	cancelAt := fmt.Sprintf("%s", time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	rec := postScheduleCancel(t, r, "sub-perm", map[string]string{"cancel_at": cancelAt})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCancelHandler_RequiresTenantContext(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-notenant", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "", "c1", []auth.Role{auth.RoleMerchant})

	cancelAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec := postScheduleCancel(t, r, "sub-notenant", map[string]string{"cancel_at": cancelAt})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestScheduleCancelHandler_NotFound(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	cancelAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec := postScheduleCancel(t, r, "sub-ghost", map[string]string{"cancel_at": cancelAt})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUnscheduleCancelHandler_HappyPath(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-del1", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	rec := deleteScheduleCancel(t, r, "sub-del1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data service.ScheduledCancellationDetail `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.CancelAt != nil {
		t.Errorf("expected nil cancel_at after DELETE, got %q", *resp.Data.CancelAt)
	}
}

func TestUnscheduleCancelHandler_NotFound(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleMerchant})

	rec := deleteScheduleCancel(t, r, "sub-ghost")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUnscheduleCancelHandler_RequiresPermission(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(&repository.SubscriptionRow{
			ID: "sub-delperm", TenantID: "t1", CustomerID: "c1", Status: "active",
		}),
		repository.NewMockPlanRepo(),
	)
	r := setupScheduleCancelRouter(svc, "t1", "c1", []auth.Role{auth.RoleCustomer}) // no manage perm

	rec := deleteScheduleCancel(t, r, "sub-delperm")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
