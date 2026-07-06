package uuidgen

import (
	"github.com/google/uuid"
	"github.com/roigada/payment-gateway/internal/domain"
)

type PaymentIDGenerator struct{}

func NewPaymentIDGenerator() PaymentIDGenerator {
	return PaymentIDGenerator{}
}

func (PaymentIDGenerator) NewPaymentID() domain.PaymentID {
	return domain.PaymentID("pay_" + uuid.NewString())
}

type BankOperationKeyGenerator struct{}

func NewBankOperationKeyGenerator() BankOperationKeyGenerator {
	return BankOperationKeyGenerator{}
}

func (BankOperationKeyGenerator) NewBankOperationKey() string {
	return "bok_" + uuid.NewString()
}
