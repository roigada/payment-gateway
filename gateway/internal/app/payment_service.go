package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

type PaymentOperationMetrics interface {
	RecordPaymentOperation(operation string, outcome string, duration time.Duration)
	RecordIdempotencyRecovery(operation string, result string)
	RecordPaymentCommandReleaseFailure(operation string)
}

const paymentOperationOutcomeReplayed = "replayed"

const idempotencyReplayWindow = 24 * time.Hour

const (
	IdempotencyRecoveryAttempted     = "attempted"
	IdempotencyRecoveryRecovered     = "recovered"
	IdempotencyRecoveryUnrecoverable = "unrecoverable"
	IdempotencyRecoveryConflict      = "conflict"
)

type PaymentService struct {
	store             PaymentStore
	paymentIDs        PaymentIDGenerator
	bankOperationKeys BankOperationKeyGenerator
	bank              BankClient
	operationMetrics  PaymentOperationMetrics
	clock             Clock
	fingerprintSecret string
	claimStuckAfter   time.Duration
}

func NewPaymentService(
	store PaymentStore,
	paymentIDs PaymentIDGenerator,
	bankOperationKeys BankOperationKeyGenerator,
	bank BankClient,
	operationMetrics PaymentOperationMetrics,
	clock Clock,
	fingerprintSecret string,
	claimStuckAfter time.Duration,
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
	if operationMetrics == nil {
		panic("payment operation metrics is required")
	}
	if clock == nil {
		panic("clock is required")
	}
	fingerprintSecret = strings.TrimSpace(fingerprintSecret)
	if fingerprintSecret == "" {
		panic("fingerprint secret is required")
	}
	if claimStuckAfter <= 0 {
		panic("idempotency claim stuck-after must be positive")
	}

	return &PaymentService{
		store:             store,
		paymentIDs:        paymentIDs,
		bankOperationKeys: bankOperationKeys,
		bank:              bank,
		operationMetrics:  operationMetrics,
		clock:             clock,
		fingerprintSecret: fingerprintSecret,
		claimStuckAfter:   claimStuckAfter,
	}
}

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
	Search(ctx context.Context, query SearchPaymentsQuery) ([]*domain.Payment, error)
	ClaimAuthorizationStart(ctx context.Context, request AuthorizationStartClaimRequest) (PaymentCommandClaim, error)
	ClaimExistingPaymentCommand(ctx context.Context, request ExistingPaymentCommandClaimRequest) (PaymentCommandClaim, error)
	CompletePaymentCommand(ctx context.Context, claim PaymentCommandClaim, result PaymentCommandResult, completedAt time.Time) error
	ReleasePaymentCommand(ctx context.Context, claim PaymentCommandClaim) error
	CleanupCompletedIdempotencyRecords(ctx context.Context, completedBefore time.Time) (int, error)
}

type PaymentCommandClaimRequest interface {
	Operation() string
	Key() string
	RequestFingerprint() string
	ExpectedStatus() domain.PaymentStatus
	Now() time.Time
	ClaimStuckAfter() time.Duration
}

type AuthorizationStartClaimRequest struct {
	operation          string
	key                string
	requestFingerprint string
	expectedStatus     domain.PaymentStatus
	payment            *domain.Payment
	now                time.Time
	claimStuckAfter    time.Duration
}

func NewAuthorizationStartClaimRequest(key string, requestFingerprint string, payment *domain.Payment, now time.Time, claimStuckAfter time.Duration) AuthorizationStartClaimRequest {
	return AuthorizationStartClaimRequest{
		operation:          AuthorizePaymentOperation,
		key:                key,
		requestFingerprint: requestFingerprint,
		expectedStatus:     domain.PaymentStatusPending,
		payment:            payment,
		now:                now,
		claimStuckAfter:    claimStuckAfter,
	}
}

func (r AuthorizationStartClaimRequest) Operation() string          { return r.operation }
func (r AuthorizationStartClaimRequest) Key() string                { return r.key }
func (r AuthorizationStartClaimRequest) RequestFingerprint() string { return r.requestFingerprint }
func (r AuthorizationStartClaimRequest) ExpectedStatus() domain.PaymentStatus {
	return r.expectedStatus
}
func (r AuthorizationStartClaimRequest) Now() time.Time                 { return r.now }
func (r AuthorizationStartClaimRequest) ClaimStuckAfter() time.Duration { return r.claimStuckAfter }
func (r AuthorizationStartClaimRequest) Payment() *domain.Payment       { return r.payment }

type ExistingPaymentCommandClaimRequest struct {
	operation                    string
	key                          string
	requestFingerprint           string
	expectedStatus               domain.PaymentStatus
	paymentID                    domain.PaymentID
	bankOperationKey             string
	authorizationCardFingerprint string
	now                          time.Time
	claimStuckAfter              time.Duration
}

func NewAuthorizationRetryClaimRequest(key string, requestFingerprint string, paymentID domain.PaymentID, authorizationCardFingerprint string, now time.Time, claimStuckAfter time.Duration) ExistingPaymentCommandClaimRequest {
	return ExistingPaymentCommandClaimRequest{
		operation:                    RetryAuthorizationOperation,
		key:                          key,
		requestFingerprint:           requestFingerprint,
		expectedStatus:               domain.PaymentStatusPending,
		paymentID:                    paymentID,
		authorizationCardFingerprint: authorizationCardFingerprint,
		now:                          now,
		claimStuckAfter:              claimStuckAfter,
	}
}

func NewCaptureClaimRequest(key string, requestFingerprint string, paymentID domain.PaymentID, bankOperationKey string, now time.Time, claimStuckAfter time.Duration) ExistingPaymentCommandClaimRequest {
	return ExistingPaymentCommandClaimRequest{
		operation:          CapturePaymentOperation,
		key:                key,
		requestFingerprint: requestFingerprint,
		expectedStatus:     domain.PaymentStatusAuthorized,
		paymentID:          paymentID,
		bankOperationKey:   bankOperationKey,
		now:                now,
		claimStuckAfter:    claimStuckAfter,
	}
}

func NewVoidClaimRequest(key string, requestFingerprint string, paymentID domain.PaymentID, bankOperationKey string, now time.Time, claimStuckAfter time.Duration) ExistingPaymentCommandClaimRequest {
	return ExistingPaymentCommandClaimRequest{
		operation:          VoidPaymentOperation,
		key:                key,
		requestFingerprint: requestFingerprint,
		expectedStatus:     domain.PaymentStatusAuthorized,
		paymentID:          paymentID,
		bankOperationKey:   bankOperationKey,
		now:                now,
		claimStuckAfter:    claimStuckAfter,
	}
}

func NewRefundClaimRequest(key string, requestFingerprint string, paymentID domain.PaymentID, bankOperationKey string, now time.Time, claimStuckAfter time.Duration) ExistingPaymentCommandClaimRequest {
	return ExistingPaymentCommandClaimRequest{
		operation:          RefundPaymentOperation,
		key:                key,
		requestFingerprint: requestFingerprint,
		expectedStatus:     domain.PaymentStatusCaptured,
		paymentID:          paymentID,
		bankOperationKey:   bankOperationKey,
		now:                now,
		claimStuckAfter:    claimStuckAfter,
	}
}

func (r ExistingPaymentCommandClaimRequest) Operation() string          { return r.operation }
func (r ExistingPaymentCommandClaimRequest) Key() string                { return r.key }
func (r ExistingPaymentCommandClaimRequest) RequestFingerprint() string { return r.requestFingerprint }
func (r ExistingPaymentCommandClaimRequest) ExpectedStatus() domain.PaymentStatus {
	return r.expectedStatus
}
func (r ExistingPaymentCommandClaimRequest) Now() time.Time                 { return r.now }
func (r ExistingPaymentCommandClaimRequest) ClaimStuckAfter() time.Duration { return r.claimStuckAfter }
func (r ExistingPaymentCommandClaimRequest) PaymentID() domain.PaymentID    { return r.paymentID }
func (r ExistingPaymentCommandClaimRequest) BankOperationKey() string       { return r.bankOperationKey }
func (r ExistingPaymentCommandClaimRequest) AuthorizationCardFingerprint() string {
	return r.authorizationCardFingerprint
}

type PaymentCommandClaim struct {
	operation          string
	key                string
	requestFingerprint string
	expectedStatus     domain.PaymentStatus
	payment            *domain.Payment
	replayResult       PaymentCommandResult
	replayed           bool
	recovered          bool
}

func NewClaimedPaymentCommand(request PaymentCommandClaimRequest, payment *domain.Payment) PaymentCommandClaim {
	return PaymentCommandClaim{
		operation:          request.Operation(),
		key:                request.Key(),
		requestFingerprint: request.RequestFingerprint(),
		expectedStatus:     request.ExpectedStatus(),
		payment:            payment,
	}
}

func NewRecoveredPaymentCommand(request PaymentCommandClaimRequest, payment *domain.Payment) PaymentCommandClaim {
	claim := NewClaimedPaymentCommand(request, payment)
	claim.recovered = true
	return claim
}

func NewReplayedPaymentCommand(request PaymentCommandClaimRequest, result PaymentCommandResult) PaymentCommandClaim {
	return PaymentCommandClaim{
		operation:          request.Operation(),
		key:                request.Key(),
		requestFingerprint: request.RequestFingerprint(),
		expectedStatus:     request.ExpectedStatus(),
		replayResult:       result,
		replayed:           true,
	}
}

func (c PaymentCommandClaim) Operation() string                    { return c.operation }
func (c PaymentCommandClaim) Key() string                          { return c.key }
func (c PaymentCommandClaim) RequestFingerprint() string           { return c.requestFingerprint }
func (c PaymentCommandClaim) ExpectedStatus() domain.PaymentStatus { return c.expectedStatus }
func (c PaymentCommandClaim) Payment() *domain.Payment             { return c.payment }
func (c PaymentCommandClaim) ReplayResult() (PaymentCommandResult, bool) {
	return c.replayResult, c.replayed
}
func (c PaymentCommandClaim) Recovered() bool { return c.recovered }

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

func (s *PaymentService) AuthorizePayment(ctx context.Context, command AuthorizePaymentCommand) (result PaymentCommandResult, err error) {
	started := time.Now()
	replayed := false
	defer func() {
		s.operationMetrics.RecordPaymentOperation(AuthorizePaymentOperation, paymentOperationOutcome(result, err, replayed), time.Since(started))
	}()

	now := s.clock.Now()
	payment, err := domain.NewPendingPayment(
		s.paymentIDs.NewPaymentID(),
		command.orderID,
		command.customerID,
		command.amountCents,
		s.bankOperationKeys.NewBankOperationKey(),
		authorizationCardFingerprint(command.card, s.fingerprintSecret),
		now,
	)
	if err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	claim, err := s.store.ClaimAuthorizationStart(ctx, NewAuthorizationStartClaimRequest(
		command.idempotencyKey,
		authorizePaymentRequestFingerprint(command, s.fingerprintSecret),
		payment,
		now,
		s.claimStuckAfter,
	))
	if err != nil {
		if recoveryErr, ok := errors.AsType[*IdempotencyRecoveryError](err); ok {
			s.recordIdempotencyRecoveryAttempt(AuthorizePaymentOperation)
			s.recordIdempotencyRecoveryFailure(AuthorizePaymentOperation, recoveryErr.Result())
		}
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	replayResult, replayed := claim.ReplayResult()
	if replayed {
		return replayResult, nil
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryAttempt(AuthorizePaymentOperation)
	}
	payment = claim.Payment()

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
		if isUnknownAuthorizationOutcome(err) {
			result = PaymentCommandResult{
				Payment:    newPaymentResult(payment),
				HTTPStatus: 202,
			}
			if err := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); err != nil {
				return PaymentCommandResult{}, ensurePaymentError(err)
			}
			if claim.Recovered() {
				s.recordIdempotencyRecoveryCompleted(AuthorizePaymentOperation)
			}

			return result, nil
		}
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := applyAuthorizationOutcome(payment, bankResult, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result = PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 201,
	}
	if err := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryCompleted(AuthorizePaymentOperation)
	}

	return result, nil
}

func (s *PaymentService) RetryAuthorization(ctx context.Context, command RetryAuthorizationCommand) (result PaymentCommandResult, err error) {
	started := time.Now()
	replayed := false
	defer func() {
		s.operationMetrics.RecordPaymentOperation(RetryAuthorizationOperation, paymentOperationOutcome(result, err, replayed), time.Since(started))
	}()

	claim, err := s.store.ClaimExistingPaymentCommand(ctx, NewAuthorizationRetryClaimRequest(
		command.idempotencyKey,
		retryAuthorizationRequestFingerprint(command, s.fingerprintSecret),
		command.paymentID,
		authorizationCardFingerprint(command.card, s.fingerprintSecret),
		s.clock.Now(),
		s.claimStuckAfter,
	))
	if err != nil {
		if recoveryErr, ok := errors.AsType[*IdempotencyRecoveryError](err); ok {
			s.recordIdempotencyRecoveryAttempt(RetryAuthorizationOperation)
			s.recordIdempotencyRecoveryFailure(RetryAuthorizationOperation, recoveryErr.Result())
		}
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	replayResult, replayed := claim.ReplayResult()
	if replayed {
		return replayResult, nil
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryAttempt(RetryAuthorizationOperation)
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

	result = PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryCompleted(RetryAuthorizationOperation)
	}

	return result, nil
}

func (s *PaymentService) CapturePayment(ctx context.Context, command CapturePaymentCommand) (result PaymentCommandResult, err error) {
	started := time.Now()
	replayed := false
	defer func() {
		s.operationMetrics.RecordPaymentOperation(CapturePaymentOperation, paymentOperationOutcome(result, err, replayed), time.Since(started))
	}()

	claim, err := s.store.ClaimExistingPaymentCommand(ctx, NewCaptureClaimRequest(
		command.idempotencyKey,
		capturePaymentRequestFingerprint(command, s.fingerprintSecret),
		command.paymentID,
		s.bankOperationKeys.NewBankOperationKey(),
		s.clock.Now(),
		s.claimStuckAfter,
	))
	if err != nil {
		if recoveryErr, ok := errors.AsType[*IdempotencyRecoveryError](err); ok {
			s.recordIdempotencyRecoveryAttempt(CapturePaymentOperation)
			s.recordIdempotencyRecoveryFailure(CapturePaymentOperation, recoveryErr.Result())
		}
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	replayResult, replayed := claim.ReplayResult()
	if replayed {
		return replayResult, nil
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryAttempt(CapturePaymentOperation)
	}
	payment := claim.Payment()
	bankResult, err := s.bank.CapturePayment(ctx, BankCaptureRequest{
		OperationKey:        payment.CaptureBankOperationKey(),
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
			if completeErr := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); completeErr != nil {
				return PaymentCommandResult{}, ensurePaymentError(completeErr)
			}
			if claim.Recovered() {
				s.recordIdempotencyRecoveryCompleted(CapturePaymentOperation)
			}
			return PaymentCommandResult{}, NewPaymentAuthorizationExpiredError(nil)
		}
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.MarkCaptured(bankResult.BankCaptureID, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result = PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryCompleted(CapturePaymentOperation)
	}

	return result, nil
}

func (s *PaymentService) VoidPayment(ctx context.Context, command VoidPaymentCommand) (result PaymentCommandResult, err error) {
	started := time.Now()
	replayed := false
	defer func() {
		s.operationMetrics.RecordPaymentOperation(VoidPaymentOperation, paymentOperationOutcome(result, err, replayed), time.Since(started))
	}()

	claim, err := s.store.ClaimExistingPaymentCommand(ctx, NewVoidClaimRequest(
		command.idempotencyKey,
		voidPaymentRequestFingerprint(command, s.fingerprintSecret),
		command.paymentID,
		s.bankOperationKeys.NewBankOperationKey(),
		s.clock.Now(),
		s.claimStuckAfter,
	))
	if err != nil {
		if recoveryErr, ok := errors.AsType[*IdempotencyRecoveryError](err); ok {
			s.recordIdempotencyRecoveryAttempt(VoidPaymentOperation)
			s.recordIdempotencyRecoveryFailure(VoidPaymentOperation, recoveryErr.Result())
		}
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	replayResult, replayed := claim.ReplayResult()
	if replayed {
		return replayResult, nil
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryAttempt(VoidPaymentOperation)
	}
	payment := claim.Payment()
	bankResult, err := s.bank.VoidPayment(ctx, BankVoidRequest{
		OperationKey:        payment.VoidBankOperationKey(),
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
			if completeErr := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); completeErr != nil {
				return PaymentCommandResult{}, ensurePaymentError(completeErr)
			}
			if claim.Recovered() {
				s.recordIdempotencyRecoveryCompleted(VoidPaymentOperation)
			}
			return PaymentCommandResult{}, NewPaymentAuthorizationExpiredError(nil)
		}
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.MarkVoided(bankResult.BankVoidID, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result = PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryCompleted(VoidPaymentOperation)
	}

	return result, nil
}

func (s *PaymentService) RefundPayment(ctx context.Context, command RefundPaymentCommand) (result PaymentCommandResult, err error) {
	started := time.Now()
	replayed := false
	defer func() {
		s.operationMetrics.RecordPaymentOperation(RefundPaymentOperation, paymentOperationOutcome(result, err, replayed), time.Since(started))
	}()

	claim, err := s.store.ClaimExistingPaymentCommand(ctx, NewRefundClaimRequest(
		command.idempotencyKey,
		refundPaymentRequestFingerprint(command, s.fingerprintSecret),
		command.paymentID,
		s.bankOperationKeys.NewBankOperationKey(),
		s.clock.Now(),
		s.claimStuckAfter,
	))
	if err != nil {
		if recoveryErr, ok := errors.AsType[*IdempotencyRecoveryError](err); ok {
			s.recordIdempotencyRecoveryAttempt(RefundPaymentOperation)
			s.recordIdempotencyRecoveryFailure(RefundPaymentOperation, recoveryErr.Result())
		}
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	replayResult, replayed := claim.ReplayResult()
	if replayed {
		return replayResult, nil
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryAttempt(RefundPaymentOperation)
	}
	payment := claim.Payment()
	bankResult, err := s.bank.RefundPayment(ctx, BankRefundRequest{
		OperationKey:  payment.RefundBankOperationKey(),
		BankCaptureID: payment.BankCaptureID(),
		AmountCents:   payment.AmountCents(),
		Currency:      payment.Currency(),
	})
	if err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	if err := payment.MarkRefunded(bankResult.BankRefundID, s.clock.Now()); err != nil {
		s.releasePaymentCommand(ctx, claim)
		return PaymentCommandResult{}, ensurePaymentError(err)
	}

	result = PaymentCommandResult{
		Payment:    newPaymentResult(payment),
		HTTPStatus: 200,
	}
	if err := s.store.CompletePaymentCommand(ctx, claim, result, s.clock.Now()); err != nil {
		return PaymentCommandResult{}, ensurePaymentError(err)
	}
	if claim.Recovered() {
		s.recordIdempotencyRecoveryCompleted(RefundPaymentOperation)
	}

	return result, nil
}

func (s *PaymentService) GetPayment(ctx context.Context, query GetPaymentQuery) (PaymentResult, error) {
	payment, err := s.store.FindByID(ctx, query.paymentID)
	if err != nil {
		return PaymentResult{}, ensurePaymentError(err)
	}
	return newPaymentResult(payment), nil
}

func (s *PaymentService) SearchPayments(ctx context.Context, query SearchPaymentsQuery) ([]PaymentResult, error) {
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

func (s *PaymentService) CleanupCompletedIdempotencyReplays(ctx context.Context) (int, error) {
	return s.store.CleanupCompletedIdempotencyRecords(ctx, s.clock.Now().Add(-idempotencyReplayWindow))
}

func (s *PaymentService) releasePaymentCommand(ctx context.Context, claim PaymentCommandClaim) {
	if err := s.store.ReleasePaymentCommand(ctx, claim); err != nil {
		s.operationMetrics.RecordPaymentCommandReleaseFailure(claim.Operation())
	}
}

func (s *PaymentService) recordIdempotencyRecoveryAttempt(operation string) {
	s.operationMetrics.RecordIdempotencyRecovery(operation, IdempotencyRecoveryAttempted)
}

func (s *PaymentService) recordIdempotencyRecoveryCompleted(operation string) {
	s.operationMetrics.RecordIdempotencyRecovery(operation, IdempotencyRecoveryRecovered)
}

func (s *PaymentService) recordIdempotencyRecoveryFailure(operation string, result string) {
	s.operationMetrics.RecordIdempotencyRecovery(operation, result)
}

func paymentOperationOutcome(result PaymentCommandResult, err error, replayed bool) string {
	if replayed {
		return paymentOperationOutcomeReplayed
	}
	if HasPaymentErrorKind(err, PaymentErrorAuthorizationExpired) {
		return string(domain.PaymentStatusExpired)
	}
	if err != nil {
		if kind, ok := PaymentErrorKindOf(err); ok {
			return string(kind)
		}
		return string(PaymentErrorInternal)
	}
	if result.Payment.Status != "" {
		return result.Payment.Status
	}
	return string(PaymentErrorInternal)
}

func applyAuthorizationOutcome(payment *domain.Payment, bankResult BankAuthorizationResult, now time.Time) error {
	if bankResult.DeclineReason != "" {
		return payment.MarkDeclined(bankResult.DeclineReason, now)
	}
	return payment.MarkAuthorized(bankResult.BankAuthorizationID, bankResult.AuthorizationExpiresAt, now)
}

func isUnknownAuthorizationOutcome(err error) bool {
	return HasPaymentErrorKind(err, PaymentErrorBankTimeout) ||
		HasPaymentErrorKind(err, PaymentErrorBankUnavailable)
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
