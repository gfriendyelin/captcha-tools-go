// Command bench measures the pass rate and latency of a running captcha
// service. It only talks HTTP, so it works against either the local worker or
// the public gateway, and against V3 (/pass_uni?gt=&challenge=) or V4
// (/pass_uni?captcha_id=) deployments.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

type outcome struct {
	Index   int     `json:"index"`
	Status  int     `json:"status"`
	Passed  bool    `json:"passed"`
	Result  string  `json:"result,omitempty"`
	Error   string  `json:"error,omitempty"`
	Latency float64 `json:"latency_ms"`
}

type latency struct {
	Min  float64 `json:"min_ms"`
	Mean float64 `json:"mean_ms"`
	P50  float64 `json:"p50_ms"`
	P90  float64 `json:"p90_ms"`
	P95  float64 `json:"p95_ms"`
	P99  float64 `json:"p99_ms"`
	Max  float64 `json:"max_ms"`
}

type report struct {
	URL         string         `json:"url"`
	Total       int            `json:"total"`
	Success     int            `json:"success"`
	Failed      int            `json:"failed"`
	SuccessRate float64        `json:"success_rate"`
	Latency     latency        `json:"latency"`
	Results     map[string]int `json:"results"`
	Failures    []outcome      `json:"failures,omitempty"`
}

func main() {
	base := flag.String("url", "http://127.0.0.1:9645/pass_uni", "service endpoint")
	captchaID := flag.String("captcha-id", "", "V4 captcha_id (or use -gt/-challenge for V3)")
	gt := flag.String("gt", "", "V3 gt")
	challenge := flag.String("challenge", "", "V3 challenge")
	total := flag.Int("n", 20, "number of requests")
	concurrency := flag.Int("c", 1, "parallel requests")
	timeout := flag.Duration("timeout", 120*time.Second, "per-request timeout")
	flag.Parse()

	if (*captchaID == "") == (*gt == "" || *challenge == "") {
		fmt.Fprintln(os.Stderr, "provide either -captcha-id, or both -gt and -challenge")
		os.Exit(2)
	}
	if *total < 1 || *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "n and c must be positive")
		os.Exit(2)
	}

	target, err := url.Parse(*base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	query := target.Query()
	if *captchaID != "" {
		query.Set("captcha_id", *captchaID)
	} else {
		query.Set("gt", *gt)
		query.Set("challenge", *challenge)
	}
	target.RawQuery = query.Encode()

	client := &http.Client{Timeout: *timeout}
	outcomes := run(client, target.String(), *total, *concurrency)
	result := summarize(target.String(), outcomes)
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

func run(client *http.Client, target string, total, concurrency int) []outcome {
	jobs := make(chan int)
	outcomes := make([]outcome, 0, total)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				item := request(client, target, index)
				mu.Lock()
				outcomes = append(outcomes, item)
				mu.Unlock()
			}
		}()
	}
	for index := 1; index <= total; index++ {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].Index < outcomes[j].Index })
	return outcomes
}

func request(client *http.Client, target string, index int) outcome {
	item := outcome{Index: index}
	started := time.Now()
	resp, err := client.Get(target)
	item.Latency = milliseconds(time.Since(started))
	if err != nil {
		item.Error = err.Error()
		return item
	}
	defer resp.Body.Close()
	item.Status = resp.StatusCode
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		item.Error = "decode response: " + err.Error()
		return item
	}
	item.Passed = passed(decoded)
	item.Result = outcomeLabel(decoded)
	return item
}

// passed accepts both upstream shapes: V4 reports data.result == "success",
// while V3 exposes a validate token.
func passed(decoded map[string]any) bool {
	data, _ := decoded["data"].(map[string]any)
	if data == nil {
		return false
	}
	if result, _ := data["result"].(string); result == "success" {
		return true
	}
	_, ok := data["validate"]
	return ok
}

func outcomeLabel(decoded map[string]any) string {
	if status, _ := decoded["status"].(string); status != "" {
		if data, ok := decoded["data"].(map[string]any); ok {
			if result, _ := data["result"].(string); result != "" {
				return status + "/" + result
			}
		}
		return status
	}
	if detail, ok := decoded["detail"].(string); ok {
		return detail
	}
	if message, ok := decoded["error"].(string); ok {
		return message
	}
	return "unknown"
}

func summarize(target string, outcomes []outcome) report {
	result := report{URL: target, Total: len(outcomes), Results: map[string]int{}}
	latencies := make([]float64, 0, len(outcomes))
	for _, item := range outcomes {
		latencies = append(latencies, item.Latency)
		label := item.Result
		if label == "" && item.Error != "" {
			label = item.Error
		}
		if label == "" {
			label = fmt.Sprintf("HTTP %d", item.Status)
		}
		result.Results[label]++
		if item.Passed {
			result.Success++
			continue
		}
		result.Failed++
		result.Failures = append(result.Failures, item)
	}
	if result.Total > 0 {
		result.SuccessRate = float64(result.Success) / float64(result.Total)
	}
	result.Latency = summarizeLatency(latencies)
	return result
}

func summarizeLatency(values []float64) latency {
	if len(values) == 0 {
		return latency{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	return latency{
		Min:  sorted[0],
		Mean: round2(sum / float64(len(sorted))),
		P50:  percentile(sorted, 50),
		P90:  percentile(sorted, 90),
		P95:  percentile(sorted, 95),
		P99:  percentile(sorted, 99),
		Max:  sorted[len(sorted)-1],
	}
}

// percentile uses the nearest-rank method, matching how latency dashboards
// report small samples.
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return round2(sorted[rank-1])
}

func milliseconds(duration time.Duration) float64 {
	return round2(float64(duration.Microseconds()) / 1000)
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}
