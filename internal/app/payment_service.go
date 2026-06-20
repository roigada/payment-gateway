package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
)

const authorizePaymentOperation = "authorize_payment"

type PaymentResult struct {
	ID            string
	OrderID       string
	CustomerID    string
	AmountCents   int64
	Currency      string
	Status        string
	DeclineReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AuthorizePaymentCommand struct {
	OrderID        string
	CustomerID     string
	AmountCents    int64
	Card           CardDetails
	IdempotencyKey string
}

type CardDetails struct {
	Number      string
	CVV         string
	ExpiryMonth int
	ExpiryYear  int
}

type PaymentRepository interface {
	Create(ctx context.Context, payment *domain.Payment) error
	FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error)
}

type IdempotencyRepository interface {
	FindCompleted(ctx context.Context, operation string, key string) (IdempotencyRecord, bool, error)
	SaveCompleted(ctx context.Context, record IdempotencyRecord) error
}

type IdempotencyRecord struct {
	Operation          string
	Key                string
	RequestFingerprint string
	Result             PaymentResult
}

type PaymentIDGenerator interface {
	NewPaymentID() domain.PaymentID
}

type BankOperationKeyGenerator interface {
	NewBankOperationKey() string
}

type BankAuthorizer interface {
	AuthorizePayment(ctx context.Context, request BankAuthorizationRequest) (BankAuthorizationResult, error)
}

type BankAuthorizationRequest struct {
	OperationKey string
	OrderID      string
	CustomerID   string
	AmountCents  int64
	Currency     string
	Card         CardDetails
}

type BankAuthorizationResult struct {
	BankAuthorizationID string
	DeclineReason       domain.DeclineReason
}

type PaymentService struct {
	paymentRepository PaymentRepository
	idempotency       IdempotencyRepository
	paymentIDs        PaymentIDGenerator
	bankOperationKeys BankOperationKeyGenerator
	bank              BankAuthorizer
	clock             Clock
	fingerprintSecret string
}

func NewPaymentService(
	paymentRepository PaymentRepository,
	idempotency IdempotencyRepository,
	paymentIDs PaymentIDGenerator,
	bankOperationKeys BankOperationKeyGenerator,
	bank BankAuthorizer,
	clock Clock,
	fingerprintSecret string,
) *PaymentService {
	if paymentRepository == nil {
		panic("payment repository is required")
	}
	if idempotency == nil {
		panic("idempotency repository is required")
	}
	if paymentIDs == nil {
		panic("payment ID generator is required")
	}
	if bankOperationKeys == nil {
		panic("bank operation key generator is required")
	}
	if bank == nil {
		panic("bank authorizer is required")
	}
	if clock == nil {
		panic("clock is required")
	}
	fingerprintSecret = strings.TrimSpace(fingerprintSecret)
	if fingerprintSecret == "" {
		panic("authorization fingerprint secret is required")
	}

	return &PaymentService{
		paymentRepository: paymentRepository,
		idempotency:       idempotency,
		paymentIDs:        paymentIDs,
		bankOperationKeys: bankOperationKeys,
		bank:              bank,
		clock:             clock,
		fingerprintSecret: fingerprintSecret,
	}
}

func (s *PaymentService) AuthorizePayment(ctx context.Context, command AuthorizePaymentCommand) (PaymentResult, error) {
	command = normalizeAuthorizePaymentCommand(command)
	if command.IdempotencyKey == "" {
		return PaymentResult{}, NewInvalidPaymentInput("idempotency key is required", nil)
	}
	if command.OrderID == "" {
		return PaymentResult{}, NewInvalidPaymentInput("order id is required", nil)
	}
	if command.CustomerID == "" {
		return PaymentResult{}, NewInvalidPaymentInput("customer id is required", nil)
	}
	if command.AmountCents <= 0 {
		return PaymentResult{}, NewInvalidPaymentInput("amount must be greater than zero", nil)
	}
	if err := validateCardDetails(command.Card); err != nil {
		return PaymentResult{}, err
	}

	fingerprint := authorizePaymentFingerprint(command, s.fingerprintSecret)
	record, found, err := s.idempotency.FindCompleted(ctx, authorizePaymentOperation, command.IdempotencyKey)
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	if found {
		if record.RequestFingerprint != fingerprint {
			return PaymentResult{}, NewPaymentIdempotencyConflict(nil)
		}
		return record.Result, nil
	}

	paymentID := s.paymentIDs.NewPaymentID()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	bankResult, err := s.bank.AuthorizePayment(ctx, BankAuthorizationRequest{
		OperationKey: bankOperationKey,
		OrderID:      command.OrderID,
		CustomerID:   command.CustomerID,
		AmountCents:  command.AmountCents,
		Currency:     domain.CurrencyUSD,
		Card:         command.Card,
	})
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	now := s.clock.Now()
	var payment *domain.Payment
	if bankResult.DeclineReason != "" {
		payment, err = domain.NewDeclinedPayment(
			paymentID,
			command.OrderID,
			command.CustomerID,
			command.AmountCents,
			bankResult.DeclineReason,
			bankOperationKey,
			now,
		)
	} else {
		payment, err = domain.NewAuthorizedPayment(
			paymentID,
			command.OrderID,
			command.CustomerID,
			command.AmountCents,
			bankResult.BankAuthorizationID,
			bankOperationKey,
			now,
		)
	}
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	if err := s.paymentRepository.Create(ctx, payment); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	if err := s.idempotency.SaveCompleted(ctx, IdempotencyRecord{
		Operation:          authorizePaymentOperation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		Result:             result,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) GetPayment(ctx context.Context, id string) (PaymentResult, error) {
	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(id))
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	return newPaymentResult(payment), nil
}

func validateCardDetails(card CardDetails) error {
	if !allDigits(card.Number) || len(card.Number) < 12 || len(card.Number) > 19 {
		return NewInvalidPaymentInput("card details are invalid", nil)
	}
	if !allDigits(card.CVV) || len(card.CVV) < 3 || len(card.CVV) > 4 {
		return NewInvalidPaymentInput("card details are invalid", nil)
	}
	if card.ExpiryMonth < 1 || card.ExpiryMonth > 12 {
		return NewInvalidPaymentInput("card details are invalid", nil)
	}
	if card.ExpiryYear <= 0 {
		return NewInvalidPaymentInput("card details are invalid", nil)
	}
	return nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeAuthorizePaymentCommand(command AuthorizePaymentCommand) AuthorizePaymentCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.OrderID = strings.TrimSpace(command.OrderID)
	command.CustomerID = strings.TrimSpace(command.CustomerID)
	command.Card.Number = strings.TrimSpace(command.Card.Number)
	command.Card.CVV = strings.TrimSpace(command.Card.CVV)
	return command
}

func authorizePaymentFingerprint(command AuthorizePaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%d\n%s\n%d\n%d",
		authorizePaymentOperation,
		command.OrderID,
		command.CustomerID,
		command.AmountCents,
		command.Card.Number,
		command.Card.ExpiryMonth,
		command.Card.ExpiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func newPaymentResult(payment *domain.Payment) PaymentResult {
	return PaymentResult{
		ID:            string(payment.ID()),
		OrderID:       payment.OrderID(),
		CustomerID:    payment.CustomerID(),
		AmountCents:   payment.AmountCents(),
		Currency:      payment.Currency(),
		Status:        string(payment.Status()),
		DeclineReason: string(payment.DeclineReason()),
		CreatedAt:     payment.CreatedAt(),
		UpdatedAt:     payment.UpdatedAt(),
	}
}
