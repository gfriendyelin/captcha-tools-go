package geetest

import (
	"encoding/json"
	"math"
	"testing"
)

func TestOrderedPythonJSON(t *testing.T) {
	input := "{\"z\":\"便笺\",\"a\":\"<>&\",\"n\":1e3,\"big\":-0,\"dup\":1,\"dup\":2}"
	v, err := parseOrderedJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := marshalPythonJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"z\":\"\\u4fbf\\u7b3a\",\"a\":\"<>&\",\"n\":1000.0,\"big\":0,\"dup\":2}"
	if string(got) != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func TestPythonJSONUnicodeAndEscapes(t *testing.T) {
	got, err := marshalPythonJSON(obj("x", "😀", "y", "\b\f\n\r\t\\\""))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"x\":\"\\ud83d\\ude00\",\"y\":\"\\b\\f\\n\\r\\t\\\\\\\"\"}"
	if string(got) != want {
		t.Fatalf("JSON = %q, want %q", got, want)
	}
}

func TestPythonFloatFormatting(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{1, "1.0"},
		{1000000, "1000000.0"},
		{1e16, "1e+16"},
		{1e-5, "1e-05"},
		{0.0001, "0.0001"},
		{math.Copysign(0, -1), "-0.0"},
	}
	for _, tt := range tests {
		if got := formatPythonFloat(tt.value); got != tt.want {
			t.Errorf("formatPythonFloat(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := marshalPythonJSON(value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJSONPSlicing(t *testing.T) {
	const callback = "geetest_1724572150164"
	if len(callback) != 21 {
		t.Fatal("test callback length changed")
	}
	text := callback + "({\"status\":\"success\",\"data\":{\"c\":[1,2,3,4,5],\"s\":\"4142\"}})"
	data, err := parseDataObject(text, 22)
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := data.get("s"); value != "4142" {
		t.Fatalf("s = %#v", value)
	}
	inner, err := stripJSONP("({\"data\":{}})", 1)
	if err != nil || inner != "{\"data\":{}}" {
		t.Fatalf("stripJSONP = %q, %v", inner, err)
	}
	if _, err := stripJSONP("short", 22); err == nil {
		t.Fatal("expected short JSONP error")
	}
}

func TestOrderedJSONRejectsTrailingData(t *testing.T) {
	if _, err := parseOrderedJSON("{} {}"); err == nil {
		t.Fatal("expected trailing JSON error")
	}
	if _, err := marshalPythonJSON(json.Number("not-a-number")); err == nil {
		t.Fatal("expected invalid number error")
	}
}
