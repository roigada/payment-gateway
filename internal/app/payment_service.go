package app

import (
	"context"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/domain"
)

type PaymentResult struct {
	ID          string
	OrderID     string
	CustomerID  string
	AmountCents int64
	Currency    string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
	AuthorizationReference string
}

type PaymentService struct {
	paymentRepository PaymentRepository
	paymentIDs        PaymentIDGenerator
	bankOperationKeys BankOperationKeyGenerator
	bank              BankAuthorizer
	clock             Clock
}

func NewPaymentService(
	paymentRepository PaymentRepository,
	paymentIDs PaymentIDGenerator,
	bankOperationKeys BankOperationKeyGenerator,
	bank BankAuthorizer,
	clock Clock,
) *PaymentService {
	return &PaymentService{
		paymentRepository: paymentRepository,
		paymentIDs:        paymentIDs,
		bankOperationKeys: bankOperationKeys,
		bank:              bank,
		clock:             clock,
	}
}

func (s *PaymentService) AuthorizePayment(ctx context.Context, command AuthorizePaymentCommand) (PaymentResult, error) {
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return PaymentResult{}, ErrMissingIdempotencyKey
	}
	if strings.TrimSpace(command.OrderID) == "" {
		return PaymentResult{}, domain.ErrInvalidOrderID
	}
	if strings.TrimSpace(command.CustomerID) == "" {
		return PaymentResult{}, domain.ErrInvalidCustomerID
	}
	if command.AmountCents <= 0 {
		return PaymentResult{}, domain.ErrInvalidAmount
	}
	if err := validateCardDetails(command.Card); err != nil {
		return PaymentResult{}, err
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
		return PaymentResult{}, err
	}

	payment, err := domain.NewAuthorizedPayment(
		paymentID,
		command.OrderID,
		command.CustomerID,
		command.AmountCents,
		bankResult.AuthorizationReference,
		bankOperationKey,
		s.clock.Now(),
	)
	if err != nil {
		return PaymentResult{}, err
	}
	if err := s.paymentRepository.Create(ctx, payment); err != nil {
		return PaymentResult{}, err
	}

	return newPaymentResult(payment), nil
}

func (s *PaymentService) GetPayment(ctx context.Context, id string) (PaymentResult, error) {
	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(id))
	if err != nil {
		return PaymentResult{}, err
	}
	return newPaymentResult(payment), nil
}

func validateCardDetails(card CardDetails) error {
	if !allDigits(card.Number) || len(card.Number) < 12 || len(card.Number) > 19 {
		return ErrInvalidCardDetails
	}
	if !allDigits(card.CVV) || len(card.CVV) < 3 || len(card.CVV) > 4 {
		return ErrInvalidCardDetails
	}
	if card.ExpiryMonth < 1 || card.ExpiryMonth > 12 {
		return ErrInvalidCardDetails
	}
	if card.ExpiryYear <= 0 {
		return ErrInvalidCardDetails
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

func newPaymentResult(payment *domain.Payment) PaymentResult {
	return PaymentResult{
		ID:          string(payment.ID()),
		OrderID:     payment.OrderID(),
		CustomerID:  payment.CustomerID(),
		AmountCents: payment.AmountCents(),
		Currency:    payment.Currency(),
		Status:      string(payment.Status()),
		CreatedAt:   payment.CreatedAt(),
		UpdatedAt:   payment.UpdatedAt(),
	}
}
