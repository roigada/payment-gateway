// Package openapivalidator validates the gateway's published OpenAPI contract.
package openapivalidator

import (
	"fmt"
	"os"

	"github.com/pb33f/libopenapi"
)

// ValidateFile loads and validates the OpenAPI document at path.
func ValidateFile(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read OpenAPI document: %w", err)
	}

	document, err := libopenapi.NewDocument(contents)
	if err != nil {
		return fmt.Errorf("parse OpenAPI document: %w", err)
	}

	if _, err := document.BuildV3Model(); err != nil {
		return fmt.Errorf("validate OpenAPI document: %w", err)
	}

	return nil
}
