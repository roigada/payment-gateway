package testsupport

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
	"github.com/roigada/payment-gateway/internal/domain"
)

type PaymentRepository struct {
	payments map[domain.PaymentID]*domain.Payment
	records  map[string]idempotencyEntry
}

func NewPaymentRepository() *PaymentRepository {
	return &PaymentRepository{
		payments: make(map[domain.PaymentID]*domain.Payment),
		records:  make(map[string]idempotencyEntry),
	}
}

func (r *PaymentRepository) Create(_ context.Context, payment *domain.Payment) error {
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

func (r *PaymentRepository) FindByID(_ context.Context, id domain.PaymentID) (*domain.Payment, error) {
	payment, ok := r.payments[id]
	if !ok {
		return nil, app.NewPaymentNotFound(string(id), nil)
	}
	return clonePayment(payment)
}

func (r *PaymentRepository) SaveIfStatus(_ context.Context, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFound(string(payment.ID()), nil)
	}
	if existing.Status() != expectedStatus {
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	return r.update(payment)
}

func (r *PaymentRepository) ExpireAuthorization(ctx context.Context, payment *domain.Payment, expectedStatus domain.PaymentStatus) error {
	return r.SaveIfStatus(ctx, payment, expectedStatus)
}

func (r *PaymentRepository) RefreshExpiredAuthorizations(_ context.Context, query app.SearchPaymentsQuery, now time.Time) error {
	for _, payment := range r.payments {
		if query.OrderID != "" && payment.OrderID() != query.OrderID {
			continue
		}
		if query.CustomerID != "" && payment.CustomerID() != query.CustomerID {
			continue
		}
		if !payment.AuthorizationExpired(now) {
			continue
		}
		if payment.CaptureBankOperationKey() != "" || payment.VoidBankOperationKey() != "" {
			continue
		}
		if err := payment.MarkExpired(now); err != nil {
			return err
		}
		if err := r.update(payment); err != nil {
			return err
		}
	}
	return nil
}

func (r *PaymentRepository) SaveBankOperationKey(_ context.Context, payment *domain.Payment, operation app.BankOperationKeyKind) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFound(string(payment.ID()), nil)
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
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	return r.update(payment)
}

func (r *PaymentRepository) ClaimPaymentCommand(_ context.Context, command app.ClaimPaymentCommand) (app.PaymentCommandClaim, error) {
	record, status := r.claim(command.Operation, command.Key, command.RequestFingerprint)
	claim := app.PaymentCommandClaim{Record: record, Status: status}
	if status != app.IdempotencyClaimed {
		return claim, nil
	}

	if command.Payment != nil {
		if err := r.Create(context.Background(), command.Payment); err != nil {
			delete(r.records, idempotencyMapKey(command.Operation, command.Key))
			return app.PaymentCommandClaim{}, err
		}
		claim.Payment = command.Payment
		return claim, nil
	}
	if command.PaymentID == "" {
		return claim, nil
	}

	payment, err := r.FindByID(context.Background(), command.PaymentID)
	if err != nil {
		delete(r.records, idempotencyMapKey(command.Operation, command.Key))
		return app.PaymentCommandClaim{}, err
	}
	if command.ExpectedStatus != "" && payment.Status() != command.ExpectedStatus {
		delete(r.records, idempotencyMapKey(command.Operation, command.Key))
		return app.PaymentCommandClaim{}, app.NewPaymentInvalidStatusConflict(nil)
	}
	if command.AuthorizationCardFingerprint != "" && command.AuthorizationCardFingerprint != payment.AuthorizationCardFingerprint() {
		delete(r.records, idempotencyMapKey(command.Operation, command.Key))
		return app.PaymentCommandClaim{}, app.NewPaymentInvalidStatusConflict(nil)
	}
	if command.BankOperationKeyKind != "" {
		if err := setBankOperationKey(payment, command.BankOperationKeyKind, command.BankOperationKey); err != nil {
			delete(r.records, idempotencyMapKey(command.Operation, command.Key))
			return app.PaymentCommandClaim{}, err
		}
		if err := r.SaveBankOperationKey(context.Background(), payment, command.BankOperationKeyKind); err != nil {
			delete(r.records, idempotencyMapKey(command.Operation, command.Key))
			return app.PaymentCommandClaim{}, err
		}
	}
	claim.Payment = payment
	return claim, nil
}

func (r *PaymentRepository) CompletePaymentCommand(_ context.Context, command app.CompletePaymentCommand) error {
	if err := r.SaveIfStatus(context.Background(), command.Payment, command.ExpectedStatus); err != nil {
		return err
	}
	return r.complete(command.Record)
}

func (r *PaymentRepository) ReleasePaymentCommand(_ context.Context, operation string, key string) error {
	delete(r.records, idempotencyMapKey(operation, key))
	return nil
}

func (r *PaymentRepository) Search(_ context.Context, query app.SearchPaymentsQuery) ([]*domain.Payment, error) {
	var matches []*domain.Payment
	for _, payment := range r.payments {
		if query.OrderID != "" && payment.OrderID() != query.OrderID {
			continue
		}
		if query.CustomerID != "" && payment.CustomerID() != query.CustomerID {
			continue
		}
		if query.Status != "" && string(payment.Status()) != query.Status {
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

func (r *PaymentRepository) update(payment *domain.Payment) error {
	if _, ok := r.payments[payment.ID()]; !ok {
		return app.NewPaymentNotFound(string(payment.ID()), nil)
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

type IdempotencyRepository struct {
	records map[string]idempotencyEntry
}

func NewIdempotencyRepository() *IdempotencyRepository {
	return &IdempotencyRepository{records: make(map[string]idempotencyEntry)}
}

func (r *IdempotencyRepository) Claim(_ context.Context, operation string, key string, requestFingerprint string) (app.IdempotencyRecord, app.IdempotencyClaimStatus, error) {
	record, status := r.claim(operation, key, requestFingerprint)
	return record, status, nil
}

func (r *IdempotencyRepository) claim(operation string, key string, requestFingerprint string) (app.IdempotencyRecord, app.IdempotencyClaimStatus) {
	mapKey := idempotencyMapKey(operation, key)
	entry, ok := r.records[mapKey]
	if !ok {
		record := app.IdempotencyRecord{
			Operation:          operation,
			Key:                key,
			RequestFingerprint: requestFingerprint,
		}
		r.records[mapKey] = idempotencyEntry{
			status: app.IdempotencyInProgress,
			record: cloneIdempotencyRecord(record),
		}
		return record, app.IdempotencyClaimed
	}
	return cloneIdempotencyRecord(entry.record), entry.status
}

func (r *IdempotencyRepository) Complete(_ context.Context, record app.IdempotencyRecord) error {
	return r.complete(record)
}

func (r *IdempotencyRepository) complete(record app.IdempotencyRecord) error {
	r.records[idempotencyMapKey(record.Operation, record.Key)] = idempotencyEntry{
		status: app.IdempotencyCompleted,
		record: cloneIdempotencyRecord(record),
	}
	return nil
}

func (r *IdempotencyRepository) Release(_ context.Context, operation string, key string) error {
	delete(r.records, idempotencyMapKey(operation, key))
	return nil
}

func (r *PaymentRepository) claim(operation string, key string, requestFingerprint string) (app.IdempotencyRecord, app.IdempotencyClaimStatus) {
	return (&IdempotencyRepository{records: r.records}).claim(operation, key, requestFingerprint)
}

func (r *PaymentRepository) complete(record app.IdempotencyRecord) error {
	return (&IdempotencyRepository{records: r.records}).complete(record)
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

type idempotencyEntry struct {
	status app.IdempotencyClaimStatus
	record app.IdempotencyRecord
}

func idempotencyMapKey(operation string, key string) string {
	return operation + "\x00" + key
}

func cloneIdempotencyRecord(record app.IdempotencyRecord) app.IdempotencyRecord {
	return app.IdempotencyRecord{
		Operation:          record.Operation,
		Key:                record.Key,
		RequestFingerprint: record.RequestFingerprint,
		Result:             record.Result,
		ResponseStatus:     record.ResponseStatus,
	}
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
