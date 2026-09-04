package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This identity is server-only, never accepted from a request body or emitted
// to the provider. It attributes spending, not the value of a contribution.
type workUsageIdentityContextKey struct{}
type workUsageIdentity struct{ ThreadID, GoalID string }

func withWorkUsageIdentity(ctx context.Context, threadID, goalID string) context.Context {
	return context.WithValue(ctx, workUsageIdentityContextKey{}, workUsageIdentity{
		ThreadID: strings.TrimSpace(threadID), GoalID: strings.TrimSpace(goalID),
	})
}

const studioWorkUsageDays = 31
const studioWorkUsageMaxBytes int64 = 8 << 20

type studioWorkUsageTotals struct {
	Calls               int     `json:"calls"`
	InputTokens         int64   `json:"inputTokens"`
	CachedInputTokens   int64   `json:"cachedInputTokens"`
	CacheCreationTokens int64   `json:"cacheCreationTokens"`
	OutputTokens        int64   `json:"outputTokens"`
	EstimatedCostUSD    float64 `json:"estimatedCostUsd"`
	UnpricedCalls       int     `json:"unpricedCalls"`
	EstimatedUsageCalls int     `json:"estimatedUsageCalls"`
}

type studioWorkUsageModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	studioWorkUsageTotals
}

type studioWorkUsageView struct {
	// No completeness claim is possible for historical untagged calls or
	// unavailable child artifacts. Zero observed calls never means free work.
	Coverage    string `json:"coverage"`
	From        string `json:"from"`
	To          string `json:"to"`
	ScanLimited bool   `json:"scanLimited"`
	ReadErrors  bool   `json:"readErrors"`
	studioWorkUsageTotals
	Models []studioWorkUsageModel `json:"models"`
}

func (total *studioWorkUsageTotals) add(entry llmUsageEntry) {
	total.Calls++
	total.InputTokens += max(int64(0), entry.InputTokens)
	total.CachedInputTokens += max(int64(0), entry.CachedInputTokens)
	total.CacheCreationTokens += max(int64(0), entry.CacheCreationTokens)
	total.OutputTokens += max(int64(0), entry.OutputTokens)
	if entry.Estimated {
		total.EstimatedUsageCalls++
	}
	// Duration-only rows cannot prove a zero cost, even with a known model.
	if entry.PriceMissing || entry.Estimated || math.IsNaN(entry.EstCostUSD) || math.IsInf(entry.EstCostUSD, 0) || entry.EstCostUSD < 0 {
		total.UnpricedCalls++
	} else {
		total.EstimatedCostUSD += entry.EstCostUSD
	}
}

// Read only bounded daily files, once per authorized detail request. Lists and
// chat receipts never scan the ledger. Entries without an exact execution
// identity, or tagged to a different goal, cannot join by room/conversation.
func readStudioWorkUsage(ctx context.Context, dir string, now time.Time, goals, threads map[string]bool, maxBytes int64) *studioWorkUsageView {
	now = now.UTC()
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(studioWorkUsageDays - 1))
	view := &studioWorkUsageView{Coverage: "unavailable", From: first.Format(time.RFC3339), To: now.Format(time.RFC3339), Models: []studioWorkUsageModel{}}
	models := map[string]*studioWorkUsageModel{}
	remaining := maxBytes
	for day := 0; day < studioWorkUsageDays; day++ {
		if ctx.Err() != nil || remaining <= 0 {
			view.ScanLimited = true
			break
		}
		path := filepath.Join(dir, "usage-"+now.AddDate(0, 0, -day).Format("2006-01-02")+".jsonl")
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			view.ReadErrors = true
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			view.ReadErrors = true
			file.Close()
			continue
		}
		limit := min(remaining, info.Size())
		if info.Size() > remaining {
			view.ScanLimited = true
		}
		reader := &io.LimitedReader{R: file, N: limit}
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			if ctx.Err() != nil {
				view.ScanLimited = true
				break
			}
			var entry llmUsageEntry
			if json.Unmarshal(scanner.Bytes(), &entry) != nil {
				view.ReadErrors = true
				continue
			}
			if entry.TS.Before(first) || entry.TS.After(now) || strings.TrimSpace(entry.Provider) == "" || strings.TrimSpace(entry.Model) == "" {
				continue
			}
			goal, thread := strings.TrimSpace(entry.GoalID), strings.TrimSpace(entry.ThreadID)
			if goal != "" && !goals[goal] || thread != "" && !threads[thread] || goal == "" && thread == "" {
				continue
			}
			view.add(entry)
			key := entry.Provider + "\x00" + entry.Model
			model := models[key]
			if model == nil {
				model = &studioWorkUsageModel{Provider: entry.Provider, Model: entry.Model}
				models[key] = model
			}
			model.add(entry)
		}
		if scanner.Err() != nil {
			view.ReadErrors = true
		}
		remaining -= limit - reader.N
		file.Close()
	}
	for _, model := range models {
		view.Models = append(view.Models, *model)
	}
	sort.Slice(view.Models, func(i, j int) bool {
		if view.Models[i].Provider == view.Models[j].Provider {
			return view.Models[i].Model < view.Models[j].Model
		}
		return view.Models[i].Provider < view.Models[j].Provider
	})
	if view.Calls > 0 {
		view.Coverage = "partial"
	}
	return view
}

func studioWorkUsageForViewer(ctx context.Context, viewer *userAccount, root artifactListAuthorizationCandidate, index scoutChatResultProjectionIndex) *studioWorkUsageView {
	goals := map[string]bool{root.Entry.ID: true}
	threads := map[string]bool{}
	rootThread := strings.TrimSpace(root.Entry.Metadata["threadId"])
	if rootThread != "" {
		threads[rootThread], goals[rootThread] = true, true
	}
	bindings := []artifactListAuthorizationCandidate{root}
	_, plan, _, _ := studioProjectClassification(root.Entry)
	// Resolve only explicit plan children, with a hard bound. A readable goal
	// does not grant access to every artifact carrying a goalParentId string.
	for position, step := range plan.Subtasks {
		if position >= 128 {
			break
		}
		child, ok := index.byID[step.ArtifactID]
		if !ok || child.Metadata["goalParentId"] != root.Entry.ID {
			continue
		}
		candidate := artifactListAuthorizationCandidate{Entry: child, Header: artifactAuthorizationHeaderFromEntry(child)}
		if !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, candidate.Header) || !studioProjectAuthorizationCandidateCurrent(ctx, kanbanApp, candidate) {
			continue
		}
		if thread := strings.TrimSpace(child.Metadata["threadId"]); thread != "" {
			threads[thread] = true
			bindings = append(bindings, candidate)
		}
	}
	view := readStudioWorkUsage(ctx, usageLedgerDir(), usageLedgerNow(), goals, threads, studioWorkUsageMaxBytes)
	// Slow I/O cannot retain authority after a correction or revocation. Drop
	// the entire projection if any identity used in its aggregate has drifted.
	for _, binding := range bindings {
		if !artifactHeaderAuthorized(ctx, viewer, ACLReadContent, binding.Header) || !studioProjectAuthorizationCandidateCurrent(ctx, kanbanApp, binding) {
			return nil
		}
	}
	return view
}
