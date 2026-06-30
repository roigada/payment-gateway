package app

import "errors"

type PaymentErrorKind string

const (
	PaymentErrorInvalidInput          PaymentErrorKind = "invalid_input"
	PaymentErrorNotFound              PaymentErrorKind = "not_found"
	PaymentErrorIdempotencyConflict   PaymentErrorKind = "idempotency_conflict"
	PaymentErrorIdempotencyInProgress PaymentErrorKind = "idempotency_in_progress"
	PaymentErrorInvalidStatusConflict PaymentErrorKind = "invalid_status_conflict"
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

func NewPaymentInvalidStatusConflictError(cause error) error {
	return &PaymentError{
		kind:    PaymentErrorInvalidStatusConflict,
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
	var paymentErr *PaymentError
	if errors.As(err, &paymentErr) {
		return paymentErr.Kind(), true
	}
	return "", false
}

func IsPaymentErrorKind(err error, kind PaymentErrorKind) bool {
	actual, ok := PaymentErrorKindOf(err)
	return ok && actual == kind
}

func asPaymentError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := PaymentErrorKindOf(err); ok {
		return err
	}
	return NewInternalPaymentError(err)
}
