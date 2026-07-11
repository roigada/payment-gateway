package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/roigada/payment-gateway/internal/openapivalidator"
)

func main() {
	contractPath := flag.String("file", "docs/api/openapi.yaml", "path to the OpenAPI contract")
	flag.Parse()

	if err := openapivalidator.ValidateFile(*contractPath); err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPI contract validation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("OpenAPI contract is valid: %s\n", *contractPath)
}
