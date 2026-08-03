package protocol

import (
	"encoding/json"
	"testing"
)

func TestUpdateProtocolBodiesRoundTrip(t *testing.T) {
	command := UpdateBody{Action: UpdateActionLatest}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var got UpdateBody
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != command || FrameUpdate != FrameType("update") || FrameUpdateStatus != FrameType("update_status") {
		t.Fatalf("update protocol did not round-trip: %#v", got)
	}
}
