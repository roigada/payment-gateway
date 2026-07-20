package mockbank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	metrics    metrics
	config     ClientConfig
}

// ClientConfig contains all Mock Bank call budgets. A zero value disables
// deadlines and automatic retries, which is useful for isolated adapter tests.
type ClientConfig struct {
	Timeout               time.Duration
	InitialAttemptTimeout time.Duration
	RetryDelay            time.Duration
	RetryAttemptTimeout   time.Duration
}

func NewClient(baseURL string, httpClient *http.Client, metrics metrics, config ClientConfig) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("mock bank base URL must be absolute")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: parsed, httpClient: httpClient, metrics: metrics, config: config}, nil
}

func requestContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

type metrics interface {
	RecordMockBankRequest(operation string, result string, duration time.Duration)
	RecordMockBankRetry(operation string, result string)
}

func (c *Client) AuthorizePayment(ctx context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	return retryTransient(c, ctx, "authorize", func(ctx context.Context, timeout time.Duration) (app.BankAuthorizationResult, error) {
		return c.authorizePaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) initialAttemptTimeout() time.Duration {
	if c.config.InitialAttemptTimeout > 0 {
		return c.config.InitialAttemptTimeout
	}
	return c.config.Timeout
}

func (c *Client) authorizePaymentAttempt(ctx context.Context, request app.BankAuthorizationRequest, timeout time.Duration) (app.BankAuthorizationResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := "internal"
	defer func() {
		c.recordRequest("authorize", result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(authorizationRequest{
		CardNumber:  request.CardNumber,
		CVV:         request.CardCVV,
		ExpiryMonth: request.CardExpiryMonth,
		ExpiryYear:  request.CardExpiryYear,
		AmountCents: request.AmountCents,
	}); err != nil {
		return app.BankAuthorizationResult{}, app.NewInternalPaymentError(err)
	}

	endpoint := c.baseURL.JoinPath("/api/v1/authorizations")
	httpRequest, err := newAuthorizationHTTPRequest(ctx, endpoint.String(), &body, request.OperationKey)
	if err != nil {
		return app.BankAuthorizationResult{}, app.NewInternalPaymentError(err)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = "timeout"
			return app.BankAuthorizationResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = "unavailable"
		return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload authorizationResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = "unavailable"
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.AuthorizationID) == "" {
			result = "unavailable"
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank authorization response missing authorization id"))
		}
		if payload.ExpiresAt.IsZero() {
			result = "unavailable"
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank authorization response missing expires_at"))
		}

		result = "success"
		return app.BankAuthorizationResult{BankAuthorizationID: payload.AuthorizationID, AuthorizationExpiresAt: payload.ExpiresAt}, nil
	case http.StatusBadRequest:
		reason, err := decodeBadRequestInvalidInputReason(response)
		if err != nil {
			result = "unavailable"
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if reason != "" {
			result = "invalid_input"
			return app.BankAuthorizationResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	case http.StatusPaymentRequired:
		result = "declined"
		return app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}, nil
	}

	result = "unavailable"
	return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank authorization failed: status %d", response.StatusCode))
}

func (c *Client) CapturePayment(ctx context.Context, request app.BankCaptureRequest) (app.BankCaptureResult, error) {
	return retryTransient(c, ctx, "capture", func(ctx context.Context, timeout time.Duration) (app.BankCaptureResult, error) {
		return c.capturePaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) capturePaymentAttempt(ctx context.Context, request app.BankCaptureRequest, timeout time.Duration) (app.BankCaptureResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := "internal"
	defer func() {
		c.recordRequest("capture", result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(captureRequest{
		AuthorizationID: request.BankAuthorizationID,
		AmountCents:     request.AmountCents,
	}); err != nil {
		return app.BankCaptureResult{}, app.NewInternalPaymentError(err)
	}

	endpoint := c.baseURL.JoinPath("/api/v1/captures")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankCaptureResult{}, app.NewInternalPaymentError(err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = "timeout"
			return app.BankCaptureResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = "unavailable"
		return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload captureResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = "unavailable"
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.CaptureID) == "" {
			result = "unavailable"
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank capture response missing capture id"))
		}

		result = "success"
		return app.BankCaptureResult{BankCaptureID: payload.CaptureID}, nil
	case http.StatusBadRequest:
		reason, conflict, expired, err := decodeBadRequestReason(response)
		if err != nil {
			result = "unavailable"
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if expired {
			result = "expired"
			return app.BankCaptureResult{}, app.NewPaymentAuthorizationExpiredError(nil)
		}
		if conflict {
			result = "state_conflict"
			return app.BankCaptureResult{}, app.NewPaymentBankStateConflictError(nil)
		}
		if reason != "" {
			result = "invalid_input"
			return app.BankCaptureResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	}

	result = "unavailable"
	return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank capture failed: status %d", response.StatusCode))
}

func (c *Client) VoidPayment(ctx context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	return retryTransient(c, ctx, "void", func(ctx context.Context, timeout time.Duration) (app.BankVoidResult, error) {
		return c.voidPaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) voidPaymentAttempt(ctx context.Context, request app.BankVoidRequest, timeout time.Duration) (app.BankVoidResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := "internal"
	defer func() {
		c.recordRequest("void", result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(voidRequest{
		AuthorizationID: request.BankAuthorizationID,
	}); err != nil {
		return app.BankVoidResult{}, app.NewInternalPaymentError(err)
	}

	endpoint := c.baseURL.JoinPath("/api/v1/voids")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankVoidResult{}, app.NewInternalPaymentError(err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = "timeout"
			return app.BankVoidResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = "unavailable"
		return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload voidResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = "unavailable"
			return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.VoidID) == "" {
			result = "unavailable"
			return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank void response missing void id"))
		}

		result = "success"
		return app.BankVoidResult{BankVoidID: payload.VoidID}, nil
	case http.StatusBadRequest:
		reason, conflict, expired, err := decodeBadRequestReason(response)
		if err != nil {
			result = "unavailable"
			return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if expired {
			result = "expired"
			return app.BankVoidResult{}, app.NewPaymentAuthorizationExpiredError(nil)
		}
		if conflict {
			result = "state_conflict"
			return app.BankVoidResult{}, app.NewPaymentBankStateConflictError(nil)
		}
		if reason != "" {
			result = "invalid_input"
			return app.BankVoidResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	}

	result = "unavailable"
	return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank void failed: status %d", response.StatusCode))
}

func (c *Client) RefundPayment(ctx context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	return retryTransient(c, ctx, "refund", func(ctx context.Context, timeout time.Duration) (app.BankRefundResult, error) {
		return c.refundPaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) refundPaymentAttempt(ctx context.Context, request app.BankRefundRequest, timeout time.Duration) (app.BankRefundResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := "internal"
	defer func() {
		c.recordRequest("refund", result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(refundRequest{
		CaptureID:   request.BankCaptureID,
		AmountCents: request.AmountCents,
	}); err != nil {
		return app.BankRefundResult{}, app.NewInternalPaymentError(err)
	}

	endpoint := c.baseURL.JoinPath("/api/v1/refunds")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankRefundResult{}, app.NewInternalPaymentError(err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = "timeout"
			return app.BankRefundResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = "unavailable"
		return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload refundResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = "unavailable"
			return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.RefundID) == "" {
			result = "unavailable"
			return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank refund response missing refund id"))
		}

		result = "success"
		return app.BankRefundResult{BankRefundID: payload.RefundID}, nil
	case http.StatusBadRequest:
		reason, conflict, _, err := decodeBadRequestReason(response)
		if err != nil {
			result = "unavailable"
			return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if conflict {
			result = "state_conflict"
			return app.BankRefundResult{}, app.NewPaymentBankStateConflictError(nil)
		}
		if reason != "" {
			result = "invalid_input"
			return app.BankRefundResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	}

	result = "unavailable"
	return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank refund failed: status %d", response.StatusCode))
}

func (c *Client) recordRequest(operation string, result string, duration time.Duration) {
	if c.metrics == nil {
		return
	}
	c.metrics.RecordMockBankRequest(operation, result, duration)
}

func (c *Client) recordRetry(operation string, result string) {
	if c.metrics == nil {
		return
	}
	c.metrics.RecordMockBankRetry(operation, result)
}

func retryTransient[T any](c *Client, ctx context.Context, operation string, attempt func(context.Context, time.Duration) (T, error)) (T, error) {
	result, err := attempt(ctx, c.initialAttemptTimeout())
	if err == nil || !isRetryablePaymentError(err) || c.config.RetryDelay <= 0 || c.config.RetryAttemptTimeout <= 0 {
		return result, err
	}

	c.recordRetry(operation, "attempted")
	if err := waitForRetry(ctx, c.config.RetryDelay); err != nil {
		var zero T
		return zero, retryWaitError(err)
	}

	result, err = attempt(ctx, c.config.RetryAttemptTimeout)
	if err != nil {
		c.recordRetry(operation, "exhausted")
		var zero T
		return zero, err
	}
	c.recordRetry(operation, "succeeded")
	return result, nil
}

func isRetryablePaymentError(err error) bool {
	return app.HasPaymentErrorKind(err, app.PaymentErrorBankTimeout) || app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryWaitError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return app.NewPaymentBankTimeoutError(err)
	}
	return app.NewPaymentBankUnavailableError(err)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

type authorizationRequest struct {
	CardNumber  string `json:"card_number"`
	CVV         string `json:"cvv"`
	ExpiryMonth int    `json:"expiry_month"`
	ExpiryYear  int    `json:"expiry_year"`
	AmountCents int64  `json:"amount"`
}

type authorizationResponse struct {
	AuthorizationID string    `json:"authorization_id"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type captureRequest struct {
	AuthorizationID string `json:"authorization_id"`
	AmountCents     int64  `json:"amount"`
}

type captureResponse struct {
	CaptureID string `json:"capture_id"`
}

type voidRequest struct {
	AuthorizationID string `json:"authorization_id"`
}

type voidResponse struct {
	VoidID string `json:"void_id"`
}

type refundRequest struct {
	CaptureID   string `json:"capture_id"`
	AmountCents int64  `json:"amount"`
}

type refundResponse struct {
	RefundID string `json:"refund_id"`
}

type authorizationErrorResponse struct {
	Error string `json:"error"`
}

func decodeBadRequestInvalidInputReason(response *http.Response) (string, error) {
	reason, _, _, err := decodeBadRequestReason(response)
	return reason, err
}

func decodeBadRequestReason(response *http.Response) (string, bool, bool, error) {
	var payload authorizationErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", false, false, err
	}

	return invalidInputReasonForBadRequest(payload.Error), isBankStateConflict(payload.Error), isAuthorizationExpired(payload.Error), nil
}

func invalidInputReasonForBadRequest(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_card", "invalid_card_number":
		return "card details are invalid"
	case "invalid_cvv":
		return "card details are invalid"
	case "card_expired":
		return "card details are invalid"
	case "invalid_amount":
		return "amount must be greater than zero"
	case "amount_mismatch":
		return "amount does not match bank authorization"
	case "authorization_not_found":
		return "bank authorization cannot be captured"
	case "capture_not_found":
		return "bank capture cannot be refunded"
	default:
		return ""
	}
}

func isAuthorizationExpired(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), "authorization_expired")
}

func isBankStateConflict(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "authorization_already_used", "already_captured", "already_voided", "already_refunded":
		return true
	default:
		return false
	}
}

func newAuthorizationHTTPRequest(ctx context.Context, endpoint string, body *bytes.Buffer, operationKey string) (*http.Request, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", operationKey)
	return httpRequest, nil
}
