package dryrun

import (
	"encoding/json"
	"fmt"
	"os"

	sdkv1 "github.com/kairos-dev-kairos-ecl/ArgusSDK/gen/go/sdk/v1"
	"github.com/kairos-dev-kairos-ecl/ArgusSDK/internal/ocsf"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// writeProto serialises a SignalBatch to two files:
//   - <jsonPath>: protojson (human-readable, field names as strings)
//   - <binPath>:  binary proto wire format (for tooling / re-ingestion)
func writeProto(jsonPath, binPath string, batch *sdkv1.SignalBatch) error {
	pjm := protojson.MarshalOptions{
		Multiline:       true,
		EmitUnpopulated: false,
		UseProtoNames:   true, // snake_case field names matching the .proto file
	}
	jsonBytes, err := pjm.Marshal(batch)
	if err != nil {
		return fmt.Errorf("protojson marshal: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonBytes, 0640); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}

	binBytes, err := proto.Marshal(batch)
	if err != nil {
		return fmt.Errorf("proto binary marshal: %w", err)
	}
	if err := os.WriteFile(binPath, binBytes, 0640); err != nil {
		return fmt.Errorf("write %s: %w", binPath, err)
	}
	return nil
}

// writeOCSF serialises OCSF events to a JSON array. Nil events (from mapper
// errors) are omitted — the Report already records those failures.
func writeOCSF(path string, events []*ocsf.Event) error {
	filtered := make([]*ocsf.Event, 0, len(events))
	for _, ev := range events {
		if ev != nil {
			filtered = append(filtered, ev)
		}
	}
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal ocsf: %w", err)
	}
	return os.WriteFile(path, data, 0640)
}
