package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/google/uuid"
)

const CanonicalNormalizationRegistryVersion = "canonical-v1"

var canonicalImportNamespace = uuid.MustParse("3d1834dc-26cb-5ccf-a00c-495532167584")

type CanonicalPayloadKind string

const (
	CanonicalPayloadIdentifier     CanonicalPayloadKind = "identifier"
	CanonicalPayloadEnum           CanonicalPayloadKind = "enum"
	CanonicalPayloadBoolean        CanonicalPayloadKind = "boolean"
	CanonicalPayloadTimestamp      CanonicalPayloadKind = "timestamp"
	CanonicalPayloadCounter        CanonicalPayloadKind = "counter"
	CanonicalPayloadDigest         CanonicalPayloadKind = "digest"
	CanonicalPayloadClassification CanonicalPayloadKind = "classification"
	CanonicalPayloadRevision       CanonicalPayloadKind = "revision"
	CanonicalPayloadContentRef     CanonicalPayloadKind = "content_ref"
)

type CanonicalPayloadField struct {
	Kind     CanonicalPayloadKind
	Required bool
	Enums    []string
}

type CanonicalPayloadSchema struct {
	Fields map[string]CanonicalPayloadField
}

type CanonicalPayloadRegistry struct {
	schemas map[string]CanonicalPayloadSchema
}

func NewCanonicalPayloadRegistry() *CanonicalPayloadRegistry {
	return &CanonicalPayloadRegistry{schemas: make(map[string]CanonicalPayloadSchema)}
}

func (registry *CanonicalPayloadRegistry) Register(eventType string, version int, schema CanonicalPayloadSchema) error {
	if registry == nil || strings.TrimSpace(eventType) == "" || version < 1 || len(schema.Fields) == 0 {
		return errors.New("event type, positive version, and fields are required")
	}
	for name, field := range schema.Fields {
		allowedKind, allowed := canonicalMetadataFieldKinds[name]
		if !canonicalFieldName.MatchString(name) || !allowed || field.Kind != allowedKind {
			return fmt.Errorf("field %q is unsafe", name)
		}
		if !validCanonicalPayloadKind(field.Kind) {
			return fmt.Errorf("field %q has invalid kind", name)
		}
	}
	key := canonicalSchemaKey(eventType, version)
	if _, exists := registry.schemas[key]; exists {
		return fmt.Errorf("schema already registered: %s", key)
	}
	fields := make(map[string]CanonicalPayloadField, len(schema.Fields))
	for name, field := range schema.Fields {
		field.Enums = append([]string(nil), field.Enums...)
		fields[name] = field
	}
	registry.schemas[key] = CanonicalPayloadSchema{Fields: fields}
	return nil
}

func (registry *CanonicalPayloadRegistry) ValidateAndNormalize(eventType string, version int, payload json.RawMessage) ([]byte, error) {
	if registry == nil {
		return nil, errors.New("payload registry is required")
	}
	schema, ok := registry.schemas[canonicalSchemaKey(eventType, version)]
	if !ok {
		return nil, errors.New("unregistered payload schema")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("payload must be one JSON object: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("payload must be one JSON object")
	}
	for name := range object {
		field, exists := schema.Fields[name]
		if !exists {
			return nil, fmt.Errorf("unknown field %q", name)
		}
		if err := validateCanonicalPayloadValue(name, object[name], field); err != nil {
			return nil, err
		}
	}
	for name, field := range schema.Fields {
		if _, exists := object[name]; field.Required && !exists {
			return nil, fmt.Errorf("required field %q missing", name)
		}
	}
	return canonicalJSON(object)
}

func NewCanonicalEventPayload(registry *CanonicalPayloadRegistry, eventType string, version int, value any) (json.RawMessage, [32]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, [32]byte{}, err
	}
	normalized, err := registry.ValidateAndNormalize(eventType, version, raw)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return json.RawMessage(normalized), sha256.Sum256(normalized), nil
}

func CanonicalStateDigest(value any) (string, error) {
	data, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalLegacyObjectKey(family string, intrinsicIDs ...string) (string, error) {
	if strings.TrimSpace(family) == "" || len(intrinsicIDs) == 0 {
		return "", errors.New("family and intrinsic IDs are required")
	}
	parts := make([]string, len(intrinsicIDs))
	for index, value := range intrinsicIDs {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", errors.New("intrinsic IDs cannot be blank")
		}
		parts[index] = strconv.Itoa(len(value)) + ":" + value
	}
	return family + "/" + strings.Join(parts, "/"), nil
}

func CanonicalImportEventID(tenantID, family, objectKey, lifecycleEvent, stateDigest string) (uuid.UUID, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(family) == "" || strings.TrimSpace(objectKey) == "" || strings.TrimSpace(lifecycleEvent) == "" || !isHexDigest(stateDigest) {
		return uuid.Nil, errors.New("tenant, family, object key, lifecycle event, and state digest are required")
	}
	name := strings.Join([]string{CanonicalNormalizationRegistryVersion, strings.TrimSpace(tenantID), family, objectKey, lifecycleEvent, stateDigest}, "\x1f")
	return uuid.NewSHA1(canonicalImportNamespace, []byte(name)), nil
}

func NormalizeCanonicalRoomID(roomID string) string {
	if strings.TrimSpace(roomID) == "" {
		return "office"
	}
	return strings.TrimSpace(roomID)
}

var canonicalFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// canonicalMetadataFieldNames is a positive review boundary for immutable
// payloads. New permanent metadata requires an explicit code change here.
var canonicalMetadataFieldKinds = map[string]CanonicalPayloadKind{
	"tenant_id": CanonicalPayloadIdentifier, "object_id": CanonicalPayloadIdentifier, "artifact_id": CanonicalPayloadIdentifier,
	"room_id": CanonicalPayloadIdentifier, "meeting_id": CanonicalPayloadIdentifier, "sitting_id": CanonicalPayloadIdentifier,
	"thread_id": CanonicalPayloadIdentifier, "package_id": CanonicalPayloadIdentifier, "card_id": CanonicalPayloadIdentifier,
	"workflow_id": CanonicalPayloadIdentifier, "job_id": CanonicalPayloadIdentifier, "approval_id": CanonicalPayloadIdentifier,
	"consent_id": CanonicalPayloadIdentifier, "grant_id": CanonicalPayloadIdentifier, "principal_id": CanonicalPayloadIdentifier,
	"content_revision": CanonicalPayloadRevision, "state_revision": CanonicalPayloadRevision, "source_revision": CanonicalPayloadRevision,
	"acl_version": CanonicalPayloadRevision, "sequence": CanonicalPayloadCounter, "count": CanonicalPayloadCounter,
	"content_sha256": CanonicalPayloadDigest, "payload_sha256": CanonicalPayloadDigest, "action_input_sha256": CanonicalPayloadDigest,
	"content_ref": CanonicalPayloadContentRef, "visibility": CanonicalPayloadEnum, "status": CanonicalPayloadEnum,
	"classification": CanonicalPayloadClassification, "policy_version": CanonicalPayloadIdentifier,
	"action": CanonicalPayloadEnum, "authority": CanonicalPayloadEnum, "scope": CanonicalPayloadEnum,
	"source_kind": CanonicalPayloadEnum, "deleted": CanonicalPayloadBoolean,
	"occurred_at": CanonicalPayloadTimestamp, "retain_until": CanonicalPayloadTimestamp, "expires_at": CanonicalPayloadTimestamp,
}

func canonicalSchemaKey(eventType string, version int) string {
	return eventType + "@" + strconv.Itoa(version)
}

func validCanonicalPayloadKind(kind CanonicalPayloadKind) bool {
	switch kind {
	case CanonicalPayloadIdentifier, CanonicalPayloadEnum, CanonicalPayloadBoolean, CanonicalPayloadTimestamp,
		CanonicalPayloadCounter, CanonicalPayloadDigest, CanonicalPayloadClassification,
		CanonicalPayloadRevision, CanonicalPayloadContentRef:
		return true
	default:
		return false
	}
}

func validateCanonicalPayloadValue(name string, value any, field CanonicalPayloadField) error {
	switch field.Kind {
	case CanonicalPayloadBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q must be boolean", name)
		}
	case CanonicalPayloadCounter, CanonicalPayloadRevision:
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("field %q must be an integer", name)
		}
		integer, err := number.Int64()
		if err != nil || integer < 0 {
			return fmt.Errorf("field %q must be a non-negative integer", name)
		}
	case CanonicalPayloadDigest:
		text, ok := value.(string)
		if !ok || !isHexDigest(text) {
			return fmt.Errorf("field %q must be a lowercase SHA-256 digest", name)
		}
	case CanonicalPayloadTimestamp:
		text, ok := value.(string)
		if !ok || !canonicalTimestamp(text) {
			return fmt.Errorf("field %q must be an RFC3339 timestamp", name)
		}
	case CanonicalPayloadIdentifier, CanonicalPayloadClassification, CanonicalPayloadContentRef:
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 1024 || hasControl(text) {
			return fmt.Errorf("field %q must be a bounded identifier", name)
		}
	case CanonicalPayloadEnum:
		text, ok := value.(string)
		if !ok || !stringIn(text, field.Enums) {
			return fmt.Errorf("field %q must be an allowed enum", name)
		}
	default:
		return fmt.Errorf("field %q has invalid kind", name)
	}
	return nil
}

func canonicalTimestamp(value string) bool {
	// Retain the original lexical form for hashing after validating its shape.
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func stringIn(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("payload contains multiple JSON values")
	}
	return err
}

func canonicalJSON(value any) ([]byte, error) {
	// RFC 8785 JCS is the cross-language hash contract. Transform normalizes
	// floating-point spelling, negative zero, escaping, and object key order.
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := validateJCSSafeNumbers(decoded); err != nil {
		return nil, err
	}
	return jsoncanonicalizer.Transform(data)
}

func validateJCSSafeNumbers(value any) error {
	switch typed := value.(type) {
	case json.Number:
		number, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return fmt.Errorf("number %q is not interoperable RFC 8785 input", typed)
		}
		if math.Trunc(number) == number && math.Abs(number) > 9007199254740991 {
			return fmt.Errorf("integral number %q is outside the RFC 8785 interoperable range", typed)
		}
	case []any:
		for _, item := range typed {
			if err := validateJCSSafeNumbers(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range typed {
			if err := validateJCSSafeNumbers(item); err != nil {
				return err
			}
		}
	}
	return nil
}
