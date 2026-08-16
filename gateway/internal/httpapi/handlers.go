package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	errorCodeInternalServer        = "internal_server_error"
	errorCodeInvalidJSONBody       = "invalid_json_body"
	errorCodeUnsupportedMediaType  = "unsupported_media_type"
	errorCodeServiceUnavailable    = "service_unavailable"
	errorCodeValidation            = "validation_error"
	errorCodeIdempotencyConflict   = "idempotency_key_conflict"
	errorCodeIdempotencyInProgress = "idempotency_key_in_progress"
	errorCodePaymentStatusConflict = "payment_status_conflict"
	errorCodePaymentNotFound       = "payment_not_found"
	errorCodeBankStateConflict     = "bank_state_conflict"
	errorCodeBankUnavailable       = "bank_unavailable"
	errorCodeBankTimeout           = "bank_timeout"
	errorCodePaymentTimeout        = "payment_timeout"
	errorCodeRequestTimeout        = "request_timeout"
)

func (s *Handler) authorizePayment(w http.ResponseWriter, r *http.Request) {
	r, cancel := s.commandRequest(r)
	defer cancel()

	if !isJSONRequest(r) {
		writeError(w, http.StatusUnsupportedMediaType, errorCodeUnsupportedMediaType, "content type must be application/json")
		return
	}

	var request struct {
		OrderID     string `json:"order_id"`
		CustomerID  string `json:"customer_id"`
		AmountCents int64  `json:"amount"`
		Card        struct {
			Number      string `json:"number"`
			CVV         string `json:"cvv"`
			ExpiryMonth int    `json:"expiry_month"`
			ExpiryYear  int    `json:"expiry_year"`
		} `json:"card"`
	}
	if err := decodeJSONRequest(w, r, &request, s.config.MaxRequestBodyBytes); err != nil {
		if errors.Is(err, errOversizedJSONBody) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if errors.Is(err, errInvalidJSONBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, invalidJSONBodyMessage)
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	command, err := app.NewAuthorizePaymentCommand(
		request.OrderID,
		request.CustomerID,
		request.AmountCents,
		request.Card.Number,
		request.Card.CVV,
		request.Card.ExpiryMonth,
		request.Card.ExpiryYear,
		r.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	result, err := s.payments.AuthorizePayment(r.Context(), command)
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/v1/payments/"+url.PathEscape(result.Payment.ID))
	writeJSON(w, result.HTTPStatus, newPaymentEnvelope(result.Payment))
}

func (s *Handler) retryAuthorization(w http.ResponseWriter, r *http.Request) {
	r, cancel := s.commandRequest(r)
	defer cancel()

	if !isJSONRequest(r) {
		writeError(w, http.StatusUnsupportedMediaType, errorCodeUnsupportedMediaType, "content type must be application/json")
		return
	}

	var request struct {
		Card struct {
			Number      string `json:"number"`
			CVV         string `json:"cvv"`
			ExpiryMonth int    `json:"expiry_month"`
			ExpiryYear  int    `json:"expiry_year"`
		} `json:"card"`
	}
	if err := decodeJSONRequest(w, r, &request, s.config.MaxRequestBodyBytes); err != nil {
		if errors.Is(err, errOversizedJSONBody) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if errors.Is(err, errInvalidJSONBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, invalidJSONBodyMessage)
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	command, err := app.NewRetryAuthorizationCommand(
		r.PathValue("payment_id"),
		request.Card.Number,
		request.Card.CVV,
		request.Card.ExpiryMonth,
		request.Card.ExpiryYear,
		r.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	result, err := s.payments.RetryAuthorization(r.Context(), command)
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	writeJSON(w, result.HTTPStatus, newPaymentEnvelope(result.Payment))
}

func (s *Handler) capturePayment(w http.ResponseWriter, r *http.Request) {
	r, cancel := s.commandRequest(r)
	defer cancel()

	if err := requireEmptyRequestBody(r); err != nil {
		if errors.Is(err, errOversizedJSONBody) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if errors.Is(err, errNonEmptyBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, "request body must be empty")
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	command, err := app.NewCapturePaymentCommand(r.PathValue("payment_id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	result, err := s.payments.CapturePayment(r.Context(), command)
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	writeJSON(w, result.HTTPStatus, newPaymentEnvelope(result.Payment))
}

func (s *Handler) voidPayment(w http.ResponseWriter, r *http.Request) {
	r, cancel := s.commandRequest(r)
	defer cancel()

	if err := requireEmptyRequestBody(r); err != nil {
		if errors.Is(err, errOversizedJSONBody) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if errors.Is(err, errNonEmptyBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, "request body must be empty")
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	command, err := app.NewVoidPaymentCommand(r.PathValue("payment_id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	result, err := s.payments.VoidPayment(r.Context(), command)
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	writeJSON(w, result.HTTPStatus, newPaymentEnvelope(result.Payment))
}

func (s *Handler) refundPayment(w http.ResponseWriter, r *http.Request) {
	r, cancel := s.commandRequest(r)
	defer cancel()

	if err := requireEmptyRequestBody(r); err != nil {
		if errors.Is(err, errOversizedJSONBody) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if errors.Is(err, errNonEmptyBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, "request body must be empty")
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	command, err := app.NewRefundPaymentCommand(r.PathValue("payment_id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	result, err := s.payments.RefundPayment(r.Context(), command)
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodePaymentTimeout, "payment command timed out; retry with the same idempotency key")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	writeJSON(w, result.HTTPStatus, newPaymentEnvelope(result.Payment))
}

func (s *Handler) getPayment(w http.ResponseWriter, r *http.Request) {
	query, err := app.NewGetPaymentQuery(r.PathValue("payment_id"))
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.config.PaymentReadTimeout)
	defer cancel()
	payment, err := s.payments.GetPayment(ctx, query)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodeRequestTimeout, "payment read timed out")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, newPaymentEnvelope(payment))
}

func (s *Handler) searchPayments(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key := range query {
		switch key {
		case "order_id", "customer_id", "status":
		default:
			writePaymentServiceError(w, r, app.NewInvalidPaymentInputError("unsupported payment search filter", nil))
			return
		}
	}

	searchQuery, err := app.NewSearchPaymentsQuery(query.Get("order_id"), query.Get("customer_id"), query.Get("status"))
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.config.PaymentReadTimeout)
	defer cancel()
	payments, err := s.payments.SearchPayments(ctx, searchQuery)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, errorCodeRequestTimeout, "payment read timed out")
		return
	}
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, newPaymentsEnvelope(payments))
}

func isJSONRequest(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		return false
	}
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

type paymentPayload struct {
	ID                     string `json:"id"`
	OrderID                string `json:"order_id"`
	CustomerID             string `json:"customer_id"`
	AmountCents            int64  `json:"amount"`
	Currency               string `json:"currency"`
	Status                 string `json:"status"`
	DeclineReason          string `json:"decline_reason,omitempty"`
	AuthorizationExpiresAt string `json:"authorization_expires_at,omitempty"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type paymentEnvelope struct {
	Payment paymentPayload `json:"payment"`
}

type paymentsEnvelope struct {
	Payments []paymentPayload `json:"payments"`
}

func newPaymentEnvelope(payment app.PaymentResult) paymentEnvelope {
	authorizationExpiresAt := ""
	if !payment.AuthorizationExpiresAt.IsZero() {
		authorizationExpiresAt = payment.AuthorizationExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return paymentEnvelope{
		Payment: paymentPayload{
			ID:                     payment.ID,
			OrderID:                payment.OrderID,
			CustomerID:             payment.CustomerID,
			AmountCents:            payment.AmountCents,
			Currency:               payment.Currency,
			Status:                 payment.Status,
			DeclineReason:          payment.DeclineReason,
			AuthorizationExpiresAt: authorizationExpiresAt,
			CreatedAt:              payment.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:              payment.UpdatedAt.UTC().Format(time.RFC3339Nano),
		},
	}
}

func newPaymentsEnvelope(payments []app.PaymentResult) paymentsEnvelope {
	payloads := make([]paymentPayload, 0, len(payments))
	for _, payment := range payments {
		payloads = append(payloads, newPaymentEnvelope(payment).Payment)
	}
	return paymentsEnvelope{Payments: payloads}
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorPayload{
			Code:    code,
			Message: message,
		},
	})
}

func writePaymentServiceError(w http.ResponseWriter, r *http.Request, err error) {
	kind, ok := app.PaymentErrorKindOf(err)
	if !ok {
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	switch kind {
	case app.PaymentErrorInvalidInput:
		writeError(w, http.StatusUnprocessableEntity, errorCodeValidation, "payment request is invalid")
	case app.PaymentErrorIdempotencyConflict:
		writeError(w, http.StatusConflict, errorCodeIdempotencyConflict, "idempotency key was already used with a different request")
	case app.PaymentErrorIdempotencyInProgress:
		writeError(w, http.StatusConflict, errorCodeIdempotencyInProgress, "idempotency key is already in progress")
	case app.PaymentErrorPaymentStatusConflict:
		writeError(w, http.StatusConflict, errorCodePaymentStatusConflict, "payment status does not allow this operation")
	case app.PaymentErrorAuthorizationExpired:
		writeError(w, http.StatusConflict, errorCodePaymentStatusConflict, "payment status does not allow this operation")
	case app.PaymentErrorNotFound:
		writeError(w, http.StatusNotFound, errorCodePaymentNotFound, "payment was not found")
	case app.PaymentErrorBankStateConflict:
		writeError(w, http.StatusBadGateway, errorCodeBankStateConflict, "bank state conflicts with local payment state")
	case app.PaymentErrorBankUnavailable:
		writeError(w, http.StatusBadGateway, errorCodeBankUnavailable, "bank is unavailable")
	case app.PaymentErrorBankTimeout:
		writeError(w, http.StatusGatewayTimeout, errorCodeBankTimeout, "bank request timed out")
	default:
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, "internal server error")
	}
}
