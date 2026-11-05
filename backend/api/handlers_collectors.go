package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

const (
	maxCollectorBodyBytes  = 8 << 20
	maxCollectorResults    = 2000
	maxCommandOutputBytes  = 4 << 10
	maxCommandLongOutBytes = 8 << 10
	maxCommandPerfdata     = 32
)

// collectorOrgID resolves the organization from the collector's install token.
func collectorOrgID(r *http.Request, tokens *services.InstallTokenService) (int64, error) {
	token := r.Header.Get("X-Install-Token")
	if token == "" {
		if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
			token = auth[7:]
		}
	}
	if token == "" {
		return 0, ErrInstallTokenRequired
	}
	orgID, err := tokens.ValidateToken(token)
	if err != nil {
		return 0, ErrInstallTokenInvalid
	}
	return orgID, nil
}

// handleCollectorSync records a zone collector and answers the checks of its zone.
func handleCollectorSync(db *sql.DB) http.HandlerFunc {
	tokens := services.NewInstallTokenService(db)
	collectors := services.NewCollectorService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID, err := collectorOrgID(r, tokens)
		if err != nil {
			respondError(w, http.StatusUnauthorized, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var sync models.CollectorSync
		if err := json.NewDecoder(r.Body).Decode(&sync); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if sync.Zone == "" || sync.Hostname == "" {
			respondError(w, http.StatusBadRequest, ErrCollectorFieldsMissing)
			return
		}
		response, err := collectors.Sync(orgID, sync)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// handleCollectorResults queues a batch of command check results for the worker.
func handleCollectorResults(db *sql.DB) http.HandlerFunc {
	tokens := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID, err := collectorOrgID(r, tokens)
		if err != nil {
			respondError(w, http.StatusUnauthorized, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCollectorBodyBytes)
		var batch models.CommandResultBatch
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if batch.Zone == "" || batch.Hostname == "" {
			respondError(w, http.StatusBadRequest, ErrCollectorFieldsMissing)
			return
		}
		if len(batch.Results) > maxCollectorResults {
			respondError(w, http.StatusRequestEntityTooLarge, ErrCollectorBatchTooLarge)
			return
		}
		// The token decides the organization, never the body.
		batch.OrganizationID = orgID
		for i := range batch.Results {
			trimCommandResult(&batch.Results[i])
		}

		for _, chunk := range splitResultBatch(batch, services.MaxOTLPMessageBytes) {
			// Keyed by zone: one zone's results stay in order on one partition.
			if err := services.PublishPayloadWithOrg(services.KafkaWriterCheckResults, chunk.Zone, chunk, orgID); err != nil {
				respondPublishError(w, err)
				return
			}
		}
		slog.Debug("queued command results", "zone", batch.Zone, "collector", batch.Hostname, "count", len(batch.Results))
		w.WriteHeader(http.StatusAccepted)
	}
}

// trimCommandResult caps what one plugin can put in a Kafka message and in the
// state row; the agent caps its output too, but at a size meant for a terminal.
func trimCommandResult(result *models.CommandResult) {
	result.Output = truncateUTF8(result.Output, maxCommandOutputBytes)
	result.LongOutput = truncateUTF8(result.LongOutput, maxCommandLongOutBytes)
	if len(result.Perfdata) > maxCommandPerfdata {
		result.Perfdata = result.Perfdata[:maxCommandPerfdata]
	}
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	// Back up to a rune boundary so the JSON stays valid UTF-8.
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

// splitResultBatch cuts a batch into chunks whose JSON stays under limit, so
// no Kafka message exceeds the broker's size.
func splitResultBatch(batch models.CommandResultBatch, limit int) []models.CommandResultBatch {
	header := batch
	header.Results = nil
	base, _ := json.Marshal(header)

	var chunks []models.CommandResultBatch
	current := header
	size := len(base)
	for _, result := range batch.Results {
		encoded, _ := json.Marshal(result)
		if len(current.Results) > 0 && size+len(encoded)+1 > limit {
			chunks = append(chunks, current)
			current = header
			size = len(base)
		}
		current.Results = append(current.Results, result)
		size += len(encoded) + 1
	}
	if len(current.Results) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}
