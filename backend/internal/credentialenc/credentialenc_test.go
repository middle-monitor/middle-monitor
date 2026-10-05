package credentialenc

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

const testHexKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func makeTestKeyRing(t *testing.T) *KeyRing {
	t.Helper()
	t.Setenv(EnvSingleKey, testHexKey)
	t.Setenv(EnvActiveKeyID, "v1")
	t.Setenv(EnvKeysJSON, "")
	kr, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestRoundTrip(t *testing.T) {
	kr := makeTestKeyRing(t)
	plain := []byte(`{"token":"secret"}`)
	blob, err := kr.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	var back Blob
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	out, err := kr.Decrypt(&back)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(plain) {
		t.Fatalf("got %q want %q", out, plain)
	}
}

func TestLoadFromEnv_InvalidJSON(t *testing.T) {
	t.Setenv(EnvKeysJSON, `{not json`)
	t.Setenv(EnvSingleKey, "")
	t.Setenv(EnvActiveKeyID, "v1")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadFromEnv_NoEnvVars(t *testing.T) {
	t.Setenv(EnvKeysJSON, "")
	t.Setenv(EnvSingleKey, "")
	t.Setenv(EnvActiveKeyID, "")
	kr, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(kr.Keys) != 0 {
		t.Fatal("expected empty key ring")
	}
}

func TestLoadFromEnv_JSONMap_ValidKey(t *testing.T) {
	// Use base64-encoded 32-byte key in JSON map
	key32 := make([]byte, 32)
	for i := range key32 {
		key32[i] = byte(i + 1)
	}
	encoded := base64.StdEncoding.EncodeToString(key32)
	jsonVal := `{"v1":"` + encoded + `"}`
	t.Setenv(EnvKeysJSON, jsonVal)
	t.Setenv(EnvSingleKey, "")
	t.Setenv(EnvActiveKeyID, "v1")
	kr, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(kr.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(kr.Keys))
	}
}

func TestLoadFromEnv_JSONMap_InvalidKey(t *testing.T) {
	t.Setenv(EnvKeysJSON, `{"v1":"notavalidkey"}`)
	t.Setenv(EnvSingleKey, "")
	t.Setenv(EnvActiveKeyID, "v1")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid key in JSON map")
	}
}

func TestLoadFromEnv_JSONMap_ActiveKeyNotInMap(t *testing.T) {
	key32 := make([]byte, 32)
	encoded := base64.StdEncoding.EncodeToString(key32)
	jsonVal := `{"v1":"` + encoded + `"}`
	t.Setenv(EnvKeysJSON, jsonVal)
	t.Setenv(EnvSingleKey, "")
	t.Setenv(EnvActiveKeyID, "v2") // v2 not in map
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error: active key not in map")
	}
}

func TestLoadFromEnv_SingleKey_InvalidKey(t *testing.T) {
	t.Setenv(EnvKeysJSON, "")
	t.Setenv(EnvSingleKey, "notavalidkey")
	t.Setenv(EnvActiveKeyID, "v1")
	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid single key")
	}
}

func TestLoadFromEnv_DefaultActiveKeyID(t *testing.T) {
	t.Setenv(EnvActiveKeyID, "")
	t.Setenv(EnvKeysJSON, "")
	t.Setenv(EnvSingleKey, testHexKey)
	kr, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if kr.ActiveID != "v1" {
		t.Fatalf("expected default active ID v1, got %q", kr.ActiveID)
	}
}

func TestDecodeKey32_EmptyString(t *testing.T) {
	_, err := decodeKey32("")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestDecodeKey32_TooShortBase64(t *testing.T) {
	// 16-byte base64 (not 32)
	_, err := decodeKey32(base64.StdEncoding.EncodeToString(make([]byte, 16)))
	if err == nil {
		t.Fatal("expected error: key must be 32 bytes")
	}
}

func TestDecodeKey32_ValidBase64_StdEncoding(t *testing.T) {
	key := make([]byte, 32)
	encoded := base64.StdEncoding.EncodeToString(key)
	b, err := decodeKey32(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatal("expected 32 bytes")
	}
}

func TestDecodeKey32_ValidBase64_URLEncoding(t *testing.T) {
	key := make([]byte, 32)
	encoded := base64.URLEncoding.EncodeToString(key)
	b, err := decodeKey32(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatal("expected 32 bytes")
	}
}

func TestDecodeKey32_ValidHex(t *testing.T) {
	b, err := decodeKey32(testHexKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatal("expected 32 bytes")
	}
}

func TestDecodeKey32_HexWrongLength(t *testing.T) {
	_, err := decodeKey32("0102") // too short
	if err == nil {
		t.Fatal("expected error for short hex key")
	}
}

func TestDecodeKey32_WithWhitespace(t *testing.T) {
	b, err := decodeKey32("  " + testHexKey + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatal("expected 32 bytes after trimming whitespace")
	}
}

func TestTrimSpace_NoSpaces(t *testing.T) {
	if got := trimSpace("hello"); got != "hello" {
		t.Fatalf("want hello, got %q", got)
	}
}

func TestTrimSpace_WithTabsAndNewlines(t *testing.T) {
	if got := trimSpace("\t key \n"); got != "key" {
		t.Fatalf("want 'key', got %q", got)
	}
}

func TestTrimSpace_EmptyString(t *testing.T) {
	if got := trimSpace(""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestMustHaveActiveKey_NilKeyRing(t *testing.T) {
	var k *KeyRing
	if err := k.MustHaveActiveKey(); err == nil {
		t.Fatal("expected error for nil key ring")
	}
}

func TestMustHaveActiveKey_KeyNotInMap(t *testing.T) {
	k := &KeyRing{ActiveID: "v1", Keys: map[string][]byte{}}
	if err := k.MustHaveActiveKey(); err == nil {
		t.Fatal("expected error: active key not configured")
	}
}

func TestMustHaveActiveKey_ValidKey(t *testing.T) {
	kr := makeTestKeyRing(t)
	if err := kr.MustHaveActiveKey(); err != nil {
		t.Fatal(err)
	}
}

func TestEncrypt_NoActiveKey(t *testing.T) {
	k := &KeyRing{ActiveID: "v1", Keys: map[string][]byte{}}
	_, err := k.Encrypt([]byte("hello"))
	if err == nil {
		t.Fatal("expected error: no active key")
	}
}

func TestDecrypt_NilKeyRing(t *testing.T) {
	var k *KeyRing
	_, err := k.Decrypt(&Blob{KID: "v1"})
	if err == nil {
		t.Fatal("expected error for nil key ring")
	}
}

func TestDecrypt_NilBlob(t *testing.T) {
	k := &KeyRing{ActiveID: "v1", Keys: map[string][]byte{}}
	_, err := k.Decrypt(nil)
	if err == nil {
		t.Fatal("expected error for nil blob")
	}
}

func TestDecrypt_KeyNotAvailable(t *testing.T) {
	k := &KeyRing{ActiveID: "v1", Keys: map[string][]byte{}}
	_, err := k.Decrypt(&Blob{KID: "v2", N: "nn", D: "dd"})
	if err == nil {
		t.Fatal("expected error: key id not available")
	}
}

func TestDecrypt_BadNonce(t *testing.T) {
	kr := makeTestKeyRing(t)
	_, err := kr.Decrypt(&Blob{KID: "v1", N: "not-valid-base64!!!", D: "dd"})
	if err == nil {
		t.Fatal("expected error for bad nonce")
	}
}

func TestDecrypt_BadCiphertext(t *testing.T) {
	kr := makeTestKeyRing(t)
	validNonce := base64.StdEncoding.EncodeToString(make([]byte, 12))
	_, err := kr.Decrypt(&Blob{KID: "v1", N: validNonce, D: "not-valid-base64!!!"})
	if err == nil {
		t.Fatal("expected error for bad ciphertext")
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	kr := makeTestKeyRing(t)
	blob, err := kr.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with ciphertext
	blob.D = base64.StdEncoding.EncodeToString([]byte("tampered"))
	_, err = kr.Decrypt(blob)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestGlobal_ReturnsKeyRing(t *testing.T) {
	// Global() uses a loadOnce; this test covers the first call in the process.
	// Subsequent calls within the same test binary return the cached result.
	kr, err := Global()
	if err != nil {
		t.Fatal(err)
	}
	// Global() returns a non-nil KeyRing (may have empty Keys if no env configured)
	if kr == nil {
		t.Fatal("expected non-nil key ring from Global()")
	}
}
