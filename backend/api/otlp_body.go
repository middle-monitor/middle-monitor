package api

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"

	"middle-monitor/backend/services"
)

// OTLP exporters batch, so a legitimate body stays far below these; past them
// the client gets a 413 that says what to change instead of a generic failure.
const (
	maxOTLPWireBytes    = 16 << 20
	maxOTLPDecodedBytes = 64 << 20
)

// readOTLPBody returns the decoded body, gunzipping when the exporter compressed
// it, as the OTLP/HTTP spec requires servers to accept.
func readOTLPBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	src := io.Reader(http.MaxBytesReader(w, r.Body, maxOTLPWireBytes))
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(src)
		if err != nil {
			return nil, ErrRequestBodyRead
		}
		defer zr.Close()
		src = zr
	default:
		return nil, ErrContentEncodingUnsupported
	}

	body, err := io.ReadAll(io.LimitReader(src, maxOTLPDecodedBytes+1))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return nil, ErrOTLPPayloadTooLarge
	case err != nil:
		return nil, ErrRequestBodyRead
	case len(body) > maxOTLPDecodedBytes:
		return nil, ErrOTLPPayloadTooLarge
	}
	return body, nil
}

// respondOTLPBodyError maps a body failure to the status OTLP clients act on.
func respondOTLPBodyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrOTLPPayloadTooLarge):
		respondError(w, http.StatusRequestEntityTooLarge, err)
	case errors.Is(err, ErrContentEncodingUnsupported):
		respondError(w, http.StatusUnsupportedMediaType, err)
	default:
		respondError(w, http.StatusBadRequest, err)
	}
}

// respondPublishError answers 503 on a broker failure, which OTLP exporters retry
// with backoff; a 500 is final for them and the data was lost.
func respondPublishError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrKafkaPublish):
		w.Header().Set("Retry-After", "5")
		respondError(w, http.StatusServiceUnavailable, err)
	case errors.Is(err, services.ErrOTLPDecode):
		respondError(w, http.StatusBadRequest, err)
	case errors.Is(err, services.ErrOTLPItemTooLarge):
		respondError(w, http.StatusRequestEntityTooLarge, err)
	default:
		respondError(w, http.StatusInternalServerError, err)
	}
}
