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
}

func NewPaymentRepository() *PaymentRepository {
	return &PaymentRepository{payments: make(map[domain.PaymentID]*domain.Payment)}
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

func (r *PaymentRepository) UpdateAuthorizationResult(_ context.Context, payment *domain.Payment) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFound(string(payment.ID()), nil)
	}
	if existing.Status() != domain.PaymentStatusPending {
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	return r.update(payment)
}

func (r *PaymentRepository) UpdateVoidResult(_ context.Context, payment *domain.Payment) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFound(string(payment.ID()), nil)
	}
	if existing.Status() != domain.PaymentStatusAuthorized {
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	cloned, err := clonePayment(payment)
	if err != nil {
		return err
	}
	r.payments[payment.ID()] = cloned
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

func (r *PaymentRepository) UpdateCaptureResult(_ context.Context, payment *domain.Payment) error {
	existing, ok := r.payments[payment.ID()]
	if !ok {
		return app.NewPaymentNotFound(string(payment.ID()), nil)
	}
	if existing.Status() != domain.PaymentStatusAuthorized {
		return app.NewPaymentInvalidStatusConflict(nil)
	}
	return r.update(payment)
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
	records map[string]app.IdempotencyRecord
}

func NewIdempotencyRepository() *IdempotencyRepository {
	return &IdempotencyRepository{records: make(map[string]app.IdempotencyRecord)}
}

func (r *IdempotencyRepository) FindCompleted(_ context.Context, operation string, key string) (app.IdempotencyRecord, bool, error) {
	record, ok := r.records[idempotencyMapKey(operation, key)]
	if !ok {
		return app.IdempotencyRecord{}, false, nil
	}
	return cloneIdempotencyRecord(record), true, nil
}

func (r *IdempotencyRepository) SaveCompleted(_ context.Context, record app.IdempotencyRecord) error {
	r.records[idempotencyMapKey(record.Operation, record.Key)] = cloneIdempotencyRecord(record)
	return nil
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
		payment.AuthorizationBankOperationKey(),
		payment.AuthorizationCardFingerprint(),
		payment.BankCaptureID(),
		payment.CaptureBankOperationKey(),
		payment.BankVoidID(),
		payment.VoidBankOperationKey(),
		payment.DeclineReason(),
		payment.CreatedAt(),
		payment.UpdatedAt(),
	)
}
