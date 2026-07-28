package app

import (
	"context"
	"errors"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
)

type paymentCommandExecutor struct {
	store   paymentCommandStore
	metrics idempotencyRecoveryRecorder
	clock   Clock
}

type paymentCommandStore interface {
	ClaimPaymentCommand(ctx context.Context, request PaymentCommandClaimRequest) (PaymentCommandClaim, error)
	CompletePaymentCommand(ctx context.Context, claim PaymentCommandClaim, result PaymentCommandResult, completedAt time.Time) error
	ReleasePaymentCommand(ctx context.Context, claim PaymentCommandClaim) error
}

type idempotencyRecoveryRecorder interface {
	RecordIdempotencyRecovery(operation string, result string)
}

type paymentCommandExecution struct {
	result   PaymentCommandResult
	replayed bool
}

type claimDispositionKind int

const (
	_ claimDispositionKind = iota
	completeClaimDisposition
	releaseClaimDisposition
	preserveClaimDisposition
)

type claimDisposition struct {
	kind   claimDispositionKind
	result PaymentCommandResult
	err    error
}

func completeClaim(result PaymentCommandResult, err error) claimDisposition {
	return claimDisposition{
		kind:   completeClaimDisposition,
		result: result,
		err:    err,
	}
}

func releaseClaim(err error) claimDisposition {
	return claimDisposition{kind: releaseClaimDisposition, err: err}
}

func preserveClaim(err error) claimDisposition {
	return claimDisposition{kind: preserveClaimDisposition, err: err}
}

type paymentCommandBehavior func(context.Context, *domain.Payment) claimDisposition

func (e paymentCommandExecutor) execute(
	ctx context.Context,
	request PaymentCommandClaimRequest,
	behavior paymentCommandBehavior,
) (paymentCommandExecution, error) {
	claim, err := e.store.ClaimPaymentCommand(ctx, request)
	if err != nil {
		e.recordRecoveryError(request.Operation(), err)
		return paymentCommandExecution{}, ensurePaymentError(err)
	}
	e.recordRecoveryAttempt(claim)

	if replayed, ok := claim.ReplayResult(); ok {
		return paymentCommandExecution{result: replayed, replayed: true}, nil
	}

	disposition := behavior(ctx, claim.Payment())
	switch disposition.kind {
	case completeClaimDisposition:
		if err := e.store.CompletePaymentCommand(ctx, claim, disposition.result, e.clock.Now()); err != nil {
			return paymentCommandExecution{}, ensurePaymentError(err)
		}
		e.recordRecoveryCompleted(claim)
		if disposition.err != nil {
			return paymentCommandExecution{}, ensurePaymentError(disposition.err)
		}
		return paymentCommandExecution{result: disposition.result}, nil
	case releaseClaimDisposition:
		if disposition.err == nil {
			return paymentCommandExecution{}, NewInternalPaymentError(errors.New("release claim disposition requires an error"))
		}
		_ = e.store.ReleasePaymentCommand(ctx, claim)
		return paymentCommandExecution{}, ensurePaymentError(disposition.err)
	case preserveClaimDisposition:
		if disposition.err == nil {
			return paymentCommandExecution{}, NewInternalPaymentError(errors.New("preserve claim disposition requires an error"))
		}
		return paymentCommandExecution{}, ensurePaymentError(disposition.err)
	default:
		return paymentCommandExecution{}, NewInternalPaymentError(errors.New("payment command behavior returned an invalid claim disposition"))
	}
}

func (e paymentCommandExecutor) recordRecoveryAttempt(claim PaymentCommandClaim) {
	if claim.Recovered() {
		e.metrics.RecordIdempotencyRecovery(claim.Operation(), IdempotencyRecoveryAttempted)
	}
}

func (e paymentCommandExecutor) recordRecoveryCompleted(claim PaymentCommandClaim) {
	if claim.Recovered() {
		e.metrics.RecordIdempotencyRecovery(claim.Operation(), IdempotencyRecoveryRecovered)
	}
}

func (e paymentCommandExecutor) recordRecoveryError(operation string, err error) {
	var recoveryErr *IdempotencyRecoveryError
	if errors.As(err, &recoveryErr) {
		e.metrics.RecordIdempotencyRecovery(operation, IdempotencyRecoveryAttempted)
		e.metrics.RecordIdempotencyRecovery(operation, recoveryErr.Result())
	}
}
