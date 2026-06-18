package testsupport

import (
	"context"
	"errors"
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
		return nil, app.ErrPaymentNotFound
	}
	return clonePayment(payment)
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

func clonePayment(payment *domain.Payment) (*domain.Payment, error) {
	return domain.LoadPayment(
		payment.ID(),
		payment.OrderID(),
		payment.CustomerID(),
		payment.AmountCents(),
		payment.Currency(),
		payment.Status(),
		payment.AuthorizationBankReference(),
		payment.AuthorizationBankOperationKey(),
		payment.CreatedAt(),
		payment.UpdatedAt(),
	)
}
