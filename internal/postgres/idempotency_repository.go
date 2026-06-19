package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

type IdempotencyRepository struct {
	db *sql.DB
}

func NewIdempotencyRepository(db *sql.DB) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

func (r *IdempotencyRepository) FindCompleted(ctx context.Context, operation string, key string) (app.IdempotencyRecord, error) {
	var (
		record       app.IdempotencyRecord
		responseBody []byte
	)
	err := r.db.QueryRowContext(
		ctx,
		`SELECT request_fingerprint,
		        response_body
		   FROM idempotency_records
		  WHERE operation = $1
		    AND key = $2`,
		operation,
		key,
	).Scan(&record.RequestFingerprint, &responseBody)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return app.IdempotencyRecord{}, app.ErrIdempotencyNotFound
		}
		return app.IdempotencyRecord{}, err
	}

	result, err := decodePaymentResultSnapshot(responseBody)
	if err != nil {
		return app.IdempotencyRecord{}, err
	}

	record.Operation = operation
	record.Key = key
	record.Result = result
	return record, nil
}

func (r *IdempotencyRepository) SaveCompleted(ctx context.Context, record app.IdempotencyRecord) error {
	responseBody, err := encodePaymentResultSnapshot(record.Result)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(
		ctx,
		`INSERT INTO idempotency_records (
		     operation,
		     key,
		     request_fingerprint,
		     response_body
		 )
		 VALUES ($1, $2, $3, $4::jsonb)`,
		record.Operation,
		record.Key,
		record.RequestFingerprint,
		string(responseBody),
	)
	return err
}

type paymentResultSnapshot struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	CustomerID    string    `json:"customer_id"`
	AmountCents   int64     `json:"amount"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	DeclineReason string    `json:"decline_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func encodePaymentResultSnapshot(result app.PaymentResult) ([]byte, error) {
	return json.Marshal(paymentResultSnapshot{
		ID:            result.ID,
		OrderID:       result.OrderID,
		CustomerID:    result.CustomerID,
		AmountCents:   result.AmountCents,
		Currency:      result.Currency,
		Status:        result.Status,
		DeclineReason: result.DeclineReason,
		CreatedAt:     result.CreatedAt,
		UpdatedAt:     result.UpdatedAt,
	})
}

func decodePaymentResultSnapshot(data []byte) (app.PaymentResult, error) {
	var snapshot paymentResultSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return app.PaymentResult{}, err
	}

	return app.PaymentResult{
		ID:            snapshot.ID,
		OrderID:       snapshot.OrderID,
		CustomerID:    snapshot.CustomerID,
		AmountCents:   snapshot.AmountCents,
		Currency:      snapshot.Currency,
		Status:        snapshot.Status,
		DeclineReason: snapshot.DeclineReason,
		CreatedAt:     snapshot.CreatedAt,
		UpdatedAt:     snapshot.UpdatedAt,
	}, nil
}
