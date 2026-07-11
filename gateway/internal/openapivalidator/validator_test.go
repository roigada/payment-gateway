package openapivalidator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/roigada/payment-gateway/internal/openapivalidator"
)

func TestValidateFile(t *testing.T) {
	t.Parallel()

	contractPath := filepath.Join("..", "..", "docs", "api", "openapi.yaml")

	require.NoError(t, openapivalidator.ValidateFile(contractPath))
}

func TestValidateFileReturnsValidationError(t *testing.T) {
	t.Parallel()

	contractPath := filepath.Join(t.TempDir(), "openapi.yaml")
	require.NoError(t, os.WriteFile(contractPath, []byte(`
openapi: 3.1.0
info:
  title: Broken contract
  version: 1.0.0
paths:
  /payments:
    get:
      responses:
        "200":
          description: Payment response
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/MissingPayment"
`), 0o600))

	require.Error(t, openapivalidator.ValidateFile(contractPath))
}
