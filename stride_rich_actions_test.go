package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSTRIDEFileAuthority struct {
	mu                  sync.Mutex
	source              ACLObjectRef
	revision            ACLRevisionRef
	destination         STRIDERichDestination
	readMetadataAllowed bool
	readContentAllowed  bool
	writeAllowed        bool
}

func (fake *fakeSTRIDEFileAuthority) ReauthorizeSource(_ context.Context, _ string, source ACLObjectRef, revision ACLRevisionRef, action ACLAction) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	allowed := action == ACLReadMetadata && fake.readMetadataAllowed || action == ACLReadContent && fake.readContentAllowed
	if !allowed || source != fake.source || revision != fake.revision {
		return ErrSTRIDEFileHandleDenied
	}
	return nil
}

func (fake *fakeSTRIDEFileAuthority) CurrentDestination(_ context.Context, _ string) (STRIDERichDestination, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.destination, nil
}

func (fake *fakeSTRIDEFileAuthority) AuthorizeDestination(_ context.Context, _ string, destination STRIDERichDestination, action ACLAction) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.writeAllowed || action != ACLWrite || !sameSTRIDERichDestination(destination, fake.destination) {
		return ErrSTRIDEFileHandleDenied
	}
	return nil
}

type fakeSTRIDEFilePoster struct {
	mu      sync.Mutex
	calls   int
	command STRIDEFilePostCommand
	err     error
	entered chan struct{}
	release chan struct{}
}

func (fake *fakeSTRIDEFilePoster) PostFileExactlyOnce(_ context.Context, command STRIDEFilePostCommand) (STRIDEFilePostReceipt, error) {
	fake.mu.Lock()
	fake.calls++
	fake.command = command
	entered, release, err := fake.entered, fake.release, fake.err
	fake.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return STRIDEFilePostReceipt{}, err
	}
	return STRIDEFilePostReceipt{ProviderReceipt: "fake-receipt", MessageID: "message-posted"}, nil
}

func strideRichTestFixture(t *testing.T) (STRIDEFileSelectionService, *fakeSTRIDEFileAuthority, *fakeSTRIDEFilePoster, *MemorySTRIDEFileSelectionRepository, *time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	source := ACLObjectRef{TenantID: "tenant", Type: "artifact", ID: "artifact-1", ACLVersion: 3}
	revision := ACLRevisionRef{ContentRevision: 7, ContentDigest: strings.Repeat("a", 64)}
	destination, err := normalizeSTRIDERichDestination(STRIDERichDestination{
		ThreadID: "thread-team", ACLVersion: 11,
		Audience: STRIDEAudience{Visibility: "channel", Principals: []string{"user-aj", "channel-team"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	authority := &fakeSTRIDEFileAuthority{
		source: source, revision: revision, destination: destination,
		readMetadataAllowed: true, readContentAllowed: true, writeAllowed: true,
	}
	poster := &fakeSTRIDEFilePoster{}
	repo := NewMemorySTRIDEFileSelectionRepository()
	randomCounter := byte(1)
	service := STRIDEFileSelectionService{
		Enabled: true, Repo: repo, Authority: authority, Poster: poster, Now: func() time.Time { return now },
		Random: func(buffer []byte) error {
			for index := range buffer {
				buffer[index] = randomCounter
			}
			randomCounter++
			return nil
		},
	}
	return service, authority, poster, repo, &now
}

func mintSTRIDEFileHandle(t *testing.T, service STRIDEFileSelectionService, authority *fakeSTRIDEFileAuthority) STRIDEFileSelectionToken {
	t.Helper()
	token, err := service.Mint(context.Background(), STRIDEFileSelectionMintRequest{
		Requester: "aj@example.com", Source: authority.source, SourceRevision: authority.revision,
		Destination: authority.destination, Purpose: "post_to_thread", TTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return token
}

func TestSTRIDEFileSelectionDefaultOffAndServerMintedBinding(t *testing.T) {
	service, authority, _, repo, _ := strideRichTestFixture(t)
	disabled := service
	disabled.Enabled = false
	if _, err := disabled.Mint(context.Background(), STRIDEFileSelectionMintRequest{}); !errors.Is(err, ErrSTRIDERichActionDisabled) {
		t.Fatalf("disabled mint error=%v", err)
	}
	token := mintSTRIDEFileHandle(t, service, authority)
	if !strings.HasPrefix(token.ID, "stride-file-") || token.ExpiresAt.IsZero() {
		t.Fatalf("token=%+v", token)
	}
	record, err := repo.Read(context.Background(), token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Requester != "aj@example.com" || record.SourceRevision != authority.revision || record.Destination.AudienceDigest == "" || record.NonceDigest == "" || record.BindingDigest == "" {
		t.Fatalf("record binding=%+v", record)
	}
	if _, err := service.Post(context.Background(), "stride-file-guessed", "aj@example.com", "exec-1"); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
		t.Fatalf("guessed handle error=%v", err)
	}
}

func TestSTRIDEFileSelectionReauthorizesAllBindingsAtUse(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeSTRIDEFileAuthority, *time.Time)
	}{
		{"source revoked", func(authority *fakeSTRIDEFileAuthority, _ *time.Time) { authority.readContentAllowed = false }},
		{"source revision changed", func(authority *fakeSTRIDEFileAuthority, _ *time.Time) { authority.revision.ContentRevision++ }},
		{"destination ACL changed", func(authority *fakeSTRIDEFileAuthority, _ *time.Time) { authority.destination.ACLVersion++ }},
		{"destination recipients changed", func(authority *fakeSTRIDEFileAuthority, _ *time.Time) {
			authority.destination.Audience.Principals = append(authority.destination.Audience.Principals, "user-tim")
		}},
		{"destination write revoked", func(authority *fakeSTRIDEFileAuthority, _ *time.Time) { authority.writeAllowed = false }},
		{"handle expired", func(_ *fakeSTRIDEFileAuthority, now *time.Time) { *now = now.Add(6 * time.Minute) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, authority, poster, _, now := strideRichTestFixture(t)
			token := mintSTRIDEFileHandle(t, service, authority)
			tc.mutate(authority, now)
			if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1"); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
				t.Fatalf("post error=%v, want denied", err)
			}
			if poster.calls != 0 {
				t.Fatalf("poster calls=%d, want zero", poster.calls)
			}
		})
	}
}

func TestSTRIDEFileSelectionHandleRevocationIsRequesterBound(t *testing.T) {
	service, authority, poster, _, _ := strideRichTestFixture(t)
	token := mintSTRIDEFileHandle(t, service, authority)
	if err := service.Revoke(context.Background(), token.ID, "tim@example.com"); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
		t.Fatalf("foreign revoke error=%v", err)
	}
	if err := service.Revoke(context.Background(), token.ID, "aj@example.com"); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if err := service.Revoke(context.Background(), token.ID, "aj@example.com"); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1"); !errors.Is(err, ErrSTRIDEFileDispatchState) {
		t.Fatalf("revoked post error=%v", err)
	}
	if poster.calls != 0 {
		t.Fatalf("poster calls=%d", poster.calls)
	}
}

func TestSTRIDEFileSelectionExactlyOnceAndFailClosedConcurrency(t *testing.T) {
	service, authority, poster, _, _ := strideRichTestFixture(t)
	token := mintSTRIDEFileHandle(t, service, authority)
	poster.entered = make(chan struct{})
	poster.release = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1")
		result <- err
	}()
	<-poster.entered
	if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1"); !errors.Is(err, ErrSTRIDEFileDispatchUnknown) {
		t.Fatalf("concurrent retry error=%v, want ambiguous and no resend", err)
	}
	close(poster.release)
	if err := <-result; err != nil {
		t.Fatalf("winner post: %v", err)
	}
	replayed, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1")
	if err != nil || replayed.ProviderReceipt != "fake-receipt" {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "different-key"); !errors.Is(err, ErrSTRIDEFileDispatchState) {
		t.Fatalf("different execution key error=%v", err)
	}
	poster.mu.Lock()
	defer poster.mu.Unlock()
	if poster.calls != 1 || poster.command.Source != authority.source || poster.command.SourceRevision != authority.revision || poster.command.ExecutionKey != "exec-1" {
		t.Fatalf("poster calls/command=%d %+v", poster.calls, poster.command)
	}
}

func TestSTRIDEFileSelectionPosterFailureBecomesAmbiguousAndNeverRetries(t *testing.T) {
	service, authority, poster, _, _ := strideRichTestFixture(t)
	token := mintSTRIDEFileHandle(t, service, authority)
	poster.err = errors.New("unknown provider result")
	if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1"); !errors.Is(err, ErrSTRIDEFileDispatchUnknown) {
		t.Fatalf("first error=%v", err)
	}
	if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-1"); !errors.Is(err, ErrSTRIDEFileDispatchUnknown) {
		t.Fatalf("retry error=%v", err)
	}
	if poster.calls != 1 {
		t.Fatalf("poster calls=%d, want one", poster.calls)
	}
}

func TestSTRIDEFileSourceChipAndOpenAreSeparatelyAuthorized(t *testing.T) {
	service, authority, _, _, now := strideRichTestFixture(t)
	token := mintSTRIDEFileHandle(t, service, authority)
	authority.readContentAllowed = false
	if err := service.AuthorizeSourceChip(context.Background(), token.ID, "tim@example.com"); err != nil {
		t.Fatalf("metadata chip authorization: %v", err)
	}
	if err := service.AuthorizeSourceOpen(context.Background(), token.ID, "tim@example.com"); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
		t.Fatalf("content open error=%v, want denied", err)
	}
	authority.readMetadataAllowed = false
	if err := service.AuthorizeSourceChip(context.Background(), token.ID, "tim@example.com"); !errors.Is(err, ErrSTRIDEFileHandleDenied) {
		t.Fatalf("revoked chip error=%v", err)
	}
	// A confirmed message's provenance outlives the short-lived posting handle;
	// opening remains a fresh source-ACL decision, not bearer-token access.
	authority.readMetadataAllowed = true
	authority.readContentAllowed = true
	if _, err := service.Post(context.Background(), token.ID, "aj@example.com", "exec-chip"); err != nil {
		t.Fatalf("confirm post: %v", err)
	}
	*now = now.Add(10 * time.Minute)
	if err := service.AuthorizeSourceOpen(context.Background(), token.ID, "tim@example.com"); err != nil {
		t.Fatalf("confirmed source open after handle expiry: %v", err)
	}
}

type fakeSTRIDELinkResolver struct {
	addresses []net.IP
	err       error
}

func (fake fakeSTRIDELinkResolver) LookupIP(context.Context, string) ([]net.IP, error) {
	return fake.addresses, fake.err
}

func TestSTRIDELinkPreviewValidationIsServerOnlyAndSSRFSafe(t *testing.T) {
	public := fakeSTRIDELinkResolver{addresses: []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")}}
	plan, err := ValidateSTRIDELinkPreviewRequest(context.Background(), STRIDELinkPreviewRequest{URL: "HTTPS://Example.COM/path#fragment"}, public)
	if err != nil || !plan.ServerFetchOnly || plan.Hostname != "example.com" || strings.Contains(plan.CanonicalURL, "#") || len(plan.ApprovedIPs) != 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	cases := []struct {
		name     string
		request  STRIDELinkPreviewRequest
		resolver STRIDELinkPreviewResolver
	}{
		{"client fetch requested", STRIDELinkPreviewRequest{URL: "https://example.com", ClientFetchRequested: true}, public},
		{"provider origin supplied", STRIDELinkPreviewRequest{URL: "https://example.com", ProviderOriginURL: "https://cdn.example.com/image"}, public},
		{"credentials", STRIDELinkPreviewRequest{URL: "https://user:pass@example.com"}, public},
		{"nonstandard port", STRIDELinkPreviewRequest{URL: "https://example.com:8443"}, public},
		{"file scheme", STRIDELinkPreviewRequest{URL: "file:///etc/passwd"}, public},
		{"localhost name", STRIDELinkPreviewRequest{URL: "http://service.local"}, public},
		{"loopback literal", STRIDELinkPreviewRequest{URL: "http://127.0.0.1"}, public},
		{"mixed DNS answers", STRIDELinkPreviewRequest{URL: "https://example.com"}, fakeSTRIDELinkResolver{addresses: []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1")}}},
		{"documentation address", STRIDELinkPreviewRequest{URL: "https://example.com"}, fakeSTRIDELinkResolver{addresses: []net.IP{net.ParseIP("192.0.2.1")}}},
		{"resolver unavailable", STRIDELinkPreviewRequest{URL: "https://example.com"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateSTRIDELinkPreviewRequest(context.Background(), tc.request, tc.resolver); !errors.Is(err, ErrSTRIDELinkPreviewDenied) {
				t.Fatalf("error=%v, want denied", err)
			}
		})
	}
	if err := ValidateSTRIDELinkPreviewProjection("/assistant/link-preview/image?asset=opaque"); err != nil {
		t.Fatalf("server proxy projection: %v", err)
	}
	for _, value := range []string{"https://provider.example/image.gif", "//provider.example/image.gif", "/other/image", "/assistant/link-preview/image-evil"} {
		if err := ValidateSTRIDELinkPreviewProjection(value); !errors.Is(err, ErrSTRIDELinkPreviewDenied) {
			t.Fatalf("projection %q error=%v", value, err)
		}
	}
}

type fakeSTRIDEGIFCatalog struct {
	request    STRIDEGIFCatalogRequest
	candidates []STRIDEGIFCandidate
	err        error
}

func (fake *fakeSTRIDEGIFCatalog) Search(_ context.Context, request STRIDEGIFCatalogRequest) ([]STRIDEGIFCandidate, error) {
	fake.request = request
	return fake.candidates, fake.err
}

type fakeSTRIDEGIFBlobStore struct{ calls int }

func (fake *fakeSTRIDEGIFBlobStore) PutImmutableGIF(_ context.Context, data []byte, mime string) (string, string, error) {
	fake.calls++
	if mime != "image/gif" || len(data) == 0 {
		return "", "", errors.New("bad gif")
	}
	return strings.Repeat("d", 64), strings.Repeat("b", 64), nil
}

func strideGIFServiceFixture() (STRIDEAgentGIFService, *fakeSTRIDEGIFCatalog, *fakeSTRIDEGIFBlobStore) {
	catalog := &fakeSTRIDEGIFCatalog{candidates: []STRIDEGIFCandidate{{ProviderItemID: "gif-1", Title: "celebration", Alt: "A joyful celebration", Rating: "g", Mime: "image/gif", Bytes: []byte("GIF89a")}}}
	blobs := &fakeSTRIDEGIFBlobStore{}
	service := STRIDEAgentGIFService{
		Enabled: true, ChannelEnabled: func(channel string) bool { return channel == "channel-team" }, Catalog: catalog, Blobs: blobs,
		Now: func() time.Time { return time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC) },
		Random: func(buffer []byte) error {
			for index := range buffer {
				buffer[index] = byte(index + 1)
			}
			return nil
		},
	}
	return service, catalog, blobs
}

func TestSTRIDEAgentGIFActionUsesAbstractIntentAndImmutableProjection(t *testing.T) {
	service, catalog, blobs := strideGIFServiceFixture()
	action, err := service.Create(context.Background(), "channel-team", STRIDEAgentGIFIntent{Reaction: "celebrate", Tone: "playful", ContextClass: "project_win"})
	if err != nil {
		t.Fatalf("create GIF action: %v", err)
	}
	if catalog.request != (STRIDEGIFCatalogRequest{Reaction: "celebrate", Tone: "playful", Rating: "g", Limit: 1}) {
		t.Fatalf("provider request=%+v, want abstract one-result G-rated intent", catalog.request)
	}
	if blobs.calls != 1 || !action.Immutable || action.BlobRef == "" || action.ContentDigest != strings.Repeat("b", 64) || action.ProviderItemID != "gif-1" || action.Alt == "" || action.DeleteCapability == "" || action.ReportCapability == "" {
		t.Fatalf("action=%+v blob calls=%d", action, blobs.calls)
	}
	if strings.Contains(action.IntentDigest, "channel-team") {
		t.Fatal("stable channel identity leaked into provider intent digest")
	}
}

func TestSTRIDEAgentGIFPolicyDeniesUnsafeOrDisabledActions(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*STRIDEAgentGIFService, *fakeSTRIDEGIFCatalog)
		intent  STRIDEAgentGIFIntent
		channel string
	}{
		{"default off", func(service *STRIDEAgentGIFService, _ *fakeSTRIDEGIFCatalog) { service.Enabled = false }, STRIDEAgentGIFIntent{Reaction: "laugh", Tone: "light"}, "channel-team"},
		{"channel kill switch", func(_ *STRIDEAgentGIFService, _ *fakeSTRIDEGIFCatalog) {}, STRIDEAgentGIFIntent{Reaction: "laugh", Tone: "light"}, "channel-private"},
		{"sensitive flag", func(_ *STRIDEAgentGIFService, _ *fakeSTRIDEGIFCatalog) {}, STRIDEAgentGIFIntent{Reaction: "laugh", Tone: "light", SensitiveContext: true}, "channel-team"},
		{"sensitive HR context", func(_ *STRIDEAgentGIFService, _ *fakeSTRIDEGIFCatalog) {}, STRIDEAgentGIFIntent{Reaction: "laugh", Tone: "light", ContextClass: "hr_termination"}, "channel-team"},
		{"free form reaction", func(_ *STRIDEAgentGIFService, _ *fakeSTRIDEGIFCatalog) {}, STRIDEAgentGIFIntent{Reaction: "make fun of erick", Tone: "light"}, "channel-team"},
		{"non-g result", func(_ *STRIDEAgentGIFService, catalog *fakeSTRIDEGIFCatalog) { catalog.candidates[0].Rating = "pg-13" }, STRIDEAgentGIFIntent{Reaction: "laugh", Tone: "light"}, "channel-team"},
		{"multiple results", func(_ *STRIDEAgentGIFService, catalog *fakeSTRIDEGIFCatalog) {
			catalog.candidates = append(catalog.candidates, catalog.candidates[0])
		}, STRIDEAgentGIFIntent{Reaction: "laugh", Tone: "light"}, "channel-team"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, catalog, blobs := strideGIFServiceFixture()
			tc.mutate(&service, catalog)
			if _, err := service.Create(context.Background(), tc.channel, tc.intent); !errors.Is(err, ErrSTRIDEGIFDenied) {
				t.Fatalf("error=%v, want denied", err)
			}
			if blobs.calls != 0 {
				t.Fatalf("blob calls=%d, want zero", blobs.calls)
			}
		})
	}
}
