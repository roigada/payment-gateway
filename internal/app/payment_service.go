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

type BankOperationKeyKind string

const (
	BankOperationKeyCapture BankOperationKeyKind = "capture"
	BankOperationKeyVoid    BankOperationKeyKind = "void"
	BankOperationKeyRefund  BankOperationKeyKind = "refund"
)

type PaymentResult struct {
	ID                     string
	OrderID                string
	CustomerID             string
	AmountCents            int64
	Currency               string
	Status                 string
	DeclineReason          string
	AuthorizationExpiresAt time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
	ResponseStatus         int
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

type PaymentStore interface {
	FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error)
	ExpireAuthorization(ctx context.Context, payment *domain.Payment, expectedStatus domain.PaymentStatus) error
	RefreshExpiredAuthorizations(ctx context.Context, query SearchPaymentsQuery, now time.Time) error
	Search(ctx context.Context, query SearchPaymentsQuery) ([]*domain.Payment, error)
	ClaimPaymentCommand(ctx context.Context, command ClaimPaymentCommand) (PaymentCommandClaim, error)
	CompletePaymentCommand(ctx context.Context, command CompletePaymentCommand) error
	ReleasePaymentCommand(ctx context.Context, operation string, key string) error
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

type ClaimPaymentCommand struct {
	Operation                    string
	Key                          string
	RequestFingerprint           string
	Payment                      *domain.Payment
	PaymentID                    domain.PaymentID
	ExpectedStatus               domain.PaymentStatus
	BankOperationKeyKind         BankOperationKeyKind
	BankOperationKey             string
	AuthorizationCardFingerprint string
}

type PaymentCommandClaim struct {
	Record  IdempotencyRecord
	Status  IdempotencyClaimStatus
	Payment *domain.Payment
}

type CompletePaymentCommand struct {
	Record         IdempotencyRecord
	Payment        *domain.Payment
	ExpectedStatus domain.PaymentStatus
}

type PaymentIDGenerator interface {
	NewPaymentID() domain.PaymentID
}

type BankOperationKeyGenerator interface {
	NewBankOperationKey() string
}

type BankClient interface {
	AuthorizePayment(ctx context.Context, request BankAuthorizationRequest) (BankAuthorizationResult, error)
	CapturePayment(ctx context.Context, request BankCaptureRequest) (BankCaptureResult, error)
	VoidPayment(ctx context.Context, request BankVoidRequest) (BankVoidResult, error)
	RefundPayment(ctx context.Context, request BankRefundRequest) (BankRefundResult, error)
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
	BankAuthorizationID    string
	AuthorizationExpiresAt time.Time
	DeclineReason          domain.DeclineReason
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
	store             PaymentStore
	paymentIDs        PaymentIDGenerator
	bankOperationKeys BankOperationKeyGenerator
	bank              BankClient
	clock             Clock
	fingerprintSecret string
}

func NewPaymentService(
	store PaymentStore,
	paymentIDs PaymentIDGenerator,
	bankOperationKeys BankOperationKeyGenerator,
	bank BankClient,
	clock Clock,
	fingerprintSecret string,
) *PaymentService {
	if store == nil {
		panic("payment store is required")
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
		store:             store,
		paymentIDs:        paymentIDs,
		bankOperationKeys: bankOperationKeys,
		bank:              bank,
		clock:             clock,
		fingerprintSecret: fingerprintSecret,
	}
}

func (s *PaymentService) AuthorizePayment(ctx context.Context, command AuthorizePaymentCommand) (PaymentResult, error) {
	command, err := prepareAuthorizePaymentCommand(command)
	if err != nil {
		return PaymentResult{}, err
	}

	fingerprint := authorizePaymentRequestFingerprint(command, s.fingerprintSecret)
	authorizationCardFingerprint := authorizationCardFingerprint(command.Card, s.fingerprintSecret)
	paymentID := s.paymentIDs.NewPaymentID()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	now := s.clock.Now()
	payment, err := domain.NewPendingPayment(paymentID, command.OrderID, command.CustomerID, command.AmountCents, bankOperationKey, authorizationCardFingerprint, now)
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:          authorizePaymentOperation,
		Key:                command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		Payment:            payment,
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return claim.Record.Result, nil
	}

	bankResult, err := s.bank.AuthorizePayment(ctx, BankAuthorizationRequest{
		OperationKey: bankOperationKey,
		OrderID:      command.OrderID,
		CustomerID:   command.CustomerID,
		AmountCents:  command.AmountCents,
		Currency:     domain.CurrencyUSD,
		Card:         command.Card,
	})
	if isUnknownAuthorizationOutcome(err) {
		s.releasePaymentCommand(ctx, authorizePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, err, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, authorizePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 201
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          authorizePaymentOperation,
			Key:                command.IdempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusPending,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) RetryAuthorization(ctx context.Context, command RetryAuthorizationCommand) (PaymentResult, error) {
	command, err := prepareRetryAuthorizationCommand(command)
	if err != nil {
		return PaymentResult{}, err
	}

	operation := retryAuthorizationOperation
	requestFingerprint := retryAuthorizationRequestFingerprint(command, s.fingerprintSecret)
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:                    operation,
		Key:                          command.IdempotencyKey,
		RequestFingerprint:           requestFingerprint,
		PaymentID:                    domain.PaymentID(command.PaymentID),
		ExpectedStatus:               domain.PaymentStatusPending,
		AuthorizationCardFingerprint: authorizationCardFingerprint(command.Card, s.fingerprintSecret),
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return claim.Record.Result, nil
	}

	payment := claim.Payment

	bankResult, err := s.bank.AuthorizePayment(ctx, BankAuthorizationRequest{
		OperationKey: payment.AuthorizationBankOperationKey(),
		OrderID:      payment.OrderID(),
		CustomerID:   payment.CustomerID(),
		AmountCents:  payment.AmountCents(),
		Currency:     payment.Currency(),
		Card:         command.Card,
	})
	if isUnknownAuthorizationOutcome(err) {
		s.releasePaymentCommand(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, err, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, operation, command.IdempotencyKey)
		return PaymentResult{}, err
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          operation,
			Key:                command.IdempotencyKey,
			RequestFingerprint: requestFingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusPending,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) CapturePayment(ctx context.Context, command CapturePaymentCommand) (PaymentResult, error) {
	command, err := prepareCapturePaymentCommand(command)
	if err != nil {
		return PaymentResult{}, err
	}

	fingerprint := capturePaymentRequestFingerprint(command, s.fingerprintSecret)
	payment, err := s.store.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	now := s.clock.Now()
	if payment.Status() == domain.PaymentStatusAuthorized && payment.AuthorizationExpired(now) && payment.CaptureBankOperationKey() == "" {
		if err := payment.MarkExpired(now); err != nil {
			return PaymentResult{}, asPaymentError(err)
		}
		if err := s.store.ExpireAuthorization(ctx, payment, domain.PaymentStatusAuthorized); err != nil {
			return PaymentResult{}, asPaymentError(err)
		}
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}

	bankOperationKey := payment.CaptureBankOperationKey()
	if bankOperationKey == "" {
		bankOperationKey = s.bankOperationKeys.NewBankOperationKey()
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:            capturePaymentOperation,
		Key:                  command.IdempotencyKey,
		RequestFingerprint:   fingerprint,
		PaymentID:            domain.PaymentID(command.PaymentID),
		ExpectedStatus:       domain.PaymentStatusAuthorized,
		BankOperationKeyKind: BankOperationKeyCapture,
		BankOperationKey:     bankOperationKey,
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return claim.Record.Result, nil
	}
	payment = claim.Payment
	bankOperationKey = payment.CaptureBankOperationKey()
	bankResult, err := s.bank.CapturePayment(ctx, BankCaptureRequest{
		OperationKey:        bankOperationKey,
		BankAuthorizationID: payment.BankAuthorizationID(),
		AmountCents:         payment.AmountCents(),
		Currency:            payment.Currency(),
	})
	if err != nil {
		if IsPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			if markErr := payment.MarkExpired(s.clock.Now()); markErr != nil {
				s.releasePaymentCommand(ctx, capturePaymentOperation, command.IdempotencyKey)
				return PaymentResult{}, asPaymentError(markErr)
			}
			result := newPaymentResult(payment)
			result.ResponseStatus = 409
			if completeErr := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
				Record: IdempotencyRecord{
					Operation:          capturePaymentOperation,
					Key:                command.IdempotencyKey,
					RequestFingerprint: fingerprint,
					Result:             result,
					ResponseStatus:     result.ResponseStatus,
				},
				Payment:        payment,
				ExpectedStatus: domain.PaymentStatusAuthorized,
			}); completeErr != nil {
				return PaymentResult{}, asPaymentError(completeErr)
			}
			return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
		}
		s.releasePaymentCommand(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := payment.Capture(bankResult.BankCaptureID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, capturePaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          capturePaymentOperation,
			Key:                command.IdempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusAuthorized,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func applyAuthorizationOutcome(payment *domain.Payment, bankResult BankAuthorizationResult, err error, now time.Time) error {
	if isUnknownAuthorizationOutcome(err) {
		return asPaymentError(payment.MarkPending(now))
	}
	if err != nil {
		return asPaymentError(err)
	}
	if bankResult.DeclineReason != "" {
		return asPaymentError(payment.MarkDeclined(bankResult.DeclineReason, now))
	}
	return asPaymentError(payment.MarkAuthorized(bankResult.BankAuthorizationID, bankResult.AuthorizationExpiresAt, now))
}

func (s *PaymentService) GetPayment(ctx context.Context, query GetPaymentQuery) (PaymentResult, error) {
	query, err := prepareGetPaymentQuery(query)
	if err != nil {
		return PaymentResult{}, err
	}

	payment, err := s.store.FindByID(ctx, domain.PaymentID(query.PaymentID))
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	payment, err = s.refreshPaymentExpiration(ctx, payment)
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	return newPaymentResult(payment), nil
}

func (s *PaymentService) SearchPayments(ctx context.Context, query SearchPaymentsQuery) ([]PaymentResult, error) {
	query, err := prepareSearchPaymentsQuery(query)
	if err != nil {
		return nil, err
	}
	if err := s.store.RefreshExpiredAuthorizations(ctx, query, s.clock.Now()); err != nil {
		return nil, asPaymentError(err)
	}

	payments, err := s.store.Search(ctx, query)
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
	command, err := prepareVoidPaymentCommand(command)
	if err != nil {
		return PaymentResult{}, err
	}

	fingerprint := voidPaymentRequestFingerprint(command, s.fingerprintSecret)
	payment, err := s.store.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	now := s.clock.Now()
	if payment.Status() == domain.PaymentStatusAuthorized && payment.AuthorizationExpired(now) && payment.VoidBankOperationKey() == "" {
		if err := payment.MarkExpired(now); err != nil {
			return PaymentResult{}, asPaymentError(err)
		}
		if err := s.store.ExpireAuthorization(ctx, payment, domain.PaymentStatusAuthorized); err != nil {
			return PaymentResult{}, asPaymentError(err)
		}
		return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
	}

	bankOperationKey := payment.VoidBankOperationKey()
	if bankOperationKey == "" {
		bankOperationKey = s.bankOperationKeys.NewBankOperationKey()
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:            voidPaymentOperation,
		Key:                  command.IdempotencyKey,
		RequestFingerprint:   fingerprint,
		PaymentID:            domain.PaymentID(command.PaymentID),
		ExpectedStatus:       domain.PaymentStatusAuthorized,
		BankOperationKeyKind: BankOperationKeyVoid,
		BankOperationKey:     bankOperationKey,
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return claim.Record.Result, nil
	}
	payment = claim.Payment
	bankOperationKey = payment.VoidBankOperationKey()
	bankResult, err := s.bank.VoidPayment(ctx, BankVoidRequest{
		OperationKey:        bankOperationKey,
		BankAuthorizationID: payment.BankAuthorizationID(),
	})
	if err != nil {
		if IsPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			if markErr := payment.MarkExpired(s.clock.Now()); markErr != nil {
				s.releasePaymentCommand(ctx, voidPaymentOperation, command.IdempotencyKey)
				return PaymentResult{}, asPaymentError(markErr)
			}
			result := newPaymentResult(payment)
			result.ResponseStatus = 409
			if completeErr := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
				Record: IdempotencyRecord{
					Operation:          voidPaymentOperation,
					Key:                command.IdempotencyKey,
					RequestFingerprint: fingerprint,
					Result:             result,
					ResponseStatus:     result.ResponseStatus,
				},
				Payment:        payment,
				ExpectedStatus: domain.PaymentStatusAuthorized,
			}); completeErr != nil {
				return PaymentResult{}, asPaymentError(completeErr)
			}
			return PaymentResult{}, NewPaymentInvalidStatusConflict(nil)
		}
		s.releasePaymentCommand(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := payment.MarkVoided(bankResult.BankVoidID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, voidPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          voidPaymentOperation,
			Key:                command.IdempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusAuthorized,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) RefundPayment(ctx context.Context, command RefundPaymentCommand) (PaymentResult, error) {
	command, err := prepareRefundPaymentCommand(command)
	if err != nil {
		return PaymentResult{}, err
	}

	fingerprint := refundPaymentRequestFingerprint(command, s.fingerprintSecret)
	payment, err := s.store.FindByID(ctx, domain.PaymentID(command.PaymentID))
	if err != nil {
		return PaymentResult{}, asPaymentError(err)
	}
	bankOperationKey := payment.RefundBankOperationKey()
	if bankOperationKey == "" {
		bankOperationKey = s.bankOperationKeys.NewBankOperationKey()
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:            refundPaymentOperation,
		Key:                  command.IdempotencyKey,
		RequestFingerprint:   fingerprint,
		PaymentID:            domain.PaymentID(command.PaymentID),
		ExpectedStatus:       domain.PaymentStatusCaptured,
		BankOperationKeyKind: BankOperationKeyRefund,
		BankOperationKey:     bankOperationKey,
	})
	if err != nil {
		return PaymentResult{}, err
	}
	if !claimed {
		return claim.Record.Result, nil
	}
	payment = claim.Payment
	bankOperationKey = payment.RefundBankOperationKey()
	bankResult, err := s.bank.RefundPayment(ctx, BankRefundRequest{
		OperationKey:  bankOperationKey,
		BankCaptureID: payment.BankCaptureID(),
		AmountCents:   payment.AmountCents(),
		Currency:      payment.Currency(),
	})
	if err != nil {
		s.releasePaymentCommand(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	if err := payment.Refund(bankResult.BankRefundID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, refundPaymentOperation, command.IdempotencyKey)
		return PaymentResult{}, asPaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          refundPaymentOperation,
			Key:                command.IdempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusCaptured,
	}); err != nil {
		return PaymentResult{}, asPaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) claimPaymentCommand(ctx context.Context, command ClaimPaymentCommand) (PaymentCommandClaim, bool, error) {
	claim, err := s.store.ClaimPaymentCommand(ctx, command)
	if err != nil {
		return PaymentCommandClaim{}, false, asPaymentError(err)
	}
	if claim.Status == IdempotencyClaimed {
		return claim, true, nil
	}
	if claim.Record.RequestFingerprint != command.RequestFingerprint {
		return PaymentCommandClaim{}, false, NewPaymentIdempotencyConflict(nil)
	}
	if claim.Status == IdempotencyInProgress {
		return PaymentCommandClaim{}, false, NewPaymentIdempotencyInProgress(nil)
	}
	return claim, false, nil
}

func (s *PaymentService) releasePaymentCommand(ctx context.Context, operation string, key string) {
	_ = s.store.ReleasePaymentCommand(ctx, operation, key)
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

func prepareAuthorizePaymentCommand(command AuthorizePaymentCommand) (AuthorizePaymentCommand, error) {
	command = normalizeAuthorizePaymentCommand(command)
	if err := validateRequired(command.IdempotencyKey, "idempotency key is required"); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	if err := validateRequired(command.OrderID, "order id is required"); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	if err := validateRequired(command.CustomerID, "customer id is required"); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	if command.AmountCents <= 0 {
		return AuthorizePaymentCommand{}, NewInvalidPaymentInput("amount must be greater than zero", nil)
	}
	if err := validateCardDetails(command.Card); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	return command, nil
}

func prepareRetryAuthorizationCommand(command RetryAuthorizationCommand) (RetryAuthorizationCommand, error) {
	command = normalizeRetryAuthorizationCommand(command)
	if err := validateRequired(command.IdempotencyKey, "idempotency key is required"); err != nil {
		return RetryAuthorizationCommand{}, err
	}
	if err := validateRequired(command.PaymentID, "payment id is required"); err != nil {
		return RetryAuthorizationCommand{}, err
	}
	if err := validateCardDetails(command.Card); err != nil {
		return RetryAuthorizationCommand{}, err
	}
	return command, nil
}

func prepareCapturePaymentCommand(command CapturePaymentCommand) (CapturePaymentCommand, error) {
	command = normalizeCapturePaymentCommand(command)
	if err := validateRequired(command.IdempotencyKey, "idempotency key is required"); err != nil {
		return CapturePaymentCommand{}, err
	}
	if err := validateRequired(command.PaymentID, "payment id is required"); err != nil {
		return CapturePaymentCommand{}, err
	}
	return command, nil
}

func prepareVoidPaymentCommand(command VoidPaymentCommand) (VoidPaymentCommand, error) {
	command = normalizeVoidPaymentCommand(command)
	if err := validateRequired(command.IdempotencyKey, "idempotency key is required"); err != nil {
		return VoidPaymentCommand{}, err
	}
	if err := validateRequired(command.PaymentID, "payment id is required"); err != nil {
		return VoidPaymentCommand{}, err
	}
	return command, nil
}

func prepareRefundPaymentCommand(command RefundPaymentCommand) (RefundPaymentCommand, error) {
	command = normalizeRefundPaymentCommand(command)
	if err := validateRequired(command.IdempotencyKey, "idempotency key is required"); err != nil {
		return RefundPaymentCommand{}, err
	}
	if err := validateRequired(command.PaymentID, "payment id is required"); err != nil {
		return RefundPaymentCommand{}, err
	}
	return command, nil
}

func prepareGetPaymentQuery(query GetPaymentQuery) (GetPaymentQuery, error) {
	query = normalizeGetPaymentQuery(query)
	if err := validateRequired(query.PaymentID, "payment id is required"); err != nil {
		return GetPaymentQuery{}, err
	}
	return query, nil
}

func prepareSearchPaymentsQuery(query SearchPaymentsQuery) (SearchPaymentsQuery, error) {
	query = normalizeSearchPaymentsQuery(query)
	if query.OrderID == "" && query.CustomerID == "" {
		return SearchPaymentsQuery{}, NewInvalidPaymentInput("order id or customer id is required", nil)
	}
	if query.Status != "" && !isValidPaymentStatus(query.Status) {
		return SearchPaymentsQuery{}, NewInvalidPaymentInput("payment status is invalid", nil)
	}
	return query, nil
}

func validateRequired(value string, message string) error {
	if value == "" {
		return NewInvalidPaymentInput(message, nil)
	}
	return nil
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
	case domain.PaymentStatusPending, domain.PaymentStatusAuthorized, domain.PaymentStatusExpired, domain.PaymentStatusDeclined, domain.PaymentStatusCaptured, domain.PaymentStatusVoided, domain.PaymentStatusRefunded:
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
		ID:                     string(payment.ID()),
		OrderID:                payment.OrderID(),
		CustomerID:             payment.CustomerID(),
		AmountCents:            payment.AmountCents(),
		Currency:               payment.Currency(),
		Status:                 string(payment.Status()),
		DeclineReason:          string(payment.DeclineReason()),
		AuthorizationExpiresAt: payment.AuthorizationExpiresAt(),
		CreatedAt:              payment.CreatedAt(),
		UpdatedAt:              payment.UpdatedAt(),
	}
}

func (s *PaymentService) refreshPaymentExpiration(ctx context.Context, payment *domain.Payment) (*domain.Payment, error) {
	now := s.clock.Now()
	if !payment.AuthorizationExpired(now) {
		return payment, nil
	}
	if payment.CaptureBankOperationKey() != "" || payment.VoidBankOperationKey() != "" {
		return payment, nil
	}
	if err := payment.MarkExpired(now); err != nil {
		return nil, err
	}
	if err := s.store.ExpireAuthorization(ctx, payment, domain.PaymentStatusAuthorized); err != nil {
		return nil, err
	}
	return payment, nil
}
