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
	ClaimPaymentCommand(ctx context.Context, request PaymentCommandClaimRequest) (PaymentCommandClaim, error)
	CompletePaymentCommand(ctx context.Context, claim PaymentCommandClaim, result PaymentCommandResult) error
	ReleasePaymentCommand(ctx context.Context, claim PaymentCommandClaim) error
}

type PaymentCommandClaimRequest struct {
	operation                    string
	key                          string
	requestFingerprint           string
	payment                      *domain.Payment
	paymentID                    domain.PaymentID
	expectedStatus               domain.PaymentStatus
	bankOperationKeyKind         BankOperationKeyKind
	bankOperationKey             string
	authorizationCardFingerprint string
	now                          time.Time
}

func NewAuthorizationStartClaim(key string, requestFingerprint string, payment *domain.Payment) PaymentCommandClaimRequest {
	return PaymentCommandClaimRequest{
		operation:          AuthorizePaymentOperation,
		key:                key,
		requestFingerprint: requestFingerprint,
		payment:            payment,
		expectedStatus:     domain.PaymentStatusPending,
	}
}

func NewAuthorizationRetryClaim(key string, requestFingerprint string, paymentID domain.PaymentID, authorizationCardFingerprint string) PaymentCommandClaimRequest {
	return PaymentCommandClaimRequest{
		operation:                    RetryAuthorizationOperation,
		key:                          key,
		requestFingerprint:           requestFingerprint,
		paymentID:                    paymentID,
		expectedStatus:               domain.PaymentStatusPending,
		authorizationCardFingerprint: authorizationCardFingerprint,
	}
}

func NewCaptureClaim(key string, requestFingerprint string, paymentID domain.PaymentID, bankOperationKey string, now time.Time) PaymentCommandClaimRequest {
	return PaymentCommandClaimRequest{
		operation:            CapturePaymentOperation,
		key:                  key,
		requestFingerprint:   requestFingerprint,
		paymentID:            paymentID,
		expectedStatus:       domain.PaymentStatusAuthorized,
		bankOperationKeyKind: BankOperationKeyCapture,
		bankOperationKey:     bankOperationKey,
		now:                  now,
	}
}

func NewVoidClaim(key string, requestFingerprint string, paymentID domain.PaymentID, bankOperationKey string, now time.Time) PaymentCommandClaimRequest {
	return PaymentCommandClaimRequest{
		operation:            VoidPaymentOperation,
		key:                  key,
		requestFingerprint:   requestFingerprint,
		paymentID:            paymentID,
		expectedStatus:       domain.PaymentStatusAuthorized,
		bankOperationKeyKind: BankOperationKeyVoid,
		bankOperationKey:     bankOperationKey,
		now:                  now,
	}
}

func NewRefundClaim(key string, requestFingerprint string, paymentID domain.PaymentID, bankOperationKey string) PaymentCommandClaimRequest {
	return PaymentCommandClaimRequest{
		operation:            RefundPaymentOperation,
		key:                  key,
		requestFingerprint:   requestFingerprint,
		paymentID:            paymentID,
		expectedStatus:       domain.PaymentStatusCaptured,
		bankOperationKeyKind: BankOperationKeyRefund,
		bankOperationKey:     bankOperationKey,
	}
}

func (r PaymentCommandClaimRequest) Operation() string                    { return r.operation }
func (r PaymentCommandClaimRequest) Key() string                          { return r.key }
func (r PaymentCommandClaimRequest) RequestFingerprint() string           { return r.requestFingerprint }
func (r PaymentCommandClaimRequest) Payment() *domain.Payment             { return r.payment }
func (r PaymentCommandClaimRequest) PaymentID() domain.PaymentID          { return r.paymentID }
func (r PaymentCommandClaimRequest) ExpectedStatus() domain.PaymentStatus { return r.expectedStatus }
func (r PaymentCommandClaimRequest) BankOperationKeyKind() BankOperationKeyKind {
	return r.bankOperationKeyKind
}
func (r PaymentCommandClaimRequest) BankOperationKey() string { return r.bankOperationKey }
func (r PaymentCommandClaimRequest) AuthorizationCardFingerprint() string {
	return r.authorizationCardFingerprint
}
func (r PaymentCommandClaimRequest) Now() time.Time { return r.now }

type PaymentCommandClaim struct {
	operation          string
	key                string
	requestFingerprint string
	expectedStatus     domain.PaymentStatus
	payment            *domain.Payment
	replayResult       PaymentCommandResult
	replay             bool
}

func NewClaimedPaymentCommand(request PaymentCommandClaimRequest, payment *domain.Payment) PaymentCommandClaim {
	return PaymentCommandClaim{
		operation:          request.operation,
		key:                request.key,
		requestFingerprint: request.requestFingerprint,
		expectedStatus:     request.expectedStatus,
		payment:            payment,
	}
}

func NewReplayedPaymentCommand(request PaymentCommandClaimRequest, result PaymentCommandResult) PaymentCommandClaim {
	return PaymentCommandClaim{
		operation:          request.operation,
		key:                request.key,
		requestFingerprint: request.requestFingerprint,
		expectedStatus:     request.expectedStatus,
		replayResult:       result,
		replay:             true,
	}
}

func (c PaymentCommandClaim) Operation() string                    { return c.operation }
func (c PaymentCommandClaim) Key() string                          { return c.key }
func (c PaymentCommandClaim) RequestFingerprint() string           { return c.requestFingerprint }
func (c PaymentCommandClaim) ExpectedStatus() domain.PaymentStatus { return c.expectedStatus }
func (c PaymentCommandClaim) Payment() *domain.Payment             { return c.payment }
func (c PaymentCommandClaim) ReplayResult() (PaymentCommandResult, bool) {
	return c.replayResult, c.replay
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
	claim, err := s.store.ClaimPaymentCommand(ctx, NewAuthorizationStartClaim(command.idempotencyKey, fingerprint, payment))
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if replayed, ok := claim.ReplayResult(); ok {
		return replayed, nil
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
	if err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 201,
	}
	if err := s.completePaymentCommand(ctx, claim, result); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) RetryAuthorization(ctx context.Context, command RetryAuthorizationCommand) (PaymentCommandResult, error) {
	requestFingerprint := retryAuthorizationRequestFingerprint(command, s.fingerprintSecret)
	claim, err := s.store.ClaimPaymentCommand(ctx, NewAuthorizationRetryClaim(
		command.idempotencyKey,
		requestFingerprint,
		command.paymentID,
		authorizationCardFingerprint(command.card, s.fingerprintSecret),
	))
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if replayed, ok := claim.ReplayResult(); ok {
		return replayed, nil
	}

	payment := claim.Payment()

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
	if err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(ctx, claim, result); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) CapturePayment(ctx context.Context, command CapturePaymentCommand) (PaymentCommandResult, error) {
	fingerprint := capturePaymentRequestFingerprint(command, s.fingerprintSecret)
	now := s.clock.Now()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	claim, err := s.store.ClaimPaymentCommand(ctx, NewCaptureClaim(command.idempotencyKey, fingerprint, command.paymentID, bankOperationKey, now))
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if replayed, ok := claim.ReplayResult(); ok {
		return replayed, nil
	}
	payment := claim.Payment()
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
				s.releasePaymentCommand(ctx, claim)
				return PaymentCommandResult{}, ensurePaymentError(markErr)
			}
			result := PaymentCommandResult{
				Payment:    newPaymentResult(payment),
				HTTPStatus: 409,
			}
			if completeErr := s.completePaymentCommand(ctx, claim, result); completeErr != nil {
				return PaymentCommandResult{}, completeErr
			}
			return PaymentCommandResult{}, NewPaymentInvalidStatusConflictError(nil)
		}
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.Capture(bankResult.BankCaptureID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(ctx, claim, result); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) VoidPayment(ctx context.Context, command VoidPaymentCommand) (PaymentCommandResult, error) {
	fingerprint := voidPaymentRequestFingerprint(command, s.fingerprintSecret)
	now := s.clock.Now()
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	claim, err := s.store.ClaimPaymentCommand(ctx, NewVoidClaim(command.idempotencyKey, fingerprint, command.paymentID, bankOperationKey, now))
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if replayed, ok := claim.ReplayResult(); ok {
		return replayed, nil
	}
	payment := claim.Payment()
	bankOperationKey = payment.VoidBankOperationKey()
	bankResult, err := s.bank.VoidPayment(ctx, BankVoidRequest{
		OperationKey:        bankOperationKey,
		BankAuthorizationID: payment.BankAuthorizationID(),
	})
	if err != nil {
		if HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
			if markErr := payment.MarkExpired(s.clock.Now()); markErr != nil {
				s.releasePaymentCommand(ctx, claim)
				return PaymentCommandResult{}, ensurePaymentError(markErr)
			}
			result := PaymentCommandResult{
				Payment:    newPaymentResult(payment),
				HTTPStatus: 409,
			}
			if completeErr := s.completePaymentCommand(ctx, claim, result); completeErr != nil {
				return PaymentCommandResult{}, completeErr
			}
			return PaymentCommandResult{}, NewPaymentInvalidStatusConflictError(nil)
		}
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.MarkVoided(bankResult.BankVoidID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(ctx, claim, result); err != nil {
		return PaymentCommandResult{}, err
	}

	return result, nil
}

func (s *PaymentService) RefundPayment(ctx context.Context, command RefundPaymentCommand) (PaymentCommandResult, error) {
	fingerprint := refundPaymentRequestFingerprint(command, s.fingerprintSecret)
	bankOperationKey := s.bankOperationKeys.NewBankOperationKey()
	claim, err := s.store.ClaimPaymentCommand(ctx, NewRefundClaim(command.idempotencyKey, fingerprint, command.paymentID, bankOperationKey))
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if replayed, ok := claim.ReplayResult(); ok {
		return replayed, nil
	}
	payment := claim.Payment()
	bankOperationKey = payment.RefundBankOperationKey()
	bankResult, err := s.bank.RefundPayment(ctx, BankRefundRequest{
		OperationKey:  bankOperationKey,
		BankCaptureID: payment.BankCaptureID(),
		AmountCents:   payment.AmountCents(),
		Currency:      payment.Currency(),
	})
	if err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.Refund(bankResult.BankRefundID, bankOperationKey, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result := PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.completePaymentCommand(ctx, claim, result); err != nil {
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

func (s *PaymentService) completePaymentCommand(ctx context.Context, claim PaymentCommandClaim, result PaymentCommandResult) error {
	return ensurePaymentError(s.store.CompletePaymentCommand(ctx, claim, result))
}

func (s *PaymentService) releasePaymentCommand(ctx context.Context, claim PaymentCommandClaim) {
	_ = s.store.ReleasePaymentCommand(ctx, claim)
}

func isValidPaymentStatus(status domain.PaymentStatus) bool {
	switch status {
	case domain.PaymentStatusPending, domain.PaymentStatusAuthorized, domain.PaymentStatusExpired, domain.PaymentStatusDeclined, domain.PaymentStatusCaptured, domain.PaymentStatusVoided, domain.PaymentStatusRefunded:
		return true
	default:
		return false
	}
}

func applyAuthorizationOutcome(payment *domain.Payment, bankResult BankAuthorizationResult, now time.Time) error {
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
