package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const (
	errorCodeInternalServer       = "internal_server_error"
	errorCodeInvalidJSONBody      = "invalid_json_body"
	errorCodeUnsupportedMediaType = "unsupported_media_type"
	errorCodeServiceUnavailable   = "service_unavailable"
	errorCodeValidation           = "validation_error"
	errorCodeIdempotencyConflict  = "idempotency_key_conflict"
	errorCodePaymentNotFound      = "payment_not_found"
	errorCodeBankUnavailable      = "bank_unavailable"
	errorCodeBankTimeout          = "bank_timeout"
)

func (s *Server) authorizePayment(w http.ResponseWriter, r *http.Request) {
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
	if err := decodeJSONRequest(w, r, &request); err != nil {
		if errors.Is(err, errInvalidJSONBody) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, invalidJSONBodyMessage)
			return
		}

		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	payment, err := s.payments.AuthorizePayment(r.Context(), app.AuthorizePaymentCommand{
		OrderID:        request.OrderID,
		CustomerID:     request.CustomerID,
		AmountCents:    request.AmountCents,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Card: app.CardDetails{
			Number:      request.Card.Number,
			CVV:         request.Card.CVV,
			ExpiryMonth: request.Card.ExpiryMonth,
			ExpiryYear:  request.Card.ExpiryYear,
		},
	})
	if err != nil {
		writePaymentServiceError(w, err)
		return
	}

	w.Header().Set("Location", "/v1/payments/"+url.PathEscape(payment.ID))
	writeJSON(w, http.StatusCreated, newPaymentEnvelope(payment))
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
	ID            string `json:"id"`
	OrderID       string `json:"order_id"`
	CustomerID    string `json:"customer_id"`
	AmountCents   int64  `json:"amount"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
	DeclineReason string `json:"decline_reason,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type paymentEnvelope struct {
	Payment paymentPayload `json:"payment"`
}

func newPaymentEnvelope(payment app.PaymentResult) paymentEnvelope {
	return paymentEnvelope{
		Payment: paymentPayload{
			ID:            payment.ID,
			OrderID:       payment.OrderID,
			CustomerID:    payment.CustomerID,
			AmountCents:   payment.AmountCents,
			Currency:      payment.Currency,
			Status:        payment.Status,
			DeclineReason: payment.DeclineReason,
			CreatedAt:     payment.CreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt:     payment.UpdatedAt.UTC().Format(time.RFC3339Nano),
		},
	}
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

func writePaymentServiceError(w http.ResponseWriter, err error) {
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
	case app.PaymentErrorNotFound:
		writeError(w, http.StatusNotFound, errorCodePaymentNotFound, "payment was not found")
	case app.PaymentErrorBankUnavailable:
		writeError(w, http.StatusBadGateway, errorCodeBankUnavailable, "bank is unavailable")
	case app.PaymentErrorBankTimeout:
		writeError(w, http.StatusGatewayTimeout, errorCodeBankTimeout, "bank request timed out")
	default:
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, "internal server error")
	}
}
