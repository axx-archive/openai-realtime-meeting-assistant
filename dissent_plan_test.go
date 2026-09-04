package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type dissentPlanGoldenVector struct {
	Name             string   `json:"name"`
	Contract         any      `json:"contract"`
	Registry         any      `json:"registry"`
	Runtime          any      `json:"runtime"`
	ExpectError      string   `json:"expectError"`
	Plan             any      `json:"plan"`
	PlanCanonicalHex string   `json:"planCanonicalHex"`
	PlanID           string   `json:"planId"`
	ContractSha256   string   `json:"contractSha256"`
	Status           string   `json:"status"`
	Topology         *string  `json:"topology"`
	WorkClasses      []string `json:"workClasses"`
	Executor         *struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"executor"`
	AssuranceSeats []struct {
		WorkClass  string `json:"workClass"`
		Reviewer   string `json:"reviewer"`
		Challenger string `json:"challenger"`
		Judge      string `json:"judge"`
	} `json:"assuranceSeats"`
}

type dissentClassifierGoldenVector struct {
	Name           string                         `json:"name"`
	Input          any                            `json:"input"`
	Classification dissentAssuranceClassification `json:"classification"`
}

type dissentPlanGoldenFile struct {
	SourceSha256      map[string]string               `json:"sourceSha256"`
	Vectors           []dissentPlanGoldenVector       `json:"vectors"`
	ClassifierVectors []dissentClassifierGoldenVector `json:"classifierVectors"`
}

func loadDissentPlanGolden(t *testing.T) dissentPlanGoldenFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/dissent_plan_golden.json")
	if err != nil {
		t.Fatalf("golden vectors missing (regenerate with testdata/dissent-plan-golden.mjs): %v", err)
	}
	var golden dissentPlanGoldenFile
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Vectors) < 20 || len(golden.ClassifierVectors) < 3 {
		t.Fatalf("golden vectors=%d classifier=%d, want the full fixture set", len(golden.Vectors), len(golden.ClassifierVectors))
	}
	// The fixtures are only evidence if they can be re-derived from the
	// TypeScript: testdata/dissent-plan-golden.mjs is committed next to them
	// and records the digest of every source it ran, so a coordination.ts /
	// assurance.ts edit that never reached the vectors is detectable instead
	// of silently green.
	for _, source := range []string{"src/coordination.ts", "src/assurance.ts"} {
		if golden.SourceSha256[source] == "" {
			t.Fatalf("golden file must record the sha256 of %s", source)
		}
	}
	return golden
}

// Every plan the real TypeScript compiled replays byte-for-byte: canonical
// bytes, plan id, contract sha, status/topology/executor/assurance seats —
// and every TS throw maps to the expected error code.
func TestDissentCompileIntelligencePlanGoldenVectorsReplay(t *testing.T) {
	golden := loadDissentPlanGolden(t)
	ready, errorsSeen := 0, 0
	for _, vector := range golden.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			plan, err := dissentCompileIntelligencePlan(vector.Contract, vector.Registry, vector.Runtime)
			if vector.ExpectError != "" {
				var planErr *dissentPlanError
				if err == nil || !errors.As(err, &planErr) || planErr.Code != vector.ExpectError {
					t.Fatalf("err=%v, want code %s", err, vector.ExpectError)
				}
				errorsSeen++
				return
			}
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			canonical, err := dissentPlanCanonical(plan)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString([]byte(canonical)); got != vector.PlanCanonicalHex {
				want, _ := hex.DecodeString(vector.PlanCanonicalHex)
				t.Fatalf("plan bytes differ:\n got %s\nwant %s", canonical, string(want))
			}
			if plan.ID != vector.PlanID || plan.ContractSha256 != vector.ContractSha256 || plan.Status != vector.Status {
				t.Fatalf("id=%s contract=%s status=%s, want %s/%s/%s", plan.ID, plan.ContractSha256, plan.Status, vector.PlanID, vector.ContractSha256, vector.Status)
			}
			if (plan.Topology == nil) != (vector.Topology == nil) || (plan.Topology != nil && *plan.Topology != *vector.Topology) {
				t.Fatalf("topology=%v, want %v", plan.Topology, vector.Topology)
			}
			if !reflect.DeepEqual(plan.WorkClasses, vector.WorkClasses) {
				t.Fatalf("workClasses=%v, want %v", plan.WorkClasses, vector.WorkClasses)
			}
			if (plan.Executor == nil) != (vector.Executor == nil) || (plan.Executor != nil && (plan.Executor.Provider != vector.Executor.Provider || plan.Executor.Model != vector.Executor.Model)) {
				t.Fatalf("executor=%+v, want %+v", plan.Executor, vector.Executor)
			}
			if len(plan.Assurance.Assignments) != len(vector.AssuranceSeats) {
				t.Fatalf("assignments=%d, want %d", len(plan.Assurance.Assignments), len(vector.AssuranceSeats))
			}
			for index, seat := range vector.AssuranceSeats {
				assignment := plan.Assurance.Assignments[index]
				if assignment.WorkClass != seat.WorkClass || assignment.Reviewer.Provider != seat.Reviewer || assignment.Challenger.Provider != seat.Challenger || assignment.Judge.Provider != seat.Judge {
					t.Fatalf("assignment[%d]=%+v, want %+v", index, assignment, seat)
				}
			}
			// The sealed plan round-trips through parseIntelligencePlan, and a
			// tampered plan fails the identity check with its own code.
			generic, err := dissentToGeneric(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dissentParseIntelligencePlan(generic); err != nil {
				t.Fatalf("parse sealed plan: %v", err)
			}
			tampered := generic.(map[string]any)
			tampered["continuityMode"] = "online"
			tampered["reasons"] = []any{"tampered"}
			var planErr *dissentPlanError
			if _, err := dissentParseIntelligencePlan(tampered); err == nil || !errors.As(err, &planErr) || planErr.Code != dissentErrPlanIdentityMismatch {
				t.Fatalf("tampered plan err=%v, want %s", err, dissentErrPlanIdentityMismatch)
			}
			if plan.Status == dissentStatusReady {
				ready++
			}
		})
	}
	if ready < 8 || errorsSeen < 4 {
		t.Fatalf("ready=%d errors=%d, want a mixed fixture set", ready, errorsSeen)
	}
}

// The classifier (assurance.ts) pinned on its own vectors, including inputs
// the contract path never produces (code_diff, decision, other).
func TestDissentClassifyAssuranceGoldenVectorsReplay(t *testing.T) {
	golden := loadDissentPlanGolden(t)
	for _, vector := range golden.ClassifierVectors {
		t.Run(vector.Name, func(t *testing.T) {
			input, err := dissentParseCheckInput(vector.Input)
			if err != nil {
				t.Fatalf("parse input: %v", err)
			}
			got := dissentClassifyAssurance(input)
			if !reflect.DeepEqual(got, vector.Classification) {
				t.Fatalf("classification=%+v, want %+v", got, vector.Classification)
			}
		})
	}
}

// executionProfileId is the sha256 of the canonical profile — the registry
// refuses a route whose profileId drifts from its runtime profile.
func TestDissentExecutionProfileIDAndRegistryIdentity(t *testing.T) {
	golden := loadDissentPlanGolden(t)
	registry, err := dissentParseExecutorRegistry(golden.Vectors[0].Registry)
	if err != nil {
		t.Fatal(err)
	}
	routes := registry.Routes["research_factuality"]
	if len(routes) == 0 {
		t.Fatal("fixture registry has no research route")
	}
	id, err := dissentExecutionProfileID(routes[0].Profile)
	if err != nil || id != routes[0].ProfileID {
		t.Fatalf("profile id=%s err=%v, want %s", id, err, routes[0].ProfileID)
	}
	drifted := routes[0].Profile
	drifted.TimeoutMs++
	if driftedID, _ := dissentExecutionProfileID(drifted); driftedID == id {
		t.Fatal("profile id must change with the profile")
	}
}

// dissentStringLength is JavaScript's String.length, the unit every zod
// .min()/.max() on a string measures. Counting runes instead lets a payload
// be accepted by one implementation and rejected by the other; the
// astral_* golden vectors pin the contract path end-to-end, and this pins
// the unit and the fourth call site (plan reasons), which no contract can
// reach.
func TestDissentStringLengthCountsUTF16CodeUnits(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  int
	}{
		{"", 0}, {"abc", 3}, {"h\u00e9llo", 5}, {"\u65e5\u672c\u8a9e", 3},
		{"\U0001F600", 2},           // one rune, two UTF-16 code units
		{"\U0001F600\U0001F600", 4}, // "\U0001F600\U0001F600".length === 4 in JS, so it clears objective's .min(3)
		// A LONE surrogate is one code unit to JS ("\ud800".length === 1).
		// Go's decoder mangles each of its three WTF-8 bytes into a separate
		// U+FFFD, so a `for _, char := range value` count says 3 and the port
		// refuses payloads the TypeScript accepts.
		{"\xed\xa0\x80", 1}, {"a\xed\xa0\x80b", 3}, {"\xed\xbf\xbf", 1},
		{"\xed\xa0\xbd\xed\xb8\x80", 2}, // a PAIR that arrived as two WTF-8 runs is still 2
	} {
		if got := dissentStringLength(testCase.value); got != testCase.want {
			t.Errorf("length(%q)=%d, want %d", testCase.value, got, testCase.want)
		}
	}
	// parseIntelligencePlan's reasons/clarification .max(500): 250 astral
	// characters are 500 code units (in bounds) and 251 are 502 (out).
	golden := loadDissentPlanGolden(t)
	var ready dissentPlanGoldenVector
	for _, vector := range golden.Vectors {
		if vector.Status == dissentStatusReady {
			ready = vector
			break
		}
	}
	plan, err := dissentCompileIntelligencePlan(ready.Contract, ready.Registry, ready.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name    string
		reason  string
		refused bool
	}{
		{"at_max", strings.Repeat("\U0001F600", 250), false},
		{"over_max", strings.Repeat("\U0001F600", 251), true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resealed, err := dissentSealPlan(withDissentReasons(plan, []string{testCase.reason}))
			if testCase.refused {
				var planErr *dissentPlanError
				if err == nil || !errors.As(err, &planErr) || planErr.Code != dissentErrPlanInvalid {
					t.Fatalf("err=%v, want %s (%d UTF-16 units is over the 500 cap)", err, dissentErrPlanInvalid, dissentStringLength(testCase.reason))
				}
				return
			}
			if err != nil || len(resealed.Reasons) != 1 {
				t.Fatalf("err=%v reasons=%v, want the 500-unit reason accepted", err, resealed.Reasons)
			}
		})
	}
}

func withDissentReasons(plan dissentIntelligencePlan, reasons []string) dissentIntelligencePlan {
	plan.Reasons = reasons
	return plan
}

// zod's .int() has no ceiling but Go's int does: three seats at 2^63-1 used to
// WRAP to 999998 ms and slip under the deadline gate, sealing a "ready" plan
// whose latency budget is a lie. The TypeScript sums in doubles, gets
// 1.8e19 > availableDeadlineMs and fails closed; the port refuses the value
// outright, which is the same fail-closed outcome one step earlier (no
// timeout above the 3_600_000 ms deadline ceiling can ever be selected).
func TestDissentIntegerRefusesUnrepresentableTimeouts(t *testing.T) {
	golden := loadDissentPlanGolden(t)
	var staffed dissentPlanGoldenVector
	for _, vector := range golden.Vectors {
		if len(vector.AssuranceSeats) > 0 {
			staffed = vector
			break
		}
	}
	if staffed.Name == "" {
		t.Fatal("fixture set has no plan with a staffed assurance panel")
	}
	if _, err := dissentCompileIntelligencePlan(staffed.Contract, staffed.Registry, staffed.Runtime); err != nil {
		t.Fatalf("baseline vector %s must compile: %v", staffed.Name, err)
	}
	runtime := dissentDeepCopyJSON(t, staffed.Runtime).(map[string]any)
	coverage := runtime["assuranceRegistry"].(map[string]any)["coverage"].(map[string]any)
	inflated := 0
	for _, classCoverage := range coverage {
		for _, role := range []string{"reviewer", "challenger"} {
			for _, seat := range classCoverage.(map[string]any)[role].([]any) {
				// float64: the value arrives from a JSON decoder, and
				// 2^63-1 is what z.number().int().positive() happily accepts.
				seat.(map[string]any)["profile"].(map[string]any)["timeoutMs"] = float64(math.MaxInt64)
				inflated++
			}
		}
	}
	if inflated == 0 {
		t.Fatal("fixture assurance registry has no seats to inflate")
	}
	_, err := dissentCompileIntelligencePlan(staffed.Contract, staffed.Registry, runtime)
	var planErr *dissentPlanError
	if err == nil || !errors.As(err, &planErr) || planErr.Code != dissentErrRuntimeInvalid {
		t.Fatalf("err=%v, want %s — an unrepresentable timeout must never seal a plan", err, dissentErrRuntimeInvalid)
	}
	// The boundary itself: MAX_SAFE_INTEGER in, one past it out.
	reader := dissentReader{code: dissentErrRuntimeInvalid}
	if got, err := reader.integer(map[string]any{"n": float64(9007199254740991)}, "n", "x", 1, math.MaxFloat64); err != nil || got != 9007199254740991 {
		t.Fatalf("integer(MAX_SAFE_INTEGER)=%d err=%v, want it accepted", got, err)
	}
	if _, err := reader.integer(map[string]any{"n": float64(9007199254740992)}, "n", "x", 1, math.MaxFloat64); err == nil {
		t.Fatal("integer(MAX_SAFE_INTEGER+1) must be refused, not wrapped")
	}
}

// parseIntelligencePlan is the validation boundary for a plan that arrives
// from elsewhere, and the plan id is an unkeyed hash — anyone can make a
// forged plan self-consistent. These are the intelligencePlanSchema
// constraints the port used to skip entirely.
func TestDissentParseIntelligencePlanEnforcesNullableFields(t *testing.T) {
	golden := loadDissentPlanGolden(t)
	var seed dissentPlanGoldenVector
	for _, vector := range golden.Vectors {
		if len(vector.AssuranceSeats) > 0 {
			seed = vector
			break
		}
	}
	plan, err := dissentCompileIntelligencePlan(seed.Contract, seed.Registry, seed.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name  string
		spoil func(map[string]any)
	}{
		{"excluded_maker_provider", func(object map[string]any) {
			object["assurance"].(map[string]any)["excludedMakerProvider"] = "attacker-model"
		}},
		{"executor_registry_source_report", func(object map[string]any) {
			object["executorRegistry"].(map[string]any)["sourceReportSha256"] = "not-a-digest"
		}},
		{"tenant_policy_source", func(object map[string]any) {
			object["tenantPolicy"].(map[string]any)["sourceSha256"] = "AAAA"
		}},
		{"assurance_source_report", func(object map[string]any) {
			object["assurance"].(map[string]any)["sourceReportSha256"] = ""
		}},
		{"assurance_registry_sha", func(object map[string]any) {
			object["assurance"].(map[string]any)["registrySha256"] = "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef"
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			generic, err := dissentToGeneric(plan)
			if err != nil {
				t.Fatal(err)
			}
			object := generic.(map[string]any)
			testCase.spoil(object)
			// Re-mint the id so the forgery is self-consistent: only the
			// field checks can refuse it.
			unsigned := map[string]any{}
			for key, value := range object {
				if key != "id" {
					unsigned[key] = value
				}
			}
			id, err := dissentSha256OfCanonical(unsigned)
			if err != nil {
				t.Fatal(err)
			}
			object["id"] = id
			var planErr *dissentPlanError
			if _, err := dissentParseIntelligencePlan(object); err == nil || !errors.As(err, &planErr) || planErr.Code != dissentErrPlanInvalid {
				t.Fatalf("err=%v, want %s", err, dissentErrPlanInvalid)
			}
		})
	}
	// null stays legal everywhere the TS schema says .nullable().
	generic, err := dissentToGeneric(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dissentParseIntelligencePlan(generic); err != nil {
		t.Fatalf("the unmodified sealed plan must still parse: %v", err)
	}
}

func dissentDeepCopyJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copied any
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatal(err)
	}
	return copied
}

// ECMAScript semantics Go's standard library does not share, pinned at the
// unit level so the reason a golden vector is green stays visible: JS \s
// matches NBSP and friends (RE2's does not), and JS toLowerCase expands
// U+0130 to "i" + U+0307 (Go's simple fold produces a bare "i", which then
// matches ASCII patterns JS never matches).
func TestDissentClassifierFollowsECMAScriptCharacterSemantics(t *testing.T) {
	var customerData *regexp.Regexp
	for _, signal := range dissentConsequenceSignals {
		if signal.name == "customerData" {
			customerData = signal.pattern
		}
	}
	if customerData == nil {
		t.Fatal("customerData signal is missing")
	}
	for _, separator := range []string{" ", "\t", "\n", "\u000b", "\u00a0", "\u1680", "\u2009", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000", "\ufeff"} {
		corpus := dissentToLowerJS("Export employee" + separator + "records to the vendor portal")
		if !customerData.MatchString(corpus) {
			t.Errorf("separator %q: customer-data work went undetected (JS \\s matches it)", separator)
		}
	}
	if customerData.MatchString(dissentToLowerJS("Export employeerecords to the vendor portal")) {
		t.Error("no separator at all must not match")
	}
	// U+0130: JS lowercases it to "i" + combining dot, which breaks the ASCII
	// word boundary the production signal needs.
	if got := dissentToLowerJS("PRODUCT\u0130ON"); got != "producti\u0307on" {
		t.Fatalf("toLowerJS=%q, want %q (node: \"PRODUCT\\u0130ON\".toLowerCase())", got, "producti\u0307on")
	}
	var production *regexp.Regexp
	for _, signal := range dissentConsequenceSignals {
		if signal.name == "production" {
			production = signal.pattern
		}
	}
	if production.MatchString(dissentToLowerJS("Review the PRODUCT\u0130ON runbook")) {
		t.Error("U+0130 must not fold into a clean \"production\" the TypeScript never sees")
	}
	if !production.MatchString(dissentToLowerJS("Review the PRODUCTION runbook")) {
		t.Error("an ordinary capital I must still fold and match")
	}
}

// dissentGoldenClone deep-copies a decoded fixture so a test can plant a byte
// in it. The copy goes through JSON BEFORE the mutation, never after:
// json.Marshal rewrites an invalid byte to U+FFFD, which is the very
// substitution these tests exist to keep out of the digest.
func dissentGoldenClone(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// dissentPlantByte appends a byte that is neither UTF-8 nor WTF-8 to the
// string at path, in place.
func dissentPlantByte(t *testing.T, root any, path ...string) any {
	t.Helper()
	node := root
	for _, key := range path[:len(path)-1] {
		object, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %T is not an object", path, node)
		}
		node = object[key]
	}
	object, ok := node.(map[string]any)
	if !ok {
		t.Fatalf("path %v: %T is not an object", path, node)
	}
	leaf := path[len(path)-1]
	text, ok := object[leaf].(string)
	if !ok {
		t.Fatalf("path %v is %T, want a string", path, object[leaf])
	}
	object[leaf] = text + "\xff"
	return root
}

// Every refusal the compiler can reach carries a dissentPlanError.Code. The
// documented caller shape is `errors.As(err, &planErr); switch planErr.Code`,
// so a bare error is not a smaller bug than a wrong code — it falls through to
// the caller's unknown-error branch.
//
// Making dissentCanonicalJSON fail closed on bytes that are neither UTF-8 nor
// WTF-8 opened exactly that hole: reader.str performs no UTF-8 validation, so
// a Go-constructed input carries a stray byte past every parser and only dies
// at the digest — one uncoded error per canonicalization site. Each case below
// plants that byte in a field whose owner is unambiguous, and reaches a
// DIFFERENT canonicalization site (contract.canonical(), registry.raw,
// tenantPolicy.raw, and the assurance registry's raw, which survives the first
// three because dissentPlanAuthorityFor never touches it).
func TestDissentCompileRefusalsAlwaysCarryACode(t *testing.T) {
	golden := loadDissentPlanGolden(t)
	var ready, assured dissentPlanGoldenVector
	for _, vector := range golden.Vectors {
		if vector.Status != dissentStatusReady {
			continue
		}
		if ready.Name == "" {
			ready = vector
		}
		// The assurance registry is only canonicalized when seats are staffed,
		// so that case needs a Full Dissent vector, not the first ready one.
		if assured.Name == "" && len(vector.AssuranceSeats) > 0 {
			assured = vector
		}
	}
	if ready.Name == "" || assured.Name == "" {
		t.Fatalf("need a ready vector (%q) and one with assurance seats (%q)", ready.Name, assured.Name)
	}
	for _, testCase := range []struct {
		name   string
		source dissentPlanGoldenVector
		input  string
		path   []string
		want   string
	}{
		{"contract_objective", ready, "contract", []string{"objective"}, dissentErrContractInvalid},
		{"registry_version", ready, "registry", []string{"version"}, dissentErrRegistryInvalid},
		{"tenant_policy_version", ready, "runtime", []string{"tenantPolicy", "version"}, dissentErrRuntimeInvalid},
		{"assurance_registry_version", assured, "runtime", []string{"assuranceRegistry", "version"}, dissentErrRuntimeInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// The unmutated fixture compiles, so any refusal is the planted byte.
			if _, err := dissentCompileIntelligencePlan(testCase.source.Contract, testCase.source.Registry, testCase.source.Runtime); err != nil {
				t.Fatalf("baseline compile of %s: %v", testCase.source.Name, err)
			}
			contract := dissentGoldenClone(t, testCase.source.Contract)
			registry := dissentGoldenClone(t, testCase.source.Registry)
			runtime := dissentGoldenClone(t, testCase.source.Runtime)
			switch testCase.input {
			case "contract":
				dissentPlantByte(t, contract, testCase.path...)
			case "registry":
				dissentPlantByte(t, registry, testCase.path...)
			case "runtime":
				dissentPlantByte(t, runtime, testCase.path...)
			}
			_, err := dissentCompileIntelligencePlan(contract, registry, runtime)
			if err == nil {
				t.Fatal("a byte that is neither UTF-8 nor WTF-8 must be refused, not hashed")
			}
			var planErr *dissentPlanError
			if !errors.As(err, &planErr) {
				t.Fatalf("err=%v is a bare error with no code; callers switch on dissentPlanError.Code", err)
			}
			if planErr.Code != testCase.want {
				t.Fatalf("code=%s, want %s (%v)", planErr.Code, testCase.want, err)
			}
		})
	}
}

// The plan generator's expectError must be DERIVED from the TypeScript, not
// echoed from the file it is regenerating. It used to short-circuit on the
// committed value, so the 8 throwing vectors were hand-authored data the drift
// gate could never contradict — and the code one of them carries,
// runtime_invalid, was not even reachable from the derivation below the
// short-circuit. Go cannot run node in this suite, so it pins the two
// properties that made the gate inert, in the generator source and in the
// fixture it produced.
func TestDissentPlanGoldenGeneratorDerivesErrorCodes(t *testing.T) {
	source, err := os.ReadFile("testdata/dissent-plan-golden.mjs")
	if err != nil {
		t.Fatal(err)
	}
	// Comments are stripped so the prose explaining the old short-circuit
	// cannot masquerade as the short-circuit itself.
	code := []string{}
	for _, line := range strings.Split(string(source), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code = append(code, line)
		}
	}
	generator := strings.Join(code, "\n")
	if strings.Contains(generator, "return vector.expectError") {
		t.Fatal("errorCodeFor short-circuits on the committed expectError: the code is echoed, never re-derived")
	}
	// Derivation asks the real schemas which input the TypeScript rejected, so
	// a refine that moves between them changes the answer.
	for _, schema := range []string{"workContractSchema", "executorQualificationRegistrySchema", "coordinationRuntimeAuthoritySchema"} {
		if !strings.Contains(generator, schema) {
			t.Fatalf("generator must derive the code from %s", schema)
		}
	}
	if !strings.Contains(generator, "but the TypeScript now implies") || !strings.Contains(generator, "no longer throws") {
		t.Fatal("a committed expectError that disagrees with the derivation (or a vector that stopped throwing) must abort the run")
	}
	// And the fixtures must actually exercise all three input-owned codes: a
	// derivation nothing reaches is as inert as one nothing checks.
	golden := loadDissentPlanGolden(t)
	seen := map[string]int{}
	for _, vector := range golden.Vectors {
		if vector.ExpectError != "" {
			seen[vector.ExpectError]++
		}
	}
	for _, code := range []string{dissentErrContractInvalid, dissentErrRegistryInvalid, dissentErrRuntimeInvalid} {
		if seen[code] == 0 {
			t.Fatalf("no golden vector carries %s; the code has no cross-language evidence (have %v)", code, seen)
		}
	}
}

// Canonicalization must be a function of the VALUE, never of Go's randomized
// map iteration order. A WTF-8 surrogate pair and the astral rune it stands
// for are two different Go strings that are ONE JavaScript string, so the
// UTF-16 key comparator rightly calls them equal — and an object-key sort
// that stops there falls through to map order, emitting {"X":1,"X":2} on one
// run and {"X":2,"X":1} on the next. Both are duplicate-key JSON that
// canonicalJson could never produce (a JS object holds one property per
// string), so the digest taken over either is unverifiable by the other side.
// The port refuses the map instead, the same fail-closed stance
// dissentDecodeJSON takes for unpaired surrogate escapes.
func TestDissentCanonicalRefusesKeysThatAreOneJavaScriptString(t *testing.T) {
	const astral = "\U0001F600"                 // F0 9F 98 80 — one astral rune
	const wtf8Pair = "\xed\xa0\xbd\xed\xb8\x80" // ED A0 BD ED B8 80 — the SAME JS string
	if astral == wtf8Pair {
		t.Fatal("the two encodings must be distinct Go strings for this test to mean anything")
	}
	if dissentCompareUTF16(astral, wtf8Pair) != 0 {
		t.Fatal("a WTF-8 surrogate pair and its astral rune are the same UTF-16 code units, so the comparator must call them equal")
	}
	// Repeat: one pass could get a stable answer by luck, a hundred cannot.
	// Without the refusal this loop sees BOTH duplicate-key orderings.
	first := ""
	for repeat := 0; repeat < 256; repeat++ {
		got, err := dissentCanonicalJSON(map[string]any{astral: 1, wtf8Pair: 2})
		if err == nil {
			t.Fatalf("two keys that are one JavaScript string must be refused; got %q", got)
		}
		if repeat == 0 {
			first = err.Error()
		}
		if err.Error() != first {
			t.Fatalf("refusal must be deterministic:\n got %q\nwant %q", err.Error(), first)
		}
	}
	if !strings.Contains(first, "same JavaScript string") {
		t.Fatalf("refusal must say why: %q", first)
	}
	// Nested values take the same path.
	if _, err := dissentCanonicalJSON(map[string]any{"a": []any{map[string]any{astral: 1, wtf8Pair: 2}}}); err == nil {
		t.Fatal("a colliding key nested inside an array must be refused too")
	}
	// Lone surrogates are NOT collisions: \ud800 and \udfff are different JS
	// strings and must still canonicalize, in UTF-16 code unit order.
	for repeat := 0; repeat < 64; repeat++ {
		got, err := dissentCanonicalJSON(map[string]any{"\xed\xa0\x80": 1, "\xed\xbf\xbf": 2, astral: 3})
		// D800 < D83D DE00 < DFFF: the astral rune sorts BETWEEN the two lone
		// surrogates, as node's Object.keys order does.
		if err != nil || got != "{\"\\ud800\":1,\"\U0001F600\":3,\"\\udfff\":2}" {
			t.Fatalf("lone-surrogate keys must still sort and emit: got %q err=%v", got, err)
		}
	}
	// …and either encoding ALONE is legal input that canonicalizes — and so
	// hashes — identically, because it is the same JavaScript string.
	viaAstral, err := dissentCanonicalJSON(map[string]any{astral: 1})
	if err != nil || viaAstral != "{\"\U0001F600\":1}" {
		t.Fatalf("astral key: got %q err=%v", viaAstral, err)
	}
	viaPair, err := dissentCanonicalJSON(map[string]any{wtf8Pair: 1})
	if err != nil || viaPair != viaAstral {
		t.Fatalf("a WTF-8 pair key must canonicalize to the astral form: got %q err=%v", viaPair, err)
	}
	if dissentSha256Hex(viaPair) != dissentSha256Hex(viaAstral) {
		t.Fatal("the same JavaScript string must produce one digest whichever encoding carried it")
	}
}
