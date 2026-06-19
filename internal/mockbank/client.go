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
		OrderID:     request.OrderID,
		CustomerID:  request.CustomerID,
		CardNumber:  request.Card.Number,
		CVV:         request.Card.CVV,
		ExpiryMonth: request.Card.ExpiryMonth,
		ExpiryYear:  request.Card.ExpiryYear,
		AmountCents: request.AmountCents,
		Currency:    request.Currency,
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
	OrderID     string `json:"order_id"`
	CustomerID  string `json:"customer_id"`
	CardNumber  string `json:"card_number"`
	CVV         string `json:"cvv"`
	ExpiryMonth int    `json:"expiry_month"`
	ExpiryYear  int    `json:"expiry_year"`
	AmountCents int64  `json:"amount"`
	Currency    string `json:"currency"`
}

type authorizationResponse struct {
	AuthorizationID string `json:"authorization_id"`
}
