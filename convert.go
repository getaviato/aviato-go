package aviato

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"
)

// toStruct converts a JSON object to a protobuf Struct. It goes through encoding/json so that
// any JSON-marshalable value works (typed slices, maps, structs with json tags, time.Time…),
// not only the handful of types structpb.NewStruct accepts. A nil map gives a nil Struct.
func toStruct(value map[string]any) (*structpb.Struct, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("aviato: value is not JSON-serializable: %w", err)
	}
	result := &structpb.Struct{}
	if err := result.UnmarshalJSON(encoded); err != nil {
		return nil, fmt.Errorf("aviato: value is not a JSON object: %w", err)
	}
	return result, nil
}

func fromStruct(value *structpb.Struct) map[string]any {
	if value == nil {
		return nil
	}
	return value.AsMap()
}

func fromStructs(values []*structpb.Struct) []Record {
	records := make([]Record, 0, len(values))
	for _, value := range values {
		records = append(records, fromStruct(value))
	}
	return records
}
