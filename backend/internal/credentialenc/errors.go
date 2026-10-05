package credentialenc

import (
	"errors"
	"fmt"
)

var (
	ErrKeyRingNotLoaded = errors.New("credential encryption: key ring not loaded")
	ErrKeyEmpty         = errors.New("empty key")
	ErrKeyLength        = errors.New("key must decode to exactly 32 bytes (use base64 or 64 hex chars)")
	ErrInvalidBlob      = errors.New("credential encryption: invalid blob")
)

// ActiveKeyMissingError reports an active key id that no configured key matches.
type ActiveKeyMissingError struct {
	KeyID string
}

func (e *ActiveKeyMissingError) Error() string {
	return fmt.Sprintf("credential encryption: active key %q is not configured (set %s or %s)", e.KeyID, EnvKeysJSON, EnvSingleKey)
}

// UnknownKeyIDError reports a blob encrypted with a key that is no longer configured.
type UnknownKeyIDError struct {
	KeyID string
}

func (e *UnknownKeyIDError) Error() string {
	return fmt.Sprintf("credential encryption: key id %q is not available (rotation: keep old keys in %s)", e.KeyID, EnvKeysJSON)
}

// Configuration errors reported at startup.
var (
	ErrKeysJSONInvalid = errors.New("keys json is not an object of base64 keys")
)
