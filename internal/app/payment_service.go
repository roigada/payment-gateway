package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	FindCompleted(ctx context.Context, operation string, key string) (IdempotencyRecord, error)
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
	DeclineReason       BankDeclineReason
}

type BankDeclineReason = domain.DeclineReason

const (
	BankDeclineReasonInsufficientFunds = domain.DeclineReasonInsufficientFunds
	BankDeclineReasonInvalidCard       = domain.DeclineReasonInvalidCard
	BankDeclineReasonExpiredCard       = domain.DeclineReasonExpiredCard
	BankDeclineReasonUnknown           = domain.DeclineReasonUnknown
)

type PaymentService struct {
	paymentRepository PaymentRepository
	idempotency       IdempotencyRepository
	paymentIDs        PaymentIDGenerator
	bankOperationKeys BankOperationKeyGenerator
	bank              BankAuthorizer
	clock             Clock
}

func NewPaymentService(
	paymentRepository PaymentRepository,
	idempotency IdempotencyRepository,
	paymentIDs PaymentIDGenerator,
	bankOperationKeys BankOperationKeyGenerator,
	bank BankAuthorizer,
	clock Clock,
) *PaymentService {
	return &PaymentService{
		paymentRepository: paymentRepository,
		idempotency:       idempotency,
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

	fingerprint := authorizePaymentFingerprint(command)
	if s.idempotency != nil {
		record, err := s.idempotency.FindCompleted(ctx, authorizePaymentOperation, command.IdempotencyKey)
		if err == nil {
			if record.RequestFingerprint != fingerprint {
				return PaymentResult{}, ErrIdempotencyConflict
			}
			return record.Result, nil
		}
		if !errors.Is(err, ErrIdempotencyNotFound) {
			return PaymentResult{}, err
		}
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

	now := s.clock.Now()
	var payment *domain.Payment
	if bankResult.DeclineReason != "" {
		payment, err = domain.NewDeclinedPayment(
			paymentID,
			command.OrderID,
			command.CustomerID,
			command.AmountCents,
			domain.DeclineReason(bankResult.DeclineReason),
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
		return PaymentResult{}, err
	}
	if err := s.paymentRepository.Create(ctx, payment); err != nil {
		return PaymentResult{}, err
	}

	result := newPaymentResult(payment)
	if s.idempotency != nil {
		if err := s.idempotency.SaveCompleted(ctx, IdempotencyRecord{
			Operation:          authorizePaymentOperation,
			Key:                command.IdempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
		}); err != nil {
			return PaymentResult{}, err
		}
	}

	return result, nil
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

func authorizePaymentFingerprint(command AuthorizePaymentCommand) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%d\n%s\n%s\n%d\n%d",
		authorizePaymentOperation,
		strings.TrimSpace(command.OrderID),
		strings.TrimSpace(command.CustomerID),
		command.AmountCents,
		strings.TrimSpace(command.Card.Number),
		strings.TrimSpace(command.Card.CVV),
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
