package api

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"middle-monitor/backend/models"
)

// A Kafka message over the broker limit is refused whole, which would drop a
// zone's results; every chunk must fit, and no result may be lost or doubled.
func TestSplitResultBatchKeepsChunksUnderTheLimit(t *testing.T) {
	batch := models.CommandResultBatch{OrganizationID: 7, Zone: "z", Hostname: "sat1"}
	for i := 0; i < 300; i++ {
		batch.Results = append(batch.Results, models.CommandResult{ServiceID: int64(i), Output: strings.Repeat("x", 1000)})
	}
	const limit = 50_000
	chunks := splitResultBatch(batch, limit)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunk, want the batch split", len(chunks))
	}
	seen := 0
	for i, chunk := range chunks {
		encoded, _ := json.Marshal(chunk)
		if len(encoded) > limit {
			t.Errorf("chunk %d is %d bytes, over %d", i, len(encoded), limit)
		}
		if chunk.OrganizationID != 7 || chunk.Zone != "z" {
			t.Errorf("chunk %d lost the batch header: %+v", i, chunk)
		}
		for _, result := range chunk.Results {
			if result.ServiceID != int64(seen) {
				t.Fatalf("result %d out of order or duplicated", result.ServiceID)
			}
			seen++
		}
	}
	if seen != 300 {
		t.Fatalf("got %d results back, want 300", seen)
	}
}

func TestTrimCommandResultKeepsValidUTF8(t *testing.T) {
	result := models.CommandResult{Output: strings.Repeat("é", maxCommandOutputBytes)}
	trimCommandResult(&result)
	if len(result.Output) > maxCommandOutputBytes || !utf8.ValidString(result.Output) {
		t.Fatalf("output %d bytes, valid utf8 %v", len(result.Output), utf8.ValidString(result.Output))
	}
}
