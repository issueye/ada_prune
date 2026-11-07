// Command mockupstream is a fake Command Code upstream for local testing.
//
// It speaks the same NDJSON wire format the real upstream uses: text deltas, a
// finish event carrying usage, and a 402 for a credential listed in -reject.
//
//	go run ./tools/mockupstream -addr 127.0.0.1:4099
//	CC_API_BASE=http://127.0.0.1:4099 go run ./cmd/prune-desktop
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4099", "listen address")
	answer := flag.String("answer", "hello from the mock upstream", "text the model streams back")
	reject := flag.String("reject", "", "comma-separated bearer secrets to answer with 402")
	flag.Parse()

	rejected := map[string]bool{}
	for _, secret := range strings.Split(*reject, ",") {
		if trimmed := strings.TrimSpace(secret); trimmed != "" {
			rejected[trimmed] = true
		}
	}

	var mu sync.Mutex
	calls := 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/alpha/generate"):
			mu.Lock()
			calls++
			mu.Unlock()

			secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if rejected[secret] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"error":{"message":"no credit","code":"quota_exceeded"}}`)
				return
			}

			raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			var envelope struct {
				Params struct {
					Model string `json:"model"`
				} `json:"params"`
			}
			model := "mock-model"
			if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Params.Model != "" {
				model = envelope.Params.Model
			}
			_ = model // echoed in the log line below; the answer text is fixed

			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)
			write := func(payload any) {
				encoded, _ := json.Marshal(payload)
				_, _ = fmt.Fprintf(w, "%s\n", encoded)
				if flusher != nil {
					flusher.Flush()
				}
			}

			// Stream the answer in two deltas so the gateway's SSE assembly is
			// exercised, not just its buffering.
			half := len(*answer) / 2
			write(map[string]any{"type": "text-delta", "text": (*answer)[:half]})
			write(map[string]any{"type": "text-delta", "text": (*answer)[half:]})
			write(map[string]any{
				"type":         "finish",
				"finishReason": "stop",
				"totalUsage": map[string]any{
					"inputTokens":  12,
					"outputTokens": 7,
					"totalTokens":  19,
				},
			})

		case strings.HasSuffix(r.URL.Path, "/provider/v1/models"):
			_, _ = io.WriteString(w, `{"data":[{"id":"mock/mock-model"},{"id":"mock/other-model"}]}`)

		case strings.HasSuffix(r.URL.Path, "/alpha/whoami"):
			_, _ = io.WriteString(w, `{"userName":"mock","email":"mock@example.com","orgId":"org_mock"}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/billing/credits"):
			_, _ = io.WriteString(w, `{"monthly":5,"purchased":0,"free":0,"totalRemaining":5}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/billing/subscriptions"):
			_, _ = io.WriteString(w, `{"subscriptions":[{"id":"individual-go","status":"active"}]}`)
		case strings.HasSuffix(r.URL.Path, "/alpha/usage/summary"):
			_, _ = io.WriteString(w, `{"totalCount":3,"totalCost":0.2}`)

		case strings.HasSuffix(r.URL.Path, "/alpha/fingerprint/record"),
			strings.HasSuffix(r.URL.Path, "/alpha/lifecycle-events"):
			_, _ = io.WriteString(w, `{"ok":true}`)

		default:
			http.NotFound(w, r)
		}
	})

	server := &http.Server{Addr: *addr, Handler: handler}
	fmt.Fprintf(os.Stderr, "[mockupstream] listening on http://%s (rejecting %d secrets)\n", *addr, len(rejected))
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "[mockupstream] %v\n", err)
		os.Exit(1)
	}
}
