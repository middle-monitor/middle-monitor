package workers

import (
	"encoding/json"
	"fmt"
)

// decodeResultMetadata turns the JSON text stored on a check result into the
// object shape the worker-results index expects. Only a JSON object is valid:
// the index maps metadata as an object, so a scalar or an array is rejected the
// same way the raw string was.
func decodeResultMetadata(raw string) (map[string]interface{}, error) {
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		// One sentinel whatever the shape: valid JSON of the wrong kind
		// (array, number, string) and invalid JSON alike are "not an object"
		// to the caller, and errors.Is must see that.
		return nil, fmt.Errorf("%w: %v", ErrMetadataNotObject, err)
	}
	if decoded == nil {
		return nil, ErrMetadataNotObject
	}
	return decoded, nil
}
