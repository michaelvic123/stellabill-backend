package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"stellarbill-backend/internal/repository"
	"stellarbill-backend/internal/security"
	"stellarbill-backend/internal/subscriptions"
	"stellarbill-backend/internal/timeutil"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const svcTracerName = "service/subscriptions"

// SubscriptionService defines the business logic interface for subscriptions.
type SubscriptionService interface {
	GetDetail(ctx context.Context, tenantID string, callerID string, subscriptionID string) (*SubscriptionDetail, []string, error)
	ChangeStatus(ctx context.Context, tenantID string, actorID string, subscriptionID string, targetStatus string) (*SubscriptionStatusChange, error)
	ScheduleCancel(ctx context.Context, tenantID string, callerID string, subscriptionID string, cancelAt time.Time) (*ScheduledCancellationDetail, error)
	UnscheduleCancel(ctx context.Context, tenantID string, callerID string, subscriptionID string) (*ScheduledCancellationDetail, error)
}

// subscriptionService is the concrete implementation of SubscriptionService.
type subscriptionService struct {
	subRepo  repository.SubscriptionRepository
	planRepo repository.PlanRepository
}

// NewSubscriptionService constructs a SubscriptionService with the given repositories.
func NewSubscriptionService(subRepo repository.SubscriptionRepository, planRepo repository.PlanRepository) SubscriptionService {
	return &subscriptionService{subRepo: subRepo, planRepo: planRepo}
}

// GetDetail retrieves a full SubscriptionDetail for the given subscriptionID.
// It enforces ownership (callerID must match the subscription's CustomerID),
//
// handles soft-deletes, joins plan metadata, and normalizes billing fields.
func (s *subscriptionService) GetDetail(ctx context.Context, tenantID string, callerID string, subscriptionID string) (*SubscriptionDetail, []string, error) {
	ctx, span := otel.Tracer(svcTracerName).Start(ctx, "SubscriptionService.GetDetail",
		trace.WithAttributes(
			attribute.String("subscription.id", subscriptionID),
			attribute.String("tenant.id", tenantID),
			attribute.String("caller.id", callerID),
		))
	defer span.End()

	var warnings []string

	// 1. Fetch subscription row scoped to tenant.
	row, err := s.subRepo.FindByIDAndTenant(ctx, subscriptionID, tenantID)
	if err != nil {
		if err == repository.ErrNotFound {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}

	// 2. Soft-delete check.
	if row.DeletedAt != nil {
		return nil, nil, ErrDeleted
	}

	// 3. Ownership check.
	if callerID != row.CustomerID {
		return nil, nil, ErrForbidden
	}

	// 4. Fetch plan metadata (non-fatal if missing).
	var planMeta *PlanMetadata
	planRow, err := s.planRepo.FindByID(ctx, row.PlanID)
	if err != nil {
		if err == repository.ErrNotFound {
			warnings = append(warnings, "plan not found")
		} else {
			return nil, nil, err
		}
	} else {
		planMeta = &PlanMetadata{
			PlanID:      planRow.ID,
			Name:        planRow.Name,
			Amount:      planRow.Amount,
			Currency:    planRow.Currency,
			Interval:    planRow.Interval,
			Description: planRow.Description,
		}
	}

	// 5. Parse amount to int64 cents.
	amountCents, parseErr := strconv.ParseInt(row.Amount, 10, 64)
	if parseErr != nil {
		security.ProductionLogger().Error("failed to parse amount",
			zap.String("amount", row.Amount),
			zap.String("subscription_id", row.ID),
			zap.Error(parseErr))
		return nil, nil, ErrBillingParse
	}

	// 6. Build BillingSummary.
	var nextBillingDate *string
	if row.NextBilling != "" {
		nb, err := timeutil.NormalizeRFC3339StringToUTC(row.NextBilling)
		if err != nil {
			nb = row.NextBilling
		}
		nextBillingDate = &nb
	}

	billing := BillingSummary{
		AmountCents:     amountCents,
		Currency:        strings.ToUpper(row.Currency),
		NextBillingDate: nextBillingDate,
	}

	// 7. Build SubscriptionDetail — CustomerID is mapped to Customer (safe to expose).
	detail := &SubscriptionDetail{
		ID:             row.ID,
		PlanID:         row.PlanID,
		Customer:       row.CustomerID,
		Status:         row.Status,
		Interval:       row.Interval,
		Plan:           planMeta,
		BillingSummary: billing,
	}

	// 8. Return detail and warnings.
	return detail, warnings, nil
}

// ChangeStatus validates and persists a tenant-scoped subscription status change.
// If the subscription has a scheduled cancellation (CancelAt) and now >= CancelAt,
// the cancellation is applied automatically regardless of the requested target status.
func (s *subscriptionService) ChangeStatus(ctx context.Context, tenantID string, actorID string, subscriptionID string, targetStatus string) (*SubscriptionStatusChange, error) {
	ctx, span := otel.Tracer(svcTracerName).Start(ctx, "SubscriptionService.ChangeStatus",
		trace.WithAttributes(
			attribute.String("subscription.id", subscriptionID),
			attribute.String("tenant.id", tenantID),
			attribute.String("actor.id", actorID),
			attribute.String("subscription.target_status", targetStatus),
		))
	defer span.End()

	targetStatus = strings.TrimSpace(targetStatus)

	row, err := s.subRepo.FindByIDAndTenant(ctx, subscriptionID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if row.DeletedAt != nil {
		return nil, ErrDeleted
	}

	// Fire scheduled cancellation when the window has arrived.
	if row.CancelAt != nil && !time.Now().Before(*row.CancelAt) {
		targetStatus = subscriptions.StatusCancelled
	}

	if !subscriptions.IsKnownStatus(targetStatus) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidStatus, targetStatus)
	}

	previousStatus := row.Status
	if err := subscriptions.CanTransition(previousStatus, targetStatus); err != nil {
		if !subscriptions.IsKnownStatus(previousStatus) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownCurrentState, previousStatus)
		}
		return nil, fmt.Errorf("%w: %s", ErrInvalidTransition, err.Error())
	}

	changed := previousStatus != targetStatus
	if changed {
		if err := s.subRepo.UpdateStatus(ctx, subscriptionID, tenantID, targetStatus); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, ErrNotFound
			}
			return nil, err
		}
	}

	return &SubscriptionStatusChange{
		ID:             subscriptionID,
		PreviousStatus: previousStatus,
		Status:         targetStatus,
		Changed:        changed,
	}, nil
}

// ScheduleCancel sets a future timestamp at which the subscription will be
// automatically cancelled on the next charge attempt. The caller must be the
// subscriber (CustomerID) or a merchant/admin (any non-empty actorID is accepted
// here; HTTP-layer RBAC enforces role restrictions). cancelAt must be strictly
// in the future.
func (s *subscriptionService) ScheduleCancel(ctx context.Context, tenantID string, callerID string, subscriptionID string, cancelAt time.Time) (*ScheduledCancellationDetail, error) {
	ctx, span := otel.Tracer(svcTracerName).Start(ctx, "SubscriptionService.ScheduleCancel",
		trace.WithAttributes(
			attribute.String("subscription.id", subscriptionID),
			attribute.String("tenant.id", tenantID),
			attribute.String("caller.id", callerID),
		))
	defer span.End()

	if !time.Now().Before(cancelAt) {
		return nil, ErrCancelAtPast
	}

	row, err := s.subRepo.FindByIDAndTenant(ctx, subscriptionID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if row.DeletedAt != nil {
		return nil, ErrDeleted
	}

	// Only subscriber or merchant (non-empty callerID) may schedule.
	if callerID == "" {
		return nil, ErrForbidden
	}

	if err := s.subRepo.ScheduleCancel(ctx, subscriptionID, tenantID, cancelAt); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	ts := cancelAt.UTC().Format(time.RFC3339)
	return &ScheduledCancellationDetail{
		SubscriptionID: subscriptionID,
		CancelAt:       &ts,
		ScheduledBy:    callerID,
	}, nil
}

// UnscheduleCancel clears any pending scheduled cancellation. Safe to call even
// if no cancellation was scheduled (idempotent).
func (s *subscriptionService) UnscheduleCancel(ctx context.Context, tenantID string, callerID string, subscriptionID string) (*ScheduledCancellationDetail, error) {
	ctx, span := otel.Tracer(svcTracerName).Start(ctx, "SubscriptionService.UnscheduleCancel",
		trace.WithAttributes(
			attribute.String("subscription.id", subscriptionID),
			attribute.String("tenant.id", tenantID),
			attribute.String("caller.id", callerID),
		))
	defer span.End()

	if callerID == "" {
		return nil, ErrForbidden
	}

	row, err := s.subRepo.FindByIDAndTenant(ctx, subscriptionID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if row.DeletedAt != nil {
		return nil, ErrDeleted
	}

	if err := s.subRepo.UnscheduleCancel(ctx, subscriptionID, tenantID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &ScheduledCancellationDetail{
		SubscriptionID: subscriptionID,
		CancelAt:       nil,
		ScheduledBy:    callerID,
	}, nil
}
