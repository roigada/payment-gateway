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

const (
	mockBankOperationAuthorize = "authorize"
	mockBankOperationCapture   = "capture"
	mockBankOperationVoid      = "void"
	mockBankOperationRefund    = "refund"
)

const (
	mockBankRequestResultInternal      = "internal"
	mockBankRequestResultTimeout       = "timeout"
	mockBankRequestResultUnavailable   = "unavailable"
	mockBankRequestResultSuccess       = "success"
	mockBankRequestResultInvalidInput  = "invalid_input"
	mockBankRequestResultDeclined      = "declined"
	mockBankRequestResultExpired       = "expired"
	mockBankRequestResultStateConflict = "state_conflict"
)

const (
	mockBankRetryAttempted = "attempted"
	mockBankRetryFailed    = "failed"
	mockBankRetrySucceeded = "succeeded"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	metrics    metrics
	config     Config
}

// Config contains the Mock Bank endpoint and call budgets. All budgets
// must be positive.
type Config struct {
	BaseURL               string
	Timeout               time.Duration
	InitialAttemptTimeout time.Duration
	RetryDelay            time.Duration
	RetryAttemptTimeout   time.Duration
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnectionTimeout time.Duration
}

// NewClient constructs a Mock Bank client with its HTTP transport configured
// from the supplied call budgets.
func NewClient(metrics metrics, config Config) (*Client, error) {
	if metrics == nil {
		return nil, fmt.Errorf("mock bank metrics are required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: config.ConnectTimeout}).DialContext,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		IdleConnTimeout:       config.IdleConnectionTimeout,
	}
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("mock bank base URL must be absolute")
	}
	return &Client{baseURL: parsed, httpClient: &http.Client{Transport: transport}, metrics: metrics, config: config}, nil
}

func (config Config) validate() error {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil {
		return err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("mock bank base URL must be absolute")
	}

	if config.Timeout <= 0 {
		return fmt.Errorf("mock bank timeout must be positive")
	}
	if config.InitialAttemptTimeout <= 0 {
		return fmt.Errorf("mock bank initial attempt timeout must be positive")
	}
	if config.RetryDelay <= 0 {
		return fmt.Errorf("mock bank retry delay must be positive")
	}
	if config.RetryAttemptTimeout <= 0 {
		return fmt.Errorf("mock bank retry attempt timeout must be positive")
	}
	if config.ConnectTimeout <= 0 {
		return fmt.Errorf("mock bank connect timeout must be positive")
	}
	if config.TLSHandshakeTimeout <= 0 {
		return fmt.Errorf("mock bank TLS handshake timeout must be positive")
	}
	if config.ResponseHeaderTimeout <= 0 {
		return fmt.Errorf("mock bank response header timeout must be positive")
	}
	if config.IdleConnectionTimeout <= 0 {
		return fmt.Errorf("mock bank idle connection timeout must be positive")
	}
	return nil
}

type metrics interface {
	RecordMockBankRequest(operation string, result string, duration time.Duration)
	RecordMockBankRetry(operation string, result string)
}

func (c *Client) AuthorizePayment(ctx context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	return retryTransient(c, ctx, mockBankOperationAuthorize, func(ctx context.Context, timeout time.Duration) (app.BankAuthorizationResult, error) {
		return c.authorizePaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) authorizePaymentAttempt(ctx context.Context, request app.BankAuthorizationRequest, timeout time.Duration) (app.BankAuthorizationResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := mockBankRequestResultInternal
	defer func() {
		c.metrics.RecordMockBankRequest(mockBankOperationAuthorize, result, time.Since(startedAt))
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

	httpRequest, err := c.newHTTPRequest(ctx, "/api/v1/authorizations", &body, request.OperationKey)
	if err != nil {
		return app.BankAuthorizationResult{}, app.NewInternalPaymentError(err)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = mockBankRequestResultTimeout
			return app.BankAuthorizationResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = mockBankRequestResultUnavailable
		return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload authorizationResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.AuthorizationID) == "" {
			result = mockBankRequestResultUnavailable
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank authorization response missing authorization id"))
		}
		if payload.ExpiresAt.IsZero() {
			result = mockBankRequestResultUnavailable
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank authorization response missing expires_at"))
		}

		result = mockBankRequestResultSuccess
		return app.BankAuthorizationResult{BankAuthorizationID: payload.AuthorizationID, AuthorizationExpiresAt: payload.ExpiresAt}, nil
	case http.StatusBadRequest:
		reason, declineReason, err := decodeBadRequestAuthorizationOutcome(response)
		if err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if declineReason != "" {
			result = mockBankRequestResultDeclined
			return app.BankAuthorizationResult{DeclineReason: declineReason}, nil
		}
		if reason != "" {
			result = mockBankRequestResultInvalidInput
			return app.BankAuthorizationResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	case http.StatusPaymentRequired:
		result = mockBankRequestResultDeclined
		return app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}, nil
	}

	result = mockBankRequestResultUnavailable
	return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank authorization failed: status %d", response.StatusCode))
}

func (c *Client) CapturePayment(ctx context.Context, request app.BankCaptureRequest) (app.BankCaptureResult, error) {
	return retryTransient(c, ctx, mockBankOperationCapture, func(ctx context.Context, timeout time.Duration) (app.BankCaptureResult, error) {
		return c.capturePaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) capturePaymentAttempt(ctx context.Context, request app.BankCaptureRequest, timeout time.Duration) (app.BankCaptureResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := mockBankRequestResultInternal
	defer func() {
		c.metrics.RecordMockBankRequest(mockBankOperationCapture, result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(captureRequest{
		AuthorizationID: request.BankAuthorizationID,
		AmountCents:     request.AmountCents,
	}); err != nil {
		return app.BankCaptureResult{}, app.NewInternalPaymentError(err)
	}

	httpRequest, err := c.newHTTPRequest(ctx, "/api/v1/captures", &body, request.OperationKey)
	if err != nil {
		return app.BankCaptureResult{}, app.NewInternalPaymentError(err)
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = mockBankRequestResultTimeout
			return app.BankCaptureResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = mockBankRequestResultUnavailable
		return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload captureResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.CaptureID) == "" {
			result = mockBankRequestResultUnavailable
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank capture response missing capture id"))
		}

		result = mockBankRequestResultSuccess
		return app.BankCaptureResult{BankCaptureID: payload.CaptureID}, nil
	case http.StatusBadRequest:
		reason, conflict, expired, err := decodeBadRequestReason(response)
		if err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if expired {
			result = mockBankRequestResultExpired
			return app.BankCaptureResult{}, app.NewPaymentAuthorizationExpiredError(nil)
		}
		if conflict {
			result = mockBankRequestResultStateConflict
			return app.BankCaptureResult{}, app.NewPaymentBankStateConflictError(nil)
		}
		if reason != "" {
			result = mockBankRequestResultInvalidInput
			return app.BankCaptureResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	}

	result = mockBankRequestResultUnavailable
	return app.BankCaptureResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank capture failed: status %d", response.StatusCode))
}

func (c *Client) VoidPayment(ctx context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	return retryTransient(c, ctx, mockBankOperationVoid, func(ctx context.Context, timeout time.Duration) (app.BankVoidResult, error) {
		return c.voidPaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) voidPaymentAttempt(ctx context.Context, request app.BankVoidRequest, timeout time.Duration) (app.BankVoidResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := mockBankRequestResultInternal
	defer func() {
		c.metrics.RecordMockBankRequest(mockBankOperationVoid, result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(voidRequest{
		AuthorizationID: request.BankAuthorizationID,
	}); err != nil {
		return app.BankVoidResult{}, app.NewInternalPaymentError(err)
	}

	httpRequest, err := c.newHTTPRequest(ctx, "/api/v1/voids", &body, request.OperationKey)
	if err != nil {
		return app.BankVoidResult{}, app.NewInternalPaymentError(err)
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = mockBankRequestResultTimeout
			return app.BankVoidResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = mockBankRequestResultUnavailable
		return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload voidResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.VoidID) == "" {
			result = mockBankRequestResultUnavailable
			return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank void response missing void id"))
		}

		result = mockBankRequestResultSuccess
		return app.BankVoidResult{BankVoidID: payload.VoidID}, nil
	case http.StatusBadRequest:
		reason, conflict, expired, err := decodeBadRequestReason(response)
		if err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if expired {
			result = mockBankRequestResultExpired
			return app.BankVoidResult{}, app.NewPaymentAuthorizationExpiredError(nil)
		}
		if conflict {
			result = mockBankRequestResultStateConflict
			return app.BankVoidResult{}, app.NewPaymentBankStateConflictError(nil)
		}
		if reason != "" {
			result = mockBankRequestResultInvalidInput
			return app.BankVoidResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	}

	result = mockBankRequestResultUnavailable
	return app.BankVoidResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank void failed: status %d", response.StatusCode))
}

func (c *Client) RefundPayment(ctx context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	return retryTransient(c, ctx, mockBankOperationRefund, func(ctx context.Context, timeout time.Duration) (app.BankRefundResult, error) {
		return c.refundPaymentAttempt(ctx, request, timeout)
	})
}

func (c *Client) refundPaymentAttempt(ctx context.Context, request app.BankRefundRequest, timeout time.Duration) (app.BankRefundResult, error) {
	ctx, cancel := requestContext(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	result := mockBankRequestResultInternal
	defer func() {
		c.metrics.RecordMockBankRequest(mockBankOperationRefund, result, time.Since(startedAt))
	}()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(refundRequest{
		CaptureID:   request.BankCaptureID,
		AmountCents: request.AmountCents,
	}); err != nil {
		return app.BankRefundResult{}, app.NewInternalPaymentError(err)
	}

	httpRequest, err := c.newHTTPRequest(ctx, "/api/v1/refunds", &body, request.OperationKey)
	if err != nil {
		return app.BankRefundResult{}, app.NewInternalPaymentError(err)
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			result = mockBankRequestResultTimeout
			return app.BankRefundResult{}, app.NewPaymentBankTimeoutError(err)
		}
		result = mockBankRequestResultUnavailable
		return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload refundResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if strings.TrimSpace(payload.RefundID) == "" {
			result = mockBankRequestResultUnavailable
			return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank refund response missing refund id"))
		}

		result = mockBankRequestResultSuccess
		return app.BankRefundResult{BankRefundID: payload.RefundID}, nil
	case http.StatusBadRequest:
		reason, conflict, _, err := decodeBadRequestReason(response)
		if err != nil {
			result = mockBankRequestResultUnavailable
			return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(err)
		}
		if conflict {
			result = mockBankRequestResultStateConflict
			return app.BankRefundResult{}, app.NewPaymentBankStateConflictError(nil)
		}
		if reason != "" {
			result = mockBankRequestResultInvalidInput
			return app.BankRefundResult{}, app.NewInvalidPaymentInputError(reason, nil)
		}
	}

	result = mockBankRequestResultUnavailable
	return app.BankRefundResult{}, app.NewPaymentBankUnavailableError(fmt.Errorf("mock bank refund failed: status %d", response.StatusCode))
}

func retryTransient[T any](c *Client, ctx context.Context, operation string, attempt func(context.Context, time.Duration) (T, error)) (T, error) {
	var zero T

	result, err := attempt(ctx, c.config.InitialAttemptTimeout)
	if err == nil || !isRetryablePaymentError(err) {
		return result, err
	}

	if err := waitForRetry(ctx, c.config.RetryDelay); err != nil {
		return zero, retryWaitError(err)
	}

	c.metrics.RecordMockBankRetry(operation, mockBankRetryAttempted)
	result, err = attempt(ctx, c.config.RetryAttemptTimeout)
	if err != nil {
		c.metrics.RecordMockBankRetry(operation, mockBankRetryFailed)
		return zero, err
	}
	c.metrics.RecordMockBankRetry(operation, mockBankRetrySucceeded)
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

func requestContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
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

func decodeBadRequestAuthorizationOutcome(response *http.Response) (string, domain.DeclineReason, error) {
	var payload authorizationErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", "", err
	}

	return invalidInputReasonForBadRequest(payload.Error), declineReasonForBadRequest(payload.Error), nil
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

func declineReasonForBadRequest(code string) domain.DeclineReason {
	if strings.EqualFold(strings.TrimSpace(code), "card_expired") {
		return domain.DeclineReasonExpiredCard
	}
	return ""
}

func isAuthorizationExpired(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), "authorization_expired")
}

func isBankStateConflict(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "authorization_not_found", "capture_not_found", "amount_mismatch", "authorization_already_used", "already_captured", "already_voided", "already_refunded":
		return true
	default:
		return false
	}
}

func (c *Client) newHTTPRequest(ctx context.Context, endpointPath string, body *bytes.Buffer, operationKey string) (*http.Request, error) {
	endpoint := c.baseURL.JoinPath(endpointPath)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", operationKey)
	return httpRequest, nil
}
