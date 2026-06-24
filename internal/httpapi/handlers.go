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
)

func (s *Server) authorizePayment(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "authorize_payment")

	if !isJSONRequest(r) {
		logGatewayErrorCode(r, errorCodeUnsupportedMediaType)
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
			logGatewayErrorCode(r, errorCodeInvalidJSONBody)
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, invalidJSONBodyMessage)
			return
		}

		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}
	addRequestLogAttrs(r, "order_id", request.OrderID, "customer_id", request.CustomerID)

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
		writePaymentServiceError(w, r, err)
		return
	}
	logPaymentResult(r, payment)

	w.Header().Set("Location", "/v1/payments/"+url.PathEscape(payment.ID))
	writeJSON(w, responseStatusOr(payment, http.StatusCreated), newPaymentEnvelope(payment))
}

func (s *Server) retryAuthorization(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "retry_authorization")
	logPaymentID(r, r.PathValue("payment_id"))

	if !isJSONRequest(r) {
		logGatewayErrorCode(r, errorCodeUnsupportedMediaType)
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
	if err := decodeJSONRequest(w, r, &request); err != nil {
		if errors.Is(err, errInvalidJSONBody) {
			logGatewayErrorCode(r, errorCodeInvalidJSONBody)
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, invalidJSONBodyMessage)
			return
		}

		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	payment, err := s.payments.RetryAuthorization(r.Context(), app.RetryAuthorizationCommand{
		PaymentID:      r.PathValue("payment_id"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Card: app.CardDetails{
			Number:      request.Card.Number,
			CVV:         request.Card.CVV,
			ExpiryMonth: request.Card.ExpiryMonth,
			ExpiryYear:  request.Card.ExpiryYear,
		},
	})
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	logPaymentResult(r, payment)

	writeJSON(w, responseStatusOr(payment, http.StatusOK), newPaymentEnvelope(payment))
}

func (s *Server) capturePayment(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "capture_payment")
	logPaymentID(r, r.PathValue("payment_id"))

	if err := requireEmptyRequestBody(r); err != nil {
		if errors.Is(err, errNonEmptyBody) {
			logGatewayErrorCode(r, errorCodeInvalidJSONBody)
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, "request body must be empty")
			return
		}

		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	payment, err := s.payments.CapturePayment(r.Context(), app.CapturePaymentCommand{
		PaymentID:      r.PathValue("payment_id"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	logPaymentResult(r, payment)

	writeJSON(w, responseStatusOr(payment, http.StatusOK), newPaymentEnvelope(payment))
}

func (s *Server) voidPayment(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "void_payment")
	logPaymentID(r, r.PathValue("payment_id"))

	if err := requireEmptyRequestBody(r); err != nil {
		if errors.Is(err, errNonEmptyBody) {
			logGatewayErrorCode(r, errorCodeInvalidJSONBody)
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, "request body must be empty")
			return
		}

		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	payment, err := s.payments.VoidPayment(r.Context(), app.VoidPaymentCommand{
		PaymentID:      r.PathValue("payment_id"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	logPaymentResult(r, payment)

	writeJSON(w, responseStatusOr(payment, http.StatusOK), newPaymentEnvelope(payment))
}

func (s *Server) refundPayment(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "refund_payment")
	logPaymentID(r, r.PathValue("payment_id"))

	if err := requireEmptyRequestBody(r); err != nil {
		if errors.Is(err, errNonEmptyBody) {
			logGatewayErrorCode(r, errorCodeInvalidJSONBody)
			writeError(w, http.StatusBadRequest, errorCodeInvalidJSONBody, "request body must be empty")
			return
		}

		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	payment, err := s.payments.RefundPayment(r.Context(), app.RefundPaymentCommand{
		PaymentID:      r.PathValue("payment_id"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	logPaymentResult(r, payment)

	writeJSON(w, responseStatusOr(payment, http.StatusOK), newPaymentEnvelope(payment))
}

func (s *Server) getPayment(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "get_payment")
	logPaymentID(r, r.PathValue("id"))

	payment, err := s.payments.GetPayment(r.Context(), app.GetPaymentQuery{
		PaymentID: r.PathValue("id"),
	})
	if err != nil {
		writePaymentServiceError(w, r, err)
		return
	}
	logPaymentResult(r, payment)

	writeJSON(w, http.StatusOK, newPaymentEnvelope(payment))
}

func (s *Server) searchPayments(w http.ResponseWriter, r *http.Request) {
	logPaymentOperation(r, "search_payments")

	query := r.URL.Query()
	for key := range query {
		switch key {
		case "order_id", "customer_id", "status":
		default:
			writePaymentServiceError(w, r, app.NewInvalidPaymentInput("unsupported payment search filter", nil))
			return
		}
	}
	addRequestLogAttrs(r, "order_id", query.Get("order_id"), "customer_id", query.Get("customer_id"), "payment_status", query.Get("status"))

	payments, err := s.payments.SearchPayments(r.Context(), app.SearchPaymentsQuery{
		OrderID:    query.Get("order_id"),
		CustomerID: query.Get("customer_id"),
		Status:     query.Get("status"),
	})
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

type paymentsEnvelope struct {
	Payments []paymentPayload `json:"payments"`
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

func responseStatusOr(payment app.PaymentResult, fallback int) int {
	if payment.ResponseStatus == 0 {
		return fallback
	}
	return payment.ResponseStatus
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
		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
		return
	}

	switch kind {
	case app.PaymentErrorInvalidInput:
		logGatewayErrorCode(r, errorCodeValidation)
		writeError(w, http.StatusUnprocessableEntity, errorCodeValidation, "payment request is invalid")
	case app.PaymentErrorIdempotencyConflict:
		logGatewayErrorCode(r, errorCodeIdempotencyConflict)
		writeError(w, http.StatusConflict, errorCodeIdempotencyConflict, "idempotency key was already used with a different request")
	case app.PaymentErrorIdempotencyInProgress:
		logGatewayErrorCode(r, errorCodeIdempotencyInProgress)
		writeError(w, http.StatusConflict, errorCodeIdempotencyInProgress, "idempotency key is already in progress")
	case app.PaymentErrorInvalidStatusConflict:
		logGatewayErrorCode(r, errorCodePaymentStatusConflict)
		writeError(w, http.StatusConflict, errorCodePaymentStatusConflict, "payment status does not allow this operation")
	case app.PaymentErrorNotFound:
		logGatewayErrorCode(r, errorCodePaymentNotFound)
		writeError(w, http.StatusNotFound, errorCodePaymentNotFound, "payment was not found")
	case app.PaymentErrorBankStateConflict:
		logGatewayErrorCode(r, errorCodeBankStateConflict)
		writeError(w, http.StatusBadGateway, errorCodeBankStateConflict, "bank state conflicts with local payment state")
	case app.PaymentErrorBankUnavailable:
		logGatewayErrorCode(r, errorCodeBankUnavailable)
		writeError(w, http.StatusBadGateway, errorCodeBankUnavailable, "bank is unavailable")
	case app.PaymentErrorBankTimeout:
		logGatewayErrorCode(r, errorCodeBankTimeout)
		writeError(w, http.StatusGatewayTimeout, errorCodeBankTimeout, "bank request timed out")
	default:
		logGatewayErrorCode(r, errorCodeInternalServer)
		writeError(w, http.StatusInternalServerError, errorCodeInternalServer, "internal server error")
	}
}
