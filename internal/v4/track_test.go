package v4

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestClickTrackInvariants(t *testing.T) {
	track, passtime := clickTrack([]Point{{0.25, 0.5}, {0.75, 0.25}})
	if track.M != 1 || track.W != trackWidth || track.H != trackHeight {
		t.Fatalf("unexpected header %+v", track)
	}
	if len(track.P) == 0 {
		t.Fatal("empty trajectory")
	}
	if track.P[0][0] != 0 || track.P[0][3] != 0 {
		t.Fatalf("unexpected first event %v", track.P[0])
	}
	last := track.P[len(track.P)-1]
	if passtime != int(last[0]) || track.E-track.S != int64(last[0]) {
		t.Fatalf("passtime %d, last %v, e-s %d", passtime, last, track.E-track.S)
	}
	if last[3] != 2 {
		t.Fatalf("last action is %v, want 2", last[3])
	}
	// build() groups non-move events after the moves once the track exceeds
	// 150 events, so only per-event bounds are checked here.
	for _, event := range track.P {
		if event[1] < 0 || event[1] > 1 || event[2] < 0 || event[2] > 1 {
			t.Fatalf("coordinate outside [0,1]: %v", event)
		}
		if event[3] != 0 && event[3] != 1 && event[3] != 2 && event[3] != 3 {
			t.Fatalf("unexpected action %v", event[3])
		}
	}
}

func TestSlideTrackPasstime(t *testing.T) {
	track, passtime := slideTrack(80)
	if passtime < 600 || passtime > 1400 {
		t.Fatalf("duration %d out of range", passtime)
	}
	if track.H != slideHeight {
		t.Fatalf("height %v", track.H)
	}
	if track.P[len(track.P)-1][3] != 2 {
		t.Fatal("slide track must end with the release action")
	}
}

func TestBoardTrackCoordinates(t *testing.T) {
	for _, gomoku := range []bool{false, true} {
		track, _ := boardTrack([][2]int{{0, 0}, {2, 2}}, gomoku)
		for _, event := range track.P {
			if event[1] < 0 || event[1] > 1 || event[2] < 0 || event[2] > 1 {
				t.Fatalf("gomoku=%v: coordinate outside [0,1]: %v", gomoku, event)
			}
		}
	}
}

func TestTrackBuilderCapsEvents(t *testing.T) {
	builder := newTrackBuilder(origin())
	for i := 0; i < 200; i++ {
		builder.move(Point{uniform(0.1, 0.9), uniform(0.1, 0.9)}, 100)
	}
	events := builder.build()
	if len(events) > 150 {
		t.Fatalf("build returned %d events", len(events))
	}
	if events[0] != builder.events[0] {
		t.Fatal("first event was dropped")
	}
	if events[len(events)-1] != builder.events[len(builder.events)-1] {
		t.Fatal("last event was dropped")
	}
}

func TestZipTrackRoundTrip(t *testing.T) {
	track, _ := gridTrack([]int{0, 4, 8}, 3)
	encoded, err := zipTrack(track)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Track
	if err := json.NewDecoder(reader).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, track) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", decoded, track)
	}
}

func TestGridTrackUsesCellCenters(t *testing.T) {
	track, _ := gridTrack([]int{0}, 3)
	// The click target is the center of cell 0 with a +/-0.05 jitter.
	last := track.P[len(track.P)-1]
	if last[1] < 0.1 || last[1] > 0.23 || last[2] < 0.1 || last[2] > 0.23 {
		t.Fatalf("unexpected cell center %v", last)
	}
}
