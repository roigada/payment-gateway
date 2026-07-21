// service-credential generates a one-time opaque Service Credential and its
// configured HMAC digest. Store only the raw credential in the Order Service.
package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"

	"github.com/roigada/payment-gateway/internal/serviceauth"
)

func main() {
	hmacKey := flag.String("hmac-key", "", "base64url-encoded Service Credential HMAC key")
	flag.Parse()
	if *hmacKey == "" {
		fmt.Fprintln(os.Stderr, "-hmac-key is required")
		os.Exit(2)
	}
	key, err := base64.RawURLEncoding.DecodeString(*hmacKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "-hmac-key must be base64url-encoded")
		os.Exit(2)
	}
	if err := serviceauth.ValidateHMACKey(key); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	credential, err := serviceauth.GenerateCredential()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate credential:", err)
		os.Exit(1)
	}
	fmt.Println("raw credential (store only in the Order Service):", credential)
	fmt.Println("ORDER_SERVICE_CREDENTIALS entry:", serviceauth.Digest(key, credential)+"=payments:read+payments:write")
}
