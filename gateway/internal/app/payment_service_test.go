package app_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testClaimStuckAfter = 5 * time.Minute

func TestAuthorizePaymentSendsBankRequestAndCompletesClaim(t *testing.T) {
	now := testTime()
	store := &paymentStoreFake{}
	bank := &bankFake{authorizeResult: app.BankAuthorizationResult{BankAuthorizationID: "auth-1", AuthorizationExpiresAt: now.Add(time.Hour)}}

	result, err := newPaymentService(store, bank, now).AuthorizePayment(context.Background(), authorizeCommand(t))

	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, result.HTTPStatus)
	assert.Equal(t, "authorized", result.Payment.Status)
	assert.Equal(t, app.BankAuthorizationRequest{OperationKey: "bok_1", OrderID: "order-1", CustomerID: "customer-1", AmountCents: 1299, Currency: "USD", CardNumber: "4111111111111111", CardCVV: "123", CardExpiryMonth: 12, CardExpiryYear: 2030}, bank.authorizeRequest)
	require.Len(t, store.completed, 1)
	assert.Equal(t, app.AuthorizePaymentOperation, store.completed[0].claim.Operation())
	assert.Equal(t, domain.PaymentStatusAuthorized, store.completed[0].claim.Payment().Status())
}

func TestAuthorizePaymentCompletesPendingPaymentForUnknownBankOutcome(t *testing.T) {
	store := &paymentStoreFake{}
	bank := &bankFake{authorizeErr: app.NewPaymentBankTimeoutError(context.DeadlineExceeded)}

	result, err := newPaymentService(store, bank, testTime()).AuthorizePayment(context.Background(), authorizeCommand(t))

	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, result.HTTPStatus)
	assert.Equal(t, "pending", result.Payment.Status)
	require.Len(t, store.completed, 1)
	assert.Equal(t, domain.PaymentStatusPending, store.completed[0].claim.Payment().Status())
	assert.Empty(t, store.released)
}

func TestAuthorizePaymentReturnsConfiguredReplayWithoutCallingBank(t *testing.T) {
	replayed := app.PaymentCommandResult{Payment: app.PaymentResult{ID: "pay_replayed", Status: "authorized"}, HTTPStatus: http.StatusCreated}
	store := &paymentStoreFake{claimAuthorizationStart: func(request app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error) {
		return app.NewReplayedPaymentCommand(request, replayed), nil
	}}
	bank := &bankFake{}

	result, err := newPaymentService(store, bank, testTime()).AuthorizePayment(context.Background(), authorizeCommand(t))

	require.NoError(t, err)
	assert.Equal(t, replayed, result)
	assert.Zero(t, bank.authorizeCalls)
	assert.Empty(t, store.completed)
}

func TestRetryAuthorizationCompletesDeclinedPayment(t *testing.T) {
	payment := pendingPayment(t, testTime())
	store := claimedPaymentStore(payment)
	bank := &bankFake{authorizeResult: app.BankAuthorizationResult{DeclineReason: domain.DeclineReasonInsufficientFunds}}
	command, err := app.NewRetryAuthorizationCommand(string(payment.ID()), "4111111111111111", "123", 12, 2030, "retry-1")
	require.NoError(t, err)

	result, err := newPaymentService(store, bank, testTime()).RetryAuthorization(context.Background(), command)

	require.NoError(t, err)
	assert.Equal(t, "declined", result.Payment.Status)
	assert.Equal(t, "insufficient_funds", result.Payment.DeclineReason)
	require.Len(t, store.completed, 1)
}

func TestCapturePaymentPersistsExpiredOutcomeAndReturnsExpirationError(t *testing.T) {
	payment := authorizedPayment(t, testTime())
	store := claimedPaymentStore(payment)
	bank := &bankFake{captureErr: app.NewPaymentAuthorizationExpiredError(nil)}

	_, err := newPaymentService(store, bank, testTime()).CapturePayment(context.Background(), captureCommand(t, payment.ID()))

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
	require.Len(t, store.completed, 1)
	assert.Equal(t, domain.PaymentStatusExpired, store.completed[0].claim.Payment().Status())
	assert.Empty(t, store.released)
}

func TestVoidPaymentReleasesClaimWhenBankFails(t *testing.T) {
	payment := authorizedPayment(t, testTime())
	store := claimedPaymentStore(payment)
	bank := &bankFake{voidErr: app.NewPaymentBankUnavailableError(errors.New("unavailable"))}
	command, err := app.NewVoidPaymentCommand(string(payment.ID()), "void-1")
	require.NoError(t, err)

	_, err = newPaymentService(store, bank, testTime()).VoidPayment(context.Background(), command)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
	require.Len(t, store.released, 1)
	assert.Empty(t, store.completed)
}

func TestRefundPaymentCompletesClaim(t *testing.T) {
	payment := capturedPayment(t, testTime())
	require.NoError(t, payment.SetRefundBankOperationKey("bok_1"))
	store := claimedPaymentStore(payment)
	bank := &bankFake{refundResult: app.BankRefundResult{BankRefundID: "refund-1"}}
	command, err := app.NewRefundPaymentCommand(string(payment.ID()), "refund-1")
	require.NoError(t, err)

	result, err := newPaymentService(store, bank, testTime()).RefundPayment(context.Background(), command)

	require.NoError(t, err)
	assert.Equal(t, "refunded", result.Payment.Status)
	require.Len(t, store.completed, 1)
	assert.Equal(t, "capture-1", bank.refundRequest.BankCaptureID)
}

func TestReadsMapStorePaymentsToPublicResults(t *testing.T) {
	payment := authorizedPayment(t, testTime())
	store := &paymentStoreFake{findPayment: payment, searchPayments: []*domain.Payment{payment}}
	service := newPaymentService(store, &bankFake{}, testTime())
	get, err := app.NewGetPaymentQuery(string(payment.ID()))
	require.NoError(t, err)
	search, err := app.NewSearchPaymentsQuery("order-1", "", "authorized")
	require.NoError(t, err)

	got, err := service.GetPayment(context.Background(), get)
	require.NoError(t, err)
	results, err := service.SearchPayments(context.Background(), search)
	require.NoError(t, err)

	assert.Equal(t, string(payment.ID()), got.ID)
	require.Len(t, results, 1)
	assert.Equal(t, "authorized", results[0].Status)
}

func TestAppInputConstructorsNormalizeAndRejectInvalidInput(t *testing.T) {
	command, err := app.NewAuthorizePaymentCommand(" order-1 ", " customer-1 ", 1299, " 4111111111111111 ", " 123 ", 12, 2030, " key-1 ")
	require.NoError(t, err)
	store := &paymentStoreFake{}
	_, err = newPaymentService(store, &bankFake{authorizeResult: app.BankAuthorizationResult{BankAuthorizationID: "auth-1", AuthorizationExpiresAt: testTime().Add(time.Hour)}}, testTime()).AuthorizePayment(context.Background(), command)
	require.NoError(t, err)
	assert.Equal(t, "order-1", store.authorizationRequest.Payment().OrderID())

	for _, err := range []error{
		constructorError(func() error {
			_, err := app.NewAuthorizePaymentCommand("", "customer-1", 1299, "4111111111111111", "123", 12, 2030, "key")
			return err
		}),
		constructorError(func() error { _, err := app.NewCapturePaymentCommand("not-a-payment-id", "key"); return err }),
		constructorError(func() error { _, err := app.NewSearchPaymentsQuery("", "", ""); return err }),
	} {
		assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInvalidInput))
	}
}

func constructorError(run func() error) error { return run() }

type paymentStoreFake struct {
	findPayment             *domain.Payment
	findErr                 error
	searchPayments          []*domain.Payment
	searchErr               error
	claimAuthorizationStart func(app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error)
	claimExisting           func(app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error)
	authorizationRequest    app.AuthorizationStartClaimRequest
	completed               []completedClaim
	released                []app.PaymentCommandClaim
	completeErr             error
	releaseErr              error
	cleanupRemoved          int
	cleanupErr              error
	cleanupBefore           time.Time
}

type completedClaim struct{ claim app.PaymentCommandClaim }

func (s *paymentStoreFake) FindByID(context.Context, domain.PaymentID) (*domain.Payment, error) {
	return s.findPayment, s.findErr
}
func (s *paymentStoreFake) Search(context.Context, app.SearchPaymentsQuery) ([]*domain.Payment, error) {
	return s.searchPayments, s.searchErr
}
func (s *paymentStoreFake) ClaimAuthorizationStart(_ context.Context, request app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error) {
	s.authorizationRequest = request
	if s.claimAuthorizationStart != nil {
		return s.claimAuthorizationStart(request)
	}
	return app.NewClaimedPaymentCommand(request, request.Payment()), nil
}
func (s *paymentStoreFake) ClaimExistingPaymentCommand(_ context.Context, request app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
	if s.claimExisting == nil {
		return app.PaymentCommandClaim{}, errors.New("unexpected existing payment command claim")
	}
	return s.claimExisting(request)
}
func (s *paymentStoreFake) CompletePaymentCommand(_ context.Context, claim app.PaymentCommandClaim, _ app.PaymentCommandResult, _ time.Time) error {
	s.completed = append(s.completed, completedClaim{claim})
	return s.completeErr
}
func (s *paymentStoreFake) ReleasePaymentCommand(_ context.Context, claim app.PaymentCommandClaim) error {
	s.released = append(s.released, claim)
	return s.releaseErr
}
func (s *paymentStoreFake) CleanupCompletedIdempotencyRecords(_ context.Context, before time.Time) (int, error) {
	s.cleanupBefore = before
	return s.cleanupRemoved, s.cleanupErr
}

type fixedPaymentIDGenerator struct{ id domain.PaymentID }

func (g fixedPaymentIDGenerator) NewPaymentID() domain.PaymentID { return g.id }

type fixedBankOperationKeyGenerator struct{ key string }

func (g fixedBankOperationKeyGenerator) NewBankOperationKey() string { return g.key }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func newPaymentService(store app.PaymentStore, bank app.BankClient, now time.Time) *app.PaymentService {
	return app.NewPaymentService(store, fixedPaymentIDGenerator{domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")}, fixedBankOperationKeyGenerator{"bok_1"}, bank, &metricsFake{}, fixedClock{now}, "secret", testClaimStuckAfter)
}

func claimedPaymentStore(payment *domain.Payment) *paymentStoreFake {
	return &paymentStoreFake{claimExisting: func(request app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
		return app.NewClaimedPaymentCommand(request, payment), nil
	}}
}

type metricsFake struct{}

func (*metricsFake) RecordPaymentOperation(string, string, time.Duration) {}
func (*metricsFake) RecordIdempotencyRecovery(string, string)             {}
func (*metricsFake) RecordPaymentCommandReleaseFailure(string)            {}

type bankFake struct {
	authorizeRequest app.BankAuthorizationRequest
	authorizeResult  app.BankAuthorizationResult
	authorizeErr     error
	authorizeCalls   int
	captureErr       error
	voidErr          error
	refundRequest    app.BankRefundRequest
	refundResult     app.BankRefundResult
}

func (b *bankFake) AuthorizePayment(_ context.Context, appRequest app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	b.authorizeRequest = appRequest
	b.authorizeCalls++
	return b.authorizeResult, b.authorizeErr
}
func (b *bankFake) CapturePayment(_ context.Context, _ app.BankCaptureRequest) (app.BankCaptureResult, error) {
	return app.BankCaptureResult{BankCaptureID: "capture-1"}, b.captureErr
}
func (b *bankFake) VoidPayment(_ context.Context, _ app.BankVoidRequest) (app.BankVoidResult, error) {
	return app.BankVoidResult{BankVoidID: "void-1"}, b.voidErr
}
func (b *bankFake) RefundPayment(_ context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	b.refundRequest = request
	return b.refundResult, nil
}

func testTime() time.Time { return time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC) }
func authorizeCommand(t *testing.T) app.AuthorizePaymentCommand {
	t.Helper()
	command, err := app.NewAuthorizePaymentCommand("order-1", "customer-1", 1299, "4111111111111111", "123", 12, 2030, "authorize-1")
	require.NoError(t, err)
	return command
}
func captureCommand(t *testing.T, id domain.PaymentID) app.CapturePaymentCommand {
	t.Helper()
	command, err := app.NewCapturePaymentCommand(string(id), "capture-1")
	require.NoError(t, err)
	return command
}
func pendingPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()
	payment, err := domain.NewPendingPayment(domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000"), "order-1", "customer-1", 1299, "bok_1", "fingerprint", now)
	require.NoError(t, err)
	return payment
}
func authorizedPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()
	payment := pendingPayment(t, now)
	require.NoError(t, payment.MarkAuthorized("auth-1", now.Add(time.Hour), now))
	return payment
}
func capturedPayment(t *testing.T, now time.Time) *domain.Payment {
	t.Helper()
	payment := authorizedPayment(t, now)
	require.NoError(t, payment.MarkCaptured("capture-1", "bok_capture", now))
	return payment
}
