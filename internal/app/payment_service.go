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

const (
	authorizePaymentOperation   = "authorize_payment"
	retryAuthorizationOperation = "retry_authorization"
	capturePaymentOperation     = "capture_payment"
	voidPaymentOperation        = "void_payment"
	refundPaymentOperation      = "refund_payment"
)

type PaymentResult struct {
	ID             string
	OrderID        string
	CustomerID     string
	AmountCents    int64
	Currency       string
	Status         string
	DeclineReason  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ResponseStatus int
}

type AuthorizePaymentCommand struct {
	OrderID        string
	CustomerID     string
	AmountCents    int64
	Card           CardDetails
	IdempotencyKey string
}

type RetryAuthorizationCommand struct {
	PaymentID      string
	Card           CardDetails
	IdempotencyKey string
}

type CapturePaymentCommand struct {
	PaymentID      string
	IdempotencyKey string
}

type VoidPaymentCommand struct {
	PaymentID      string
	IdempotencyKey string
}

type RefundPaymentCommand struct {
	PaymentID      string
	IdempotencyKey string
}

type GetPaymentQuery struct {
	PaymentID string
}

type SearchPaymentsQuery struct {
	OrderID    string
	CustomerID string
	Status     string
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
	UpdateAuthorizationResult(ctx context.Context, payment *domain.Payment) error
	UpdateVoidResult(ctx context.Context, payment *domain.Payment) error
	SaveVoidBankOperationKey(ctx context.Context, payment *domain.Payment) error
	Search(ctx context.Context, query SearchPaymentsQuery) ([]*domain.Payment, error)
	UpdateCaptureResult(ctx context.Context, payment *domain.Payment) error
	SaveCaptureBankOperationKey(ctx context.Context, payment *domain.Payment) error
	UpdateRefundResult(ctx context.Context, payment *domain.Payment) error
	SaveRefundBankOperationKey(ctx context.Context, payment *domain.Payment) error
}

type IdempotencyRepository interface {
	Claim(ctx context.Context, operation string, key string, requestFingerprint string) (IdempotencyRecord, IdempotencyClaimStatus, error)
	Complete(ctx context.Context, record IdempotencyRecord) error
	Release(ctx context.Context, operation string, key string) error
}

type IdempotencyRecord struct {
	Operation          string
	Key                string
	RequestFingerprint string
	Result             PaymentResult
	ResponseStatus     int
}

type IdempotencyClaimStatus string

const (
	IdempotencyClaimed    IdempotencyClaimStatus = "claimed"
	IdempotencyCompleted  IdempotencyClaimStatus = "completed"
	IdempotencyInProgress IdempotencyClaimStatus = "in_progress"
)

type PaymentIDGenerator interface {
	NewPaymentID() domain.PaymentID
}

type BankOperationKeyGenerator interface {
	NewBankOperationKey() string
}

type BankAuthorizer interface {
	AuthorizePayment(ctx context.Context, request BankAuthorizationRequest) (BankAuthorizationResult, error)
	VoidPayment(ctx context.Context, request BankVoidRequest) (BankVoidResult, error)
}

type BankCapturer interface {
	CapturePayment(ctx context.Context, request BankCaptureRequest) (BankCaptureResult, error)
}

type BankRefunder interface {
	RefundPayment(ctx context.Context, request BankRefundRequest) (BankRefundResult, error)
}

type BankClient interface {
	BankAuthorizer
	BankCapturer
	BankRefunder
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

type BankCaptureRequest struct {
	OperationKey        string
	BankAuthorizationID string
	AmountCents         int64
	Currency            string
}

type BankCaptureResult struct {
	BankCaptureID string
}

type BankVoidRequest struct {
	OperationKey        string
	BankAuthorizationID string
}

type BankVoidResult struct {
	BankVoidID string
}

type BankRefundRequest struct {
	OperationKey  string
	BankCaptureID string
	AmountCents   int64
	Currency      string
}

type BankRefundResult struct {
	BankRefundID string
}

type PaymentService struct {
	paymentRepository PaymentRepository
	idempotency       IdempotencyRepository
	paymentIDs        PaymentIDGenerator
	bankOperationKeys BankOperationKeyGenerator
	bank              BankClient
	clock             Clock
	fingerprintSecret string
}

func NewPaymentService(
	paymentRepository PaymentRepository,
	idempotency IdempotencyRepository,
	paymentIDs PaymentIDGenerator,
	bankOperationKeys BankOperationKeyGenerator,
	bank BankClient,
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
		panic("fingerprint secret is required")
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

	fingerprint := authorizePaymentRequestFingerprint(command, s.fingerprintSecret)
	authorizationCardFingerprint := authorizationCardFingerprint(command.Card, s.fingerprintSecret)
	record, claimed, err := s.claimIdempotency(ctx, authorizePaymentOperation, command.IdempotencyKey, fingerprint)
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
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

	now := s.clock.Now()
	var payment *domain.Payment
	if isUnknownAuthorizationOutcome(err) {
		payment, err = domain.NewPendingPayment(paymentID, command.OrderID, command.CustomerID, command.AmountCents, bankOperationKey, authorizationCardFingerprint, now)
	} else if err != nil {
		s.releaseIdempotency(ctx, authorizePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	} else if bankResult.DeclineReason != "" {
		payment, err = domain.NewDeclinedPayment(paymentID, command.OrderID, command.CustomerID, command.AmountCents, bankResult.DeclineReason, bankOperationKey, authorizationCardFingerprint, now)
	} else {
		payment, err = domain.NewAuthorizedPayment(paymentID, command.OrderID, command.CustomerID, command.AmountCents, bankResult.BankAuthorizationID, bankOperationKey, authorizationCardFingerprint, now)
	}
	if err != nil {
		s.releaseIdempotency(ctx, authorizePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if err := s.paymentRepository.Create(ctx, payment); err != nil {
		s.releaseIdempotency(ctx, authorizePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 201
	if err := s.idempotency.Complete(ctx, IdempotencyRecord{
		Operation:          authorizePaymentOperation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		Result:             result,
		ResponseStatus:     result.ResponseStatus,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) RetryAuthorization(ctx context.Context, command RetryAuthorizationCommand) (PaymentResult, error) {
	command = normalizeRetryAuthorizationCommand(command)
	if command.IdempotencyKey == "" {
		return PaymentResult{}, NewInvalidPaymentInput("idempotency key is required", nil)
	}
	if command.PaymentID == "" {
		return PaymentResult{}, NewInvalidPaymentInput("payment id is required", nil)
	}
	if err := validateCardDetails(command.Card); err != nil {
		return PaymentResult{}, err
	}

	operation := retryAuthorizationOperation + ":" + command.PaymentID
	requestFingerprint := retryAuthorizationRequestFingerprint(command, s.fingerprintSecret)
	record, claimed, err := s.claimIdempotency(ctx, operation, command.IdempotencyKey, requestFingerprint)
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return record.Result, nil
	}

	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		s.releaseIdempotency(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if payment.Status() != domain.PaymentStatusPending {
		s.releaseIdempotency(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}
	if authorizationCardFingerprint(command.Card, s.fingerprintSecret) != payment.AuthorizationCardFingerprint() {
		s.releaseIdempotency(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}

	bankResult, err := s.bank.AuthorizePayment(ctx, BankAuthorizationRequest{
		OperationKey: payment.AuthorizationBankOperationKey(),
		OrderID:      payment.OrderID(),
		CustomerID:   payment.CustomerID(),
		AmountCents:  payment.AmountCents(),
		Currency:     payment.Currency(),
		Card:         command.Card,
	})

	now := s.clock.Now()
	if isUnknownAuthorizationOutcome(err) {
		err = payment.MarkPending(now)
	} else if err != nil {
		s.releaseIdempotency(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	} else if bankResult.DeclineReason != "" {
		err = payment.MarkDeclined(bankResult.DeclineReason, now)
	} else {
		err = payment.MarkAuthorized(bankResult.BankAuthorizationID, now)
	}
	if err != nil {
		s.releaseIdempotency(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if err := s.paymentRepository.UpdateAuthorizationResult(ctx, payment); err != nil {
		s.releaseIdempotency(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.idempotency.Complete(ctx, IdempotencyRecord{
		Operation:          operation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: requestFingerprint,
		Result:             result,
		ResponseStatus:     result.ResponseStatus,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) CapturePayment(ctx context.Context, command CapturePaymentCommand) (PaymentResult, error) {
	command = normalizeCapturePaymentCommand(command)
	if command.IdempotencyKey == "" {
		return PaymentResult{}, NewInvalidPaymentInput("idempotency key is required", nil)
	}
	if command.PaymentID == "" {
		return PaymentResult{}, NewInvalidPaymentInput("payment id is required", nil)
	}

	fingerprint := capturePaymentRequestFingerprint(command, s.fingerprintSecret)
	record, claimed, err := s.claimIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey, fingerprint)
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return record.Result, nil
	}

	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		s.releaseIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if payment.Status() != domain.PaymentStatusAuthorized {
		s.releaseIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}

	bankOperationKey, err := s.captureBankOperationKey(ctx, payment)
	if err != nil {
		s.releaseIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, err
	}
	bankResult, err := s.bank.CapturePayment(ctx, BankCaptureRequest{
		OperationKey:        bankOperationKey,
		BankAuthorizationID: payment.BankAuthorizationID(),
		AmountCents:         payment.AmountCents(),
		Currency:            payment.Currency(),
	})
	if err != nil {
		s.releaseIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := payment.Capture(bankResult.BankCaptureID, bankOperationKey, s.clock.Now()); err != nil {
		s.releaseIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if err := s.paymentRepository.UpdateCaptureResult(ctx, payment); err != nil {
		s.releaseIdempotency(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.idempotency.Complete(ctx, IdempotencyRecord{
		Operation:          capturePaymentOperation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		Result:             result,
		ResponseStatus:     result.ResponseStatus,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) GetPayment(ctx context.Context, query GetPaymentQuery) (PaymentResult, error) {
	query = normalizeGetPaymentQuery(query)
	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(query.PaymentID))
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	return newPaymentResult(payment), nil
}

func (s *PaymentService) SearchPayments(ctx context.Context, query SearchPaymentsQuery) ([]PaymentResult, error) {
	query = normalizeSearchPaymentsQuery(query)
	if query.OrderID == "" && query.CustomerID == "" {
		return nil, NewInvalidPaymentInput("order id or customer id is required", nil)
	}
	if query.Status != "" && !isValidPaymentStatus(query.Status) {
		return nil, NewInvalidPaymentInput("payment status is invalid", nil)
	}

	payments, err := s.paymentRepository.Search(ctx, query)
	if err != nil {
		return nil, asPaymentError(err)
	}

	results := make([]PaymentResult, 0, len(payments))
	for _, payment := range payments {
		results = append(results, newPaymentResult(payment))
	}
	return results, nil
}

func (s *PaymentService) VoidPayment(ctx context.Context, command VoidPaymentCommand) (PaymentResult, error) {
	command = normalizeVoidPaymentCommand(command)
	if command.IdempotencyKey == "" {
		return PaymentResult{}, NewInvalidPaymentInput("idempotency key is required", nil)
	}
	if command.PaymentID == "" {
		return PaymentResult{}, NewInvalidPaymentInput("payment id is required", nil)
	}

	fingerprint := voidPaymentRequestFingerprint(command, s.fingerprintSecret)
	record, claimed, err := s.claimIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey, fingerprint)
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return record.Result, nil
	}

	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		s.releaseIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if payment.Status() != domain.PaymentStatusAuthorized {
		s.releaseIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}

	bankOperationKey, err := s.voidBankOperationKey(ctx, payment)
	if err != nil {
		s.releaseIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, err
	}
	bankResult, err := s.bank.VoidPayment(ctx, BankVoidRequest{
		OperationKey:        bankOperationKey,
		BankAuthorizationID: payment.BankAuthorizationID(),
	})
	if err != nil {
		s.releaseIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := payment.MarkVoided(bankResult.BankVoidID, bankOperationKey, s.clock.Now()); err != nil {
		s.releaseIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if err := s.paymentRepository.UpdateVoidResult(ctx, payment); err != nil {
		s.releaseIdempotency(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.idempotency.Complete(ctx, IdempotencyRecord{
		Operation:          voidPaymentOperation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		Result:             result,
		ResponseStatus:     result.ResponseStatus,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) RefundPayment(ctx context.Context, command RefundPaymentCommand) (PaymentResult, error) {
	command = normalizeRefundPaymentCommand(command)
	if command.IdempotencyKey == "" {
		return PaymentResult{}, NewInvalidPaymentInput("idempotency key is required", nil)
	}
	if command.PaymentID == "" {
		return PaymentResult{}, NewInvalidPaymentInput("payment id is required", nil)
	}

	fingerprint := refundPaymentRequestFingerprint(command, s.fingerprintSecret)
	record, claimed, err := s.claimIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey, fingerprint)
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return record.Result, nil
	}

	payment, err := s.paymentRepository.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		s.releaseIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if payment.Status() != domain.PaymentStatusCaptured {
		s.releaseIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}

	bankOperationKey, err := s.refundBankOperationKey(ctx, payment)
	if err != nil {
		s.releaseIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, err
	}
	bankResult, err := s.bank.RefundPayment(ctx, BankRefundRequest{
		OperationKey:  bankOperationKey,
		BankCaptureID: payment.BankCaptureID(),
		AmountCents:   payment.AmountCents(),
		Currency:      payment.Currency(),
	})
	if err != nil {
		s.releaseIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := payment.Refund(bankResult.BankRefundID, bankOperationKey, s.clock.Now()); err != nil {
		s.releaseIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}
	if err := s.paymentRepository.UpdateRefundResult(ctx, payment); err != nil {
		s.releaseIdempotency(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.idempotency.Complete(ctx, IdempotencyRecord{
		Operation:          refundPaymentOperation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		Result:             result,
		ResponseStatus:     result.ResponseStatus,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) claimIdempotency(ctx context.Context, operation string, key string, requestFingerprint string) (IdempotencyRecord, bool, error) {
	record, status, err := s.idempotency.Claim(ctx, operation, key, requestFingerprint)
	if err != nil {
		return IdempotencyRecord{}, false, asPaymentError(err)
	}
	if status == IdempotencyClaimed {
		return record, true, nil
	}
	if record.RequestFingerprint != requestFingerprint {
		return IdempotencyRecord{}, false, NewPaymentIdempotencyConflict(nil)
	}
	if status == IdempotencyInProgress {
		return IdempotencyRecord{}, false, NewPaymentIdempotencyInProgress(nil)
	}
	return record, false, nil
}

func (s *PaymentService) releaseIdempotency(ctx context.Context, operation string, key string) {
	_ = s.idempotency.Release(ctx, operation, key)
}

func (s *PaymentService) captureBankOperationKey(ctx context.Context, payment *domain.Payment) (string, error) {
	if payment.CaptureBankOperationKey() != "" {
		return payment.CaptureBankOperationKey(), nil
	}
	if err := payment.SetCaptureBankOperationKey(s.bankOperationKeys.NewBankOperationKey()); err != nil {
		return "", asPaymentError(err)
	}
	if err := s.paymentRepository.SaveCaptureBankOperationKey(ctx, payment); err != nil {
		return "", asPaymentError(err)
	}
	return payment.CaptureBankOperationKey(), nil
}

func (s *PaymentService) voidBankOperationKey(ctx context.Context, payment *domain.Payment) (string, error) {
	if payment.VoidBankOperationKey() != "" {
		return payment.VoidBankOperationKey(), nil
	}
	if err := payment.SetVoidBankOperationKey(s.bankOperationKeys.NewBankOperationKey()); err != nil {
		return "", asPaymentError(err)
	}
	if err := s.paymentRepository.SaveVoidBankOperationKey(ctx, payment); err != nil {
		return "", asPaymentError(err)
	}
	return payment.VoidBankOperationKey(), nil
}

func (s *PaymentService) refundBankOperationKey(ctx context.Context, payment *domain.Payment) (string, error) {
	if payment.RefundBankOperationKey() != "" {
		return payment.RefundBankOperationKey(), nil
	}
	if err := payment.SetRefundBankOperationKey(s.bankOperationKeys.NewBankOperationKey()); err != nil {
		return "", asPaymentError(err)
	}
	if err := s.paymentRepository.SaveRefundBankOperationKey(ctx, payment); err != nil {
		return "", asPaymentError(err)
	}
	return payment.RefundBankOperationKey(), nil
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

func normalizeRetryAuthorizationCommand(command RetryAuthorizationCommand) RetryAuthorizationCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	command.Card.Number = strings.TrimSpace(command.Card.Number)
	command.Card.CVV = strings.TrimSpace(command.Card.CVV)
	return command
}

func normalizeCapturePaymentCommand(command CapturePaymentCommand) CapturePaymentCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	return command
}

func normalizeVoidPaymentCommand(command VoidPaymentCommand) VoidPaymentCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	return command
}

func normalizeRefundPaymentCommand(command RefundPaymentCommand) RefundPaymentCommand {
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.PaymentID = strings.TrimSpace(command.PaymentID)
	return command
}

func normalizeGetPaymentQuery(query GetPaymentQuery) GetPaymentQuery {
	query.PaymentID = strings.TrimSpace(query.PaymentID)
	return query
}

func normalizeSearchPaymentsQuery(query SearchPaymentsQuery) SearchPaymentsQuery {
	query.OrderID = strings.TrimSpace(query.OrderID)
	query.CustomerID = strings.TrimSpace(query.CustomerID)
	query.Status = strings.TrimSpace(query.Status)
	return query
}

func isValidPaymentStatus(status string) bool {
	switch domain.PaymentStatus(status) {
	case domain.PaymentStatusPending, domain.PaymentStatusAuthorized, domain.PaymentStatusDeclined, domain.PaymentStatusCaptured, domain.PaymentStatusVoided, domain.PaymentStatusRefunded:
		return true
	default:
		return false
	}
}

func authorizePaymentRequestFingerprint(command AuthorizePaymentCommand, secret string) string {
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

func retryAuthorizationRequestFingerprint(command RetryAuthorizationCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%d\n%d",
		retryAuthorizationOperation,
		command.PaymentID,
		command.Card.Number,
		command.Card.ExpiryMonth,
		command.Card.ExpiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func capturePaymentRequestFingerprint(command CapturePaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", capturePaymentOperation, command.PaymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func voidPaymentRequestFingerprint(command VoidPaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", voidPaymentOperation, command.PaymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func refundPaymentRequestFingerprint(command RefundPaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", refundPaymentOperation, command.PaymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func authorizationCardFingerprint(card CardDetails, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%d\n%d",
		"authorization",
		card.Number,
		card.ExpiryMonth,
		card.ExpiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func isUnknownAuthorizationOutcome(err error) bool {
	return IsPaymentErrorKind(err, PaymentErrorBankTimeout) || IsPaymentErrorKind(err, PaymentErrorBankUnavailable)
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
