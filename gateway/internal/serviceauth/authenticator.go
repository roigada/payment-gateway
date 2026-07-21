// Package serviceauth verifies opaque credentials used by trusted services.
package serviceauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

const (
	OrderServicePrincipal       = "order-service"
	ScopePaymentsRead     Scope = "payments:read"
	ScopePaymentsWrite    Scope = "payments:write"
	credentialBytes             = 32
	digestBytes                 = sha256.Size
)

type Scope string

type Credential struct {
	Digest string
	Scopes []Scope
}

type Principal struct{ scopes map[Scope]struct{} }

type Authenticator struct {
	key         []byte
	credentials []credential
}

type credential struct {
	digest [digestBytes]byte
	scopes map[Scope]struct{}
}

func GenerateCredential() (string, error) {
	raw := make([]byte, credentialBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func Digest(key []byte, rawCredential string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(rawCredential))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func NewAuthenticator(key []byte, configured []Credential) (*Authenticator, error) {
	if err := ValidateHMACKey(key); err != nil {
		return nil, err
	}
	if len(configured) == 0 {
		return nil, errors.New("at least one service credential is required")
	}

	authenticator := &Authenticator{key: append([]byte(nil), key...)}
	seen := make(map[string]struct{}, len(configured))
	for _, configuredCredential := range configured {
		digest, err := base64.RawURLEncoding.DecodeString(configuredCredential.Digest)
		if err != nil || len(digest) != digestBytes {
			return nil, errors.New("service credential digest must be a base64url-encoded SHA-256 HMAC")
		}
		if _, ok := seen[configuredCredential.Digest]; ok {
			return nil, errors.New("service credential digests must be unique")
		}
		seen[configuredCredential.Digest] = struct{}{}
		if len(configuredCredential.Scopes) == 0 {
			return nil, errors.New("service credential must grant at least one scope")
		}

		credential := credential{scopes: make(map[Scope]struct{}, len(configuredCredential.Scopes))}
		copy(credential.digest[:], digest)
		for _, scope := range configuredCredential.Scopes {
			scope = Scope(strings.TrimSpace(string(scope)))
			if scope != ScopePaymentsRead && scope != ScopePaymentsWrite {
				return nil, errors.New("service credential contains an unsupported scope")
			}
			credential.scopes[scope] = struct{}{}
		}
		authenticator.credentials = append(authenticator.credentials, credential)
	}
	return authenticator, nil
}

func ValidateHMACKey(key []byte) error {
	if len(key) < credentialBytes {
		return errors.New("service credential HMAC key must be at least 32 bytes")
	}
	return nil
}

func (a *Authenticator) Authenticate(r *http.Request) (Principal, bool) {
	credential, ok := bearerCredential(r.Header.Get("Authorization"))
	if !ok {
		return Principal{}, false
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(credential))
	digest := mac.Sum(nil)
	matched := -1
	for i := range a.credentials {
		if hmac.Equal(digest, a.credentials[i].digest[:]) {
			matched = i
		}
	}
	if matched < 0 {
		return Principal{}, false
	}
	return Principal{scopes: a.credentials[matched].scopes}, true
}

func (p Principal) HasScope(scope Scope) bool {
	_, ok := p.scopes[scope]
	return ok
}

func bearerCredential(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
