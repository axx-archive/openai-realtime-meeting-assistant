// Command e10-worker-harness executes the local token-free worker-boundary
// contract. It does not start a worker, dial a network, read credentials, or
// integrate with the production queue.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10worker"
	"github.com/openai/openai-realtime-meeting-assistant/internal/e9readiness"
)

func main() {
	policyPath := flag.String("policy", "deploy/e9/worker-isolation-policy.json", "closed E9 worker-isolation policy")
	flag.Parse()

	file, err := os.Open(*policyPath)
	if err != nil {
		fatal("open policy", err)
	}
	policy, err := e9readiness.DecodeStrict[e9readiness.WorkerIsolationPolicy](file)
	closeErr := file.Close()
	if err != nil {
		fatal("decode policy", err)
	}
	if closeErr != nil {
		fatal("close policy", closeErr)
	}
	receipt, err := e10worker.ExecuteHarness(policy)
	if err != nil {
		fatal("execute harness", err)
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		fatal("encode receipt", err)
	}
	fmt.Println(string(encoded))
}

func fatal(scope string, err error) {
	fmt.Fprintf(os.Stderr, "e10 worker harness %s: %v\n", scope, err)
	os.Exit(1)
}
