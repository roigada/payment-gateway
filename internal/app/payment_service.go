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
	orderID        string
	customerID     string
	amountCents    int64
	card           CardDetails
	idempotencyKey string
}

type RetryAuthorizationCommand struct {
	paymentID      domain.PaymentID
	card           CardDetails
	idempotencyKey string
}

type CapturePaymentCommand struct {
	paymentID      domain.PaymentID
	idempotencyKey string
}

type VoidPaymentCommand struct {
	paymentID      domain.PaymentID
	idempotencyKey string
}

type RefundPaymentCommand struct {
	paymentID      domain.PaymentID
	idempotencyKey string
}

type GetPaymentQuery struct {
	paymentID domain.PaymentID
}

type SearchPaymentsQuery struct {
	orderID    string
	customerID string
	status     domain.PaymentStatus
}

type CardDetails struct {
	Number      string
	CVV         string
	ExpiryMonth int
	ExpiryYear  int
}

func NewAuthorizePaymentCommand(orderID string, customerID string, amountCents int64, card CardDetails, idempotencyKey string) (AuthorizePaymentCommand, error) {
	command := AuthorizePaymentCommand{
		orderID:        strings.TrimSpace(orderID),
		customerID:     strings.TrimSpace(customerID),
		amountCents:    amountCents,
		card:           normalizeCardDetails(card),
		idempotencyKey: strings.TrimSpace(idempotencyKey),
	}
	if err := validateRequired(command.idempotencyKey, "idempotency key is required"); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	if err := validateRequired(command.orderID, "order id is required"); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	if err := validateRequired(command.customerID, "customer id is required"); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	if command.amountCents <= 0 {
		return AuthorizePaymentCommand{}, NewInvalidPaymentInputError("amount must be greater than zero", nil)
	}
	if err := validateCardDetails(command.card); err != nil {
		return AuthorizePaymentCommand{}, err
	}
	return command, nil
}

func NewRetryAuthorizationCommand(paymentID string, card CardDetails, idempotencyKey string) (RetryAuthorizationCommand, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	paymentID = strings.TrimSpace(paymentID)
	if err := validateRequired(idempotencyKey, "idempotency key is required"); err != nil {
		return RetryAuthorizationCommand{}, err
	}
	parsedPaymentID, err := parsePaymentID(paymentID)
	if err != nil {
		return RetryAuthorizationCommand{}, err
	}
	card = normalizeCardDetails(card)
	if err := validateCardDetails(card); err != nil {
		return RetryAuthorizationCommand{}, err
	}
	return RetryAuthorizationCommand{paymentID: parsedPaymentID, card: card, idempotencyKey: idempotencyKey}, nil
}

func NewCapturePaymentCommand(paymentID string, idempotencyKey string) (CapturePaymentCommand, error) {
	id, key, err := parsePaymentOperationInput(paymentID, idempotencyKey)
	if err != nil {
		return CapturePaymentCommand{}, err
	}
	return CapturePaymentCommand{paymentID: id, idempotencyKey: key}, nil
}

func NewVoidPaymentCommand(paymentID string, idempotencyKey string) (VoidPaymentCommand, error) {
	id, key, err := parsePaymentOperationInput(paymentID, idempotencyKey)
	if err != nil {
		return VoidPaymentCommand{}, err
	}
	return VoidPaymentCommand{paymentID: id, idempotencyKey: key}, nil
}

func NewRefundPaymentCommand(paymentID string, idempotencyKey string) (RefundPaymentCommand, error) {
	id, key, err := parsePaymentOperationInput(paymentID, idempotencyKey)
	if err != nil {
		return RefundPaymentCommand{}, err
	}
	return RefundPaymentCommand{paymentID: id, idempotencyKey: key}, nil
}

func NewGetPaymentQuery(paymentID string) (GetPaymentQuery, error) {
	parsedPaymentID, err := parsePaymentID(strings.TrimSpace(paymentID))
	if err != nil {
		return GetPaymentQuery{}, err
	}
	return GetPaymentQuery{paymentID: parsedPaymentID}, nil
}

func NewSearchPaymentsQuery(orderID string, customerID string, status string) (SearchPaymentsQuery, error) {
	query := SearchPaymentsQuery{
		orderID:    strings.TrimSpace(orderID),
		customerID: strings.TrimSpace(customerID),
		status:     domain.PaymentStatus(strings.TrimSpace(status)),
	}
	if query.orderID == "" && query.customerID == "" {
		return SearchPaymentsQuery{}, NewInvalidPaymentInputError("order id or customer id is required", nil)
	}
	if query.status != "" && !isValidPaymentStatus(query.status) {
		return SearchPaymentsQuery{}, NewInvalidPaymentInputError("payment status is invalid", nil)
	}
	return query, nil
}

func (q SearchPaymentsQuery) OrderID() string {
	return q.orderID
}

func (q SearchPaymentsQuery) CustomerID() string {
	return q.customerID
}

func (q SearchPaymentsQuery) Status() string {
	return string(q.status)
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
	fingerprint := authorizePaymentRequestFingerprint(command, s.fingerprintSecret)
	authorizationCardFingerprint := authorizationCardFingerprint(command.card, s.fingerprintSecret)
	paymentID := s.paymentIDs.NewPaymentID()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	now := s.clock.Now()
	payment, err := domain.NewPendingPayment(paymentID, command.orderID, command.customerID, command.amountCents, bankOperationKey, authorizationCardFingerprint, now)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:          authorizePaymentOperation,
		Key:                command.idempotencyKey,
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
		OrderID:      command.orderID,
		CustomerID:   command.customerID,
		AmountCents:  command.amountCents,
		Currency:     domain.CurrencyUSD,
		Card:         command.card,
	})
	if isUnknownAuthorizationOutcome(err) {
		s.releasePaymentCommand(ctx, authorizePaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, err, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, authorizePaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 201
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          authorizePaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusPending,
	}); err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) RetryAuthorization(ctx context.Context, command RetryAuthorizationCommand) (PaymentResult, error) {
	operation := retryAuthorizationOperation
	requestFingerprint := retryAuthorizationRequestFingerprint(command, s.fingerprintSecret)
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:                    operation,
		Key:                          command.idempotencyKey,
		RequestFingerprint:           requestFingerprint,
		PaymentID:                    command.paymentID,
		ExpectedStatus:               domain.PaymentStatusPending,
		AuthorizationCardFingerprint: authorizationCardFingerprint(command.card, s.fingerprintSecret),
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
		Card:         command.card,
	})
	if isUnknownAuthorizationOutcome(err) {
		s.releasePaymentCommand(ctx, operation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, err, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, operation, command.idempotencyKey)
		return PaymentResult{}, err
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          operation,
			Key:                command.idempotencyKey,
			RequestFingerprint: requestFingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusPending,
	}); err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) CapturePayment(ctx context.Context, command CapturePaymentCommand) (PaymentResult, error) {
	fingerprint := capturePaymentRequestFingerprint(command, s.fingerprintSecret)
	payment, err := s.store.FindByID(ctx, command.paymentID)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	now := s.clock.Now()
	if payment.Status() == domain.PaymentStatusAuthorized && payment.AuthorizationExpired(now) && payment.CaptureBankOperationKey() == "" {
		if err := payment.MarkExpired(now); err != nil {
			return PaymentResult{}, ensurePaymentError(err)
		}
		if err := s.store.ExpireAuthorization(ctx, payment, domain.PaymentStatusAuthorized); err != nil {
			return PaymentResult{}, ensurePaymentError(err)
		}
		return PaymentResult{}, NewPaymentInvalidStatusConflictError(nil)
	}

	bankOperationKey := payment.CaptureBankOperationKey()
	if bankOperationKey == "" {
		bankOperationKey = s.bankOperationKeys.NewBankOperationKey()
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:            capturePaymentOperation,
		Key:                  command.idempotencyKey,
		RequestFingerprint:   fingerprint,
		PaymentID:            command.paymentID,
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
		if HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			if markErr := payment.MarkExpired(s.clock.Now()); markErr != nil {
				s.releasePaymentCommand(ctx, capturePaymentOperation, command.idempotencyKey)
				return PaymentResult{}, ensurePaymentError(markErr)
			}
			result := newPaymentResult(payment)
			result.ResponseStatus = 409
			if completeErr := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
				Record: IdempotencyRecord{
					Operation:          capturePaymentOperation,
					Key:                command.idempotencyKey,
					RequestFingerprint: fingerprint,
					Result:             result,
					ResponseStatus:     result.ResponseStatus,
				},
				Payment:        payment,
				ExpectedStatus: domain.PaymentStatusAuthorized,
			}); completeErr != nil {
				return PaymentResult{}, ensurePaymentError(completeErr)
			}
			return PaymentResult{}, NewPaymentInvalidStatusConflictError(nil)
		}
		s.releasePaymentCommand(ctx, capturePaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	if err := payment.Capture(bankResult.BankCaptureID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, capturePaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          capturePaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusAuthorized,
	}); err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}

	return result, nil
}

func applyAuthorizationOutcome(payment *domain.Payment, bankResult BankAuthorizationResult, err error, now time.Time) error {
	if isUnknownAuthorizationOutcome(err) {
		return ensurePaymentError(payment.MarkPending(now))
	}
	if err != nil {
		return ensurePaymentError(err)
	}
	if bankResult.DeclineReason != "" {
		return ensurePaymentError(payment.MarkDeclined(bankResult.DeclineReason, now))
	}
	return ensurePaymentError(payment.MarkAuthorized(bankResult.BankAuthorizationID, bankResult.AuthorizationExpiresAt, now))
}

func (s *PaymentService) GetPayment(ctx context.Context, query GetPaymentQuery) (PaymentResult, error) {
	payment, err := s.store.FindByID(ctx, query.paymentID)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	payment, err = s.refreshPaymentExpiration(ctx, payment)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	return newPaymentResult(payment), nil
}

func (s *PaymentService) SearchPayments(ctx context.Context, query SearchPaymentsQuery) ([]PaymentResult, error) {
	if err := s.store.RefreshExpiredAuthorizations(ctx, query, s.clock.Now()); err != nil {
		return nil, ensurePaymentError(err)
	}

	payments, err := s.store.Search(ctx, query)
	if err != nil {
		return nil, ensurePaymentError(err)
	}

	results := make([]PaymentResult, 0, len(payments))
	for _, payment := range payments {
		results = append(results, newPaymentResult(payment))
	}
	return results, nil
}

func (s *PaymentService) VoidPayment(ctx context.Context, command VoidPaymentCommand) (PaymentResult, error) {
	fingerprint := voidPaymentRequestFingerprint(command, s.fingerprintSecret)
	payment, err := s.store.FindByID(ctx, command.paymentID)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	now := s.clock.Now()
	if payment.Status() == domain.PaymentStatusAuthorized && payment.AuthorizationExpired(now) && payment.VoidBankOperationKey() == "" {
		if err := payment.MarkExpired(now); err != nil {
			return PaymentResult{}, ensurePaymentError(err)
		}
		if err := s.store.ExpireAuthorization(ctx, payment, domain.PaymentStatusAuthorized); err != nil {
			return PaymentResult{}, ensurePaymentError(err)
		}
		return PaymentResult{}, NewPaymentInvalidStatusConflictError(nil)
	}

	bankOperationKey := payment.VoidBankOperationKey()
	if bankOperationKey == "" {
		bankOperationKey = s.bankOperationKeys.NewBankOperationKey()
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:            voidPaymentOperation,
		Key:                  command.idempotencyKey,
		RequestFingerprint:   fingerprint,
		PaymentID:            command.paymentID,
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
		if HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			if markErr := payment.MarkExpired(s.clock.Now()); markErr != nil {
				s.releasePaymentCommand(ctx, voidPaymentOperation, command.idempotencyKey)
				return PaymentResult{}, ensurePaymentError(markErr)
			}
			result := newPaymentResult(payment)
			result.ResponseStatus = 409
			if completeErr := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
				Record: IdempotencyRecord{
					Operation:          voidPaymentOperation,
					Key:                command.idempotencyKey,
					RequestFingerprint: fingerprint,
					Result:             result,
					ResponseStatus:     result.ResponseStatus,
				},
				Payment:        payment,
				ExpectedStatus: domain.PaymentStatusAuthorized,
			}); completeErr != nil {
				return PaymentResult{}, ensurePaymentError(completeErr)
			}
			return PaymentResult{}, NewPaymentInvalidStatusConflictError(nil)
		}
		s.releasePaymentCommand(ctx, voidPaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	if err := payment.MarkVoided(bankResult.BankVoidID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, voidPaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          voidPaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusAuthorized,
	}); err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) RefundPayment(ctx context.Context, command RefundPaymentCommand) (PaymentResult, error) {
	fingerprint := refundPaymentRequestFingerprint(command, s.fingerprintSecret)
	payment, err := s.store.FindByID(ctx, command.paymentID)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	bankOperationKey := payment.RefundBankOperationKey()
	if bankOperationKey == "" {
		bankOperationKey = s.bankOperationKeys.NewBankOperationKey()
	}
	claim, claimed, err := s.claimPaymentCommand(ctx, ClaimPaymentCommand{
		Operation:            refundPaymentOperation,
		Key:                  command.idempotencyKey,
		RequestFingerprint:   fingerprint,
		PaymentID:            command.paymentID,
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
		s.releasePaymentCommand(ctx, refundPaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	if err := payment.Refund(bankResult.BankRefundID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, refundPaymentOperation, command.idempotencyKey)
		return PaymentResult{}, ensurePaymentError(err)
	}

	result := newPaymentResult(payment)
	result.ResponseStatus = 200
	if err := s.store.CompletePaymentCommand(ctx, CompletePaymentCommand{
		Record: IdempotencyRecord{
			Operation:          refundPaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
			ResponseStatus:     result.ResponseStatus,
		},
		Payment:        payment,
		ExpectedStatus: domain.PaymentStatusCaptured,
	}); err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}

	return result, nil
}

func (s *PaymentService) claimPaymentCommand(ctx context.Context, command ClaimPaymentCommand) (PaymentCommandClaim, bool, error) {
	claim, err := s.store.ClaimPaymentCommand(ctx, command)
	if err != nil {
		return PaymentCommandClaim{}, false, ensurePaymentError(err)
	}
	if claim.Status == IdempotencyClaimed {
		return claim, true, nil
	}
	if claim.Record.RequestFingerprint != command.RequestFingerprint {
		return PaymentCommandClaim{}, false, NewPaymentIdempotencyConflictError(nil)
	}
	if claim.Status == IdempotencyInProgress {
		return PaymentCommandClaim{}, false, NewPaymentIdempotencyInProgressError(nil)
	}
	return claim, false, nil
}

func (s *PaymentService) releasePaymentCommand(ctx context.Context, operation string, key string) {
	_ = s.store.ReleasePaymentCommand(ctx, operation, key)
}

func validateCardDetails(card CardDetails) error {
	if !allDigits(card.Number) || len(card.Number) < 12 || len(card.Number) > 19 {
		return NewInvalidPaymentInputError("card details are invalid", nil)
	}
	if !allDigits(card.CVV) || len(card.CVV) < 3 || len(card.CVV) > 4 {
		return NewInvalidPaymentInputError("card details are invalid", nil)
	}
	if card.ExpiryMonth < 1 || card.ExpiryMonth > 12 {
		return NewInvalidPaymentInputError("card details are invalid", nil)
	}
	if card.ExpiryYear <= 0 {
		return NewInvalidPaymentInputError("card details are invalid", nil)
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

func validateRequired(value string, message string) error {
	if value == "" {
		return NewInvalidPaymentInputError(message, nil)
	}
	return nil
}

func parsePaymentOperationInput(paymentID string, idempotencyKey string) (domain.PaymentID, string, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	paymentID = strings.TrimSpace(paymentID)
	if err := validateRequired(idempotencyKey, "idempotency key is required"); err != nil {
		return "", "", err
	}
	parsedPaymentID, err := parsePaymentID(paymentID)
	if err != nil {
		return "", "", err
	}
	return parsedPaymentID, idempotencyKey, nil
}

func parsePaymentID(value string) (domain.PaymentID, error) {
	if err := validateRequired(value, "payment id is required"); err != nil {
		return "", err
	}
	paymentID, err := domain.ParsePaymentID(value)
	if err != nil {
		return "", NewInvalidPaymentInputError("payment id is invalid", err)
	}
	return paymentID, nil
}

func normalizeCardDetails(card CardDetails) CardDetails {
	card.Number = strings.TrimSpace(card.Number)
	card.CVV = strings.TrimSpace(card.CVV)
	return card
}

func isValidPaymentStatus(status domain.PaymentStatus) bool {
	switch status {
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
		command.orderID,
		command.customerID,
		command.amountCents,
		command.card.Number,
		command.card.ExpiryMonth,
		command.card.ExpiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func retryAuthorizationRequestFingerprint(command RetryAuthorizationCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%d\n%d",
		retryAuthorizationOperation,
		command.paymentID,
		command.card.Number,
		command.card.ExpiryMonth,
		command.card.ExpiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func capturePaymentRequestFingerprint(command CapturePaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", capturePaymentOperation, command.paymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func voidPaymentRequestFingerprint(command VoidPaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", voidPaymentOperation, command.paymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func refundPaymentRequestFingerprint(command RefundPaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", refundPaymentOperation, command.paymentID)
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
	return HasPaymentErrorKind(err, PaymentErrorBankTimeout) || HasPaymentErrorKind(err, PaymentErrorBankUnavailable)
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
