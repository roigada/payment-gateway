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

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
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
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

func (c *Client) AuthorizePayment(ctx context.Context, request app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(authorizationRequest{
		CardNumber:  request.Card.Number,
		CVV:         request.Card.CVV,
		ExpiryMonth: request.Card.ExpiryMonth,
		ExpiryYear:  request.Card.ExpiryYear,
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
			return app.BankAuthorizationResult{}, app.NewPaymentBankTimeout(err)
		}
		return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailable(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload authorizationResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailable(err)
		}
		if strings.TrimSpace(payload.AuthorizationID) == "" {
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank authorization response missing authorization id"))
		}

		return app.BankAuthorizationResult{BankAuthorizationID: payload.AuthorizationID}, nil
	case http.StatusBadRequest:
		reason, err := decodeBadRequestInvalidInputReason(response)
		if err != nil {
			return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailable(err)
		}
		if reason != "" {
			return app.BankAuthorizationResult{}, app.NewInvalidPaymentInput(reason, nil)
		}
	case http.StatusPaymentRequired:
		return app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}, nil
	}

	return app.BankAuthorizationResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank authorization failed: status %d", response.StatusCode))
}

func (c *Client) CapturePayment(ctx context.Context, request app.BankCaptureRequest) (app.BankCaptureResult, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(captureRequest{
		AuthorizationID: request.BankAuthorizationID,
		AmountCents:     request.AmountCents,
	}); err != nil {
		return app.BankCaptureResult{}, err
	}

	endpoint := c.baseURL.JoinPath("/api/v1/captures")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankCaptureResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			return app.BankCaptureResult{}, app.NewPaymentBankTimeout(err)
		}
		return app.BankCaptureResult{}, app.NewPaymentBankUnavailable(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload captureResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailable(err)
		}
		if strings.TrimSpace(payload.CaptureID) == "" {
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank capture response missing capture id"))
		}

		return app.BankCaptureResult{BankCaptureID: payload.CaptureID}, nil
	case http.StatusBadRequest:
		reason, err := decodeBadRequestInvalidInputReason(response)
		if err != nil {
			return app.BankCaptureResult{}, app.NewPaymentBankUnavailable(err)
		}
		if reason != "" {
			return app.BankCaptureResult{}, app.NewInvalidPaymentInput(reason, nil)
		}
	}

	return app.BankCaptureResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank capture failed: status %d", response.StatusCode))
}

func (c *Client) VoidPayment(ctx context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(voidRequest{
		AuthorizationID: request.BankAuthorizationID,
	}); err != nil {
		return app.BankVoidResult{}, err
	}

	endpoint := c.baseURL.JoinPath("/api/v1/voids")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankVoidResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			return app.BankVoidResult{}, app.NewPaymentBankTimeout(err)
		}
		return app.BankVoidResult{}, app.NewPaymentBankUnavailable(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload voidResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return app.BankVoidResult{}, app.NewPaymentBankUnavailable(err)
		}
		if strings.TrimSpace(payload.VoidID) == "" {
			return app.BankVoidResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank void response missing void id"))
		}

		return app.BankVoidResult{BankVoidID: payload.VoidID}, nil
	case http.StatusBadRequest:
		reason, err := decodeBadRequestInvalidInputReason(response)
		if err != nil {
			return app.BankVoidResult{}, app.NewPaymentBankUnavailable(err)
		}
		if reason != "" {
			return app.BankVoidResult{}, app.NewInvalidPaymentInput(reason, nil)
		}
	}

	return app.BankVoidResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank void failed: status %d", response.StatusCode))
}

func (c *Client) RefundPayment(ctx context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(refundRequest{
		CaptureID:   request.BankCaptureID,
		AmountCents: request.AmountCents,
	}); err != nil {
		return app.BankRefundResult{}, err
	}

	endpoint := c.baseURL.JoinPath("/api/v1/refunds")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankRefundResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if isTimeout(err) {
			return app.BankRefundResult{}, app.NewPaymentBankTimeout(err)
		}
		return app.BankRefundResult{}, app.NewPaymentBankUnavailable(err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		var payload refundResponse
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			return app.BankRefundResult{}, app.NewPaymentBankUnavailable(err)
		}
		if strings.TrimSpace(payload.RefundID) == "" {
			return app.BankRefundResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank refund response missing refund id"))
		}

		return app.BankRefundResult{BankRefundID: payload.RefundID}, nil
	case http.StatusBadRequest:
		reason, err := decodeBadRequestInvalidInputReason(response)
		if err != nil {
			return app.BankRefundResult{}, app.NewPaymentBankUnavailable(err)
		}
		if reason != "" {
			return app.BankRefundResult{}, app.NewInvalidPaymentInput(reason, nil)
		}
	}

	return app.BankRefundResult{}, app.NewPaymentBankUnavailable(fmt.Errorf("mock bank refund failed: status %d", response.StatusCode))
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
	AuthorizationID string `json:"authorization_id"`
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
	var payload authorizationErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", err
	}

	return invalidInputReasonForBadRequest(payload.Error), nil
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
	case "authorization_not_found", "authorization_expired", "authorization_already_used", "already_captured", "already_voided":
		return "bank authorization cannot be captured"
	case "capture_not_found", "already_refunded":
		return "bank capture cannot be refunded"
	default:
		return ""
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
