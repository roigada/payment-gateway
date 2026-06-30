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
	AuthorizePaymentOperation   = "authorize_payment"
	RetryAuthorizationOperation = "retry_authorization"
	CapturePaymentOperation     = "capture_payment"
	VoidPaymentOperation        = "void_payment"
	RefundPaymentOperation      = "refund_payment"
)

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
}

type PaymentCommandResult struct {
	Payment    PaymentResult
	HTTPStatus int
}

type PaymentStore interface {
	FindByID(ctx context.Context, id domain.PaymentID) (*domain.Payment, error)
	ExpireAuthorization(ctx context.Context, payment *domain.Payment, expectedStatus domain.PaymentStatus) error
	RefreshExpiredAuthorizations(ctx context.Context, query SearchPaymentsQuery, now time.Time) error
	Search(ctx context.Context, query SearchPaymentsQuery) ([]*domain.Payment, error)
	ClaimAuthorizationStart(ctx context.Context, request ClaimAuthorizationStartInput) (PaymentCommandClaim, error)
	ClaimAuthorizationRetry(ctx context.Context, request ClaimAuthorizationRetryInput) (PaymentCommandClaim, error)
	ClaimCapture(ctx context.Context, request ClaimCaptureInput) (PaymentCommandClaim, error)
	ClaimVoid(ctx context.Context, request ClaimVoidInput) (PaymentCommandClaim, error)
	ClaimRefund(ctx context.Context, request ClaimRefundInput) (PaymentCommandClaim, error)
	CompletePaymentCommand(ctx context.Context, record IdempotencyRecord, payment *domain.Payment, expectedStatus domain.PaymentStatus) error
	ReleasePaymentCommand(ctx context.Context, operation string, key string) error
}

type IdempotencyRecord struct {
	Operation          string
	Key                string
	RequestFingerprint string
	Result             PaymentCommandResult
}

type IdempotencyClaimStatus string

const (
	IdempotencyClaimed    IdempotencyClaimStatus = "claimed"
	IdempotencyCompleted  IdempotencyClaimStatus = "completed"
	IdempotencyInProgress IdempotencyClaimStatus = "in_progress"
)

type ClaimAuthorizationStartInput struct {
	Key                string
	RequestFingerprint string
	Payment            *domain.Payment
}

type ClaimAuthorizationRetryInput struct {
	Key                          string
	RequestFingerprint           string
	PaymentID                    domain.PaymentID
	AuthorizationCardFingerprint string
}

type ClaimCaptureInput struct {
	Key                string
	RequestFingerprint string
	PaymentID          domain.PaymentID
	BankOperationKey   string
	Now                time.Time
}

type ClaimVoidInput struct {
	Key                string
	RequestFingerprint string
	PaymentID          domain.PaymentID
	BankOperationKey   string
	Now                time.Time
}

type ClaimRefundInput struct {
	Key                string
	RequestFingerprint string
	PaymentID          domain.PaymentID
	BankOperationKey   string
}

type PaymentCommandClaim struct {
	Record  IdempotencyRecord
	Status  IdempotencyClaimStatus
	Payment *domain.Payment
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
	OperationKey    string
	OrderID         string
	CustomerID      string
	AmountCents     int64
	Currency        string
	CardNumber      string
	CardCVV         string
	CardExpiryMonth int
	CardExpiryYear  int
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

func (s *PaymentService) AuthorizePayment(ctx context.Context, command AuthorizePaymentCommand) (PaymentCommandResult, error) {
	fingerprint := authorizePaymentRequestFingerprint(command, s.fingerprintSecret)
	authorizationCardFingerprint := authorizationCardFingerprint(command.card, s.fingerprintSecret)
	paymentID := s.paymentIDs.NewPaymentID()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	now := s.clock.Now()
	payment, err := domain.NewPendingPayment(paymentID, command.orderID, command.customerID, command.amountCents, bankOperationKey, authorizationCardFingerprint, now)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	claim, err := s.store.ClaimAuthorizationStart(ctx, ClaimAuthorizationStartInput{
		Key:                command.idempotencyKey,
		RequestFingerprint: fingerprint,
		Payment:            payment,
	})
	claim, claimed, err := s.ensurePaymentCommandClaim(claim, fingerprint, err)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if !claimed {
		return claim.Record.Result, nil
	}

	bankResult, err := s.bank.AuthorizePayment(ctx, BankAuthorizationRequest{
		OperationKey:    bankOperationKey,
		OrderID:         command.orderID,
		CustomerID:      command.customerID,
		AmountCents:     command.amountCents,
		Currency:        domain.CurrencyUSD,
		CardNumber:      command.card.number,
		CardCVV:         command.card.cvv,
		CardExpiryMonth: command.card.expiryMonth,
		CardExpiryYear:  command.card.expiryYear,
	})
	if isUnknownAuthorizationOutcome(err) {
		s.releasePaymentCommand(ctx, AuthorizePaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, err, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, AuthorizePaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 201,
	}
	if err := s.completePaymentCommand(
		ctx,
		IdempotencyRecord{
			Operation:          AuthorizePaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
		},
		payment,
		domain.PaymentStatusPending,
	); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) RetryAuthorization(ctx context.Context, command RetryAuthorizationCommand) (PaymentCommandResult, error) {
	operation := RetryAuthorizationOperation
	requestFingerprint := retryAuthorizationRequestFingerprint(command, s.fingerprintSecret)
	claim, err := s.store.ClaimAuthorizationRetry(ctx, ClaimAuthorizationRetryInput{
		Key:                          command.idempotencyKey,
		RequestFingerprint:           requestFingerprint,
		PaymentID:                    command.paymentID,
		AuthorizationCardFingerprint: authorizationCardFingerprint(command.card, s.fingerprintSecret),
	})
	claim, claimed, err := s.ensurePaymentCommandClaim(claim, requestFingerprint, err)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if !claimed {
		return claim.Record.Result, nil
	}

	payment := claim.Payment

	bankResult, err := s.bank.AuthorizePayment(ctx, BankAuthorizationRequest{
		OperationKey:    payment.AuthorizationBankOperationKey(),
		OrderID:         payment.OrderID(),
		CustomerID:      payment.CustomerID(),
		AmountCents:     payment.AmountCents(),
		Currency:        payment.Currency(),
		CardNumber:      command.card.number,
		CardCVV:         command.card.cvv,
		CardExpiryMonth: command.card.expiryMonth,
		CardExpiryYear:  command.card.expiryYear,
	})
	if isUnknownAuthorizationOutcome(err) {
		s.releasePaymentCommand(ctx, operation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, err, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, operation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(
		ctx,
		IdempotencyRecord{
			Operation:          operation,
			Key:                command.idempotencyKey,
			RequestFingerprint: requestFingerprint,
			Result:             result,
		},
		payment,
		domain.PaymentStatusPending,
	); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) CapturePayment(ctx context.Context, command CapturePaymentCommand) (PaymentCommandResult, error) {
	fingerprint := capturePaymentRequestFingerprint(command, s.fingerprintSecret)
	now := s.clock.Now()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	claim, err := s.store.ClaimCapture(ctx, ClaimCaptureInput{
		Key:                command.idempotencyKey,
		RequestFingerprint: fingerprint,
		PaymentID:          command.paymentID,
		BankOperationKey:   bankOperationKey,
		Now:                now,
	})
	claim, claimed, err := s.ensurePaymentCommandClaim(claim, fingerprint, err)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if !claimed {
		return claim.Record.Result, nil
	}
	payment := claim.Payment
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
				s.releasePaymentCommand(ctx, CapturePaymentOperation, command.idempotencyKey)
				return PaymentCommandResult{}, ensurePaymentError(markErr)
			}
			result := PaymentCommandResult{
				Payment:    newPaymentResult(payment),
				HTTPStatus: 409,
			}
			if completeErr := s.completePaymentCommand(
				ctx,
				IdempotencyRecord{
					Operation:          CapturePaymentOperation,
					Key:                command.idempotencyKey,
					RequestFingerprint: fingerprint,
					Result:             result,
				},
				payment,
				domain.PaymentStatusAuthorized,
			); completeErr != nil {
				return PaymentCommandResult{}, completeErr
			}
			return PaymentCommandResult{}, NewPaymentInvalidStatusConflictError(nil)
		}
		s.releasePaymentCommand(ctx, CapturePaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.Capture(bankResult.BankCaptureID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, CapturePaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(
		ctx,
		IdempotencyRecord{
			Operation:          CapturePaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
		},
		payment,
		domain.PaymentStatusAuthorized,
	); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) VoidPayment(ctx context.Context, command VoidPaymentCommand) (PaymentCommandResult, error) {
	fingerprint := voidPaymentRequestFingerprint(command, s.fingerprintSecret)
	now := s.clock.Now()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	claim, err := s.store.ClaimVoid(ctx, ClaimVoidInput{
		Key:                command.idempotencyKey,
		RequestFingerprint: fingerprint,
		PaymentID:          command.paymentID,
		BankOperationKey:   bankOperationKey,
		Now:                now,
	})
	claim, claimed, err := s.ensurePaymentCommandClaim(claim, fingerprint, err)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if !claimed {
		return claim.Record.Result, nil
	}
	payment := claim.Payment
	bankOperationKey = payment.VoidBankOperationKey()
	bankResult, err := s.bank.VoidPayment(ctx, BankVoidRequest{
		OperationKey:        bankOperationKey,
		BankAuthorizationID: payment.BankAuthorizationID(),
	})
	if err != nil {
		if HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			if markErr := payment.MarkExpired(s.clock.Now()); markErr != nil {
				s.releasePaymentCommand(ctx, VoidPaymentOperation, command.idempotencyKey)
				return PaymentCommandResult{}, ensurePaymentError(markErr)
			}
			result := PaymentCommandResult{
				Payment:    newPaymentResult(payment),
				HTTPStatus: 409,
			}
			if completeErr := s.completePaymentCommand(
				ctx,
				IdempotencyRecord{
					Operation:          VoidPaymentOperation,
					Key:                command.idempotencyKey,
					RequestFingerprint: fingerprint,
					Result:             result,
				},
				payment,
				domain.PaymentStatusAuthorized,
			); completeErr != nil {
				return PaymentCommandResult{}, completeErr
			}
			return PaymentCommandResult{}, NewPaymentInvalidStatusConflictError(nil)
		}
		s.releasePaymentCommand(ctx, VoidPaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.MarkVoided(bankResult.BankVoidID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, VoidPaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(
		ctx,
		IdempotencyRecord{
			Operation:          VoidPaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
		},
		payment,
		domain.PaymentStatusAuthorized,
	); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) RefundPayment(ctx context.Context, command RefundPaymentCommand) (PaymentCommandResult, error) {
	fingerprint := refundPaymentRequestFingerprint(command, s.fingerprintSecret)
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	claim, err := s.store.ClaimRefund(ctx, ClaimRefundInput{
		Key:                command.idempotencyKey,
		RequestFingerprint: fingerprint,
		PaymentID:          command.paymentID,
		BankOperationKey:   bankOperationKey,
	})
	claim, claimed, err := s.ensurePaymentCommandClaim(claim, fingerprint, err)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if !claimed {
		return claim.Record.Result, nil
	}
	payment := claim.Payment
	bankOperationKey = payment.RefundBankOperationKey()
	bankResult, err := s.bank.RefundPayment(ctx, BankRefundRequest{
		OperationKey:  bankOperationKey,
		BankCaptureID: payment.BankCaptureID(),
		AmountCents:   payment.AmountCents(),
		Currency:      payment.Currency(),
	})
	if err != nil {
		s.releasePaymentCommand(ctx, RefundPaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.Refund(bankResult.BankRefundID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, RefundPaymentOperation, command.idempotencyKey)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(
		ctx,
		IdempotencyRecord{
			Operation:          RefundPaymentOperation,
			Key:                command.idempotencyKey,
			RequestFingerprint: fingerprint,
			Result:             result,
		},
		payment,
		domain.PaymentStatusCaptured,
	); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
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

func (s *PaymentService) ensurePaymentCommandClaim(claim PaymentCommandClaim, requestFingerprint string, err error) (PaymentCommandClaim, bool, error) {
	if err != nil {
		return PaymentCommandClaim{}, false, ensurePaymentError(err)
	}
	if claim.Status == IdempotencyClaimed {
		return claim, true, nil
	}
	if claim.Record.RequestFingerprint != requestFingerprint {
		return PaymentCommandClaim{}, false, NewPaymentIdempotencyConflictError(nil)
	}
	if claim.Status == IdempotencyInProgress {
		return PaymentCommandClaim{}, false, NewPaymentIdempotencyInProgressError(nil)
	}
	return claim, false, nil
}

func (s *PaymentService) completePaymentCommand(ctx context.Context, record IdempotencyRecord, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	return ensurePaymentError(s.store.CompletePaymentCommand(ctx, record, payment, expectedStatus))
}

func (s *PaymentService) releasePaymentCommand(ctx context.Context, operation string, key string) {
	_ = s.store.ReleasePaymentCommand(ctx, operation, key)
}

func isValidPaymentStatus(status domain.PaymentStatus) bool {
	switch status {
	case domain.PaymentStatusPending, domain.PaymentStatusAuthorized, domain.PaymentStatusExpired, domain.PaymentStatusDeclined, domain.PaymentStatusCaptured, domain.PaymentStatusVoided, domain.PaymentStatusRefunded:
		return true
	default:
		return false
	}
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

func authorizePaymentRequestFingerprint(command AuthorizePaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%d\n%s\n%d\n%d",
		AuthorizePaymentOperation,
		command.orderID,
		command.customerID,
		command.amountCents,
		command.card.number,
		command.card.expiryMonth,
		command.card.expiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func retryAuthorizationRequestFingerprint(command RetryAuthorizationCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%s\n%d\n%d",
		RetryAuthorizationOperation,
		command.paymentID,
		command.card.number,
		command.card.expiryMonth,
		command.card.expiryYear,
	)
	return hex.EncodeToString(hash.Sum(nil))
}

func capturePaymentRequestFingerprint(command CapturePaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", CapturePaymentOperation, command.paymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func voidPaymentRequestFingerprint(command VoidPaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", VoidPaymentOperation, command.paymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func refundPaymentRequestFingerprint(command RefundPaymentCommand, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(hash, "%s\n%s", RefundPaymentOperation, command.paymentID)
	return hex.EncodeToString(hash.Sum(nil))
}

func authorizationCardFingerprint(card cardDetails, secret string) string {
	hash := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(
		hash,
		"%s\n%s\n%d\n%d",
		"authorization",
		card.number,
		card.expiryMonth,
		card.expiryYear,
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
