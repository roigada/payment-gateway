package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	invalidJSONBodyMessage = "invalid JSON body"
	maxJSONBodyBytes       = 1 << 20
)

var (
	errInvalidJSONBody    = errors.New("invalid JSON body")
	errMalformedJSONBody  = errors.New("malformed JSON body")
	errIncorrectJSONType  = errors.New("incorrect JSON type")
	errEmptyJSONBody      = errors.New("empty JSON body")
	errNonEmptyBody       = errors.New("non-empty body")
	errUnknownJSONField   = errors.New("unknown JSON field")
	errOversizedJSONBody  = errors.New("oversized JSON body")
	errMultipleJSONValues = errors.New("multiple JSON values")
)

func requireEmptyRequestBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}

	var firstByte [1]byte
	n, err := io.ReadFull(r.Body, firstByte[:])
	if n > 0 {
		return errNonEmptyBody
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, body any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(body); err != nil {
		if syntaxError, ok := errors.AsType[*json.SyntaxError](err); ok {
			return fmt.Errorf("%w: %w: at character %d", errInvalidJSONBody, errMalformedJSONBody, syntaxError.Offset)
		}

		if errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: %w", errInvalidJSONBody, errMalformedJSONBody)
		}

		if unmarshalTypeError, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("%w: %w: field %q", errInvalidJSONBody, errIncorrectJSONType, unmarshalTypeError.Field)
			}
			return fmt.Errorf("%w: %w: at character %d", errInvalidJSONBody, errIncorrectJSONType, unmarshalTypeError.Offset)
		}

		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: %w", errInvalidJSONBody, errEmptyJSONBody)
		}

		if after, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			fieldName := after
			return fmt.Errorf("%w: %w: %s", errInvalidJSONBody, errUnknownJSONField, fieldName)
		}

		if maxBytesError, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return fmt.Errorf("%w: %w: limit %d bytes", errInvalidJSONBody, errOversizedJSONBody, maxBytesError.Limit)
		}

		if _, ok := errors.AsType[*json.InvalidUnmarshalError](err); ok {
			panic(err)
		}

		return err
	}

	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: %w: %v", errInvalidJSONBody, errMalformedJSONBody, err)
	}

	return fmt.Errorf("%w: %w", errInvalidJSONBody, errMultipleJSONValues)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
