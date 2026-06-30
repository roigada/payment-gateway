package app

import (
	"context"

	"github.com/roigada/payment-gateway/internal/domain"
)

type paymentCommandRunner struct {
	store PaymentStore
}

type paymentCommandRun struct {
	operation          string
	key                string
	requestFingerprint string
	expectedStatus     domain.PaymentStatus
	claim              func(context.Context) (PaymentCommandClaim, error)
	handle             func(context.Context, *domain.Payment) (paymentCommandOutcome, error)
}

type paymentCommandOutcome struct {
	httpStatus       int
	returnAfterError error
}

func (r paymentCommandRunner) run(ctx context.Context, command paymentCommandRun) (PaymentCommandResult, error) {
	claim, err := command.claim(ctx)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if claim.Status == IdempotencyClaimed {
		return r.runClaimed(ctx, command, claim.Payment)
	}
	if claim.Record.RequestFingerprint != command.requestFingerprint {
		return PaymentCommandResult{}, NewPaymentIdempotencyConflictError(nil)
	}
	if claim.Status == IdempotencyInProgress {
		return PaymentCommandResult{}, NewPaymentIdempotencyInProgressError(nil)
	}
	return claim.Record.Result, nil
}

func (r paymentCommandRunner) runClaimed(ctx context.Context, command paymentCommandRun, payment *domain.Payment) (PaymentCommandResult, error) {
	outcome, err := command.handle(ctx, payment)
	if err != nil {
		_ = r.store.ReleasePaymentCommand(ctx, command.operation, command.key)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: outcome.httpStatus,
	}
	record := IdempotencyRecord{
		Operation:          command.operation,
		Key:                command.key,
		RequestFingerprint: command.requestFingerprint,
		Result:             result,
	}
	if err := r.store.CompletePaymentCommand(ctx, record, payment, command.expectedStatus); err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if outcome.returnAfterError != nil {
		return PaymentCommandResult{}, ensurePaymentError(outcome.returnAfterError)
	}
	return result, nil
}
