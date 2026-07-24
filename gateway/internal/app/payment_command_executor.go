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

type paymentCommandBehaviorOutcomeKind int

const (
	paymentCommandSuccessfulCompletion paymentCommandBehaviorOutcomeKind = iota
	paymentCommandReleasableFailure
	paymentCommandDefinitiveExceptionalCompletion
	paymentCommandDefinitiveFailure
)

type paymentCommandBehaviorOutcome struct {
	kind   paymentCommandBehaviorOutcomeKind
	result PaymentCommandResult
	err    error
}

func completedPaymentCommand(result PaymentCommandResult) paymentCommandBehaviorOutcome {
	return paymentCommandBehaviorOutcome{
		kind:   paymentCommandSuccessfulCompletion,
		result: result,
	}
}

func releasablePaymentCommandFailure(err error) paymentCommandBehaviorOutcome {
	return paymentCommandBehaviorOutcome{kind: paymentCommandReleasableFailure, err: err}
}

func definitivePaymentCommandFailure(err error) paymentCommandBehaviorOutcome {
	return paymentCommandBehaviorOutcome{kind: paymentCommandDefinitiveFailure, err: err}
}

func definitivePaymentCommandCompletion(result PaymentCommandResult, err error) paymentCommandBehaviorOutcome {
	return paymentCommandBehaviorOutcome{
		kind:   paymentCommandDefinitiveExceptionalCompletion,
		result: result,
		err:    err,
	}
}

type paymentCommandBehavior func(context.Context, *domain.Payment) paymentCommandBehaviorOutcome

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

	outcome := behavior(ctx, claim.Payment())
	if outcome.kind == paymentCommandReleasableFailure {
		_ = e.store.ReleasePaymentCommand(ctx, claim)
		return paymentCommandExecution{}, ensurePaymentError(outcome.err)
	}

	if outcome.kind == paymentCommandSuccessfulCompletion || outcome.kind == paymentCommandDefinitiveExceptionalCompletion {
		if err := e.store.CompletePaymentCommand(ctx, claim, outcome.result, e.clock.Now()); err != nil {
			return paymentCommandExecution{}, ensurePaymentError(err)
		}
		e.recordRecoveryCompleted(claim)
	}

	if outcome.kind == paymentCommandDefinitiveExceptionalCompletion || outcome.kind == paymentCommandDefinitiveFailure {
		return paymentCommandExecution{}, ensurePaymentError(outcome.err)
	}

	return paymentCommandExecution{result: outcome.result}, nil
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
