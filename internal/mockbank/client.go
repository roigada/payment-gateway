package mockbank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/roigada/payment-gateway/internal/app"
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
		return app.BankAuthorizationResult{}, err
	}

	endpoint := c.baseURL.JoinPath("/api/v1/authorizations")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return app.BankAuthorizationResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.OperationKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return app.BankAuthorizationResult{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		if result, ok := decodeAuthorizationDecline(response); ok {
			return result, nil
		}
		return app.BankAuthorizationResult{}, fmt.Errorf("mock bank authorization failed: status %d", response.StatusCode)
	}

	var payload authorizationResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return app.BankAuthorizationResult{}, err
	}
	if strings.TrimSpace(payload.AuthorizationID) == "" {
		return app.BankAuthorizationResult{}, fmt.Errorf("mock bank authorization response missing authorization id")
	}

	return app.BankAuthorizationResult{BankAuthorizationID: payload.AuthorizationID}, nil
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

type bankErrorResponse struct {
	Code      string `json:"code"`
	ErrorCode string `json:"error_code"`
	Error     struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decodeAuthorizationDecline(response *http.Response) (app.BankAuthorizationResult, bool) {
	if !isDefinitiveDeclineStatus(response.StatusCode) {
		return app.BankAuthorizationResult{}, false
	}

	var payload bankErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return app.BankAuthorizationResult{}, false
	}

	code := payload.declineCode()
	reason, ok := mapBankDeclineReason(code)
	if !ok {
		if strings.TrimSpace(code) == "" {
			return app.BankAuthorizationResult{}, false
		}
		reason = app.BankDeclineReasonUnknown
	}

	return app.BankAuthorizationResult{DeclineReason: reason}, true
}

func (r bankErrorResponse) declineCode() string {
	switch {
	case strings.TrimSpace(r.Error.Code) != "":
		return r.Error.Code
	case strings.TrimSpace(r.Code) != "":
		return r.Code
	default:
		return r.ErrorCode
	}
}

func mapBankDeclineReason(code string) (app.BankDeclineReason, bool) {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "insufficient_funds":
		return app.BankDeclineReasonInsufficientFunds, true
	case "invalid_card", "invalid_card_number", "invalid_cvv":
		return app.BankDeclineReasonInvalidCard, true
	case "expired_card":
		return app.BankDeclineReasonExpiredCard, true
	default:
		return "", false
	}
}

func isDefinitiveDeclineStatus(status int) bool {
	return status == http.StatusPaymentRequired || status == http.StatusUnprocessableEntity
}
