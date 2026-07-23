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

	result, err := behavior(ctx, claim.Payment())
	if err != nil {
		if request.CompletesAuthorizationExpiration() && HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			return e.completeAuthorizationExpiration(ctx, claim)
		}
		_ = e.store.ReleasePaymentCommand(ctx, claim)
		return paymentCommandExecution{}, ensurePaymentError(err)
	}

	if err := e.store.CompletePaymentCommand(ctx, claim, result, e.clock.Now()); err != nil {
		return paymentCommandExecution{}, ensurePaymentError(err)
	}
	e.recordRecoveryCompleted(claim)

	return paymentCommandExecution{result: result}, nil
}

func (e paymentCommandExecutor) completeAuthorizationExpiration(
	ctx context.Context,
	claim PaymentCommandClaim,
) (paymentCommandExecution, error) {
	if err := claim.Payment().MarkExpired(e.clock.Now()); err != nil {
		_ = e.store.ReleasePaymentCommand(ctx, claim)
		return paymentCommandExecution{}, ensurePaymentError(err)
	}
	result := PaymentCommandResult{
		Payment:    newPaymentResult(claim.Payment()),
		HTTPStatus: 409,
	}
	if err := e.store.CompletePaymentCommand(ctx, claim, result, e.clock.Now()); err != nil {
		return paymentCommandExecution{}, ensurePaymentError(err)
	}
	e.recordRecoveryCompleted(claim)
	return paymentCommandExecution{}, NewPaymentAuthorizationExpiredError(nil)
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
