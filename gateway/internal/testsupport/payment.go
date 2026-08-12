package testsupport

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type PaymentStore struct {
	payments map[domain.PaymentID]*domain.Payment
	records  map[string]idempotencyEntry
}

func NewPaymentStore() *PaymentStore {
	return &PaymentStore{
		payments: make(map[domain.PaymentID]*domain.Payment),
		records:  make(map[string]idempotencyEntry),
	}
}

func (r *PaymentStore) SeedPayment(_ context.Context, payment *domain.Payment) error {
	if _, ok := r.payments[payment.ID()]; ok {
		return errors.New("payment already exists")
	}
	cloned, err := clonePayment(payment)
	if err != nil {
		return err
	}
	r.payments[payment.ID()] = cloned
	return nil
}

func (r *PaymentStore) ReplacePayment(payment *domain.Payment) error {
	return r.update(payment)
}

func (r *PaymentStore) SeedAuthorizationClaim(operation string, key string, requestFingerprint string, paymentID domain.PaymentID, claimedAt time.Time) {
	r.records[idempotencyMapKey(operation, key)] = idempotencyEntry{
		status: idempotencyRecordInProgress,
		record: idempotencyRecord{
			operation:          operation,
			key:                key,
			requestFingerprint: requestFingerprint,
			paymentID:          paymentID,
			status:             idempotencyRecordInProgress,
			claimedAt:          claimedAt,
		},
	}
}

func (r *PaymentStore) AgeClaim(operation string, key string, claimedAt time.Time) {
	mapKey := idempotencyMapKey(operation, key)
	entry, ok := r.records[mapKey]
	if !ok {
		return
	}
	entry.record.claimedAt = claimedAt
	r.records[mapKey] = entry
}

func (r *PaymentStore) FindByID(_ context.Context, id domain.PaymentID, now time.Time) (*domain.Payment, error) {
	payment, ok := r.payments[id]
	if !ok {
		return nil, app.NewPaymentNotFoundError(string(id), nil)
	}
	if err := r.refreshReadExpiration(payment, now); err != nil {
		return nil, err
	}
	return clonePayment(payment)
}

func (r *PaymentStore) saveIfStatus(_ context.Context, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFoundError(string(payment.ID()), nil)
	}
	if existing.Status() != expectedStatus {
		return app.NewPaymentStatusConflictError(nil)
	}
	return r.update(payment)
}

func (r *PaymentStore) refreshExpiredAuthorizations(query app.SearchPaymentsQuery, now time.Time) error {
	for _, payment := range r.payments {
		if query.OrderID() != "" && payment.OrderID() != query.OrderID() {
			continue
		}
		if query.CustomerID() != "" && payment.CustomerID() != query.CustomerID() {
			continue
		}
		if err := r.refreshReadExpiration(payment, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *PaymentStore) refreshReadExpiration(payment *domain.Payment, now time.Time) error {
	if now.IsZero() || payment.Status() != domain.PaymentStatusAuthorized || !payment.AuthorizationExpired(now) {
		return nil
	}
	if payment.CaptureBankOperationKey() != "" || payment.VoidBankOperationKey() != "" {
		return nil
	}
	if err := payment.MarkExpired(now); err != nil {
		return err
	}
	return r.update(payment)
}

func (r *PaymentStore) saveBankOperationKey(_ context.Context, payment *domain.Payment, operation app.BankOperationKeyKind) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFoundError(string(payment.ID()), nil)
	}

	var expectedStatus domain.PaymentStatus
	switch operation {
	case app.BankOperationKeyCapture, app.BankOperationKeyVoid:
		expectedStatus = domain.PaymentStatusAuthorized
	case app.BankOperationKeyRefund:
		expectedStatus = domain.PaymentStatusCaptured
	default:
		return app.NewInternalPaymentError(errors.New("unknown bank operation"))
	}
	if existing.Status() != expectedStatus {
		return app.NewPaymentStatusConflictError(nil)
	}
	return r.update(payment)
}

func (r *PaymentStore) ClaimAuthorizationStart(_ context.Context, request app.AuthorizationStartClaimRequest) (app.PaymentCommandClaim, error) {
	payment := request.Payment()
	if payment == nil {
		return app.PaymentCommandClaim{}, app.NewInternalPaymentError(errors.New("authorization start claim requires a payment"))
	}
	record, outcome := r.claim(request, payment.ID())
	if outcome != idempotencyClaimAcquired {
		if outcome == idempotencyClaimRecovered {
			payment, err := r.FindByID(context.Background(), record.paymentID, time.Time{})
			if err != nil {
				if app.HasPaymentErrorKind(err, app.PaymentErrorNotFound) {
					return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(err))
				}
				return app.PaymentCommandClaim{}, err
			}
			if payment.Status() != request.ExpectedStatus() {
				return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
			}
			return app.NewRecoveredPaymentCommand(request, payment), nil
		}
		return replayOrError(request, record)
	}

	if err := r.SeedPayment(context.Background(), request.Payment()); err != nil {
		delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
		return app.PaymentCommandClaim{}, err
	}
	return app.NewClaimedPaymentCommand(request, request.Payment()), nil
}

func (r *PaymentStore) ClaimExistingPaymentCommand(_ context.Context, request app.ExistingPaymentCommandClaimRequest) (app.PaymentCommandClaim, error) {
	record, outcome := r.claim(request, request.PaymentID())
	if outcome != idempotencyClaimAcquired {
		if outcome == idempotencyClaimRecovered {
			payment, err := r.FindByID(context.Background(), record.paymentID, time.Time{})
			if err != nil {
				if app.HasPaymentErrorKind(err, app.PaymentErrorNotFound) {
					return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(err))
				}
				return app.PaymentCommandClaim{}, err
			}
			if request.PaymentID() != record.paymentID {
				return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryConflict, app.NewPaymentIdempotencyConflictError(nil))
			}
			if request.ExpectedStatus() != "" && payment.Status() != request.ExpectedStatus() {
				return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
			}
			if request.AuthorizationCardFingerprint() != "" && request.AuthorizationCardFingerprint() != payment.AuthorizationCardFingerprint() {
				return app.PaymentCommandClaim{}, app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryConflict, app.NewPaymentIdempotencyConflictError(nil))
			}
			if err := ensureRecoveredBankOperationKey(payment, request.BankOperationKeyKind()); err != nil {
				return app.PaymentCommandClaim{}, err
			}
			return app.NewRecoveredPaymentCommand(request, payment), nil
		}
		return replayOrError(request, record)
	}

	payment, err := r.FindByID(context.Background(), request.PaymentID(), time.Time{})
	if err != nil {
		delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
		return app.PaymentCommandClaim{}, err
	}
	if request.ExpectedStatus() != "" && payment.Status() != request.ExpectedStatus() {
		delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
		return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
	}
	if request.AuthorizationCardFingerprint() != "" && request.AuthorizationCardFingerprint() != payment.AuthorizationCardFingerprint() {
		delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
		return app.PaymentCommandClaim{}, app.NewPaymentStatusConflictError(nil)
	}
	if shouldExpireBeforeNewBankCall(request, payment) {
		if err := payment.MarkExpired(request.Now()); err != nil {
			delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
			return app.PaymentCommandClaim{}, app.NewInternalPaymentError(err)
		}
		if err := r.saveIfStatus(context.Background(), payment, domain.PaymentStatusAuthorized); err != nil {
			delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
			return app.PaymentCommandClaim{}, err
		}
		delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
		return app.PaymentCommandClaim{}, app.NewPaymentAuthorizationExpiredError(nil)
	}
	if request.BankOperationKeyKind() != "" {
		if err := setBankOperationKey(payment, request.BankOperationKeyKind(), request.BankOperationKey()); err != nil {
			delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
			return app.PaymentCommandClaim{}, err
		}
		if err := r.saveBankOperationKey(context.Background(), payment, request.BankOperationKeyKind()); err != nil {
			delete(r.records, idempotencyMapKey(request.Operation(), request.Key()))
			return app.PaymentCommandClaim{}, err
		}
	}
	return app.NewClaimedPaymentCommand(request, payment), nil
}

func shouldExpireBeforeNewBankCall(request app.ExistingPaymentCommandClaimRequest, payment *domain.Payment) bool {
	if request.Now().IsZero() || payment.Status() != domain.PaymentStatusAuthorized || !payment.AuthorizationExpired(request.Now()) {
		return false
	}
	switch request.BankOperationKeyKind() {
	case app.BankOperationKeyCapture:
		return payment.CaptureBankOperationKey() == ""
	case app.BankOperationKeyVoid:
		return payment.VoidBankOperationKey() == ""
	default:
		return false
	}
}

func (r *PaymentStore) CompletePaymentCommand(_ context.Context, claim app.PaymentCommandClaim, result app.PaymentCommandResult, completedAt time.Time) error {
	if err := r.saveIfStatus(context.Background(), claim.Payment(), claim.ExpectedStatus()); err != nil {
		return err
	}
	return r.complete(claim, result, completedAt)
}

func (r *PaymentStore) ReleasePaymentCommand(_ context.Context, claim app.PaymentCommandClaim) error {
	delete(r.records, idempotencyMapKey(claim.Operation(), claim.Key()))
	return nil
}

func (r *PaymentStore) CleanupCompletedIdempotencyRecords(_ context.Context, completedBefore time.Time) (int, error) {
	removed := 0
	for mapKey, entry := range r.records {
		if entry.status == idempotencyRecordCompleted && entry.record.completedAt.Before(completedBefore) {
			delete(r.records, mapKey)
			removed++
		}
	}
	return removed, nil
}

func (r *PaymentStore) Search(_ context.Context, query app.SearchPaymentsQuery, now time.Time) ([]*domain.Payment, error) {
	if err := r.refreshExpiredAuthorizations(query, now); err != nil {
		return nil, err
	}

	var matches []*domain.Payment
	for _, payment := range r.payments {
		if query.OrderID() != "" && payment.OrderID() != query.OrderID() {
			continue
		}
		if query.CustomerID() != "" && payment.CustomerID() != query.CustomerID() {
			continue
		}
		if query.Status() != "" && string(payment.Status()) != query.Status() {
			continue
		}
		cloned, err := clonePayment(payment)
		if err != nil {
			return nil, err
		}
		matches = append(matches, cloned)
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt().After(matches[j].CreatedAt())
	})
	if len(matches) > 100 {
		matches = matches[:100]
	}
	return matches, nil
}

func (r *PaymentStore) update(payment *domain.Payment) error {
	if _, ok := r.payments[payment.ID()]; !ok {
		return app.NewPaymentNotFoundError(string(payment.ID()), nil)
	}
	cloned, err := clonePayment(payment)
	if err != nil {
		return err
	}
	r.payments[payment.ID()] = cloned
	return nil
}

type FixedPaymentIDGenerator struct {
	ID domain.PaymentID
}

func (g FixedPaymentIDGenerator) NewPaymentID() domain.PaymentID {
	return g.ID
}

type FixedBankOperationKeyGenerator struct {
	Key string
}

func (g FixedBankOperationKeyGenerator) NewBankOperationKey() string {
	return g.Key
}

type FixedClock struct {
	Time time.Time
}

func (c FixedClock) Now() time.Time {
	return c.Time
}

func (r *PaymentStore) claim(request app.PaymentCommandClaimRequest, paymentID domain.PaymentID) (idempotencyRecord, idempotencyClaimOutcome) {
	mapKey := idempotencyMapKey(request.Operation(), request.Key())
	now := request.Now()
	if now.IsZero() {
		now = time.Now()
	}
	entry, ok := r.records[mapKey]
	if !ok {
		record := idempotencyRecord{
			operation:          request.Operation(),
			key:                request.Key(),
			requestFingerprint: request.RequestFingerprint(),
			paymentID:          paymentID,
			claimedAt:          now,
		}
		r.records[mapKey] = idempotencyEntry{
			status: idempotencyRecordInProgress,
			record: cloneIdempotencyRecord(record),
		}
		return record, idempotencyClaimAcquired
	}
	record := cloneIdempotencyRecord(entry.record)
	record.status = entry.status
	if canRecoverIdempotencyClaim(request, record, entry.status, now) {
		record.claimedAt = now
		record.status = idempotencyRecordInProgress
		r.records[mapKey] = idempotencyEntry{status: idempotencyRecordInProgress, record: cloneIdempotencyRecord(record)}
		return record, idempotencyClaimRecovered
	}
	return record, idempotencyClaimExisting
}

func canRecoverIdempotencyClaim(request app.PaymentCommandClaimRequest, record idempotencyRecord, status idempotencyRecordStatus, now time.Time) bool {
	return canAttemptIdempotencyRecovery(request) &&
		status == idempotencyRecordInProgress &&
		record.requestFingerprint == request.RequestFingerprint() &&
		!record.claimedAt.IsZero() &&
		!record.claimedAt.After(now.Add(-request.ClaimStuckAfter()))
}

func canAttemptIdempotencyRecovery(request app.PaymentCommandClaimRequest) bool {
	switch request.Operation() {
	case app.AuthorizePaymentOperation, app.RetryAuthorizationOperation, app.CapturePaymentOperation, app.VoidPaymentOperation, app.RefundPaymentOperation:
		return request.ClaimStuckAfter() > 0
	default:
		return false
	}
}

func (r *PaymentStore) complete(claim app.PaymentCommandClaim, result app.PaymentCommandResult, completedAt time.Time) error {
	r.records[idempotencyMapKey(claim.Operation(), claim.Key())] = idempotencyEntry{
		status: idempotencyRecordCompleted,
		record: idempotencyRecord{
			operation:          claim.Operation(),
			key:                claim.Key(),
			requestFingerprint: claim.RequestFingerprint(),
			completedAt:        completedAt,
			result:             result,
		},
	}
	return nil
}

func setBankOperationKey(payment *domain.Payment, operation app.BankOperationKeyKind, key string) error {
	switch operation {
	case app.BankOperationKeyCapture:
		if payment.CaptureBankOperationKey() != "" {
			return nil
		}
		return payment.SetCaptureBankOperationKey(key)
	case app.BankOperationKeyVoid:
		if payment.VoidBankOperationKey() != "" {
			return nil
		}
		return payment.SetVoidBankOperationKey(key)
	case app.BankOperationKeyRefund:
		if payment.RefundBankOperationKey() != "" {
			return nil
		}
		return payment.SetRefundBankOperationKey(key)
	default:
		return app.NewInternalPaymentError(errors.New("unknown bank operation"))
	}
}

func ensureRecoveredBankOperationKey(payment *domain.Payment, operation app.BankOperationKeyKind) error {
	var key string
	switch operation {
	case "":
		return nil
	case app.BankOperationKeyCapture:
		key = payment.CaptureBankOperationKey()
	case app.BankOperationKeyVoid:
		key = payment.VoidBankOperationKey()
	case app.BankOperationKeyRefund:
		key = payment.RefundBankOperationKey()
	default:
		return app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(errors.New("unknown bank operation")))
	}
	if key == "" {
		return app.NewIdempotencyRecoveryError(app.IdempotencyRecoveryUnrecoverable, app.NewInternalPaymentError(errors.New("missing recovered bank operation key")))
	}
	return nil
}

type idempotencyEntry struct {
	status idempotencyRecordStatus
	record idempotencyRecord
}

type idempotencyRecord struct {
	operation          string
	key                string
	requestFingerprint string
	paymentID          domain.PaymentID
	status             idempotencyRecordStatus
	claimedAt          time.Time
	completedAt        time.Time
	result             app.PaymentCommandResult
}

type idempotencyRecordStatus string

const (
	idempotencyRecordInProgress idempotencyRecordStatus = "in_progress"
	idempotencyRecordCompleted  idempotencyRecordStatus = "completed"
)

type idempotencyClaimOutcome int

const (
	idempotencyClaimAcquired idempotencyClaimOutcome = iota
	idempotencyClaimExisting
	idempotencyClaimRecovered
)

func idempotencyMapKey(operation string, key string) string {
	return operation + "\x00" + key
}

func cloneIdempotencyRecord(record idempotencyRecord) idempotencyRecord {
	return idempotencyRecord{
		operation:          record.operation,
		key:                record.key,
		requestFingerprint: record.requestFingerprint,
		paymentID:          record.paymentID,
		status:             record.status,
		claimedAt:          record.claimedAt,
		completedAt:        record.completedAt,
		result:             record.result,
	}
}

func replayOrError(request app.PaymentCommandClaimRequest, record idempotencyRecord) (app.PaymentCommandClaim, error) {
	if record.requestFingerprint != request.RequestFingerprint() {
		return app.PaymentCommandClaim{}, app.NewPaymentIdempotencyConflictError(nil)
	}
	if record.status == idempotencyRecordInProgress {
		return app.PaymentCommandClaim{}, app.NewPaymentIdempotencyInProgressError(nil)
	}
	return app.NewReplayedPaymentCommand(request, record.result), nil
}

func clonePayment(payment *domain.Payment) (*domain.Payment, error) {
	return domain.LoadPayment(
		payment.ID(),
		payment.OrderID(),
		payment.CustomerID(),
		payment.AmountCents(),
		payment.Currency(),
		payment.Status(),
		payment.BankAuthorizationID(),
		payment.AuthorizationExpiresAt(),
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		payment.BankCaptureID(),
		payment.CaptureBankOperationKey(),
		payment.BankRefundID(),
		payment.RefundBankOperationKey(),
		payment.BankVoidID(),
		payment.VoidBankOperationKey(),
		payment.DeclineReason(),
		payment.CreatedAt(),
		payment.UpdatedAt(),
	)
}
