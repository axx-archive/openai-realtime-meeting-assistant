package e9readiness

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	LocalIntegrationReceiptSchema = "stride.e9.local-integration-receipt/v1"
	LocalIntegrationTestName      = "TestE9LocalAppControlPersistenceFailoverRestore"
	LocalIntegrationHermeticEnv   = "E9_LOCAL_INTEGRATION_HERMETIC"
)

// LocalIntegrationApplicationEnvironment is the complete application-owned
// environment admitted to the E9 child process. All writable paths are rooted
// below tempRoot, every provider credential and external endpoint is blank,
// background workers are explicitly disabled, and the only enabled runtime is
// the local STRIDE system exercised by the drill.
func LocalIntegrationApplicationEnvironment(tempRoot, receiptPath string) (map[string]string, error) {
	tempRoot = filepath.Clean(strings.TrimSpace(tempRoot))
	receiptPath = filepath.Clean(strings.TrimSpace(receiptPath))
	if tempRoot == "." || !filepath.IsAbs(tempRoot) {
		return nil, errors.New("local integration temp root must be absolute")
	}
	if receiptPath == "." || !pathWithin(receiptPath, tempRoot) {
		return nil, errors.New("local integration receipt must remain under the temp root")
	}

	dataDir := filepath.Join(tempRoot, "app-data")
	workerDir := filepath.Join(tempRoot, "workers")
	authorityKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	return map[string]string{
		"E9_LOCAL_INTEGRATION_ROOT":                         tempRoot,
		"E9_LOCAL_INTEGRATION_RECEIPT":                      receiptPath,
		LocalIntegrationHermeticEnv:                         "true",
		"MEETING_MEMORY_PATH":                               filepath.Join(dataDir, "meeting-memory.jsonl"),
		"KANBAN_BOARD_PATH":                                 filepath.Join(dataDir, "kanban-board.json"),
		"BONFIRE_USERS_PATH":                                filepath.Join(dataDir, "users.json"),
		"BONFIRE_SESSIONS_PATH":                             filepath.Join(dataDir, "sessions.json"),
		"BONFIRE_ROOMS_PATH":                                filepath.Join(dataDir, "rooms.json"),
		"MEETINGS_PATH":                                     filepath.Join(dataDir, "meetings.json"),
		"NOTIFICATIONS_PATH":                                filepath.Join(dataDir, "notifications.json"),
		"ADMISSION_ANCHORS_PATH":                            filepath.Join(dataDir, "admission-anchors.json"),
		"EMBEDDINGS_PATH":                                   filepath.Join(dataDir, "embeddings.jsonl"),
		"USAGE_LEDGER_PATH":                                 filepath.Join(dataDir, "usage-ledger.jsonl"),
		"PUSH_SUBSCRIPTIONS_PATH":                           filepath.Join(dataDir, "push-subscriptions.json"),
		"DEVICE_PUSH_TOKENS_PATH":                           filepath.Join(dataDir, "device-push-tokens.json"),
		"THREAD_MUTES_PATH":                                 filepath.Join(dataDir, "thread-mutes.json"),
		"THREAD_READ_MARKERS_PATH":                          filepath.Join(dataDir, "thread-read-markers.json"),
		"BONFIRE_FILE_FOLDERS_PATH":                         filepath.Join(dataDir, "file-folders.json"),
		"BONFIRE_CODEX_SCRATCH_ROOT":                        filepath.Join(workerDir, "codex-scratch"),
		"BONFIRE_CODEX_QUEUE_PATH":                          filepath.Join(workerDir, "codex-jobs"),
		"BONFIRE_CODEX_HEARTBEAT_PATH":                      filepath.Join(workerDir, "codex-heartbeat.json"),
		"BONFIRE_RENDER_QUEUE_PATH":                         filepath.Join(workerDir, "render-jobs"),
		"BONFIRE_RENDER_HEARTBEAT_PATH":                     filepath.Join(workerDir, "render-heartbeat.json"),
		"STRIDE_MEETING_SPECIALIST_CONTROL_ACTIVATION_PATH": filepath.Join(dataDir, "meeting-specialist-activation.json"),
		"STRIDE_RUNTIME_SNAPSHOT_PATH":                      filepath.Join(dataDir, "stride", "runtime.snapshot.json"),
		"STRIDE_RUNTIME_GENERATION_PATH":                    filepath.Join(dataDir, "stride", "runtime.generation.json"),

		"BONFIRE_CANONICAL_MODE":                    "off",
		"BONFIRE_CANONICAL_DATABASE_URL":            "",
		"BONFIRE_CANONICAL_TENANT_ID":               "tenant-1",
		"BONFIRE_PUBLIC_URL":                        "http://127.0.0.1",
		"BONFIRE_RESTORE_MODE":                      "",
		"STRIDE_RUNTIME_ENABLED":                    "true",
		"STRIDE_RUNTIME_BOOTSTRAP_EMPTY":            "true",
		"STRIDE_RUNTIME_MIN_GENERATION":             "91",
		"STRIDE_RUNTIME_RECALL_THREAD_IDS":          "team",
		"STRIDE_RUNTIME_SNAPSHOT_KEY_ID":            "e9_local_failover_key",
		"STRIDE_RUNTIME_SNAPSHOT_MAC_KEY":           authorityKey,
		"STRIDE_LOCAL_PRODUCT_PREVIEW_ENABLED":      "false",
		"STRIDE_MEETING_SPECIALIST_CONTROL_ENABLED": "false",

		"OPENAI_API_KEY":                "",
		"OPENAI_REALTIME_API_KEY":       "",
		"OPENAI_TRANSCRIPTION_API_KEY":  "",
		"OPENAI_RESPONSES_BASE_URL":     "",
		"ANTHROPIC_API_KEY":             "",
		"ANTHROPIC_BASE_URL":            "",
		"FISCAL_API_KEY":                "",
		"FISCAL_AI_API_KEY":             "",
		"PERPLEXITY_API_KEY":            "",
		"RESEND_API_KEY":                "",
		"GIPHY_API_KEY":                 "",
		"EXPO_ACCESS_TOKEN":             "",
		"GOOGLE_CALENDAR_CLIENT_ID":     "",
		"GOOGLE_CALENDAR_CLIENT_SECRET": "",
		"GOOGLE_CALENDAR_REDIRECT_URL":  "",
		"BACKUP_S3_ENDPOINT":            "",
		"BACKUP_S3_BUCKET":              "",
		"BACKUP_S3_ACCESS_KEY":          "",
		"BACKUP_S3_SECRET_KEY":          "",
		"BONFIRE_RENDER_CALLBACK_URL":   "",
		"BONFIRE_RUNNER_CALLBACK_URL":   "",
		"BONFIRE_RUNNER_TOKEN":          "",

		"BACKUP_DISABLED":                      "true",
		"BONFIRE_WORKFLOW_TICKER_DISABLED":     "true",
		"BONFIRE_CODEX_EXECUTION_ENABLED":      "false",
		"BONFIRE_CODEX_EXTERNAL_WRITE_ENABLED": "false",
		"BONFIRE_AGENT_THREAD_WORKER":          "off",
		"MEETING_BRAIN_DISABLED":               "true",
		"MEETING_BOARD_DISABLED":               "true",
		"MEETING_DIGEST_DISABLED":              "true",
		"DAY_DIGEST_DISABLED":                  "true",
		"DAY_REFLECTION_DISABLED":              "true",
		"DECISION_LEDGER_DISABLED":             "true",
		"ENTITY_LEDGER_DISABLED":               "true",
		"COMPANY_DIGEST_DISABLED":              "true",
		"MISSION_INTEL_DISABLED":               "true",
		"NARRATIVE_MAINTAINER_DISABLED":        "true",
		"RESEARCH_SUGGESTIONS_DISABLED":        "true",
		"RESEARCH_SUGGESTION_DISABLED":         "true",
		"SLOP_CLASSIFIER_DISABLED":             "true",
		"EMBEDDINGS_DISABLED":                  "true",
		"USAGE_LEDGER_DISABLED":                "true",
		"USAGE_ALERTS_DISABLED":                "true",
		"USAGE_ROLLUP_DISABLED":                "true",
		"MEETING_DISABLE_DEFAULT_STUN":         "true",
		"MEETING_DISABLE_SERVER_TURN":          "true",
		"MEETING_ICE_SERVERS_JSON":             "[]",
	}, nil
}

// IsLocalIntegrationToolEnvironmentKey identifies the deliberately tiny
// build-tool surface inherited by cmd/e9-readiness. Application configuration,
// proxies, credentials, and activation switches are intentionally absent.
func IsLocalIntegrationToolEnvironmentKey(key string) bool {
	switch key {
	case "PATH", "GOROOT", "GOTOOLDIR", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOEXPERIMENT",
		"CGO_ENABLED", "CC", "CXX", "AR", "PKG_CONFIG", "SDKROOT", "DEVELOPER_DIR", "MACOSX_DEPLOYMENT_TARGET", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT":
		return true
	default:
		return false
	}
}

// ValidateLocalIntegrationChildEnvironment proves that the test process saw
// exactly the local application contract plus the admitted Go tool variables.
func ValidateLocalIntegrationChildEnvironment(environ []string, tempRoot, receiptPath string) error {
	expected, err := LocalIntegrationApplicationEnvironment(tempRoot, receiptPath)
	if err != nil {
		return err
	}
	seen := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("invalid child environment entry %q", entry)
		}
		seen[key] = value
		if wanted, controlled := expected[key]; controlled {
			if value != wanted {
				return fmt.Errorf("child environment %s did not match its temp-local contract", key)
			}
			continue
		}
		if IsLocalIntegrationToolEnvironmentKey(key) || isLocalIntegrationControlledToolKey(key) || key == "PWD" {
			continue
		}
		return fmt.Errorf("unexpected inherited child environment variable %s", key)
	}
	for key := range expected {
		if _, ok := seen[key]; !ok {
			return fmt.Errorf("child environment omitted %s", key)
		}
	}
	return nil
}

func isLocalIntegrationControlledToolKey(key string) bool {
	switch key {
	case "HOME", "TMPDIR", "TMP", "TEMP", "GOCACHE", "GOTMPDIR", "GOMODCACHE", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "GOTELEMETRY", "GOENV", "GOWORK", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME":
		return true
	default:
		return false
	}
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// LocalIntegrationReceipt is emitted by the root-package integration test
// after it has exercised the real app-owned STRIDE runtime over temporary
// durable files and loopback HTTP servers. It is deliberately narrower than a
// production HA, WebRTC, provider, release, or restore qualification receipt.
type LocalIntegrationReceipt struct {
	SchemaVersion      string                         `json:"schemaVersion"`
	EvidenceClass      string                         `json:"evidenceClass"`
	State              string                         `json:"state"`
	TempResourcesOnly  bool                           `json:"tempResourcesOnly"`
	NetworkScope       string                         `json:"networkScope"`
	ProviderCalls      bool                           `json:"providerCalls"`
	ProductionMutation bool                           `json:"productionMutation"`
	DockerMutation     bool                           `json:"dockerMutation"`
	ExecutedSystems    []string                       `json:"executedSystems"`
	Routing            LocalRoutingObservation        `json:"routing"`
	MediaIsolation     LocalMediaIsolationObservation `json:"mediaIsolation"`
	Persistence        LocalPersistenceObservation    `json:"persistence"`
	Timings            []MeasuredTiming               `json:"timings"`
	ClaimsExcluded     []string                       `json:"claimsExcluded"`
}

type LocalRoutingObservation struct {
	InitialReplica         string `json:"initialReplica"`
	FailoverReplica        string `json:"failoverReplica"`
	RestoredControlReplica string `json:"restoredControlReplica"`
	PersistenceReplica     string `json:"persistenceReplica"`
	InitialStatus          int    `json:"initialStatus"`
	FailoverStatus         int    `json:"failoverStatus"`
	RestoredControlStatus  int    `json:"restoredControlStatus"`
	PersistenceStatus      int    `json:"persistenceStatus"`
	DeadPrimaryObserved    bool   `json:"deadPrimaryObserved"`
	SelectedRoutePersisted bool   `json:"selectedRoutePersisted"`
}

type LocalMediaIsolationObservation struct {
	EvidenceClass       string `json:"evidenceClass"`
	RoomA               string `json:"roomA"`
	RoomB               string `json:"roomB"`
	SameRoomStatus      int    `json:"sameRoomStatus"`
	CrossRoomStatus     int    `json:"crossRoomStatus"`
	DuringAppLossStatus int    `json:"duringAppLossStatus"`
	RoomLaneDistinct    bool   `json:"roomLaneDistinct"`
}

type LocalPersistenceObservation struct {
	InitialGeneration      uint64 `json:"initialGeneration"`
	FailoverGeneration     uint64 `json:"failoverGeneration"`
	PurgedGeneration       uint64 `json:"purgedGeneration"`
	RestoredGeneration     uint64 `json:"restoredGeneration"`
	InitialDecisionCount   int    `json:"initialDecisionCount"`
	FailoverDecisionCount  int    `json:"failoverDecisionCount"`
	PurgedDecisionCount    int    `json:"purgedDecisionCount"`
	RestoredDecisionCount  int    `json:"restoredDecisionCount"`
	PurgeGeneration        int64  `json:"purgeGeneration"`
	PurgePersisted         bool   `json:"purgePersisted"`
	StaleRollbackRefused   bool   `json:"staleRollbackRefused"`
	CurrentRestoreAccepted bool   `json:"currentRestoreAccepted"`
}

// MeasuredTiming contains an elapsed duration observed around an operation in
// the integration process. No timing is supplied by a manifest or fixture.
type MeasuredTiming struct {
	Operation          string `json:"operation"`
	ElapsedNanoseconds int64  `json:"elapsedNanoseconds"`
}

var requiredLocalIntegrationSystems = []string{
	"meetingassist.kanban_app",
	"meetingassist.stride_runtime",
	"meetingassist.temporal_purge",
	"meetingassist.signed_snapshot_restore",
	"loopback.http_replica_router",
	"loopback.room_scope_media_probe",
}

var requiredLocalIntegrationExclusions = []string{
	"paid provider quality or compatibility",
	"WebRTC, TURN, RTP, media-device, or physical-device continuity",
	"production HA, traffic shift, restore, RPO, RTO, or soak",
	"release, deployment, or live-data qualification",
}

// ValidateLocalIntegrationReceipt prevents a partial or mislabeled test run
// from being surfaced as local integrated evidence.
func ValidateLocalIntegrationReceipt(receipt LocalIntegrationReceipt) error {
	if receipt.SchemaVersion != LocalIntegrationReceiptSchema || receipt.EvidenceClass != "local_deterministic_integration" || receipt.State != "passed" {
		return errors.New("local integration receipt schema, evidence class, or state is invalid")
	}
	if !receipt.TempResourcesOnly || receipt.NetworkScope != "loopback_only" || receipt.ProviderCalls || receipt.ProductionMutation || receipt.DockerMutation {
		return errors.New("local integration receipt crossed its resource or mutation boundary")
	}
	if !containsAll(receipt.ExecutedSystems, requiredLocalIntegrationSystems) {
		return errors.New("local integration receipt omits an executed system")
	}
	if !containsAll(receipt.ClaimsExcluded, requiredLocalIntegrationExclusions) {
		return errors.New("local integration receipt omits a required claim exclusion")
	}
	routing := receipt.Routing
	if routing.InitialReplica == "" || routing.FailoverReplica == "" || routing.RestoredControlReplica == "" || routing.PersistenceReplica == "" ||
		routing.InitialReplica == routing.FailoverReplica || routing.InitialStatus != 200 || routing.FailoverStatus != 200 ||
		routing.RestoredControlStatus != 200 || routing.PersistenceStatus != 200 || !routing.DeadPrimaryObserved || !routing.SelectedRoutePersisted {
		return errors.New("local integration routing observations are incomplete")
	}
	media := receipt.MediaIsolation
	if media.EvidenceClass != "room_scope_control_probe" || media.RoomA == "" || media.RoomB == "" || media.RoomA == media.RoomB ||
		media.SameRoomStatus != 200 || media.CrossRoomStatus != 403 || media.DuringAppLossStatus != 200 || !media.RoomLaneDistinct {
		return errors.New("local integration media-scope observations are incomplete")
	}
	persistence := receipt.Persistence
	if persistence.InitialGeneration == 0 || persistence.FailoverGeneration < persistence.InitialGeneration ||
		persistence.PurgedGeneration <= persistence.FailoverGeneration || persistence.RestoredGeneration < persistence.PurgedGeneration ||
		persistence.InitialDecisionCount != 1 || persistence.FailoverDecisionCount != 1 || persistence.PurgedDecisionCount != 0 ||
		persistence.RestoredDecisionCount != 0 || persistence.PurgeGeneration < 1 || !persistence.PurgePersisted ||
		!persistence.StaleRollbackRefused || !persistence.CurrentRestoreAccepted {
		return errors.New("local integration persistence observations are incomplete")
	}
	if len(receipt.Timings) < 5 {
		return errors.New("local integration receipt omits measured timings")
	}
	seenTimings := map[string]bool{}
	for _, timing := range receipt.Timings {
		if timing.Operation == "" || timing.ElapsedNanoseconds <= 0 || seenTimings[timing.Operation] {
			return fmt.Errorf("invalid measured timing %q", timing.Operation)
		}
		seenTimings[timing.Operation] = true
	}
	for _, required := range []string{"app_failover", "media_during_app_loss", "control_restore", "purge_persist", "signed_state_restore", "stale_rollback_refusal"} {
		if !seenTimings[required] {
			return fmt.Errorf("missing measured timing %q", required)
		}
	}
	return nil
}

// SortLocalIntegrationReceipt stabilizes order-insensitive receipt fields
// without altering any observed values.
func SortLocalIntegrationReceipt(receipt *LocalIntegrationReceipt) {
	if receipt == nil {
		return
	}
	sort.Strings(receipt.ExecutedSystems)
	sort.Strings(receipt.ClaimsExcluded)
	sort.Slice(receipt.Timings, func(i, j int) bool { return receipt.Timings[i].Operation < receipt.Timings[j].Operation })
}
