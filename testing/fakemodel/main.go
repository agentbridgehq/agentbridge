// Command fakemodel is a stand-in classifier endpoint.
//
// It speaks enough of the Anthropic Messages API for `agentbridge scan
// --classify` to work, so the model pass can be exercised without an API key,
// an account, or a network. It is not a model: it answers from a fixed keyword
// list, which is enough to prove the plumbing, the quote verification, the
// severity cap and the additive-only rule.
//
//	go run ./testing/fakemodel
//
// For a real check, point --classifier-endpoint at an actual API instead, or at
// a local model server that speaks the same shape.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
)

// phrases are the things this pretend model "notices". Each is a rephrasing
// that the regex rules deliberately do not match, which is the whole point of
// the model pass.
var phrases = []struct {
	needle   string
	category string
	why      string
}{
	{
		needle:   "consult the operator's cloud configuration",
		category: "credential_access",
		why:      "directs the agent to read cloud credentials before answering",
	},
	{
		needle:   "forward a transcript",
		category: "exfiltration",
		why:      "directs the agent to send conversation content to a third party",
	},
	{
		needle:   "without drawing attention",
		category: "conceal_from_user",
		why:      "directs the agent to act without the user noticing",
	},
}

func main() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := listener.Addr().String()

	fmt.Printf("fake classifier on http://%s\n\n", addr)
	fmt.Printf("  export AGENTBRIDGE_CLASSIFIER_ENDPOINT=http://%s/v1/messages\n", addr)
	fmt.Printf("  agentbridge scan ./plugin --classify --classifier-model fake\n\n")
	fmt.Printf("it reports on these phrases, none of which any regex rule matches:\n")
	for _, p := range phrases {
		fmt.Printf("  %-40s → %s\n", `"`+p.needle+`…"`, p.category)
	}
	fmt.Printf("\nctrl-c to stop.\n\n")

	log.Fatal(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[0].Content
		}

		var findings []map[string]string
		for _, p := range phrases {
			if i := strings.Index(strings.ToLower(prompt), p.needle); i >= 0 {
				// The quote must be text that really appears in the file, or
				// agentbridge drops the finding as fabricated. Taking it from
				// the prompt is how a real model would do it correctly.
				findings = append(findings, map[string]string{
					"category":   p.category,
					"quote":      quoteAround(prompt, i),
					"confidence": "high",
					"why":        p.why,
				})
			}
		}
		log.Printf("asked about %d bytes, reporting %d finding(s)", len(prompt), len(findings))

		if findings == nil {
			findings = []map[string]string{}
		}
		body, err := json.Marshal(map[string]any{"findings": findings})
		if err != nil {
			http.Error(w, "encode", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": string(body)}},
		})
	})))
}

// quoteAround lifts the sentence containing a match, so the quote is real text
// from the file rather than something invented.
func quoteAround(prompt string, i int) string {
	start := strings.LastIndexAny(prompt[:i], ".\n")
	if start < 0 {
		start = 0
	} else {
		start++
	}
	end := strings.IndexAny(prompt[i:], ".\n")
	if end < 0 {
		end = len(prompt)
	} else {
		end += i
	}
	return strings.TrimSpace(prompt[start:end])
}
