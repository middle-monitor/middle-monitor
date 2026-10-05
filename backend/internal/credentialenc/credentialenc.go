// Package credentialenc provides AES-256-GCM encryption for at-rest secrets (e.g. HTTP check auth).
//
// Key configuration (rotation-friendly):
//
//   - CREDENTIALS_ENCRYPTION_KEYS_JSON — JSON map of key id -> base64-encoded 32-byte key, e.g. {"v1":"...","v2":"..."}
//   - CREDENTIALS_ENCRYPTION_ACTIVE_KEY_ID — which key id is used for new encryptions (default: v1)
//
// Simple single-key setup (dev / small deploys):
//
//   - CREDENTIALS_ENCRYPTION_KEY — single base64 (or hex) 32-byte key; treated as the active key (default id v1).
//
// Rotation: add the new key to CREDENTIALS_ENCRYPTION_KEYS_JSON, set ACTIVE_KEY_ID to the new id,
// keep old ids in JSON so existing ciphertext can still be decrypted. Re-encrypt stored rows if you
// want to drop old keys from config later.
package credentialenc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const (
	// EnvKeysJSON is a JSON object: { "v1": "<base64 32-byte key>", "v2": "..." }
	EnvKeysJSON = "CREDENTIALS_ENCRYPTION_KEYS_JSON"
	// EnvActiveKeyID selects which key from the map is used for Encrypt.
	EnvActiveKeyID = "CREDENTIALS_ENCRYPTION_ACTIVE_KEY_ID"
	// EnvSingleKey is a convenience: one base64/hex 32-byte key; uses ActiveKeyID (default v1).
	EnvSingleKey = "CREDENTIALS_ENCRYPTION_KEY"
)

// Blob is the serialized form stored inside service credentials JSON.
type Blob struct {
	KID string `json:"kid"`
	N   string `json:"n"` // nonce, base64
	D   string `json:"d"` // ciphertext + tag, base64
}

var (
	loadOnce sync.Once
	kr       *KeyRing
	loadErr  error
)

// KeyRing holds AES-256 keys by logical id.
type KeyRing struct {
	ActiveID string
	Keys     map[string][]byte
}

// Global returns the lazily loaded key ring (may have empty Keys if no env configured).
func Global() (*KeyRing, error) {
	loadOnce.Do(func() {
		kr, loadErr = LoadFromEnv()
	})
	return kr, loadErr
}

// MustHaveActiveKey returns an error if encryption is required but no active key exists.
func (k *KeyRing) MustHaveActiveKey() error {
	if k == nil {
		return ErrKeyRingNotLoaded
	}
	key, ok := k.Keys[k.ActiveID]
	if !ok || len(key) != 32 {
		return &ActiveKeyMissingError{KeyID: k.ActiveID}
	}
	return nil
}

// LoadFromEnv parses encryption keys. Empty config yields Keys == empty map (encryption disabled).
func LoadFromEnv() (*KeyRing, error) {
	active := os.Getenv(EnvActiveKeyID)
	if active == "" {
		active = "v1"
	}
	keys := make(map[string][]byte)

	if j := os.Getenv(EnvKeysJSON); j != "" {
		var raw map[string]string
		if err := json.Unmarshal([]byte(j), &raw); err != nil {
			return nil, fmt.Errorf("%s: %w: %w", EnvKeysJSON, ErrKeysJSONInvalid, err)
		}
		for id, s := range raw {
			kb, err := decodeKey32(s)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", id, err)
			}
			keys[id] = kb
		}
	} else if single := os.Getenv(EnvSingleKey); single != "" {
		kb, err := decodeKey32(single)
		if err != nil {
			return nil, err
		}
		keys[active] = kb
	}

	if len(keys) > 0 {
		if _, ok := keys[active]; !ok {
			return nil, &ActiveKeyMissingError{KeyID: active}
		}
	}

	return &KeyRing{ActiveID: active, Keys: keys}, nil
}

func decodeKey32(s string) ([]byte, error) {
	s = trimSpace(s)
	if s == "" {
		return nil, ErrKeyEmpty
	}
	// Base64
	for _, dec := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		b, err := dec.DecodeString(s)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}
	// Hex
	if len(s) == 64 {
		b, err := hex.DecodeString(s)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, ErrKeyLength
}

func trimSpace(s string) string {
	// avoid importing strings for tiny helper
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}

// Encrypt encrypts plaintext with the active key. Returns a Blob suitable for JSON marshaling.
func (k *KeyRing) Encrypt(plaintext []byte) (*Blob, error) {
	if err := k.MustHaveActiveKey(); err != nil {
		return nil, err
	}
	key := k.Keys[k.ActiveID]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return &Blob{
		KID: k.ActiveID,
		N:   base64.StdEncoding.EncodeToString(nonce),
		D:   base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

// Decrypt decrypts a blob using the key id recorded in the blob.
func (k *KeyRing) Decrypt(blob *Blob) ([]byte, error) {
	if k == nil || blob == nil {
		return nil, ErrInvalidBlob
	}
	key, ok := k.Keys[blob.KID]
	if !ok || len(key) != 32 {
		return nil, &UnknownKeyIDError{KeyID: blob.KID}
	}
	nonce, err := base64.StdEncoding.DecodeString(blob.N)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w: %w", ErrInvalidBlob, err)
	}
	ct, err := base64.StdEncoding.DecodeString(blob.D)
	if err != nil {
		return nil, fmt.Errorf("ciphertext: %w: %w", ErrInvalidBlob, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, nil)
}
