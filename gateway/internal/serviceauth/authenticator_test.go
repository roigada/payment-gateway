package serviceauth

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verifies that authenticator authenticates overlapping credentials and scopes.
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
		// Verifies the table-defined scenario for this case.
		t.Run(tt.name, func(t *testing.T) {
			principal, authenticated := authenticator.Authenticate(tt.credential)
			authorized := authenticated && principal.HasScope(tt.scope)
			assert.Equal(t, tt.authenticated, authenticated)
			assert.Equal(t, tt.authorized, authorized)
		})
	}
}

// Verifies that authenticator rejects invalid configuration.
func TestAuthenticatorRejectsInvalidConfiguration(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	for _, configured := range [][]Credential{nil, {{Digest: "not-base64", Scopes: []Scope{ScopePaymentsRead}}}, {{Digest: Digest(key, "credential")}}} {
		_, err := NewAuthenticator(key, configured)
		assert.Error(t, err)
	}
}

// Verifies that generate credential produces an opaque, URL-safe value carrying
// the full credential entropy, and that successive values do not repeat.
func TestGenerateCredentialProducesOpaqueHighEntropyValue(t *testing.T) {
	const generated = 100

	seen := make(map[string]struct{}, generated)
	for range generated {
		credential, err := GenerateCredential()
		require.NoError(t, err)

		raw, err := base64.RawURLEncoding.DecodeString(credential)
		require.NoError(t, err, "credential must decode as unpadded base64url")
		assert.Len(t, raw, credentialBytes, "credential must carry the full random payload")
		assert.NotEqual(t, make([]byte, credentialBytes), raw, "credential must not be all zero bytes")
		assert.Len(t, credential, base64.RawURLEncoding.EncodedLen(credentialBytes))

		_, repeated := seen[credential]
		assert.False(t, repeated, "credentials must not repeat")
		seen[credential] = struct{}{}
	}

	assert.Len(t, seen, generated)
}
