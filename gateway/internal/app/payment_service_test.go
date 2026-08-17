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

// A successful authorization creates an Authorized Payment, sends the expected card details
// to the Mock Bank, and records the final result so the Idempotency Key can be replayed.
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

// An unexpected store failure must not escape from the exported service as raw infrastructure
// text. The service returns a safe Internal Payment Error while retaining the original cause.
func TestAuthorizePaymentNormalizesUnexpectedStoreError(t *testing.T) {
	storeErr := errors.New("database driver: connection refused")
	store := &paymentStoreFake{claimAuthorizationStart: func(app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error) {
		return app.PaymentCommandClaim{}, storeErr
	}}

	_, err := newPaymentService(store, &bankFake{}, testTime()).AuthorizePayment(context.Background(), authorizeCommand(t))

	require.Error(t, err)
	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorInternal))
	assert.Equal(t, "internal server error", err.Error())
	assert.ErrorIs(t, err, storeErr)
	assert.NotContains(t, err.Error(), storeErr.Error())
}

// A Mock Bank timeout leaves the authorization outcome unknown, so the Payment remains Pending
// and its 202 response is completed for replay instead of treating the command as failed.
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

// A repeated authorization with a completed Idempotency Key returns its stored response;
// it must not make another Mock Bank call or create another completed command.
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

// A replay of any command on an existing Payment returns its stored response without calling
// the Mock Bank, preventing a retry from capturing, voiding, refunding, or authorizing twice.
func TestExistingPaymentCommandsReturnConfiguredReplayWithoutCallingBank(t *testing.T) {
	replayed := app.PaymentCommandResult{Payment: app.PaymentResult{ID: "pay_replayed", Status: "captured"}, HTTPStatus: http.StatusOK}
	paymentID := domain.PaymentID("pay_550e8400-e29b-41d4-a716-446655440000")

	tests := []struct {
		name      string
		execute   func(*testing.T, *app.PaymentService) (app.PaymentCommandResult, error)
		bankCalls func(*bankFake) int
	}{
		{
			name: "retry authorization",
			execute: func(t *testing.T, service *app.PaymentService) (app.PaymentCommandResult, error) {
				command, err := app.NewRetryAuthorizationCommand(string(paymentID), "4111111111111111", "123", 12, 2030, "retry-1")
				require.NoError(t, err)
				return service.RetryAuthorization(context.Background(), command)
			},
			bankCalls: func(bank *bankFake) int { return bank.authorizeCalls },
		},
		{
			name: "capture",
			execute: func(t *testing.T, service *app.PaymentService) (app.PaymentCommandResult, error) {
				return service.CapturePayment(context.Background(), captureCommand(t, paymentID))
			},
			bankCalls: func(bank *bankFake) int { return bank.captureCalls },
		},
		{
			name: "void",
			execute: func(t *testing.T, service *app.PaymentService) (app.PaymentCommandResult, error) {
				command, err := app.NewVoidPaymentCommand(string(paymentID), "void-1")
				require.NoError(t, err)
				return service.VoidPayment(context.Background(), command)
			},
			bankCalls: func(bank *bankFake) int { return bank.voidCalls },
		},
		{
			name: "refund",
			execute: func(t *testing.T, service *app.PaymentService) (app.PaymentCommandResult, error) {
				command, err := app.NewRefundPaymentCommand(string(paymentID), "refund-1")
				require.NoError(t, err)
				return service.RefundPayment(context.Background(), command)
			},
			bankCalls: func(bank *bankFake) int { return bank.refundCalls },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &paymentStoreFake{claimExisting: func(request app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
				return app.NewReplayedPaymentCommand(request, replayed), nil
			}}
			bank := &bankFake{}

			result, err := test.execute(t, newPaymentService(store, bank, testTime()))

			require.NoError(t, err)
			assert.Equal(t, replayed, result)
			assert.Zero(t, test.bankCalls(bank))
			assert.Empty(t, store.completed)
		})
	}
}

// Retrying a Pending Payment can receive a definitive decline from the Mock Bank; the Payment
// then becomes Declined with the bank's mapped reason and the command is completed for replay.
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

// Capturing an Authorized Payment sends its authorization reference and amount to the Mock Bank,
// then persists the Captured lifecycle transition and its successful command result.
func TestCapturePaymentSendsBankRequestAndCompletesClaim(t *testing.T) {
	payment := authorizedPayment(t, testTime())
	store := claimedPaymentStore(payment)
	bank := &bankFake{}

	result, err := newPaymentService(store, bank, testTime()).CapturePayment(context.Background(), captureCommand(t, payment.ID()))

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, result.HTTPStatus)
	assert.Equal(t, "captured", result.Payment.Status)
	assert.Equal(t, app.BankCaptureRequest{OperationKey: "bok_1", BankAuthorizationID: "auth-1", AmountCents: 1299, Currency: "USD"}, bank.captureRequest)
	require.Len(t, store.completed, 1)
	assert.Equal(t, domain.PaymentStatusCaptured, store.completed[0].claim.Payment().Status())
}

// A definitive expiration response from the Mock Bank changes the Payment to Expired and is
// saved for replay, while the current caller receives an authorization-expired error.
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

// Voiding an Authorized Payment sends its authorization reference to the Mock Bank, then stores
// the Voided transition and successful result so a later identical request can be replayed.
func TestVoidPaymentSendsBankRequestAndCompletesClaim(t *testing.T) {
	payment := authorizedPayment(t, testTime())
	store := claimedPaymentStore(payment)
	bank := &bankFake{}
	command, err := app.NewVoidPaymentCommand(string(payment.ID()), "void-1")
	require.NoError(t, err)

	result, err := newPaymentService(store, bank, testTime()).VoidPayment(context.Background(), command)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, result.HTTPStatus)
	assert.Equal(t, "voided", result.Payment.Status)
	assert.Equal(t, app.BankVoidRequest{OperationKey: "bok_1", BankAuthorizationID: "auth-1"}, bank.voidRequest)
	require.Len(t, store.completed, 1)
	assert.Equal(t, domain.PaymentStatusVoided, store.completed[0].claim.Payment().Status())
}

// A void can reveal that the Mock Bank authorization has expired; this is a final Payment state,
// so it is persisted and returned to the caller as an authorization-expired error.
func TestVoidPaymentPersistsExpiredOutcomeAndReturnsExpirationError(t *testing.T) {
	payment := authorizedPayment(t, testTime())
	store := claimedPaymentStore(payment)
	bank := &bankFake{voidErr: app.NewPaymentAuthorizationExpiredError(nil)}
	command, err := app.NewVoidPaymentCommand(string(payment.ID()), "void-1")
	require.NoError(t, err)

	_, err = newPaymentService(store, bank, testTime()).VoidPayment(context.Background(), command)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorAuthorizationExpired))
	require.Len(t, store.completed, 1)
	assert.Equal(t, domain.PaymentStatusExpired, store.completed[0].claim.Payment().Status())
	assert.Empty(t, store.released)
}

// A temporary Mock Bank failure does not establish a final void outcome, so the command claim is
// released rather than completed, allowing the caller to retry safely with the same key.
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

// Refunding a Captured Payment sends its original Mock Bank capture ID, then persists the
// Refunded transition and completes the command for future idempotent replays.
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

// A temporary Mock Bank failure during refund leaves the final outcome unknown, so the command
// claim is released—not completed—and a later retry may attempt recovery.
func TestRefundPaymentReleasesClaimWhenBankFails(t *testing.T) {
	payment := capturedPayment(t, testTime())
	require.NoError(t, payment.SetRefundBankOperationKey("bok_1"))
	store := claimedPaymentStore(payment)
	bank := &bankFake{refundErr: app.NewPaymentBankUnavailableError(errors.New("unavailable"))}
	command, err := app.NewRefundPaymentCommand(string(payment.ID()), "refund-1")
	require.NoError(t, err)

	_, err = newPaymentService(store, bank, testTime()).RefundPayment(context.Background(), command)

	assert.True(t, app.HasPaymentErrorKind(err, app.PaymentErrorBankUnavailable))
	require.Len(t, store.released, 1)
	assert.Empty(t, store.completed)
}

// Read operations translate the domain Payment returned by the store into the public result type
// used by callers, for both a single Payment lookup and a Payment search.
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

// The service owns the 24-hour Idempotency Replay Window: it supplies the store with the precise
// cutoff and caller context, while the store selects records, deletes them, and returns the count.
func TestCleanupCompletedIdempotencyReplaysUsesReplayWindowCutoffAndReturnsStoreCount(t *testing.T) {
	now := time.Date(2000, time.January, 2, 12, 0, 0, 0, time.UTC)
	store := &paymentStoreFake{cleanupRemoved: 3}
	service := newPaymentService(store, &bankFake{}, now)
	ctx := context.WithValue(context.Background(), cleanupContextKey{}, "cleanup")

	removed, err := service.CleanupCompletedIdempotencyReplays(ctx)

	require.NoError(t, err)
	assert.Equal(t, 3, removed)
	assert.Equal(t, now.Add(-24*time.Hour), store.cleanupBefore)
	assert.Same(t, ctx, store.cleanupContext)
}

// Cleanup is best effort, but its store errors must remain visible to the runner so it can log
// the failure and retry on a later scheduled run instead of reporting a false removal count.
func TestCleanupCompletedIdempotencyReplaysPropagatesStoreError(t *testing.T) {
	storeErr := errors.New("database unavailable")
	store := &paymentStoreFake{cleanupErr: storeErr}
	service := newPaymentService(store, &bankFake{}, time.Time{})

	removed, err := service.CleanupCompletedIdempotencyReplays(context.Background())

	assert.Zero(t, removed)
	require.ErrorIs(t, err, storeErr)
}

// Application input constructors trim accepted text before commands are used and consistently
// reject malformed commands and queries with the gateway's InvalidInput error kind.
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

type cleanupContextKey struct{}

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
	cleanupContext          context.Context
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
func (s *paymentStoreFake) CleanupCompletedIdempotencyRecords(ctx context.Context, before time.Time) (int, error) {
	s.cleanupContext = ctx
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
		switch request.Operation() {
		case app.CapturePaymentOperation:
			if err := payment.SetCaptureBankOperationKey(request.BankOperationKey()); err != nil {
				return app.PaymentCommandClaim{}, err
			}
		case app.VoidPaymentOperation:
			if err := payment.SetVoidBankOperationKey(request.BankOperationKey()); err != nil {
				return app.PaymentCommandClaim{}, err
			}
		}
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
	captureRequest   app.BankCaptureRequest
	captureCalls     int
	captureErr       error
	voidRequest      app.BankVoidRequest
	voidCalls        int
	voidErr          error
	refundRequest    app.BankRefundRequest
	refundResult     app.BankRefundResult
	refundErr        error
	refundCalls      int
}

func (b *bankFake) AuthorizePayment(_ context.Context, appRequest app.BankAuthorizationRequest) (app.BankAuthorizationResult, error) {
	b.authorizeRequest = appRequest
	b.authorizeCalls++
	return b.authorizeResult, b.authorizeErr
}
func (b *bankFake) CapturePayment(_ context.Context, request app.BankCaptureRequest) (app.BankCaptureResult, error) {
	b.captureRequest = request
	b.captureCalls++
	return app.BankCaptureResult{BankCaptureID: "capture-1"}, b.captureErr
}
func (b *bankFake) VoidPayment(_ context.Context, request app.BankVoidRequest) (app.BankVoidResult, error) {
	b.voidRequest = request
	b.voidCalls++
	return app.BankVoidResult{BankVoidID: "void-1"}, b.voidErr
}
func (b *bankFake) RefundPayment(_ context.Context, request app.BankRefundRequest) (app.BankRefundResult, error) {
	b.refundRequest = request
	b.refundCalls++
	return b.refundResult, b.refundErr
}

func testTime() time.Time { return time.Date(2000, time.January, 1, 12, 0, 0, 0, time.UTC) }
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
	require.NoError(t, payment.SetCaptureBankOperationKey("bok_capture"))
	require.NoError(t, payment.MarkCaptured("capture-1", now))
	return payment
}
