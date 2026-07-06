package app

import (
	"strings"

	"github.com/roigada/payment-gateway/internal/domain"
)

type GetPaymentQuery struct {
	paymentID domain.PaymentID
}

type SearchPaymentsQuery struct {
	orderID    string
	customerID string
	status     domain.PaymentStatus
}

func NewGetPaymentQuery(paymentID string) (GetPaymentQuery, error) {
	parsedPaymentID, err := parsePaymentID(paymentID)
	if err != nil {
		return GetPaymentQuery{}, ensurePaymentError(err)
	}
	return GetPaymentQuery{paymentID: parsedPaymentID}, nil
}

func NewSearchPaymentsQuery(orderID string, customerID string, status string) (SearchPaymentsQuery, error) {
	query := SearchPaymentsQuery{
		orderID:    strings.TrimSpace(orderID),
		customerID: strings.TrimSpace(customerID),
		status:     domain.PaymentStatus(strings.TrimSpace(status)),
	}
	if query.orderID == "" && query.customerID == "" {
		return SearchPaymentsQuery{}, NewInvalidPaymentInputError("order id or customer id is required", nil)
	}
	if query.status != "" && !isValidPaymentStatus(query.status) {
		return SearchPaymentsQuery{}, NewInvalidPaymentInputError("payment status is invalid", nil)
	}
	return query, nil
}

func (q SearchPaymentsQuery) OrderID() string {
	return q.orderID
}

func (q SearchPaymentsQuery) CustomerID() string {
	return q.customerID
}

func (q SearchPaymentsQuery) Status() string {
	return string(q.status)
}
