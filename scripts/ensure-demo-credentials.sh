#!/bin/sh
set -eu

credentials_file="${DEMO_CREDENTIALS_FILE:-.env}"

if [ -f "$credentials_file" ]; then
  exit 0
fi

umask 077

key_hex="$(openssl rand -hex 32)"
hmac_key="$(printf '%s' "$key_hex" | xxd -r -p | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
credential="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n')"
digest="$(printf '%s' "$credential" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$key_hex" -binary | openssl base64 -A | tr '+/' '-_' | tr -d '=')"
fingerprint_secret="$(openssl rand -base64 32 | tr -d '\n')"

cat > "$credentials_file" <<EOF
# Generated for the local Compose demo. Do not commit this file.
# Delete it and run make demo again to rotate the throwaway credential.
FINGERPRINT_SECRET=$fingerprint_secret
SERVICE_CREDENTIAL_HMAC_KEY=$hmac_key
ORDER_SERVICE_CREDENTIALS=$digest=payments:read+payments:write
ORDER_SERVICE_CREDENTIAL=$credential
EOF

printf 'generated local throwaway demo credentials in %s\n' "$credentials_file"
