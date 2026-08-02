package main

// E8 routing/economics is a static control-plane contract. It deliberately
// contains no provider client, credentials, environment lookup, or activation
// path. E10 must separately re-check current provider documentation and run
// the referenced canaries; this file only makes omissions observable first.

import (
	"errors"
	"math"
	"sort"
	"time"
)

var (
	ErrE8RoutingInvalid       = errors.New("invalid E8 routing/economics manifest")
	ErrE8UnknownSeat          = errors.New("unknown E8 route seat")
	ErrE8UnknownModel         = errors.New("unknown E8 route model")
	ErrE8PriceMissing         = errors.New("E8 route model has no pinned price")
	ErrE8HiddenFallback       = errors.New("E8 route receipt contains a hidden fallback")
	ErrE8SafetyRegression     = errors.New("E8 canary has an unsafe safety regression")
	ErrE8TooManyChanges       = errors.New("E8 canary changes more than one variable")
	ErrE8CanaryDefaultOff     = errors.New("E8 canary is default-off pending E10")
	ErrE8SyntheticReceipt     = errors.New("synthetic E8 receipt cannot qualify a live route")
	ErrE8ReceiptStale         = errors.New("stale E8 accounting receipt")
	ErrE8BudgetExceeded       = errors.New("E8 route budget or circuit breaker exceeded")
	ErrE8ReconciliationFailed = errors.New("E8 provider/internal reconciliation failed")
)

const e8MaxReceiptAge = 24 * time.Hour

type E8PriceRevision struct {
	ID     string            `json:"id"`
	AsOf   time.Time         `json:"asOf"`
	Models []E8ModelPricePin `json:"models"`
	Digest string            `json:"digest"`
}

// E8ModelPricePin is a digest of the locally reviewed, dated price row. It
// avoids copying prices into a second authority while binding a route to the
// exact price-table row consumed by usage_ledger.go.
type E8ModelPricePin struct {
	Model      string `json:"model"`
	SourceDate string `json:"sourceDate"`
	RowDigest  string `json:"rowDigest"`
}

func NewE8PriceRevision(id string, asOf time.Time, models []string) (E8PriceRevision, error) {
	if !strideIdentifier(id) || asOf.IsZero() || len(models) == 0 {
		return E8PriceRevision{}, ErrE8RoutingInvalid
	}
	seen := map[string]bool{}
	pins := make([]E8ModelPricePin, 0, len(models))
	for _, model := range models {
		if !strideIdentifier(model) || seen[model] {
			return E8PriceRevision{}, ErrE8RoutingInvalid
		}
		seen[model] = true
		row, ok := priceForModel(model, asOf)
		if !ok || row.SourceDate == "" {
			return E8PriceRevision{}, ErrE8PriceMissing
		}
		rowDigest, err := STRIDEContractDigest(struct {
			Model string     `json:"model"`
			Row   modelPrice `json:"row"`
		}{model, row})
		if err != nil {
			return E8PriceRevision{}, ErrE8RoutingInvalid
		}
		pins = append(pins, E8ModelPricePin{Model: model, SourceDate: row.SourceDate, RowDigest: rowDigest})
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].Model < pins[j].Model })
	digest, err := STRIDEContractDigest(struct {
		ID     string            `json:"id"`
		AsOf   time.Time         `json:"asOf"`
		Models []E8ModelPricePin `json:"models"`
	}{id, asOf.UTC(), pins})
	if err != nil {
		return E8PriceRevision{}, ErrE8RoutingInvalid
	}
	return E8PriceRevision{ID: id, AsOf: asOf.UTC(), Models: pins, Digest: digest}, nil
}

func (revision E8PriceRevision) Validate() error {
	if !strideIdentifier(revision.ID) || revision.AsOf.IsZero() || len(revision.Models) == 0 || !isHexDigest(revision.Digest) {
		return ErrE8RoutingInvalid
	}
	models := make([]string, 0, len(revision.Models))
	previous := ""
	for _, pin := range revision.Models {
		if !strideIdentifier(pin.Model) || pin.Model <= previous || pin.SourceDate == "" || !isHexDigest(pin.RowDigest) {
			return ErrE8RoutingInvalid
		}
		previous = pin.Model
		models = append(models, pin.Model)
	}
	want, err := NewE8PriceRevision(revision.ID, revision.AsOf, models)
	if err != nil {
		return err
	}
	if want.Digest != revision.Digest || len(want.Models) != len(revision.Models) {
		return ErrE8RoutingInvalid
	}
	for index := range want.Models {
		if want.Models[index] != revision.Models[index] {
			return ErrE8RoutingInvalid
		}
	}
	return nil
}

type E8RouteDescriptor struct {
	Seat                string `json:"seat"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	Effort              string `json:"effort"`
	PromptDigest        string `json:"promptDigest"`
	SchemaDigest        string `json:"schemaDigest"`
	SafetyDigest        string `json:"safetyDigest"`
	RouteRevision       string `json:"routeRevision"`
	PriceRevisionDigest string `json:"priceRevisionDigest"`
	StrictOutputSchema  bool   `json:"strictOutputSchema"`
	ReadOnlyTools       bool   `json:"readOnlyTools"`
	PromptCache         bool   `json:"promptCache"`
	PersistedReasoning  bool   `json:"persistedReasoning"`
	ProgrammaticTools   bool   `json:"programmaticTools"`
	BoundedMultiAgent   bool   `json:"boundedMultiAgent"`
	MultiAgentLimit     int    `json:"multiAgentLimit"`
	Digest              string `json:"digest"`
}

func NewE8RouteDescriptor(route E8RouteDescriptor) (E8RouteDescriptor, error) {
	route.Digest = ""
	if !e8KnownRouteSeat(route.Seat) {
		return E8RouteDescriptor{}, ErrE8UnknownSeat
	}
	if !e8KnownRouteModel(route.Model) {
		return E8RouteDescriptor{}, ErrE8UnknownModel
	}
	if validateE8RouteShape(route) != nil {
		return E8RouteDescriptor{}, ErrE8RoutingInvalid
	}
	digest, err := STRIDEContractDigest(route)
	if err != nil {
		return E8RouteDescriptor{}, ErrE8RoutingInvalid
	}
	route.Digest = digest
	return route, nil
}

// allLLMSeats is the existing usage-ledger authority. Reusing it prevents an
// economics manifest from introducing an unmetered seat vocabulary.
func e8KnownRouteSeat(seat string) bool {
	for _, known := range allLLMSeats {
		if seat == known {
			return true
		}
	}
	return false
}

// Known model identity is derived from the existing price authority plus the
// planned, intentionally unpriced transcription aliases. A planned alias is
// still rejected at price-revision construction until a dated row is added.
func e8KnownRouteModel(model string) bool {
	if _, found := modelPriceTable[model]; found {
		return true
	}
	return model == "gpt-transcribe" || model == "gpt-live-transcribe"
}

func (route E8RouteDescriptor) Validate() error {
	got := route.Digest
	want, err := NewE8RouteDescriptor(route)
	if err != nil || got != want.Digest {
		return ErrE8RoutingInvalid
	}
	return nil
}

func validateE8RouteShape(route E8RouteDescriptor) error {
	if !strideIdentifier(route.Seat) || !oneOf(route.Provider, providerOpenAI, providerAnthropic, "codex") || !strideIdentifier(route.Model) ||
		!oneOf(route.Effort, "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra") ||
		!isHexDigest(route.PromptDigest) || !isHexDigest(route.SchemaDigest) || !isHexDigest(route.SafetyDigest) || !strideIdentifier(route.RouteRevision) || !isHexDigest(route.PriceRevisionDigest) {
		return ErrE8RoutingInvalid
	}
	if route.ProgrammaticTools && (!route.ReadOnlyTools || !route.StrictOutputSchema) ||
		(route.BoundedMultiAgent && (route.MultiAgentLimit < 1 || route.MultiAgentLimit > 8 || !route.ReadOnlyTools)) ||
		(!route.BoundedMultiAgent && route.MultiAgentLimit != 0) {
		return ErrE8RoutingInvalid
	}
	return nil
}

type E8ExperimentKind string

const (
	E8ExperimentModel              E8ExperimentKind = "model"
	E8ExperimentReasoning          E8ExperimentKind = "reasoning"
	E8ExperimentPrompt             E8ExperimentKind = "prompt"
	E8ExperimentSchema             E8ExperimentKind = "schema"
	E8ExperimentPromptCache        E8ExperimentKind = "prompt_cache"
	E8ExperimentPersistedReasoning E8ExperimentKind = "persisted_reasoning"
	E8ExperimentProgrammaticTools  E8ExperimentKind = "programmatic_tools"
	E8ExperimentBoundedMultiAgent  E8ExperimentKind = "bounded_multi_agent"
)

type E8CanaryManifest struct {
	ID                  string            `json:"id"`
	Seat                string            `json:"seat"`
	Kind                E8ExperimentKind  `json:"kind"`
	BaselineRouteDigest string            `json:"baselineRouteDigest"`
	Candidate           E8RouteDescriptor `json:"candidate"`
	CorpusDigest        string            `json:"corpusDigest"`
	RollbackRouteDigest string            `json:"rollbackRouteDigest"`
	DefaultOff          bool              `json:"defaultOff"`
	E10Only             bool              `json:"e10Only"`
	Availability        string            `json:"availability"`
	SafetyRegression    bool              `json:"safetyRegression"`
	Digest              string            `json:"digest"`
}

func NewE8CanaryManifest(canary E8CanaryManifest) (E8CanaryManifest, error) {
	canary.Digest = ""
	if !strideIdentifier(canary.ID) || !strideIdentifier(canary.Seat) || !validE8ExperimentKind(canary.Kind) ||
		!isHexDigest(canary.BaselineRouteDigest) || canary.Candidate.Validate() != nil || !isHexDigest(canary.CorpusDigest) ||
		!isHexDigest(canary.RollbackRouteDigest) || !canary.DefaultOff || !canary.E10Only || canary.Availability != "unverified" || canary.SafetyRegression {
		return E8CanaryManifest{}, ErrE8RoutingInvalid
	}
	digest, err := STRIDEContractDigest(canary)
	if err != nil {
		return E8CanaryManifest{}, ErrE8RoutingInvalid
	}
	canary.Digest = digest
	return canary, nil
}

func validE8ExperimentKind(kind E8ExperimentKind) bool {
	return kind == E8ExperimentModel || kind == E8ExperimentReasoning || kind == E8ExperimentPrompt || kind == E8ExperimentSchema ||
		kind == E8ExperimentPromptCache || kind == E8ExperimentPersistedReasoning || kind == E8ExperimentProgrammaticTools || kind == E8ExperimentBoundedMultiAgent
}

type E8RouteBudget struct {
	Seat                  string `json:"seat"`
	MaxCallCostMicros     int64  `json:"maxCallCostMicros"`
	MaxWorkflowCostMicros int64  `json:"maxWorkflowCostMicros"`
	MaxCalls              int    `json:"maxCalls"`
	RollbackRouteDigest   string `json:"rollbackRouteDigest"`
}

// E8BlockedCanaryManifest is intentionally not a runnable route. It preserves
// an E10 experiment's identity and rollback requirement when a prerequisite
// such as a dated price row is absent; it cannot accidentally look eligible.
type E8BlockedCanaryManifest struct {
	ID             string `json:"id"`
	Seat           string `json:"seat"`
	CandidateModel string `json:"candidateModel"`
	Blocker        string `json:"blocker"`
	DefaultOff     bool   `json:"defaultOff"`
	E10Only        bool   `json:"e10Only"`
	Availability   string `json:"availability"`
	Digest         string `json:"digest"`
}

func NewE8BlockedCanaryManifest(blocked E8BlockedCanaryManifest) (E8BlockedCanaryManifest, error) {
	blocked.Digest = ""
	if !strideIdentifier(blocked.ID) || !strideIdentifier(blocked.Seat) || !strideIdentifier(blocked.CandidateModel) || blocked.Blocker != "missing_price" || !blocked.DefaultOff || !blocked.E10Only || blocked.Availability != "unverified" {
		return E8BlockedCanaryManifest{}, ErrE8RoutingInvalid
	}
	digest, err := STRIDEContractDigest(blocked)
	if err != nil {
		return E8BlockedCanaryManifest{}, ErrE8RoutingInvalid
	}
	blocked.Digest = digest
	return blocked, nil
}

type E8RoutingEconomicsManifest struct {
	Version         int                       `json:"version"`
	PriceRevision   E8PriceRevision           `json:"priceRevision"`
	Incumbents      []E8RouteDescriptor       `json:"incumbents"`
	Canaries        []E8CanaryManifest        `json:"canaries"`
	BlockedCanaries []E8BlockedCanaryManifest `json:"blockedCanaries,omitempty"`
	Budgets         []E8RouteBudget           `json:"budgets"`
	Digest          string                    `json:"digest"`
}

func NewE8RoutingEconomicsManifest(manifest E8RoutingEconomicsManifest) (E8RoutingEconomicsManifest, error) {
	manifest.Digest = ""
	if manifest.Version < 1 || manifest.PriceRevision.Validate() != nil || len(manifest.Incumbents) == 0 {
		return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
	}
	sort.Slice(manifest.Incumbents, func(i, j int) bool { return manifest.Incumbents[i].Seat < manifest.Incumbents[j].Seat })
	sort.Slice(manifest.Canaries, func(i, j int) bool { return manifest.Canaries[i].ID < manifest.Canaries[j].ID })
	sort.Slice(manifest.BlockedCanaries, func(i, j int) bool { return manifest.BlockedCanaries[i].ID < manifest.BlockedCanaries[j].ID })
	sort.Slice(manifest.Budgets, func(i, j int) bool { return manifest.Budgets[i].Seat < manifest.Budgets[j].Seat })
	byDigest, bySeat := map[string]E8RouteDescriptor{}, map[string]E8RouteDescriptor{}
	for _, route := range manifest.Incumbents {
		if route.Validate() != nil || route.PriceRevisionDigest != manifest.PriceRevision.Digest || bySeat[route.Seat].Seat != "" {
			return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
		}
		if !e8PricePinsModel(manifest.PriceRevision, route.Model) {
			return E8RoutingEconomicsManifest{}, ErrE8PriceMissing
		}
		bySeat[route.Seat], byDigest[route.Digest] = route, route
	}
	for _, budget := range manifest.Budgets {
		if !strideIdentifier(budget.Seat) || budget.MaxCallCostMicros < 1 || budget.MaxWorkflowCostMicros < budget.MaxCallCostMicros || budget.MaxCalls < 1 || !isHexDigest(budget.RollbackRouteDigest) || bySeat[budget.Seat].Seat == "" || budget.RollbackRouteDigest != bySeat[budget.Seat].Digest {
			return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
		}
	}
	if len(manifest.Budgets) != len(bySeat) {
		return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
	}
	for _, canary := range manifest.Canaries {
		candidate, err := NewE8CanaryManifest(canary)
		if err != nil || candidate.Digest != canary.Digest || canary.Candidate.PriceRevisionDigest != manifest.PriceRevision.Digest {
			return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
		}
		if !e8PricePinsModel(manifest.PriceRevision, canary.Candidate.Model) {
			return E8RoutingEconomicsManifest{}, ErrE8PriceMissing
		}
		baseline, found := byDigest[canary.BaselineRouteDigest]
		if !found || baseline.Seat != canary.Seat || canary.RollbackRouteDigest != baseline.Digest {
			return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
		}
		if canary.SafetyRegression || baseline.SafetyDigest != canary.Candidate.SafetyDigest {
			return E8RoutingEconomicsManifest{}, ErrE8SafetyRegression
		}
		if e8RouteChangeCount(baseline, canary.Candidate) != 1 || !e8RouteChangesKind(baseline, canary.Candidate, canary.Kind) {
			return E8RoutingEconomicsManifest{}, ErrE8TooManyChanges
		}
	}
	for _, blocked := range manifest.BlockedCanaries {
		candidate, err := NewE8BlockedCanaryManifest(blocked)
		if err != nil || candidate.Digest != blocked.Digest || bySeat[blocked.Seat].Seat == "" {
			return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
		}
	}
	digest, err := STRIDEContractDigest(struct {
		Version int                       `json:"version"`
		Price   E8PriceRevision           `json:"priceRevision"`
		Routes  []E8RouteDescriptor       `json:"incumbents"`
		Canary  []E8CanaryManifest        `json:"canaries"`
		Blocked []E8BlockedCanaryManifest `json:"blockedCanaries"`
		Budget  []E8RouteBudget           `json:"budgets"`
	}{manifest.Version, manifest.PriceRevision, manifest.Incumbents, manifest.Canaries, manifest.BlockedCanaries, manifest.Budgets})
	if err != nil {
		return E8RoutingEconomicsManifest{}, ErrE8RoutingInvalid
	}
	manifest.Digest = digest
	return manifest, nil
}

func (manifest E8RoutingEconomicsManifest) Validate() error {
	want, err := NewE8RoutingEconomicsManifest(manifest)
	if err != nil || want.Digest != manifest.Digest {
		return errOrE8Invalid(err)
	}
	return nil
}

func errOrE8Invalid(err error) error {
	if err != nil {
		return err
	}
	return ErrE8RoutingInvalid
}

func e8PricePinsModel(revision E8PriceRevision, model string) bool {
	for _, pin := range revision.Models {
		if pin.Model == model {
			return true
		}
	}
	return false
}

func e8RouteChangeCount(a, b E8RouteDescriptor) int {
	changes := 0
	if a.Model != b.Model {
		changes++
	}
	if a.Effort != b.Effort {
		changes++
	}
	if a.PromptDigest != b.PromptDigest {
		changes++
	}
	if a.SchemaDigest != b.SchemaDigest {
		changes++
	}
	if a.PromptCache != b.PromptCache {
		changes++
	}
	if a.PersistedReasoning != b.PersistedReasoning {
		changes++
	}
	if a.ProgrammaticTools != b.ProgrammaticTools {
		changes++
	}
	if a.BoundedMultiAgent != b.BoundedMultiAgent || a.MultiAgentLimit != b.MultiAgentLimit {
		changes++
	}
	return changes
}

func e8RouteChangesKind(a, b E8RouteDescriptor, kind E8ExperimentKind) bool {
	switch kind {
	case E8ExperimentModel:
		return a.Model != b.Model
	case E8ExperimentReasoning:
		return a.Effort != b.Effort
	case E8ExperimentPrompt:
		return a.PromptDigest != b.PromptDigest
	case E8ExperimentSchema:
		return a.SchemaDigest != b.SchemaDigest
	case E8ExperimentPromptCache:
		return a.PromptCache != b.PromptCache
	case E8ExperimentPersistedReasoning:
		return a.PersistedReasoning != b.PersistedReasoning
	case E8ExperimentProgrammaticTools:
		return a.ProgrammaticTools != b.ProgrammaticTools
	case E8ExperimentBoundedMultiAgent:
		return a.BoundedMultiAgent != b.BoundedMultiAgent || a.MultiAgentLimit != b.MultiAgentLimit
	}
	return false
}

// CurrentRoute returns only the incumbent map. Candidate routes have no
// resolver path here, which keeps the known current route authoritative.
func (manifest E8RoutingEconomicsManifest) CurrentRoute(seat string) (E8RouteDescriptor, error) {
	if manifest.Validate() != nil {
		return E8RouteDescriptor{}, ErrE8RoutingInvalid
	}
	for _, route := range manifest.Incumbents {
		if route.Seat == seat {
			return route, nil
		}
	}
	return E8RouteDescriptor{}, ErrE8UnknownSeat
}

type E8OutputClassification string

const (
	E8OutputAccepted E8OutputClassification = "accepted"
	E8OutputRejected E8OutputClassification = "rejected"
)

type E8CallReceipt struct {
	Seat                  string                 `json:"seat"`
	RouteDigest           string                 `json:"routeDigest"`
	Provider              string                 `json:"provider"`
	Model                 string                 `json:"model"`
	Effort                string                 `json:"effort"`
	PromptDigest          string                 `json:"promptDigest"`
	SchemaDigest          string                 `json:"schemaDigest"`
	PriceRevisionDigest   string                 `json:"priceRevisionDigest"`
	ProviderReceiptDigest string                 `json:"providerReceiptDigest"`
	InternalReceiptDigest string                 `json:"internalReceiptDigest"`
	Classification        E8OutputClassification `json:"classification"`
	RejectionReason       string                 `json:"rejectionReason,omitempty"`
	CostMicros            int64                  `json:"costMicros"`
	At                    time.Time              `json:"at"`
	Synthetic             bool                   `json:"synthetic"`
	FallbackRouteDigest   string                 `json:"fallbackRouteDigest,omitempty"`
}

func (receipt E8CallReceipt) ValidateAgainst(manifest E8RoutingEconomicsManifest, now time.Time) error {
	if manifest.Validate() != nil || !strideIdentifier(receipt.Seat) || !isHexDigest(receipt.RouteDigest) || !isHexDigest(receipt.ProviderReceiptDigest) || !isHexDigest(receipt.InternalReceiptDigest) || receipt.At.IsZero() || receipt.CostMicros < 0 ||
		!oneOf(string(receipt.Classification), string(E8OutputAccepted), string(E8OutputRejected)) || (receipt.Classification == E8OutputAccepted && receipt.RejectionReason != "") || (receipt.Classification == E8OutputRejected && !strideIdentifier(receipt.RejectionReason)) {
		return ErrE8RoutingInvalid
	}
	if receipt.Synthetic {
		return ErrE8SyntheticReceipt
	}
	if receipt.FallbackRouteDigest != "" {
		return ErrE8HiddenFallback
	}
	if now.IsZero() || receipt.At.After(now.Add(5*time.Minute)) || now.Sub(receipt.At) > e8MaxReceiptAge {
		return ErrE8ReceiptStale
	}
	route, err := manifest.CurrentRoute(receipt.Seat)
	if err != nil {
		return err
	}
	if receipt.RouteDigest != route.Digest || receipt.Provider != route.Provider || receipt.Model != route.Model || receipt.Effort != route.Effort || receipt.PromptDigest != route.PromptDigest || receipt.SchemaDigest != route.SchemaDigest || receipt.PriceRevisionDigest != manifest.PriceRevision.Digest {
		return ErrE8RoutingInvalid
	}
	for _, budget := range manifest.Budgets {
		if budget.Seat == receipt.Seat && receipt.CostMicros > budget.MaxCallCostMicros {
			return ErrE8BudgetExceeded
		}
	}
	return nil
}

// E8BudgetCircuit is an in-memory deterministic gate for a single workflow
// attempt. It records no provider data and, once open, only returns the
// incumbent rollback pointer. A runtime integration must create a new circuit
// for every idempotent workflow attempt rather than carrying spend between
// tenants or retries.
type E8BudgetCircuit struct {
	calls map[string]int
	costs map[string]int64
	open  map[string]bool
}

type E8CircuitDecision struct {
	Seat                string `json:"seat"`
	Open                bool   `json:"open"`
	RollbackRouteDigest string `json:"rollbackRouteDigest"`
}

func NewE8BudgetCircuit() *E8BudgetCircuit {
	return &E8BudgetCircuit{calls: map[string]int{}, costs: map[string]int64{}, open: map[string]bool{}}
}

func (circuit *E8BudgetCircuit) Observe(manifest E8RoutingEconomicsManifest, receipt E8CallReceipt, now time.Time) (E8CircuitDecision, error) {
	if circuit == nil {
		return E8CircuitDecision{}, ErrE8RoutingInvalid
	}
	if err := receipt.ValidateAgainst(manifest, now); err != nil {
		return E8CircuitDecision{}, err
	}
	var budget E8RouteBudget
	for _, candidate := range manifest.Budgets {
		if candidate.Seat == receipt.Seat {
			budget = candidate
			break
		}
	}
	if budget.Seat == "" {
		return E8CircuitDecision{}, ErrE8UnknownSeat
	}
	decision := E8CircuitDecision{Seat: receipt.Seat, RollbackRouteDigest: budget.RollbackRouteDigest}
	if circuit.open[receipt.Seat] || circuit.calls[receipt.Seat]+1 > budget.MaxCalls || circuit.costs[receipt.Seat]+receipt.CostMicros > budget.MaxWorkflowCostMicros {
		circuit.open[receipt.Seat] = true
		decision.Open = true
		return decision, ErrE8BudgetExceeded
	}
	circuit.calls[receipt.Seat]++
	circuit.costs[receipt.Seat] += receipt.CostMicros
	return decision, nil
}

type E8ReconciliationReceipt struct {
	Seat                    string    `json:"seat"`
	RouteDigest             string    `json:"routeDigest"`
	ProviderTotalMicros     int64     `json:"providerTotalMicros"`
	InternalTotalMicros     int64     `json:"internalTotalMicros"`
	ProviderStatementDigest string    `json:"providerStatementDigest"`
	InternalLedgerDigest    string    `json:"internalLedgerDigest"`
	ObservedAt              time.Time `json:"observedAt"`
	Synthetic               bool      `json:"synthetic"`
}

func (receipt E8ReconciliationReceipt) ValidateAgainst(manifest E8RoutingEconomicsManifest, now time.Time) error {
	if manifest.Validate() != nil || !strideIdentifier(receipt.Seat) || !isHexDigest(receipt.RouteDigest) || !isHexDigest(receipt.ProviderStatementDigest) || !isHexDigest(receipt.InternalLedgerDigest) || receipt.ProviderTotalMicros < 0 || receipt.InternalTotalMicros < 0 || receipt.ObservedAt.IsZero() {
		return ErrE8RoutingInvalid
	}
	if receipt.Synthetic {
		return ErrE8SyntheticReceipt
	}
	if now.IsZero() || receipt.ObservedAt.After(now.Add(5*time.Minute)) || now.Sub(receipt.ObservedAt) > e8MaxReceiptAge {
		return ErrE8ReceiptStale
	}
	route, err := manifest.CurrentRoute(receipt.Seat)
	if err != nil {
		return err
	}
	if route.Digest != receipt.RouteDigest {
		return ErrE8RoutingInvalid
	}
	difference := math.Abs(float64(receipt.ProviderTotalMicros - receipt.InternalTotalMicros))
	allowed := math.Max(100000, 0.02*float64(receipt.ProviderTotalMicros)) // max(USD 0.10, 2%)
	if difference > allowed {
		return ErrE8ReconciliationFailed
	}
	return nil
}

type E8FinalRouteReplayManifest struct {
	FinalRouteMapDigest string `json:"finalRouteMapDigest"`
	CorpusDigest        string `json:"corpusDigest"`
	RollbackMapDigest   string `json:"rollbackMapDigest"`
	DefaultOff          bool   `json:"defaultOff"`
	E10Only             bool   `json:"e10Only"`
}

func (replay E8FinalRouteReplayManifest) Validate() error {
	if !isHexDigest(replay.FinalRouteMapDigest) || !isHexDigest(replay.CorpusDigest) || !isHexDigest(replay.RollbackMapDigest) || !replay.DefaultOff || !replay.E10Only {
		return ErrE8RoutingInvalid
	}
	return nil
}

type E8SoakManifest struct {
	RouteMapDigest  string        `json:"routeMapDigest"`
	MinimumDuration time.Duration `json:"minimumDuration"`
	MinimumSittings int           `json:"minimumSittings"`
	DefaultOff      bool          `json:"defaultOff"`
	E10Only         bool          `json:"e10Only"`
}

func (soak E8SoakManifest) Validate() error {
	if !isHexDigest(soak.RouteMapDigest) || soak.MinimumDuration < 24*time.Hour || soak.MinimumSittings < 10 || !soak.DefaultOff || !soak.E10Only {
		return ErrE8RoutingInvalid
	}
	return nil
}

type E8PreparedRoutingEconomicsPlan struct {
	Manifest E8RoutingEconomicsManifest `json:"manifest"`
	Replay   E8FinalRouteReplayManifest `json:"replay"`
	Soak     E8SoakManifest             `json:"soak"`
}

// NewE8PreparedRoutingEconomicsPlan binds the frozen replay and 24-hour /
// ten-sitting soak declarations to exactly the same static map. It still has
// no execution method: E10 owns actual provider traffic and observation.
func NewE8PreparedRoutingEconomicsPlan() (E8PreparedRoutingEconomicsPlan, error) {
	manifest, err := E8PreparedRoutingEconomicsManifest()
	if err != nil {
		return E8PreparedRoutingEconomicsPlan{}, err
	}
	hash := func(value string) string { digest, _ := STRIDEContractDigest(value); return digest }
	plan := E8PreparedRoutingEconomicsPlan{
		Manifest: manifest,
		Replay:   E8FinalRouteReplayManifest{FinalRouteMapDigest: manifest.Digest, CorpusDigest: hash("e10-final-route-replay-corpus-v1"), RollbackMapDigest: manifest.Digest, DefaultOff: true, E10Only: true},
		Soak:     E8SoakManifest{RouteMapDigest: manifest.Digest, MinimumDuration: 24 * time.Hour, MinimumSittings: 10, DefaultOff: true, E10Only: true},
	}
	if plan.Replay.Validate() != nil || plan.Soak.Validate() != nil {
		return E8PreparedRoutingEconomicsPlan{}, ErrE8RoutingInvalid
	}
	return plan, nil
}

// E8PreparedRoutingEconomicsManifest is the token-free, closed seed map for
// E10. Every viable candidate is an unverified/default-off one-variable
// experiment. Current official pricing is pinned here, but no candidate is
// provider-qualified or selectable until E10 produces its live receipts.
func E8PreparedRoutingEconomicsManifest() (E8RoutingEconomicsManifest, error) {
	asOf := openAIPriceRefreshAug1
	prices, err := NewE8PriceRevision("price-table-20260801", asOf, []string{"claude-opus-4-8", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-realtime-2", "gpt-realtime-2.1", "gpt-realtime-whisper", "gpt-transcribe", "gpt-live-transcribe"})
	if err != nil {
		return E8RoutingEconomicsManifest{}, err
	}
	hash := func(value string) string { digest, _ := STRIDEContractDigest(value); return digest }
	newRoute := func(seat, provider, model, effort string) (E8RouteDescriptor, error) {
		return NewE8RouteDescriptor(E8RouteDescriptor{Seat: seat, Provider: provider, Model: model, Effort: effort, PromptDigest: hash(seat + ":prompt:v1"), SchemaDigest: hash(seat + ":schema:v1"), SafetyDigest: hash(seat + ":safety:v1"), RouteRevision: seat + "-incumbent-r1", PriceRevisionDigest: prices.Digest, StrictOutputSchema: true, ReadOnlyTools: true})
	}
	routes := make([]E8RouteDescriptor, 0, 7)
	for _, spec := range [][4]string{{seatBrain, providerOpenAI, "gpt-5.6-luna", "low"}, {seatBoard, providerOpenAI, "gpt-5.6-terra", "low"}, {seatOrchestrator, providerOpenAI, "gpt-5.6-sol", "medium"}, {seatReview, providerAnthropic, "claude-opus-4-8", "high"}, {seatCodex, "codex", "gpt-5.6-sol", "high"}, {seatVoiceRoom, providerOpenAI, "gpt-realtime-2", "high"}, {seatTranscriptionLane, providerOpenAI, "gpt-realtime-whisper", "none"}} {
		route, routeErr := newRoute(spec[0], spec[1], spec[2], spec[3])
		if routeErr != nil {
			return E8RoutingEconomicsManifest{}, routeErr
		}
		routes = append(routes, route)
	}
	bySeat := map[string]E8RouteDescriptor{}
	for _, route := range routes {
		bySeat[route.Seat] = route
	}
	canary := func(id, seat string, kind E8ExperimentKind, change func(*E8RouteDescriptor)) (E8CanaryManifest, error) {
		baseline := bySeat[seat]
		candidate := baseline
		change(&candidate)
		var candidateErr error
		candidate, candidateErr = NewE8RouteDescriptor(candidate)
		if candidateErr != nil {
			return E8CanaryManifest{}, candidateErr
		}
		return NewE8CanaryManifest(E8CanaryManifest{ID: id, Seat: seat, Kind: kind, BaselineRouteDigest: baseline.Digest, Candidate: candidate, CorpusDigest: hash(seat + ":frozen-corpus:v1"), RollbackRouteDigest: baseline.Digest, DefaultOff: true, E10Only: true, Availability: "unverified"})
	}
	canaries := make([]E8CanaryManifest, 0, 11)
	for _, spec := range []struct {
		id, seat string
		kind     E8ExperimentKind
		change   func(*E8RouteDescriptor)
	}{
		{"brain-terra-model", seatBrain, E8ExperimentModel, func(r *E8RouteDescriptor) { r.Model = "gpt-5.6-terra" }},
		{"board-sol-model", seatBoard, E8ExperimentModel, func(r *E8RouteDescriptor) { r.Model = "gpt-5.6-sol" }},
		{"orchestrator-low-reasoning", seatOrchestrator, E8ExperimentReasoning, func(r *E8RouteDescriptor) { r.Effort = "low" }},
		{"critic-xhigh-reasoning", seatReview, E8ExperimentReasoning, func(r *E8RouteDescriptor) { r.Effort = "xhigh" }},
		{"codex-terra-model", seatCodex, E8ExperimentModel, func(r *E8RouteDescriptor) { r.Model = "gpt-5.6-terra" }},
		{"voice-realtime-2_1-model", seatVoiceRoom, E8ExperimentModel, func(r *E8RouteDescriptor) { r.Model = "gpt-realtime-2.1" }},
		{"stt-gpt-transcribe-model", seatTranscriptionLane, E8ExperimentModel, func(r *E8RouteDescriptor) { r.Model = "gpt-transcribe" }},
		{"brain-prompt-cache", seatBrain, E8ExperimentPromptCache, func(r *E8RouteDescriptor) { r.PromptCache = true }},
		{"brain-persisted-reasoning", seatBrain, E8ExperimentPersistedReasoning, func(r *E8RouteDescriptor) { r.PersistedReasoning = true }},
		{"brain-programmatic-tools", seatBrain, E8ExperimentProgrammaticTools, func(r *E8RouteDescriptor) { r.ProgrammaticTools = true }},
		{"brain-bounded-multi-agent", seatBrain, E8ExperimentBoundedMultiAgent, func(r *E8RouteDescriptor) { r.BoundedMultiAgent, r.MultiAgentLimit = true, 2 }},
	} {
		value, canaryErr := canary(spec.id, spec.seat, spec.kind, spec.change)
		if canaryErr != nil {
			return E8RoutingEconomicsManifest{}, canaryErr
		}
		canaries = append(canaries, value)
	}
	budgets := make([]E8RouteBudget, 0, len(routes))
	for _, route := range routes {
		budgets = append(budgets, E8RouteBudget{Seat: route.Seat, MaxCallCostMicros: 500000, MaxWorkflowCostMicros: 5000000, MaxCalls: 16, RollbackRouteDigest: route.Digest})
	}
	return NewE8RoutingEconomicsManifest(E8RoutingEconomicsManifest{Version: 1, PriceRevision: prices, Incumbents: routes, Canaries: canaries, Budgets: budgets})
}
