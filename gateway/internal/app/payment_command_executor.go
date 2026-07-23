package app

import (
	"context"
	"errors"

	"github.com/roigada/payment-gateway/internal/domain"
)

type paymentCommandExecutor struct {
	store   PaymentStore
	metrics PaymentOperationMetrics
	clock   Clock
}

type paymentCommandExecution struct {
	result   PaymentCommandResult
	replayed bool
}

type paymentCommandBehavior func(context.Context, *domain.Payment) (PaymentCommandResult, error)

type completedPaymentCommandError struct {
	result PaymentCommandResult
	cause  error
}

func newCompletedPaymentCommandError(result PaymentCommandResult, cause error) error {
	return &completedPaymentCommandError{result: result, cause: cause}
}

func (e *completedPaymentCommandError) Error() string { return e.cause.Error() }
func (e *completedPaymentCommandError) Unwrap() error { return e.cause }

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
	if claim.Recovered() {
		e.metrics.RecordIdempotencyRecovery(claim.Operation(), IdempotencyRecoveryAttempted)
	}
	if replayed, ok := claim.ReplayResult(); ok {
		return paymentCommandExecution{result: replayed, replayed: true}, nil
	}

	result, err := behavior(ctx, claim.Payment())
	if err != nil {
		var completedErr *completedPaymentCommandError
		if errors.As(err, &completedErr) {
			if err := e.store.CompletePaymentCommand(ctx, claim, completedErr.result, e.clock.Now()); err != nil {
				return paymentCommandExecution{}, ensurePaymentError(err)
			}
			if claim.Recovered() {
				e.metrics.RecordIdempotencyRecovery(claim.Operation(), IdempotencyRecoveryRecovered)
			}
			return paymentCommandExecution{}, ensurePaymentError(completedErr.cause)
		}
		_ = e.store.ReleasePaymentCommand(ctx, claim)
		return paymentCommandExecution{}, ensurePaymentError(err)
	}
	if err := e.store.CompletePaymentCommand(ctx, claim, result, e.clock.Now()); err != nil {
		return paymentCommandExecution{}, ensurePaymentError(err)
	}
	if claim.Recovered() {
		e.metrics.RecordIdempotencyRecovery(claim.Operation(), IdempotencyRecoveryRecovered)
	}
	return paymentCommandExecution{result: result}, nil
}

func (e paymentCommandExecutor) recordRecoveryError(operation string, err error) {
	var recoveryErr *IdempotencyRecoveryError
	if errors.As(err, &recoveryErr) {
		e.metrics.RecordIdempotencyRecovery(operation, IdempotencyRecoveryAttempted)
		e.metrics.RecordIdempotencyRecovery(operation, recoveryErr.Result())
	}
}
