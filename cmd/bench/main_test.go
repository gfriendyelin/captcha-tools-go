package main

import (
	"encoding/json"
	"testing"
)

func TestPassedAcceptsV3AndV4(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"status":"success","data":{"result":"success","seccode":{}}}`, true},
		{`{"status":"success","data":{"result":"fail"}}`, false},
		{`{"status":"success","data":{"validate":"abc"}}`, true},
		{`{"status":"error","data":{}}`, false},
		{`{"detail":"boom"}`, false},
	}
	for _, tc := range cases {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(tc.body), &decoded); err != nil {
			t.Fatal(err)
		}
		if got := passed(decoded); got != tc.want {
			t.Errorf("passed(%s) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestSummarizeReportsSuccessRateAndLatency(t *testing.T) {
	outcomes := []outcome{
		{Index: 1, Passed: true, Result: "success", Latency: 10},
		{Index: 2, Passed: true, Result: "success", Latency: 20},
		{Index: 3, Passed: false, Result: "success/fail", Latency: 30},
		{Index: 4, Passed: false, Error: "timeout", Latency: 40},
	}
	report := summarize("http://example.test/pass_uni", outcomes)
	if report.Total != 4 || report.Success != 2 || report.Failed != 2 {
		t.Fatalf("counts = %+v", report)
	}
	if report.SuccessRate != 0.5 {
		t.Fatalf("success rate = %v, want 0.5", report.SuccessRate)
	}
	if report.Results["success"] != 2 || report.Results["success/fail"] != 1 || report.Results["timeout"] != 1 {
		t.Fatalf("result breakdown = %v", report.Results)
	}
	if report.Latency.Min != 10 || report.Latency.Max != 40 || report.Latency.P50 != 20 {
		t.Fatalf("latency = %+v", report.Latency)
	}
	if len(report.Failures) != 2 {
		t.Fatalf("failures = %d, want 2", len(report.Failures))
	}
}

func TestPercentileNearestRank(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(values, 50); got != 5 {
		t.Fatalf("p50 = %v, want 5", got)
	}
	if got := percentile(values, 95); got != 10 {
		t.Fatalf("p95 = %v, want 10", got)
	}
	if got := percentile(nil, 99); got != 0 {
		t.Fatalf("empty p99 = %v, want 0", got)
	}
}
