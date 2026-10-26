//go:build e2e

package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

// The storage key pair embedded in every token-vault fixture. RSA, not EC:
// RSA-OAEP-256 wrapping is Go stdlib; EC would need ECDH-ES+A256KW.
const tokenVaultStorageKeyPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEAt2VCbR85SCwNbU3qwV7v/UPJZftr41+DLETO5pk8F/mhep+L
RRQdGeyZeICguNwG67NzQXCbXAUqQLOnoztAB42e1CGP428nG1Ze/jYMYpwlSEIK
20TCb47pF4POzoO7svouhsqJLApxcqQX4eVd0hiUl0RXF9ze0awbTKb7e9t4N9B0
XILGXaD6ybb8rBbRPXhhBd0FsKk5ND6lCIzEirRf5povaS+deBdnTaLdrPv0CnT4
uLQzk/BiSGDp/uFDxk/tTfDDTYesapZX7793jY+8pZCV9FpMBTDNgCpCG5+Tm66j
p7LSfCit3Hk6H9tJI7YoJdJNvb6V5sxK8mQWrQIDAQABAoIBAAalwXgiGxnuGth8
7AbxUYKdG+Y57Em0ExWeMt+x6zc1QbCnqs0osvtXE6MGCo1Y0FlnykQzeDYoK8Vn
napxgdskO38V7ljAlSrNASl2vREv9rv9RNZ40hFoHTPQf8UFil6s6dXR7pHtmPoe
T41NGo78fBpVjFCdnWf9NIodPV1aYcJvfQko4RSXu3ORk1x6s3+MBOH9rvhdk8o9
GEocGlWB7F3aFx1CyFCOzAOj5HY9EYScqgYKitTR6I+LD31VkkjzEUeWGqdWjF5x
kzzi3QzCIagEpRxOQ67B4dwIKXPoVAhMFRN3pnD4CrbEixaGv94LcMc8K9rxxVmC
nsV59KECgYEA/m7Jk2EueCVnflJqumJTJDo9k6yNPBccTWCZPDQpsn8llWDAA+OB
5OUuT9DdHuIplTUOF7QXrnzkFQGtHFhUV5qsA2UwclOlRCxLfIllIadDjE9kj30D
sIoLN22SA8YFHBVTvMvAvkpKE9ZVc/bpwGRoKxuoVAvB/RfkyJWMHdECgYEAuIZ0
Q+VM2ACjF8eG/9gQTOlq7XZZAXi6+Yhv9EH/VqB2uALdyKmFrW/tnDV+qX6FBSb2
MbaUxj/5PXCOgzHws6UAaCQyeCfTPmB14+rlJWQ0HLT5ajWq/XBPK+59kqeHnTrM
Vwn91dbBKV/T7zYplZf9j0KjomRF9FNoJwUY1h0CgYEAumNak/bbXfS4zld+DDA/
jAiGV4pOpLihaSXc27ey3Co81CNeRvsBizdBKANwGVHA+Kc3PbIWryKay7DAqkUi
9FJAtPQJ+5a/OSPh4MeeKHsqBr2ag2snqspV0TMZhl4oHBybjXEOL9ioT8PyMdOu
Bd0IWpwWQgui/s/kL/UuAiECgYAFnfouEu3a6XINiEJgyWf9n2Fk5yRgehde/ztB
OT0XmvziLxwnPpnWJMEb7zjy2eoctQnri35N5qakqyd7cOq93Tj88pUiuTuh+3e7
P2xIThuUACJTjFyvKFsQoErsSvNC4wZTY/d5KfdpVgRylLiBWEYctU+b4ueKP3u9
LFeMNQKBgQD8ZUUHqYhPeHZn+1WnMt4UcH+7PV6Dbthr06nIUTCF0XQWe9ZLDqCv
b7562NMXjf7FaXNu+ez1uCogXND2WMmfgU+ENN+/FlegZNZe5ZflfDg+ItChYv9d
jm9K9f8LkGU4f5ijQzaYv2JuVb/f35k2x9vmV0pn4pU9UT6h4F6xAQ==
-----END RSA PRIVATE KEY-----`

var (
	tokenVaultKeyOnce sync.Once
	tokenVaultKey     *rsa.PrivateKey
)

func tokenVaultStorageKey() *rsa.PrivateKey {
	tokenVaultKeyOnce.Do(func() {
		block, _ := pem.Decode([]byte(tokenVaultStorageKeyPEM))
		if block == nil {
			panic("tokenVaultStorageKeyPEM is not a valid PEM block")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			panic("tokenVaultStorageKeyPEM is not a PKCS#1 RSA key: " + err.Error())
		}
		tokenVaultKey = key
	})
	return tokenVaultKey
}

// tokenVaultKeyID returns the RFC 7638 thumbprint Kong derives for the
// storage key (base64url of SHA-256 over the canonical public JWK).
func tokenVaultKeyID(key *rsa.PrivateKey) string {
	jwk := fmt.Sprintf(`{"e":%q,"kty":"RSA","n":%q}`,
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		base64.RawURLEncoding.EncodeToString(key.N.Bytes()))
	sum := sha256.Sum256([]byte(jwk))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// sealTokenVaultCredential seals plaintext into a Token Vault envelope
// (kong/llm/token_vault/crypto.lua format). The gateway decrypts it with
// the matching storage key.
func sealTokenVaultCredential(t *testing.T, plaintext string) string {
	t.Helper()

	key := tokenVaultStorageKey()

	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("generating DEK: %v", err)
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("generating IV: %v", err)
	}

	// RSA-OAEP-256: SHA-256 for both the OAEP digest and MGF1.
	wrap, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &key.PublicKey, dek, nil)
	if err != nil {
		t.Fatalf("wrapping DEK: %v", err)
	}

	block, err := aes.NewCipher(dek)
	if err != nil {
		t.Fatalf("initializing AES-256: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("initializing GCM: %v", err)
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), []byte("kong.token-vault-envelope/1"))
	ct, tag := sealed[:len(sealed)-gcm.Overhead()], sealed[len(sealed)-gcm.Overhead():]

	envelope, err := json.Marshal(map[string]any{
		"v": 1,
		"deks": []map[string]string{{
			"kid":  tokenVaultKeyID(key),
			"alg":  "RSA-OAEP-256",
			"wrap": base64.RawURLEncoding.EncodeToString(wrap),
		}},
		"iv":  base64.RawURLEncoding.EncodeToString(iv),
		"ct":  base64.RawURLEncoding.EncodeToString(ct),
		"tag": base64.RawURLEncoding.EncodeToString(tag),
	})
	if err != nil {
		t.Fatalf("encoding envelope: %v", err)
	}
	return string(envelope)
}
