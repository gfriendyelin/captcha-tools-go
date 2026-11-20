package geetest

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestEmbeddedMousePathFixture(t *testing.T) {
	canonical := strings.ReplaceAll(embeddedMousePath, "\r\n", "\n")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	if hash != "334b731526f45b8ae66056ca72f410bd5ffd80ce44ab621a54b8b8cb5601941f" {
		t.Fatalf("canonical mousepath hash = %s", hash)
	}
	path, err := defaultMousePath()
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 334 {
		t.Fatalf("raw event count = %d", len(path))
	}
	filtered, err := filterMouseEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 326 {
		t.Fatalf("pointer-filtered count = %d", len(filtered))
	}
	prepared, err := preprocessMousePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 300 {
		t.Fatalf("prepared count = %d", len(prepared))
	}
	first := prepared[0]
	if first.kind != "move" || first.x != 326 || first.y != 498 || first.timestamp != 0 {
		t.Fatalf("first effective event = %+v", first)
	}
	last := prepared[len(prepared)-1]
	if last.kind != "up" || last.x != 0 || last.y != 0 || last.timestamp != 59 {
		t.Fatalf("last effective event = %+v", last)
	}
}

func TestMouseEncodingReferenceVectors(t *testing.T) {
	c := []int64{1, 2, 3, 4, 5}
	tests := []struct {
		name     string
		path     []mouseEvent
		want     string
		wantPost string
	}{
		{
			name: "empty", path: nil,
			want: "M(*((1((M((", wantPost: "M(*A(B(1((M((",
		},
		{
			name: "simple",
			path: []mouseEvent{
				{kind: "move", x: 10, y: 20, timestamp: 1000, dom: "pointermove", fields: 5},
				{kind: "move", x: 12, y: 22, timestamp: 1016, dom: "pointermove", fields: 5},
				{kind: "down", x: 12, y: 22, timestamp: 1020, dom: "pointerdown", fields: 5},
				{kind: "focus", timestamp: 1021, fields: 2},
				{kind: "up", x: 13, y: 24, timestamp: 1100, dom: "pointerup", fields: 5},
			},
			want:     "M(C(.*M(A99--.8M(9(UM-9(1M*N(9",
			wantPost: "M(C(.*M(A99--.8AM(9(UM-9(1M*NB(9",
		},
		{
			name: "clamp",
			path: []mouseEvent{
				{kind: "move", x: 0, y: 0, timestamp: 1000, dom: "pointermove", fields: 5},
				{kind: "move", x: 1, y: 2, timestamp: 41000, dom: "pointermove", fields: 5},
			},
			want:     "M(3(M(1bLqp((M(A()()(",
			wantPost: "M(B3(M(1bLqp((M(AA()()(",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeMousePath(tt.path, c, "")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("encoding = %q, want %q", got, tt.want)
			}
			got, err = encodeMousePath(tt.path, c, "4142")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantPost {
				t.Fatalf("postprocessed encoding = %q, want %q", got, tt.wantPost)
			}
		})
	}
}

func TestMouseInsertionModulusDifference(t *testing.T) {
	c := []int64{0, 0, 0, 0, 4}
	fixed, err := postprocessMouse("AAAA", c, "4142")
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := transformAjaxMouse("AAAA", c, "4142")
	if err != nil {
		t.Fatal(err)
	}
	if fixed != "BAAAAA" || dynamic != "AAAABA" {
		t.Fatalf("fixed = %q, dynamic = %q", fixed, dynamic)
	}
	unchanged, err := transformAjaxMouse("AAAA", nil, "4142")
	if err != nil || unchanged != "AAAA" {
		t.Fatalf("empty-c transform = %q, %v", unchanged, err)
	}
}
