package app

import "errors"

type PaymentErrorKind string

const (
	PaymentErrorInvalidInput          PaymentErrorKind = "invalid_input"
	PaymentErrorNotFound              PaymentErrorKind = "not_found"
	PaymentErrorIdempotencyConflict   PaymentErrorKind = "idempotency_conflict"
	PaymentErrorIdempotencyInProgress PaymentErrorKind = "idempotency_in_progress"
	PaymentErrorPaymentStatusConflict PaymentErrorKind = "payment_status_conflict"
	PaymentErrorAuthorizationExpired  PaymentErrorKind = "authorization_expired"
	PaymentErrorBankStateConflict     PaymentErrorKind = "bank_state_conflict"
	PaymentErrorBankUnavailable       PaymentErrorKind = "bank_unavailable"
	PaymentErrorBankTimeout           PaymentErrorKind = "bank_timeout"
	PaymentErrorInternal              PaymentErrorKind = "internal"
)

type PaymentError struct {
	kind    PaymentErrorKind
	message string
	cause   error
}

// IdempotencyRecoveryError preserves the payment error produced while
// recovering a stuck idempotency claim, along with its recovery outcome for
// operational metrics.
type IdempotencyRecoveryError struct {
	result string
	cause  error
}

func NewIdempotencyRecoveryError(result string, cause error) error {
	return &IdempotencyRecoveryError{result: result, cause: cause}
}

func (e *IdempotencyRecoveryError) Result() string { return e.result }
func (e *IdempotencyRecoveryError) Error() string  { return e.cause.Error() }
func (e *IdempotencyRecoveryError) Unwrap() error  { return e.cause }

func NewInvalidPaymentInputError(reason string, cause error) error {
	return &PaymentError{
		kind:    PaymentErrorInvalidInput,
		message: reason,
		cause:   cause,
	}
}

func NewPaymentNotFoundError(id string, cause error) error {
	return &PaymentError{
		kind:    PaymentErrorNotFound,
		message: "payment " + id + " was not found",
		cause:   cause,
	}
}

func NewPaymentIdempotencyConflictError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorIdempotencyConflict,
		message: "idempotency key was already used with a different request",
		cause:   cause,
	}
}

func NewPaymentIdempotencyInProgressError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorIdempotencyInProgress,
		message: "idempotency key is already in progress",
		cause:   cause,
	}
}

func NewPaymentStatusConflictError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorPaymentStatusConflict,
		message: "payment status does not allow this operation",
		cause:   cause,
	}
}

func NewPaymentAuthorizationExpiredError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorAuthorizationExpired,
		message: "authorization expired",
		cause:   cause,
	}
}

func NewPaymentBankStateConflictError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorBankStateConflict,
		message: "bank state conflicts with local payment state",
		cause:   cause,
	}
}

func NewPaymentBankUnavailableError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorBankUnavailable,
		message: "bank is unavailable",
		cause:   cause,
	}
}

func NewPaymentBankTimeoutError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorBankTimeout,
		message: "bank request timed out",
		cause:   cause,
	}
}

func NewInternalPaymentError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorInternal,
		message: "internal server error",
		cause:   cause,
	}
}

func (e *PaymentError) Kind() PaymentErrorKind {
	return e.kind
}

func (e *PaymentError) Error() string {
	return e.message
}

func (e *PaymentError) Unwrap() error {
	return e.cause
}

func PaymentErrorKindOf(err error) (PaymentErrorKind, bool) {
	if paymentErr, ok := errors.AsType[*PaymentError](err); ok {
		return paymentErr.Kind(), true
	}
	return "", false
}

func HasPaymentErrorKind(err error, kind PaymentErrorKind) bool {
	actual, ok := PaymentErrorKindOf(err)
	return ok && actual == kind
}

// NewPaymentErrorOfKind builds the payment error a kind stands for. Kinds whose
// message carries request data — invalid input and not found — cannot be
// rebuilt from the kind alone and are not produced here; call their
// constructors directly. Every other kind maps to its constructor, so a caller
// holding only a kind produces the identical error, message and all.
func NewPaymentErrorOfKind(kind PaymentErrorKind, cause error) error {
	switch kind {
	case PaymentErrorIdempotencyConflict:
		return NewPaymentIdempotencyConflictError(cause)
	case PaymentErrorIdempotencyInProgress:
		return NewPaymentIdempotencyInProgressError(cause)
	case PaymentErrorPaymentStatusConflict:
		return NewPaymentStatusConflictError(cause)
	case PaymentErrorAuthorizationExpired:
		return NewPaymentAuthorizationExpiredError(cause)
	case PaymentErrorBankStateConflict:
		return NewPaymentBankStateConflictError(cause)
	case PaymentErrorBankUnavailable:
		return NewPaymentBankUnavailableError(cause)
	case PaymentErrorBankTimeout:
		return NewPaymentBankTimeoutError(cause)
	case PaymentErrorInternal:
		return NewInternalPaymentError(cause)
	default:
		return NewInternalPaymentError(errors.New("payment error kind " + string(kind) + " cannot be rebuilt from its kind"))
	}
}

// IsTerminalFailureKind reports whether a kind may be stored as the failure of
// a completed payment command. A kind qualifies only when its command persisted
// a Payment status transition; transient kinds must release the claim instead,
// so the same idempotency key stays retryable.
func IsTerminalFailureKind(kind PaymentErrorKind) bool {
	switch kind {
	case PaymentErrorAuthorizationExpired:
		return true
	default:
		return false
	}
}

func ensurePaymentError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := PaymentErrorKindOf(err); ok {
		return err
	}
	return NewInternalPaymentError(err)
}
