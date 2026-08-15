package serviceauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatorAuthenticatesOverlappingCredentialsAndScopes(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	readCredential := "read-credential"
	writeCredential := "write-credential"
	authenticator, err := NewAuthenticator(key, []Credential{
		{Digest: Digest(key, readCredential), Scopes: []Scope{ScopePaymentsRead}},
		{Digest: Digest(key, writeCredential), Scopes: []Scope{ScopePaymentsRead, ScopePaymentsWrite}},
	})
	require.NoError(t, err)

	for _, tt := range []struct {
		name, credential          string
		scope                     Scope
		authenticated, authorized bool
	}{
		{"read credential", readCredential, ScopePaymentsRead, true, true},
		{"read credential lacks write", readCredential, ScopePaymentsWrite, true, false},
		{"rotated credential", writeCredential, ScopePaymentsWrite, true, true},
		{"missing", "", ScopePaymentsRead, false, false},
		{"invalid", "not-configured", ScopePaymentsRead, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			principal, authenticated := authenticator.Authenticate(tt.credential)
			authorized := authenticated && principal.HasScope(tt.scope)
			assert.Equal(t, tt.authenticated, authenticated)
			assert.Equal(t, tt.authorized, authorized)
		})
	}
}

func TestAuthenticatorRejectsInvalidConfiguration(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	for _, configured := range [][]Credential{nil, {{Digest: "not-base64", Scopes: []Scope{ScopePaymentsRead}}}, {{Digest: Digest(key, "credential")}}} {
		_, err := NewAuthenticator(key, configured)
		assert.Error(t, err)
	}
}

func TestGenerateCredentialProducesOpaqueHighEntropyValue(t *testing.T) {
	credential, err := GenerateCredential()
	require.NoError(t, err)
	assert.Len(t, credential, 43)
}
