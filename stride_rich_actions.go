package main

// Token-free E4 foundations for rich coworker actions. These types deliberately
// expose no HTTP route or live provider adapter. A caller must opt in, supply a
// durable repository plus current authorization resolvers, and consume only
// server-minted capabilities.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrSTRIDERichActionDisabled  = errors.New("STRIDE rich actions are disabled")
	ErrSTRIDEFileHandleDenied    = errors.New("file selection is unavailable")
	ErrSTRIDEFileDispatchState   = errors.New("file selection dispatch cannot proceed")
	ErrSTRIDEFileDispatchUnknown = errors.New("file selection dispatch is ambiguous")
	ErrSTRIDELinkPreviewDenied   = errors.New("link preview request is unavailable")
	ErrSTRIDEGIFDenied           = errors.New("agent GIF action is unavailable")
)

const (
	strideFileSelectionPending   = "pending"
	strideFileSelectionSending   = "sending"
	strideFileSelectionConfirmed = "confirmed"
	strideFileSelectionAmbiguous = "ambiguous"
	strideFileSelectionRevoked   = "revoked"
)

// STRIDERichDestination binds a file capability to one exact audience
// revision. AudienceDigest is derived server-side; clients never supply it.
type STRIDERichDestination struct {
	ThreadID       string         `json:"threadId"`
	Audience       STRIDEAudience `json:"audience"`
	ACLVersion     int64          `json:"aclVersion"`
	AudienceDigest string         `json:"audienceDigest"`
}

func normalizeSTRIDERichDestination(value STRIDERichDestination) (STRIDERichDestination, error) {
	value.ThreadID = strings.TrimSpace(value.ThreadID)
	if !strideIdentifier(value.ThreadID) || value.ACLVersion < 1 || value.Audience.Validate() != nil {
		return STRIDERichDestination{}, ErrSTRIDEFileHandleDenied
	}
	principals := append([]string(nil), value.Audience.Principals...)
	sort.Strings(principals)
	value.Audience.Principals = principals
	canonical := strings.Join([]string{
		"stride-rich-destination/v1",
		value.ThreadID,
		value.Audience.Visibility,
		strings.Join(principals, "\x1f"),
		fmt.Sprint(value.ACLVersion),
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	value.AudienceDigest = fmt.Sprintf("sha256:%x", digest[:])
	return value, nil
}

type STRIDEFileSelectionRecord struct {
	ID                 string                `json:"id"`
	Requester          string                `json:"requester"`
	Source             ACLObjectRef          `json:"source"`
	SourceRevision     ACLRevisionRef        `json:"sourceRevision"`
	Destination        STRIDERichDestination `json:"destination"`
	Purpose            string                `json:"purpose"`
	NonceDigest        string                `json:"nonceDigest"`
	BindingDigest      string                `json:"bindingDigest"`
	CreatedAt          time.Time             `json:"createdAt"`
	ExpiresAt          time.Time             `json:"expiresAt"`
	Status             string                `json:"status"`
	ExecutionKeyDigest string                `json:"executionKeyDigest,omitempty"`
	ProviderReceipt    string                `json:"providerReceipt,omitempty"`
	ConfirmedMessageID string                `json:"confirmedMessageId,omitempty"`
	LastError          string                `json:"lastError,omitempty"`
}

type STRIDEFileSelectionToken struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// STRIDEFileSelectionMintRequest contains object identities only. There is no
// blob-ref field: possession of a content hash is never read or share authority.
type STRIDEFileSelectionMintRequest struct {
	Requester      string
	Source         ACLObjectRef
	SourceRevision ACLRevisionRef
	Destination    STRIDERichDestination
	Purpose        string
	TTL            time.Duration
}

type STRIDEFilePostCommand struct {
	HandleID       string
	Requester      string
	Source         ACLObjectRef
	SourceRevision ACLRevisionRef
	Destination    STRIDERichDestination
	Purpose        string
	ExecutionKey   string
}

type STRIDEFilePostReceipt struct {
	ProviderReceipt string
	MessageID       string
}

type STRIDEFileActionAuthority interface {
	ReauthorizeSource(context.Context, string, ACLObjectRef, ACLRevisionRef, ACLAction) error
	CurrentDestination(context.Context, string) (STRIDERichDestination, error)
	AuthorizeDestination(context.Context, string, STRIDERichDestination, ACLAction) error
}

type STRIDEFilePoster interface {
	PostFileExactlyOnce(context.Context, STRIDEFilePostCommand) (STRIDEFilePostReceipt, error)
}

type STRIDEFileSelectionRepository interface {
	Create(context.Context, STRIDEFileSelectionRecord) error
	Read(context.Context, string) (STRIDEFileSelectionRecord, error)
	Transact(context.Context, string, func(*STRIDEFileSelectionRecord) error) error
}

type STRIDEFileSelectionService struct {
	Enabled   bool
	Repo      STRIDEFileSelectionRepository
	Authority STRIDEFileActionAuthority
	Poster    STRIDEFilePoster
	Now       func() time.Time
	Random    func([]byte) error
}

func (service STRIDEFileSelectionService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func (service STRIDEFileSelectionService) random(buffer []byte) error {
	if service.Random != nil {
		return service.Random(buffer)
	}
	_, err := rand.Read(buffer)
	return err
}

func (service STRIDEFileSelectionService) Mint(ctx context.Context, request STRIDEFileSelectionMintRequest) (STRIDEFileSelectionToken, error) {
	if !service.Enabled {
		return STRIDEFileSelectionToken{}, ErrSTRIDERichActionDisabled
	}
	if service.Repo == nil || service.Authority == nil {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	request.Requester = normalizeAccountEmail(request.Requester)
	request.Purpose = strings.TrimSpace(request.Purpose)
	destination, err := normalizeSTRIDERichDestination(request.Destination)
	if request.Requester == "" || !validSTRIDEFilePurpose(request.Purpose) || request.Source.ACLVersion < 1 ||
		strings.TrimSpace(request.Source.TenantID) == "" || strings.TrimSpace(request.Source.Type) == "" || strings.TrimSpace(request.Source.ID) == "" ||
		request.SourceRevision.ContentRevision < 1 || !isHexDigest(request.SourceRevision.ContentDigest) || err != nil {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	if request.TTL <= 0 || request.TTL > 15*time.Minute {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	if err := service.Authority.ReauthorizeSource(ctx, request.Requester, request.Source, request.SourceRevision, ACLReadContent); err != nil {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	currentDestination, err := service.Authority.CurrentDestination(ctx, destination.ThreadID)
	if err != nil || !sameSTRIDERichDestination(destination, currentDestination) {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	if err := service.Authority.AuthorizeDestination(ctx, request.Requester, destination, ACLWrite); err != nil {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	random := make([]byte, 64)
	if err := service.random(random); err != nil {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	handleID := "stride-file-" + base64.RawURLEncoding.EncodeToString(random[:32])
	nonceDigestBytes := sha256.Sum256(random[32:])
	nonceDigest := fmt.Sprintf("sha256:%x", nonceDigestBytes[:])
	bindingDigest := strideFileSelectionBindingDigest(request.Requester, request.Source, request.SourceRevision, destination, request.Purpose, nonceDigest)
	now := service.now()
	record := STRIDEFileSelectionRecord{
		ID: handleID, Requester: request.Requester, Source: request.Source, SourceRevision: request.SourceRevision,
		Destination: destination, Purpose: request.Purpose, NonceDigest: nonceDigest, BindingDigest: bindingDigest,
		CreatedAt: now, ExpiresAt: now.Add(request.TTL), Status: strideFileSelectionPending,
	}
	if err := service.Repo.Create(ctx, record); err != nil {
		return STRIDEFileSelectionToken{}, ErrSTRIDEFileHandleDenied
	}
	return STRIDEFileSelectionToken{ID: record.ID, ExpiresAt: record.ExpiresAt}, nil
}

func validSTRIDEFilePurpose(value string) bool {
	switch value {
	case "post_to_thread", "attach_to_work", "share_existing_file":
		return true
	default:
		return false
	}
}

func strideFileSelectionBindingDigest(requester string, source ACLObjectRef, revision ACLRevisionRef, destination STRIDERichDestination, purpose, nonceDigest string) string {
	canonical := strings.Join([]string{
		"stride-file-selection/v1", normalizeAccountEmail(requester), source.TenantID, source.Type, source.ID,
		fmt.Sprint(source.ACLVersion), fmt.Sprint(revision.ContentRevision), revision.ContentDigest,
		destination.ThreadID, destination.AudienceDigest, fmt.Sprint(destination.ACLVersion), purpose, nonceDigest,
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func sameSTRIDERichDestination(left, right STRIDERichDestination) bool {
	left, leftErr := normalizeSTRIDERichDestination(left)
	right, rightErr := normalizeSTRIDERichDestination(right)
	return leftErr == nil && rightErr == nil && left.ThreadID == right.ThreadID && left.ACLVersion == right.ACLVersion && left.AudienceDigest == right.AudienceDigest
}

func strideExecutionKeyDigest(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func (service STRIDEFileSelectionService) Post(ctx context.Context, handleID, requester, executionKey string) (STRIDEFilePostReceipt, error) {
	if !service.Enabled {
		return STRIDEFilePostReceipt{}, ErrSTRIDERichActionDisabled
	}
	if service.Repo == nil || service.Authority == nil || service.Poster == nil || strings.TrimSpace(executionKey) == "" {
		return STRIDEFilePostReceipt{}, ErrSTRIDEFileHandleDenied
	}
	requester = normalizeAccountEmail(requester)
	executionDigest := strideExecutionKeyDigest(executionKey)
	var command STRIDEFilePostCommand
	var replay STRIDEFilePostReceipt
	replayConfirmed := false
	err := service.Repo.Transact(ctx, strings.TrimSpace(handleID), func(record *STRIDEFileSelectionRecord) error {
		if record == nil || requester == "" || requester != record.Requester || !record.ExpiresAt.After(service.now()) ||
			record.BindingDigest != strideFileSelectionBindingDigest(record.Requester, record.Source, record.SourceRevision, record.Destination, record.Purpose, record.NonceDigest) {
			return ErrSTRIDEFileHandleDenied
		}
		switch record.Status {
		case strideFileSelectionConfirmed:
			if record.ExecutionKeyDigest != executionDigest {
				return ErrSTRIDEFileDispatchState
			}
			replay = STRIDEFilePostReceipt{ProviderReceipt: record.ProviderReceipt, MessageID: record.ConfirmedMessageID}
			replayConfirmed = true
			return nil
		case strideFileSelectionSending, strideFileSelectionAmbiguous:
			return ErrSTRIDEFileDispatchUnknown
		case strideFileSelectionPending:
		default:
			return ErrSTRIDEFileDispatchState
		}
		if err := service.Authority.ReauthorizeSource(ctx, requester, record.Source, record.SourceRevision, ACLReadContent); err != nil {
			return ErrSTRIDEFileHandleDenied
		}
		currentDestination, err := service.Authority.CurrentDestination(ctx, record.Destination.ThreadID)
		if err != nil || !sameSTRIDERichDestination(record.Destination, currentDestination) {
			return ErrSTRIDEFileHandleDenied
		}
		if err := service.Authority.AuthorizeDestination(ctx, requester, record.Destination, ACLWrite); err != nil {
			return ErrSTRIDEFileHandleDenied
		}
		record.Status = strideFileSelectionSending
		record.ExecutionKeyDigest = executionDigest
		command = STRIDEFilePostCommand{
			HandleID: record.ID, Requester: requester, Source: record.Source, SourceRevision: record.SourceRevision,
			Destination: record.Destination, Purpose: record.Purpose, ExecutionKey: executionKey,
		}
		return nil
	})
	if err != nil {
		return STRIDEFilePostReceipt{}, err
	}
	if replayConfirmed {
		return replay, nil
	}
	receipt, postErr := service.Poster.PostFileExactlyOnce(ctx, command)
	if postErr != nil {
		_ = service.Repo.Transact(ctx, handleID, func(record *STRIDEFileSelectionRecord) error {
			if record.Status == strideFileSelectionSending && record.ExecutionKeyDigest == executionDigest {
				record.Status = strideFileSelectionAmbiguous
				record.LastError = "poster result is ambiguous"
			}
			return nil
		})
		return STRIDEFilePostReceipt{}, ErrSTRIDEFileDispatchUnknown
	}
	if strings.TrimSpace(receipt.ProviderReceipt) == "" || strings.TrimSpace(receipt.MessageID) == "" {
		return STRIDEFilePostReceipt{}, ErrSTRIDEFileDispatchUnknown
	}
	if err := service.Repo.Transact(ctx, handleID, func(record *STRIDEFileSelectionRecord) error {
		if record.Status != strideFileSelectionSending || record.ExecutionKeyDigest != executionDigest {
			return ErrSTRIDEFileDispatchUnknown
		}
		record.Status = strideFileSelectionConfirmed
		record.ProviderReceipt = strings.TrimSpace(receipt.ProviderReceipt)
		record.ConfirmedMessageID = strings.TrimSpace(receipt.MessageID)
		return nil
	}); err != nil {
		return STRIDEFilePostReceipt{}, ErrSTRIDEFileDispatchUnknown
	}
	return receipt, nil
}

func (service STRIDEFileSelectionService) Revoke(ctx context.Context, handleID, requester string) error {
	if !service.Enabled || service.Repo == nil {
		return ErrSTRIDEFileHandleDenied
	}
	requester = normalizeAccountEmail(requester)
	return service.Repo.Transact(ctx, strings.TrimSpace(handleID), func(record *STRIDEFileSelectionRecord) error {
		if requester == "" || record.Requester != requester {
			return ErrSTRIDEFileHandleDenied
		}
		switch record.Status {
		case strideFileSelectionPending:
			record.Status = strideFileSelectionRevoked
			return nil
		case strideFileSelectionRevoked:
			return nil
		default:
			return ErrSTRIDEFileDispatchState
		}
	})
}

func (service STRIDEFileSelectionService) authorizeSourceDisplay(ctx context.Context, handleID, viewer string, action ACLAction) error {
	if !service.Enabled || service.Repo == nil || service.Authority == nil || (action != ACLReadMetadata && action != ACLReadContent) {
		return ErrSTRIDEFileHandleDenied
	}
	record, err := service.Repo.Read(ctx, strings.TrimSpace(handleID))
	if err != nil || record.Status == strideFileSelectionRevoked || record.Status == strideFileSelectionAmbiguous ||
		(record.Status != strideFileSelectionConfirmed && !record.ExpiresAt.After(service.now())) {
		return ErrSTRIDEFileHandleDenied
	}
	if err := service.Authority.ReauthorizeSource(ctx, normalizeAccountEmail(viewer), record.Source, record.SourceRevision, action); err != nil {
		return ErrSTRIDEFileHandleDenied
	}
	return nil
}

func (service STRIDEFileSelectionService) AuthorizeSourceChip(ctx context.Context, handleID, viewer string) error {
	return service.authorizeSourceDisplay(ctx, handleID, viewer, ACLReadMetadata)
}

func (service STRIDEFileSelectionService) AuthorizeSourceOpen(ctx context.Context, handleID, viewer string) error {
	return service.authorizeSourceDisplay(ctx, handleID, viewer, ACLReadContent)
}

// MemorySTRIDEFileSelectionRepository is a fake/local implementation used by
// tests and dry-run planning only. Production wiring requires a serializable
// database transaction with the same interface.
type MemorySTRIDEFileSelectionRepository struct {
	mu      sync.Mutex
	records map[string]STRIDEFileSelectionRecord
}

func NewMemorySTRIDEFileSelectionRepository() *MemorySTRIDEFileSelectionRepository {
	return &MemorySTRIDEFileSelectionRepository{records: map[string]STRIDEFileSelectionRecord{}}
}

func (repo *MemorySTRIDEFileSelectionRepository) Create(_ context.Context, record STRIDEFileSelectionRecord) error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if _, exists := repo.records[record.ID]; exists {
		return ErrSTRIDEFileHandleDenied
	}
	repo.records[record.ID] = record
	return nil
}

func (repo *MemorySTRIDEFileSelectionRepository) Read(_ context.Context, id string) (STRIDEFileSelectionRecord, error) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	record, ok := repo.records[strings.TrimSpace(id)]
	if !ok {
		return STRIDEFileSelectionRecord{}, ErrSTRIDEFileHandleDenied
	}
	return record, nil
}

func (repo *MemorySTRIDEFileSelectionRepository) Transact(_ context.Context, id string, mutate func(*STRIDEFileSelectionRecord) error) error {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	record, ok := repo.records[strings.TrimSpace(id)]
	if !ok || mutate == nil {
		return ErrSTRIDEFileHandleDenied
	}
	if err := mutate(&record); err != nil {
		return err
	}
	repo.records[record.ID] = record
	return nil
}

// STRIDELinkPreviewResolver is injected so validation tests and dry runs never
// perform DNS. Runtime integration must pin the returned addresses in the
// server-side dialer and re-run these checks on every redirect.
type STRIDELinkPreviewResolver interface {
	LookupIP(context.Context, string) ([]net.IP, error)
}

type STRIDELinkPreviewRequest struct {
	URL                  string
	ClientFetchRequested bool
	ProviderOriginURL    string
}

type STRIDELinkPreviewPlan struct {
	CanonicalURL    string
	Hostname        string
	ApprovedIPs     []string
	ServerFetchOnly bool
}

func ValidateSTRIDELinkPreviewRequest(ctx context.Context, request STRIDELinkPreviewRequest, resolver STRIDELinkPreviewResolver) (STRIDELinkPreviewPlan, error) {
	if request.ClientFetchRequested || strings.TrimSpace(request.ProviderOriginURL) != "" || resolver == nil {
		return STRIDELinkPreviewPlan{}, ErrSTRIDELinkPreviewDenied
	}
	parsed, err := normalizeLinkPreviewURL(strings.TrimSpace(request.URL))
	if err != nil {
		return STRIDELinkPreviewPlan{}, ErrSTRIDELinkPreviewDenied
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
		return STRIDELinkPreviewPlan{}, ErrSTRIDELinkPreviewDenied
	}
	addresses, err := resolver.LookupIP(ctx, host)
	if err != nil || len(addresses) == 0 {
		return STRIDELinkPreviewPlan{}, ErrSTRIDELinkPreviewDenied
	}
	approved := make([]string, 0, len(addresses))
	seen := map[string]struct{}{}
	for _, address := range addresses {
		if !strideLinkPreviewPublicIP(address) {
			return STRIDELinkPreviewPlan{}, ErrSTRIDELinkPreviewDenied
		}
		canonical := address.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		approved = append(approved, canonical)
	}
	sort.Strings(approved)
	parsed.Host = strings.ToLower(parsed.Host)
	return STRIDELinkPreviewPlan{CanonicalURL: parsed.String(), Hostname: host, ApprovedIPs: approved, ServerFetchOnly: true}, nil
}

func strideLinkPreviewPublicIP(ip net.IP) bool {
	if !linkPreviewPublicIP(ip) {
		return false
	}
	// Documentation, protocol-assignment, reserved, and future-use ranges are
	// never valid preview origins. Keeping them out also prevents test/staging
	// aliases from becoming a production SSRF path later.
	blocked := []string{
		"0.0.0.0/8", "192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"2001:db8::/32",
	}
	for _, value := range blocked {
		_, network, _ := net.ParseCIDR(value)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func ValidateSTRIDELinkPreviewProjection(imagePath string) error {
	value := strings.TrimSpace(imagePath)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != "/assistant/link-preview/image" {
		return ErrSTRIDELinkPreviewDenied
	}
	return nil
}

type STRIDEAgentGIFIntent struct {
	Reaction         string
	Tone             string
	ContextClass     string
	SensitiveContext bool
}

type STRIDEGIFCatalogRequest struct {
	Reaction string
	Tone     string
	Rating   string
	Limit    int
}

type STRIDEGIFCandidate struct {
	ProviderItemID string
	Title          string
	Alt            string
	Rating         string
	Mime           string
	Bytes          []byte
}

type STRIDEGIFCatalog interface {
	Search(context.Context, STRIDEGIFCatalogRequest) ([]STRIDEGIFCandidate, error)
}

type STRIDEGIFBlobStore interface {
	PutImmutableGIF(context.Context, []byte, string) (ref string, digest string, err error)
}

type STRIDEAgentGIFAction struct {
	BlobRef          string    `json:"blobRef"`
	ContentDigest    string    `json:"contentDigest"`
	Provider         string    `json:"provider"`
	ProviderItemID   string    `json:"providerItemId"`
	IntentDigest     string    `json:"intentDigest"`
	Rating           string    `json:"rating"`
	Alt              string    `json:"alt"`
	RetrievedAt      time.Time `json:"retrievedAt"`
	Immutable        bool      `json:"immutable"`
	DeleteCapability string    `json:"deleteCapability"`
	ReportCapability string    `json:"reportCapability"`
}

type STRIDEAgentGIFService struct {
	Enabled        bool
	ChannelEnabled func(string) bool
	Catalog        STRIDEGIFCatalog
	Blobs          STRIDEGIFBlobStore
	// Provider labels the selected catalog in the immutable action. Empty keeps
	// the existing production-facing default; deterministic preview callers
	// must identify themselves explicitly as local_fixture.
	Provider string
	Now      func() time.Time
	Random   func([]byte) error
}

func (service STRIDEAgentGIFService) Create(ctx context.Context, channelID string, intent STRIDEAgentGIFIntent) (STRIDEAgentGIFAction, error) {
	if !service.Enabled || service.ChannelEnabled == nil || !service.ChannelEnabled(strings.TrimSpace(channelID)) || service.Catalog == nil || service.Blobs == nil {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	intent.Reaction = strings.ToLower(strings.TrimSpace(intent.Reaction))
	intent.Tone = strings.ToLower(strings.TrimSpace(intent.Tone))
	intent.ContextClass = strings.ToLower(strings.TrimSpace(intent.ContextClass))
	if intent.SensitiveContext || sensitiveSTRIDEGIFContext(intent.ContextClass) || !validSTRIDEGIFReaction(intent.Reaction) || !validSTRIDEGIFTone(intent.Tone) {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	// The provider adapter receives abstract semantics only: no channel, user,
	// tenant, company, thread, message, or stable correlation identifier.
	request := STRIDEGIFCatalogRequest{Reaction: intent.Reaction, Tone: intent.Tone, Rating: "g", Limit: 1}
	candidates, err := service.Catalog.Search(ctx, request)
	if err != nil || len(candidates) != 1 {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	candidate := candidates[0]
	if strings.TrimSpace(candidate.ProviderItemID) == "" || strings.ToLower(strings.TrimSpace(candidate.Rating)) != "g" || strings.ToLower(strings.TrimSpace(candidate.Mime)) != "image/gif" || len(candidate.Bytes) == 0 {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	ref, digest, err := service.Blobs.PutImmutableGIF(ctx, candidate.Bytes, "image/gif")
	if err != nil || !validBlobRef(ref) || !isHexDigest(digest) {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	random := make([]byte, 64)
	if service.Random != nil {
		err = service.Random(random)
	} else {
		_, err = rand.Read(random)
	}
	if err != nil {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	intentCanonical := strings.Join([]string{"stride-gif-intent/v1", intent.Reaction, intent.Tone, intent.ContextClass}, "\x00")
	intentHash := sha256.Sum256([]byte(intentCanonical))
	now := time.Now().UTC()
	if service.Now != nil {
		now = service.Now().UTC()
	}
	alt := strings.TrimSpace(candidate.Alt)
	if alt == "" {
		alt = strings.TrimSpace(candidate.Title)
	}
	if alt == "" || len(alt) > 240 {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	provider := strings.TrimSpace(service.Provider)
	if provider == "" {
		provider = "giphy"
	}
	if !oneOf(provider, "giphy", "local_fixture") {
		return STRIDEAgentGIFAction{}, ErrSTRIDEGIFDenied
	}
	return STRIDEAgentGIFAction{
		BlobRef: ref, ContentDigest: digest, Provider: provider, ProviderItemID: trimForStorage(candidate.ProviderItemID, 120),
		IntentDigest: fmt.Sprintf("sha256:%x", intentHash[:]), Rating: "g", Alt: alt, RetrievedAt: now, Immutable: true,
		DeleteCapability: "stride-gif-delete-" + base64.RawURLEncoding.EncodeToString(random[:32]),
		ReportCapability: "stride-gif-report-" + base64.RawURLEncoding.EncodeToString(random[32:]),
	}, nil
}

func sensitiveSTRIDEGIFContext(value string) bool {
	for _, blocked := range []string{"health", "medical", "hr", "legal", "layoff", "termination", "harassment", "grief", "crisis", "financial", "discipline", "safety", "private"} {
		if strings.Contains(value, blocked) {
			return true
		}
	}
	return false
}

func validSTRIDEGIFReaction(value string) bool {
	switch value {
	case "celebrate", "agree", "surprised", "laugh", "encourage", "facepalm", "thank_you":
		return true
	default:
		return false
	}
}

func validSTRIDEGIFTone(value string) bool {
	switch value {
	case "warm", "playful", "dry", "supportive", "light":
		return true
	default:
		return false
	}
}
