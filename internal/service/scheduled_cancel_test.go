package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stellarbill-backend/internal/repository"
	"stellarbill-backend/internal/service"
)

func activeSub(id string) *repository.SubscriptionRow {
	return &repository.SubscriptionRow{
		ID:         id,
		PlanID:     "plan-1",
		TenantID:   "tenant-1",
		CustomerID: "cust-1",
		Status:     "active",
		Amount:     "1000",
		Currency:   "USD",
		Interval:   "month",
	}
}

// --- ScheduleCancel ---

func TestScheduleCancel_HappyPath(t *testing.T) {
	sub := activeSub("sub-sc1")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)

	cancelAt := time.Now().Add(24 * time.Hour)
	result, err := svc.ScheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-sc1", cancelAt)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.SubscriptionID != "sub-sc1" {
		t.Errorf("unexpected subscription_id: %q", result.SubscriptionID)
	}
	if result.CancelAt == nil {
		t.Fatal("expected cancel_at to be set")
	}
	if result.ScheduledBy != "cust-1" {
		t.Errorf("expected scheduled_by cust-1, got %q", result.ScheduledBy)
	}
}

func TestScheduleCancel_PastTimestampRejected(t *testing.T) {
	sub := activeSub("sub-sc2")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)

	_, err := svc.ScheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-sc2", time.Now().Add(-time.Second))
	if !errors.Is(err, service.ErrCancelAtPast) {
		t.Fatalf("expected ErrCancelAtPast, got %v", err)
	}
}

func TestScheduleCancel_NowTimestampRejected(t *testing.T) {
	// cancel_at == now is not strictly in the future
	sub := activeSub("sub-sc-now")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)

	_, err := svc.ScheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-sc-now", time.Now().Add(-time.Millisecond))
	if !errors.Is(err, service.ErrCancelAtPast) {
		t.Fatalf("expected ErrCancelAtPast for past timestamp, got %v", err)
	}
}

func TestScheduleCancel_NotFound(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.ScheduleCancel(context.Background(), "tenant-1", "cust-1", "missing", time.Now().Add(time.Hour))
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestScheduleCancel_CrossTenantNotFound(t *testing.T) {
	sub := activeSub("sub-sc3")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.ScheduleCancel(context.Background(), "tenant-2", "cust-1", "sub-sc3", time.Now().Add(time.Hour))
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant, got %v", err)
	}
}

func TestScheduleCancel_DeletedSubscription(t *testing.T) {
	sub := activeSub("sub-sc4")
	now := time.Now()
	sub.DeletedAt = &now
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.ScheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-sc4", time.Now().Add(time.Hour))
	if !errors.Is(err, service.ErrDeleted) {
		t.Fatalf("expected ErrDeleted, got %v", err)
	}
}

func TestScheduleCancel_EmptyCallerForbidden(t *testing.T) {
	sub := activeSub("sub-sc5")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.ScheduleCancel(context.Background(), "tenant-1", "", "sub-sc5", time.Now().Add(time.Hour))
	if !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for empty caller, got %v", err)
	}
}

// --- UnscheduleCancel ---

func TestUnscheduleCancel_HappyPath(t *testing.T) {
	sub := activeSub("sub-us1")
	t0 := time.Now().Add(24 * time.Hour)
	sub.CancelAt = &t0
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)

	result, err := svc.UnscheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-us1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.CancelAt != nil {
		t.Errorf("expected cancel_at nil after unschedule, got %q", *result.CancelAt)
	}
}

func TestUnscheduleCancel_IdempotentWhenNoneScheduled(t *testing.T) {
	sub := activeSub("sub-us2")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.UnscheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-us2")
	if err != nil {
		t.Fatalf("expected no error (idempotent), got %v", err)
	}
}

func TestUnscheduleCancel_NotFound(t *testing.T) {
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.UnscheduleCancel(context.Background(), "tenant-1", "cust-1", "missing")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUnscheduleCancel_DeletedSubscription(t *testing.T) {
	sub := activeSub("sub-us3")
	now := time.Now()
	sub.DeletedAt = &now
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.UnscheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-us3")
	if !errors.Is(err, service.ErrDeleted) {
		t.Fatalf("expected ErrDeleted, got %v", err)
	}
}

func TestUnscheduleCancel_EmptyCallerForbidden(t *testing.T) {
	sub := activeSub("sub-us4")
	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)
	_, err := svc.UnscheduleCancel(context.Background(), "tenant-1", "", "sub-us4")
	if !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

// --- ChangeStatus fires scheduled cancellation ---

func TestChangeStatus_FiresScheduledCancellation(t *testing.T) {
	sub := activeSub("sub-fire1")
	// cancel_at is in the past — should fire on next ChangeStatus call
	past := time.Now().Add(-time.Second)
	sub.CancelAt = &past

	subRepo := repository.NewMockSubscriptionRepo(sub)
	svc := service.NewSubscriptionService(subRepo, repository.NewMockPlanRepo())

	// caller requests "paused" but cancel_at has fired — must cancel instead
	result, err := svc.ChangeStatus(context.Background(), "tenant-1", "merchant-1", "sub-fire1", "paused")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != "cancelled" {
		t.Fatalf("expected status cancelled after cancel_at fired, got %q", result.Status)
	}
	if !result.Changed {
		t.Fatal("expected changed=true")
	}

	row, _ := subRepo.FindByIDAndTenant(context.Background(), "sub-fire1", "tenant-1")
	if row.Status != "cancelled" {
		t.Fatalf("expected persisted status cancelled, got %q", row.Status)
	}
}

func TestChangeStatus_DoesNotFireWhenCancelAtInFuture(t *testing.T) {
	sub := activeSub("sub-notfire")
	future := time.Now().Add(24 * time.Hour)
	sub.CancelAt = &future

	svc := service.NewSubscriptionService(
		repository.NewMockSubscriptionRepo(sub),
		repository.NewMockPlanRepo(),
	)

	result, err := svc.ChangeStatus(context.Background(), "tenant-1", "merchant-1", "sub-notfire", "paused")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Status != "paused" {
		t.Fatalf("expected status paused (cancel_at not yet due), got %q", result.Status)
	}
}

func TestScheduleCancel_ThenUnscheduleBeforeFire(t *testing.T) {
	sub := activeSub("sub-und1")
	subRepo := repository.NewMockSubscriptionRepo(sub)
	svc := service.NewSubscriptionService(subRepo, repository.NewMockPlanRepo())

	cancelAt := time.Now().Add(48 * time.Hour)
	_, err := svc.ScheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-und1", cancelAt)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}

	row, _ := subRepo.FindByIDAndTenant(context.Background(), "sub-und1", "tenant-1")
	if row.CancelAt == nil {
		t.Fatal("expected CancelAt to be set after schedule")
	}

	_, err = svc.UnscheduleCancel(context.Background(), "tenant-1", "cust-1", "sub-und1")
	if err != nil {
		t.Fatalf("unschedule: %v", err)
	}

	row, _ = subRepo.FindByIDAndTenant(context.Background(), "sub-und1", "tenant-1")
	if row.CancelAt != nil {
		t.Fatal("expected CancelAt to be nil after unschedule")
	}

	// Now a ChangeStatus should NOT cancel
	result, err := svc.ChangeStatus(context.Background(), "tenant-1", "merchant-1", "sub-und1", "paused")
	if err != nil {
		t.Fatalf("change status after unschedule: %v", err)
	}
	if result.Status != "paused" {
		t.Fatalf("expected paused after unschedule cleared cancel, got %q", result.Status)
	}
}
