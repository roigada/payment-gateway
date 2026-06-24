package httpapi

import (
	"context"
	"net/http"

	"github.com/roigada/payment-gateway/internal/app"
)

type requestLogContextKey struct{}

type requestLogContext struct {
	attrs []any
}

func newRequestLogContext(r *http.Request) (*http.Request, *requestLogContext) {
	logCtx := &requestLogContext{}
	ctx := context.WithValue(r.Context(), requestLogContextKey{}, logCtx)
	return r.WithContext(ctx), logCtx
}

func requestLogAttrs(r *http.Request) []any {
	logCtx, ok := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if !ok {
		return nil
	}
	return logCtx.attrs
}

func addRequestLogAttrs(r *http.Request, attrs ...any) {
	logCtx, ok := r.Context().Value(requestLogContextKey{}).(*requestLogContext)
	if !ok {
		return
	}
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			continue
		}
		upsertRequestLogAttr(logCtx, key, attrs[i+1])
	}
}

func upsertRequestLogAttr(logCtx *requestLogContext, key string, value any) {
	for i := 0; i+1 < len(logCtx.attrs); i += 2 {
		if logCtx.attrs[i] == key {
			logCtx.attrs[i+1] = value
			return
		}
	}
	logCtx.attrs = append(logCtx.attrs, key, value)
}

func logPaymentOperation(r *http.Request, operation string) {
	addRequestLogAttrs(r, "operation", operation)
}

func logPaymentID(r *http.Request, paymentID string) {
	if paymentID == "" {
		return
	}
	addRequestLogAttrs(r, "payment_id", paymentID)
}

func logPaymentResult(r *http.Request, payment app.PaymentResult) {
	attrs := []any{
		"payment_id", payment.ID,
		"order_id", payment.OrderID,
		"customer_id", payment.CustomerID,
		"payment_status", payment.Status,
	}
	addRequestLogAttrs(r, attrs...)
}

func logGatewayErrorCode(r *http.Request, code string) {
	addRequestLogAttrs(r, "gateway_error_code", code)
}
