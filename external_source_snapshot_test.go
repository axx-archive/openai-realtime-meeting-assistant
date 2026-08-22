package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExternalEvidencePublicIPRejectsSSRFAddresses(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.4", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1",
		"192.31.196.1", "192.52.193.1", "192.88.99.1", "192.175.48.1",
		"2001:2::1", "2001:30::1", "3fff::1", "2620:4f:8000::1",
	} {
		if externalEvidencePublicIP(net.ParseIP(raw)) {
			t.Fatalf("non-public address %s passed", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !externalEvidencePublicIP(net.ParseIP(raw)) {
			t.Fatalf("public address %s was rejected", raw)
		}
	}
	for _, raw := range []string{
		"https://127.0.0.1/private", "https://10.0.0.1/private", "https://169.254.169.254/latest/meta-data", "https://example.com:8443/source", "http://example.com/source",
	} {
		if _, err := externalEvidenceSafeHTTPSURL(context.Background(), raw); err == nil {
			t.Fatalf("unsafe source URL %q passed", raw)
		}
	}
}

func TestExternalEvidenceCanonicalizesFragmentsButBindsExactProviderURL(t *testing.T) {
	lookup := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	const providerURL = "https://source.example/report#creator-count"
	canonical, err := externalEvidenceSafeHTTPSURLWithLookup(context.Background(), providerURL, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.String() != "https://source.example/report" || canonical.Fragment != "" {
		t.Fatalf("canonical fetch URL=%q fragment=%q", canonical.String(), canonical.Fragment)
	}
	base := externalEvidenceEnvelopeRow{ResearchQuestion: "How many?", SourceFact: "There are 4,200 creators.", SourceTitle: "Report", URL: providerURL, PublishedOrUpdated: "Accessed 2026-08-21", Units: "creators", Confidence: "High", DeckImplication: "Use it."}
	changed := base
	changed.URL = "https://source.example/report#other-section"
	if externalEvidenceCandidateID(base) == externalEvidenceCandidateID(changed) {
		t.Fatal("fragment-bearing exact provider URLs must remain distinct in the candidate receipt")
	}
}

func TestExternalEvidenceResolverRejectsMixedAnswersAndDialTimeRebinding(t *testing.T) {
	mixed := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.8")}}, nil
	}
	if _, err := externalEvidenceResolvePublicIPsWithLookup(context.Background(), "source.example", mixed); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("mixed public/private DNS answers passed: %v", err)
	}
	var lookups, dials atomic.Int32
	rebinding := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	_, err := fetchExternalEvidenceSourceWithNetwork(context.Background(), "https://source.example/report", externalEvidenceFetchNetwork{
		LookupIPAddr: rebinding,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("must not dial")
		},
		Now: time.Now,
	})
	if err == nil || !strings.Contains(err.Error(), "non-public") || dials.Load() != 0 || lookups.Load() < 2 {
		t.Fatalf("dial-time DNS rebinding was not stopped before dial: err=%v lookups=%d dials=%d", err, lookups.Load(), dials.Load())
	}
}

func TestExternalEvidenceRedirectPolicyRejectsUnsafeTargetsBeforeRequest(t *testing.T) {
	origin, _ := url.Parse("https://source.example/report")
	var lookups atomic.Int32
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookups.Add(1)
		if host == "www.source.example" {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.9")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	policy := externalEvidenceRedirectPolicy(origin, lookup)
	check := func(raw string) (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			return nil, err
		}
		return req, policy(req, []*http.Request{{URL: origin}})
	}
	for _, raw := range []string{
		"http://source.example/insecure",
		"https://other.example/cross-site",
		"https://cdn.source.example/sibling",
		"https://www.source.example/private",
	} {
		if _, err := check(raw); err == nil {
			t.Fatalf("unsafe redirect %q passed", raw)
		}
	}
	before := lookups.Load()
	req, err := check("https://source.example/final#section")
	if err != nil || req.URL.String() != "https://source.example/final" {
		t.Fatalf("safe exact-host redirect failed canonicalization: url=%v err=%v", req.URL, err)
	}
	if lookups.Load() <= before {
		t.Fatal("redirect target was not independently resolved before acceptance")
	}
}

func newExternalEvidenceTLSServer(t *testing.T, handler http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "source.example"},
		DNSNames:              []string{"source.example"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tlsCertificate(der, key)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(parsed)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, roots
}

func tlsCertificate(der []byte, key *rsa.PrivateKey) (tls.Certificate, error) {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func TestExternalEvidenceFetchPreservesTLSHostnameVersionAndCanonicalChain(t *testing.T) {
	var serverName string
	var tlsVersion uint16
	server, roots := newExternalEvidenceTLSServer(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		serverName = request.TLS.ServerName
		tlsVersion = request.TLS.Version
		if request.URL.Path == "/report" {
			http.Redirect(response, request, "/final#proof", http.StatusFound)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte("The verified program has 4,200 opted-in creators."))
	}))
	publicLookup := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	var dialAddress string
	document, err := fetchExternalEvidenceSourceWithNetwork(context.Background(), "https://source.example/report#participants", externalEvidenceFetchNetwork{
		LookupIPAddr: publicLookup,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialAddress = address
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSRootCAs: roots,
		Now:        func() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if serverName != "source.example" || tlsVersion != tls.VersionTLS12 || dialAddress != "93.184.216.34:443" {
		t.Fatalf("literal dial plus TLS identity/version not preserved: dial=%q serverName=%q version=%x", dialAddress, serverName, tlsVersion)
	}
	if document.RequestedURL != "https://source.example/report" || document.FinalURL != "https://source.example/final" || len(document.RedirectChain) != 2 || document.RedirectChain[0] != document.RequestedURL || document.RedirectChain[1] != document.FinalURL {
		t.Fatalf("canonical request/final redirect chain not bound: %+v", document)
	}
}

func TestExternalEvidencePDFsFailClosedWithExplicitIngestionReroute(t *testing.T) {
	server, roots := newExternalEvidenceTLSServer(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/declared" {
			response.Header().Set("Content-Type", "application/pdf")
		} else {
			response.Header().Set("Content-Type", "text/plain")
		}
		_, _ = response.Write([]byte("%PDF-1.7\nnot directly admissible"))
	}))
	network := externalEvidenceFetchNetwork{
		LookupIPAddr: func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSRootCAs: roots,
		Now:        time.Now,
	}
	for _, path := range []string{"/declared", "/sniffed"} {
		_, err := fetchExternalEvidenceSourceWithNetwork(context.Background(), "https://source.example"+path, network)
		if !errors.Is(err, errExternalSourcePDFRequiresExtraction) || !strings.Contains(err.Error(), "reroute="+externalSourcePDFRerouteContract) {
			t.Fatalf("PDF %s did not fail closed with the explicit reroute contract: %v", path, err)
		}
	}
}

func TestExternalEvidenceHTMLBlocksExcludeActiveContentAndKeepAnchors(t *testing.T) {
	blocks, err := externalEvidenceHTMLBlocks([]byte(`<!doctype html><html><body><h2>2026 creator report</h2><script>The program reports 99,999 fake creators.</script><p>The official program reports 4,200 opted-in creators in 2026.</p><nav><p>Ignore navigation.</p></nav></body></html>`))
	if err != nil || len(blocks) != 1 {
		t.Fatalf("blocks=%+v err=%v", blocks, err)
	}
	if blocks[0].Anchor != "2026 creator report" || !strings.Contains(blocks[0].Text, "4,200") || strings.Contains(blocks[0].Text, "99,999") || strings.Contains(blocks[0].Text, "navigation") {
		t.Fatalf("unsafe or unanchored HTML extraction: %+v", blocks[0])
	}
}

func TestExternalEvidenceHTMLBlocksNeverExposeHiddenText(t *testing.T) {
	body := []byte(`<!doctype html><html><body>
		<h2>Visible creator report</h2>
		<h3 aria-hidden="true">Hidden creator report 91,007</h3>
		<p hidden>The program reports 91,001 hidden creators.</p>
		<p aria-hidden=" TRUE ">The program reports 91,002 hidden creators.</p>
		<section style="DISPLAY: /**/ none !important"><p>The program reports 91,003 hidden creators.</p></section>
		<p style="visibility : hidden!important">The program reports 91,004 hidden creators.</p>
		<p>The official program reports <span hidden>91,005 hidden, not </span>4,200 visible creators in 2026.</p>
		<table><tr><th>Year</th><th>Creators</th></tr><tr style="display:none"><td>2026</td><td>91,006</td></tr></table>
	</body></html>`)
	blocks, err := externalEvidenceHTMLBlocks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Anchor != "Visible creator report" || blocks[0].Text != "The official program reports 4,200 visible creators in 2026." {
		t.Fatalf("hidden DOM text escaped or visible text was lost: %+v", blocks)
	}
	document := externalSourceDocument{Blocks: blocks}
	for _, hiddenCount := range []string{"91,001", "91,002", "91,003", "91,004", "91,005", "91,006"} {
		claim := "The program reports " + hiddenCount + " hidden creators."
		if windows := externalEvidenceRelevantWindows(claim, document); len(windows) != 0 {
			t.Fatalf("hidden claim %q became a source window: %+v", claim, windows)
		}
	}
	visibleClaim := "The official program reports 4,200 visible creators in 2026."
	if windows := externalEvidenceRelevantWindows(visibleClaim, document); len(windows) != 1 {
		t.Fatalf("visible claim did not remain admissible: %+v", windows)
	}
}

func TestExternalEvidenceHTMLBlocksApplyStylesheetVisibility(t *testing.T) {
	body := []byte(`<!doctype html><html><head><style>
		.secret, #retired { display: none !important; }
		@media screen { .responsive-secret { visibility: hidden; } }
	</style></head><body><h2>Visible creator report</h2>
		<p class="secret">Acme has 9,999 creators in 2026.</p>
		<p id="retired">Acme has 8,888 creators in 2026.</p>
		<section class="responsive-secret"><p>Acme has 7,777 creators in 2026.</p></section>
		<p>Applications are open for verified participants.</p>
	</body></html>`)
	blocks, err := externalEvidenceHTMLBlocks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Text != "Applications are open for verified participants." {
		t.Fatalf("stylesheet-hidden source text escaped extraction: %+v", blocks)
	}
	for _, block := range blocks {
		if strings.Contains(block.Text, "9,999") || strings.Contains(block.Text, "8,888") || strings.Contains(block.Text, "7,777") {
			t.Fatalf("stylesheet-hidden source text became evidence: %+v", blocks)
		}
	}
}

func TestExternalEvidenceHTMLBlocksRejectVisibilityEvasions(t *testing.T) {
	cases := map[string]string{
		"external stylesheet": `<html><head><link rel="stylesheet" href="/site.css"></head><body><p>Acme has 9,999 creators in 2026.</p></body></html>`,
		"inline opacity":      `<p style="opacity:0">Acme has 9,999 creators in 2026.</p>`,
		"stylesheet opacity":  `<style>.secret{opacity:0}</style><p class="secret">Acme has 9,999 creators in 2026.</p>`,
		"inline clip":         `<p style="clip-path:inset(50%)">Acme has 9,999 creators in 2026.</p>`,
		"content visibility":  `<p style="content-visibility:hidden">Acme has 9,999 creators in 2026.</p>`,
		"zero font":           `<p style="font-size:0">Acme has 9,999 creators in 2026.</p>`,
		"transparent text":    `<p style="color:transparent">Acme has 9,999 creators in 2026.</p>`,
		"offscreen indent":    `<p style="text-indent:-9999px">Acme has 9,999 creators in 2026.</p>`,
		"zero transform":      `<p style="transform:scale(0)">Acme has 9,999 creators in 2026.</p>`,
		"closed dialog":       `<dialog><p>Acme has 9,999 creators in 2026.</p></dialog>`,
		"closed popover":      `<div popover><p>Acme has 9,999 creators in 2026.</p></div>`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			blocks, err := externalEvidenceHTMLBlocks([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			for _, block := range blocks {
				if strings.Contains(block.Text, "9,999") {
					t.Fatalf("non-visible source text became evidence: %+v", blocks)
				}
			}
		})
	}
}

func TestExternalEvidenceHTMLBlocksFailClosedOnUnresolvedHidingSelector(t *testing.T) {
	blocks, err := externalEvidenceHTMLBlocks([]byte(`<style>[data-private]{display:none}</style><p>Visible-looking claim has 9,999 creators.</p>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("unresolved stylesheet hiding selector did not fail closed: %+v", blocks)
	}
}

func TestExternalEvidenceDeclaredPlainHTMLUsesSafeHTMLExtraction(t *testing.T) {
	server, roots := newExternalEvidenceTLSServer(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte(`Plain preface that must not force plain extraction.
			<script>The program reports 99,999 fabricated creators.</script>
			<p aria-hidden="true">The program reports 88,888 hidden creators.</p>
			<p>The audited program reports 4,200 creators in 2026.</p>`))
	}))
	document, err := fetchExternalEvidenceSourceWithNetwork(context.Background(), "https://source.example/report", externalEvidenceFetchNetwork{
		LookupIPAddr: func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSRootCAs: roots,
		Now:        func() time.Time { return time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if document.ContentType != "text/html" {
		t.Fatalf("mislabeled HTML used unsafe content type %q", document.ContentType)
	}
	for _, claim := range []string{
		"The program reports 99,999 fabricated creators.",
		"The program reports 88,888 hidden creators.",
	} {
		if windows := externalEvidenceRelevantWindows(claim, document); len(windows) != 0 {
			t.Fatalf("non-visible claim %q became evidence: %+v", claim, windows)
		}
	}
	visibleClaim := "The audited program reports 4,200 creators in 2026."
	if windows := externalEvidenceRelevantWindows(visibleClaim, document); len(windows) != 1 {
		t.Fatalf("visible mislabeled HTML claim was not safely extracted: %+v", windows)
	}
}

func TestExternalEvidenceHTMLTablesPreserveHeaderValueContext(t *testing.T) {
	blocks, err := externalEvidenceHTMLBlocks([]byte(`<!doctype html><html><body><h2>Creator program</h2><table><caption>Verified participants</caption><thead><tr><th>Year</th><th>Opted-in creators</th><th>Market</th></tr></thead><tbody><tr><td>2026</td><td>4,200</td><td>United States</td></tr><tr><th>Target</th><td>7,500</td><td>North America</td></tr></tbody></table></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("table blocks=%+v", blocks)
	}
	if blocks[0].Anchor != "Creator program — Verified participants — table row 2" || blocks[0].Text != "Year: 2026 | Opted-in creators: 4,200 | Market: United States" {
		t.Fatalf("first table row lost header/value context: %+v", blocks[0])
	}
	if !strings.Contains(blocks[1].Text, "Year: Target") || !strings.Contains(blocks[1].Text, "Opted-in creators: 7,500") {
		t.Fatalf("mixed row header lost column context: %+v", blocks[1])
	}
}

func TestExternalEvidenceCompleteContextsFailClosedInsteadOfClippingQualifier(t *testing.T) {
	longTarget := "The report says " + strings.Repeat("creator participation remained measurable and consistent across every surveyed regional cohort ", 7) + "at 4,200 creators."
	shortRefutation := "That claim is false."
	if got := externalEvidenceCompleteContexts(longTarget + " " + shortRefutation); len(got) != 0 {
		t.Fatalf("long target with clipped refutation produced contexts: %#v", got)
	}
	shortTarget := "The report says participation reached 4,200 creators."
	oversizedQualifier := "However, " + strings.Repeat("the methodology did not establish that population and explicitly disclaimed the estimate ", 12) + "."
	if got := externalEvidenceCompleteContexts(shortTarget + " " + oversizedQualifier); len(got) != 0 {
		t.Fatalf("short target with oversized qualifier produced contexts: %#v", got)
	}
	normal := "The survey covered opted-in creators. The verified total was 4,200 creators in 2026. The appendix describes the counting method."
	if got := externalEvidenceCompleteContexts(normal); len(got) == 0 || !strings.Contains(got[0], "4,200") {
		t.Fatalf("normal complete paragraph was lost: %#v", got)
	}
}

func sourceWindowForSemanticTest(anchor, assertion, context string) externalSourceWindow {
	window := externalSourceWindow{Anchor: anchor, Assertion: assertion, Text: context}
	window.Digest = externalEvidenceSourceWindowDigest(anchor, assertion, context)
	return window
}

func TestExternalEvidenceAdmissionRequiresOneExactUnattributedAssertion(t *testing.T) {
	const claim = "Acme paid 4,200 creators in 2026."
	tests := []struct {
		name      string
		assertion string
		context   string
	}{
		{name: "inner negative clause", assertion: "The claim that Acme paid 4,200 creators in 2026 is false.", context: "The claim that Acme paid 4,200 creators in 2026 is false."},
		{name: "attributed assertion", assertion: "Acme alleged that it paid 4,200 creators in 2026.", context: "Acme alleged that it paid 4,200 creators in 2026."},
		{name: "next sentence refutes", assertion: claim, context: claim + " The regulator later said that figure is false."},
		{name: "reversed relationship", assertion: "In 2026, 4,200 creators paid Acme.", context: "In 2026, 4,200 creators paid Acme."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if externalEvidenceWindowEntailsCandidate(claim, sourceWindowForSemanticTest("Audit", test.assertion, test.context)) {
				t.Fatalf("unsafe assertion was admitted: assertion=%q context=%q", test.assertion, test.context)
			}
		})
	}
	if !externalEvidenceWindowEntailsCandidate(claim, sourceWindowForSemanticTest("Audit", claim, claim+" The audited payment ledger is attached.")) {
		t.Fatal("one exact, direct assertion with non-refuting adjacent context was rejected")
	}
}

func TestExternalEvidenceSentenceExtractionPreservesAbbreviatedScope(t *testing.T) {
	text := "Dr. Avery audited Acme Inc. The U.S. market reached 4,200 creators in 2026. The appendix describes the sample."
	items := externalEvidenceAssertionContexts(text)
	if len(items) < 2 || items[0].Assertion != "Dr. Avery audited Acme Inc. The U.S. market reached 4,200 creators in 2026." {
		t.Fatalf("abbreviations clipped or broadened the complete assertion: %#v", items)
	}
	document := externalSourceDocument{Blocks: []externalSourceTextBlock{{Anchor: "Market", Text: text}}}
	if got := externalEvidenceRelevantWindows("market reached 4,200 creators in 2026.", document); len(got) != 0 {
		t.Fatalf("suffix stripped of U.S. scope became an assertion: %#v", got)
	}
}

func TestExternalEvidenceOrdinaryLongSentenceAndCanonicalTableAssertions(t *testing.T) {
	claim := "Across the audited western region, the opt-in program enrolled 4,200 active creators who completed identity checks, accepted campaign terms, selected at least one eligible experience, and remained available for assignments during the measured thirty-day period across all participating counties in the study."
	paragraph := "The methodology defines active participation. " + claim + " The appendix lists the exclusions."
	windows := externalEvidenceRelevantWindows(claim, externalSourceDocument{Blocks: []externalSourceTextBlock{{Anchor: "Methodology", Text: paragraph}}})
	if len(strings.Fields(claim)) < 40 || len(strings.Fields(claim)) > 80 || len(windows) != 1 || windows[0].Assertion != claim || !externalEvidenceWindowEntailsCandidate(claim, windows[0]) {
		t.Fatalf("ordinary 40-80 word assertion did not survive exact extraction: words=%d windows=%+v", len(strings.Fields(claim)), windows)
	}
	tableAssertion := "Year: 2026 | Opted-in creators: 4,200 | Market: United States"
	tableWindows := externalEvidenceRelevantWindows(tableAssertion, externalSourceDocument{Blocks: []externalSourceTextBlock{{Anchor: "Verified participants", Text: tableAssertion, Kind: externalSourceTextBlockTableAssertion}}})
	if len(tableWindows) != 1 || tableWindows[0].Assertion != tableAssertion || !externalEvidenceWindowEntailsCandidate(tableAssertion, tableWindows[0]) {
		t.Fatalf("canonical table assertion did not survive exact extraction: %+v", tableWindows)
	}
}

func TestExternalEvidenceComplexTableGeometryFailsClosed(t *testing.T) {
	for _, body := range []string{
		`<table><tr><th colspan="2">Market</th><th>2026</th></tr><tr><th>US</th><th>EU</th><th>Total</th></tr><tr><td>$12m</td><td>$7m</td><td>$19m</td></tr></table>`,
		`<table><tr><th>Region</th><th>Revenue</th></tr><tr><th>2026</th><th>USD</th></tr><tr><td>US</td><td>$12m</td></tr></table>`,
	} {
		blocks, err := externalEvidenceHTMLBlocks([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if len(blocks) != 0 {
			t.Fatalf("complex table was flattened into unsafe assertions: %+v", blocks)
		}
	}
}

func TestExternalEvidenceDateAndUnitAdmissionIsExplicit(t *testing.T) {
	claim := "The 2026-08-20 event enrolled 4,200 creators."
	window := sourceWindowForSemanticTest("Event results", claim, claim)
	snapshot := externalSourceSnapshot{FetchedAt: "2026-08-21T12:00:00Z"}
	if !externalEvidencePublishedDateEntailed("Accessed 2026-08-21", snapshot, window) {
		t.Fatal("exact access-date label was rejected")
	}
	for _, value := range []string{"2026-08-21", "unknown", "Accessed 2026-08-20", "Published 2026-08-20", "Published 2026-08-21"} {
		if externalEvidencePublishedDateEntailed(value, snapshot, window) {
			t.Fatalf("arbitrary, wrong, or event-as-publication date was admitted: %q", value)
		}
	}
	publishedContext := "Published 2026-08-20. " + claim
	publishedWindow := sourceWindowForSemanticTest("Event results", claim, publishedContext)
	if !externalEvidencePublishedDateEntailed("Published 2026-08-20", snapshot, publishedWindow) {
		t.Fatal("explicit source publication label and exact date were rejected")
	}
	if externalEvidenceUnitsEntailed("N/A", claim, claim) {
		t.Fatal("N/A units admitted a measure-bearing claim")
	}
	bareDollar := "The program generated $12 million in purchases."
	if externalEvidenceUnitsEntailed("USD", bareDollar, bareDollar) || externalEvidenceUnitsEntailed("AUD", bareDollar, bareDollar) {
		t.Fatal("bare dollar sign was upgraded to a national currency")
	}
	if !externalEvidenceUnitsEntailed("currency unspecified", bareDollar, bareDollar) || !externalEvidenceUnitsEntailed("USD", "The program generated USD 12 million in purchases.", "The program generated USD 12 million in purchases.") {
		t.Fatal("explicit unspecified or named currency units were rejected")
	}
}

func TestCompileExternalEvidenceSourceSnapshotsBindsServerFetchedWindows(t *testing.T) {
	app := newIsolatedKanbanBoardApp(t)
	const supportedURL = "https://example.org/creator-program"
	const unsupportedURL = "https://example.org/commerce"
	const researchQuestion = "How many opted-in creators does the official program report in 2026?"
	evidence := externalEvidenceEnvelope{
		ResearchQuestions: []string{researchQuestion},
		Evidence: []externalEvidenceEnvelopeRow{
			{ResearchQuestion: researchQuestion, SourceFact: "The official program reports 4,200 opted-in creators in 2026.", SourceTitle: "Creator report", URL: supportedURL, PublishedOrUpdated: "Accessed 2026-08-21", Units: "creators", Confidence: "High", DeckImplication: "Use after checking."},
			{ResearchQuestion: researchQuestion, SourceFact: "The program generated $12 million in purchases in 2026.", SourceTitle: "Commerce report", URL: unsupportedURL, PublishedOrUpdated: "Accessed 2026-08-21", Units: "currency unspecified", Confidence: "Medium", DeckImplication: "Use after checking."},
		},
	}
	providerBound, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, evidence), openAIResponseWebEvidence{
		ResponseID: "resp_source_snapshot_fixture", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{{Title: "Creator report", URL: supportedURL}, {Title: "Commerce report", URL: unsupportedURL}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := app.createOSArtifactWithMetadata("workflow", "Parent goal", "goal", scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	contextBody := `{"direct_ask":"Check how many opted-in creators the official program reports in 2026","audience":"decision makers","decision":"whether the program size supports proceeding","desired_response":"make a grounded decision","slide_count":6,"context_used":[],"settled_decisions":[],"taste_signals":[],"brand_assets":[],"research_mode":"external","research_questions":["How many opted-in creators does the official program report in 2026?"],"known_facts":[],"uncertain_claims":[],"reversible_inferences":[]}`
	contextArtifact, _, err := app.createOSArtifactWithMetadata("workflow", "Context snapshot", contextBody, scoutParticipantName, map[string]string{
		"goalParentId": parent.ID, "goalSubtaskId": "context_snapshot", "outputContract": "deck_context_snapshot_v2",
		"processId": packagingStudioProcessID, "processStage": "context_snapshot", "status": "complete", "threadStatus": "complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	researchMetadata := map[string]string{
		"goalParentId": parent.ID, "goalSubtaskId": "external_research", "outputContract": packagingStudioExternalEvidenceContract,
		"processId": packagingStudioProcessID, "processStage": "external_research", "status": "complete", "threadStatus": "complete",
		"researchAcceptedContentDigest": sha256Hex([]byte(providerBound)), "researchAcceptedArtifactVersion": "1",
	}
	for key, value := range researchArtifactEvidenceMetadata(scoutAgentThread{Mode: "research"}, providerBound) {
		researchMetadata[key] = value
	}
	research, _, err := app.createOSArtifactWithMetadata("workflow", "External research", providerBound, scoutParticipantName, researchMetadata)
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{Objective: "Check how many opted-in creators the official program reports in 2026", ProcessID: packagingStudioProcessID, Subtasks: []goalSubtask{
		{ID: "context_snapshot", Status: subtaskComplete, ArtifactID: contextArtifact.ID},
		{ID: "external_research", Status: subtaskComplete, ArtifactID: research.ID},
	}}
	fetch := func(_ context.Context, rawURL string) (externalSourceDocument, error) {
		blocks := []externalSourceTextBlock{{Anchor: "2026 creator report", Text: "The official program reports 4,200 opted-in creators in 2026."}}
		if rawURL == unsupportedURL {
			blocks = []externalSourceTextBlock{{Anchor: "About", Text: "The program explains creator onboarding and community guidelines."}}
		}
		return externalSourceDocument{RequestedURL: rawURL, FinalURL: rawURL, RedirectChain: []string{rawURL}, ContentType: "text/html", FetchedAt: "2026-08-21T12:00:00Z", ContentDigest: sha256Hex([]byte(rawURL)), Blocks: blocks}, nil
	}
	body, metadata, err := compileExternalEvidenceSourceSnapshotsWithFetcher(app, &plan, parent.ID, fetch)
	if err != nil {
		t.Fatal(err)
	}
	envelope, digest, err := externalSourceSnapshotEnvelopeFromText(body)
	if err != nil {
		t.Fatal(err)
	}
	if digest != metadata["sourceSnapshotDigest"] || metadata["sourceSnapshotRows"] != "2" || metadata["sourceSnapshotAdmissible"] != "1" || len(envelope.Snapshots) != 2 {
		t.Fatalf("snapshot receipt/metadata mismatch: digest=%s metadata=%v envelope=%+v", digest, metadata, envelope)
	}
	if envelope.Snapshots[0].Status != "fetched_with_relevant_text" || len(envelope.Snapshots[0].Windows) != 1 || !strings.Contains(envelope.Snapshots[0].Windows[0].Text, "4,200") {
		t.Fatalf("supported source did not produce a bound window: %+v", envelope.Snapshots[0])
	}
	if envelope.Snapshots[1].Status != "fetched_no_relevant_text" || len(envelope.Snapshots[1].Windows) != 0 {
		t.Fatalf("unrelated source text became a relevant window: %+v", envelope.Snapshots[1])
	}
	tampered := strings.Replace(body, "4,200", "9,200", 1)
	if _, _, err := externalSourceSnapshotEnvelopeFromText(tampered); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered server snapshot passed: %v", err)
	}
	for _, key := range []string{
		"researchQualityGate", "researchEvidenceBinding", "researchAcceptedArtifactVersion", "researchAcceptedContentDigest",
		"researchWordCount", "researchCitationCount", "researchSourceDomainCount", "researchWebSearchCallCount",
		"researchVisibleSourceDigest", "researchResponseDigest", "researchReceiptHasProviderAudit",
		"researchProviderSourceCount", "researchProviderSourceDomainCount", "researchProviderSourceDigest", "researchProviderResponseDigest",
	} {
		forged := cloneMemoryEntry(research)
		forged.Metadata[key] = "forged"
		if err := validateExternalResearchSnapshotAuthority(forged); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("forged terminal authority field %s passed: %v", key, err)
		}
	}

	const fakeURL = "https://fabricated.example/self-consistent"
	fakeEvidence := externalEvidenceEnvelope{
		ResearchQuestions: []string{researchQuestion},
		Evidence: []externalEvidenceEnvelopeRow{{
			ResearchQuestion: researchQuestion, SourceFact: "A fabricated source claims 99,000 opted-in creators in 2026.",
			SourceTitle: "Fabricated report", URL: fakeURL, PublishedOrUpdated: "Accessed 2026-08-21", Units: "creators", Confidence: "High", DeckImplication: "Do not use.",
		}},
	}
	fakeBody, err := normalizeExternalEvidenceArtifact(appendOpenAIResponseWebSources(externalEvidenceJSONForTest(t, fakeEvidence), openAIResponseWebEvidence{
		ResponseID: "resp_self_consistent_forgery", SearchCalls: 1,
		Citations: []openAIResponseWebCitation{{Title: "Fabricated report", URL: fakeURL}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.updateOSArtifactWithMetadata(research.ID, "", fakeBody, "attacker", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	var forgedFetches atomic.Int32
	_, _, err = compileExternalEvidenceSourceSnapshotsWithFetcher(app, &plan, parent.ID, func(_ context.Context, rawURL string) (externalSourceDocument, error) {
		forgedFetches.Add(1)
		return externalSourceDocument{RequestedURL: rawURL, FinalURL: rawURL, RedirectChain: []string{rawURL}, ContentType: "text/plain", FetchedAt: "2026-08-21T12:00:00Z", ContentDigest: sha256Hex([]byte("fake")), Blocks: []externalSourceTextBlock{{Anchor: "Fake", Text: "A fabricated source claims 99,000 opted-in creators in 2026."}}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "terminal authority field") || forgedFetches.Load() != 0 {
		t.Fatalf("self-consistent rewritten ledger+receipt reached network snapshotting: err=%v fetches=%d", err, forgedFetches.Load())
	}
}

func TestExternalSourceSnapshotMaxEnvelopeRoundTripsThroughStageInputs(t *testing.T) {
	envelope := externalSourceSnapshotEnvelope{Schema: externalSourceSnapshotSchema}
	for index := 0; index < 12; index++ {
		candidate := strings.Repeat("bounded claim ", 38) + strconv.Itoa(index) + "."
		rawURL := "https://source.example/report/" + strconv.Itoa(index)
		windows := make([]externalSourceWindow, 0, 3)
		for windowIndex := 0; windowIndex < 3; windowIndex++ {
			anchor := "Evidence table " + strconv.Itoa(index) + " row " + strconv.Itoa(windowIndex)
			text := candidate
			windows = append(windows, externalSourceWindow{Anchor: anchor, Assertion: candidate, Text: text, Digest: externalEvidenceSourceWindowDigest(anchor, candidate, text)})
		}
		envelope.Snapshots = append(envelope.Snapshots, externalSourceSnapshot{
			CandidateID: sha256Hex([]byte("candidate-" + strconv.Itoa(index))), ResearchQuestion: "What bounded claim matters for source " + strconv.Itoa(index) + "?", CandidateFact: candidate, URL: rawURL,
			SourceTitle: strings.Repeat("Source title ", 15) + strconv.Itoa(index), Status: "fetched_with_relevant_text",
			FinalURL: rawURL, RedirectChain: []string{rawURL}, ContentType: "text/html", ContentDigest: sha256Hex([]byte("body-" + strconv.Itoa(index))),
			FetchedAt: "2026-08-21T12:00:00Z", Windows: windows,
		})
	}
	initial, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) <= externalSourceSnapshotMaxPayload {
		t.Fatalf("fixture is not a max-envelope trimming exercise: bytes=%d", len(initial))
	}
	normalized, body, digest, err := renderExternalSourceSnapshotEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := false
	for _, snapshot := range normalized.Snapshots {
		if len(snapshot.Windows) < 3 {
			trimmed = true
		}
		if len(snapshot.Windows) < 1 {
			t.Fatal("payload fitting dropped a candidate's only complete context")
		}
	}
	if !trimmed || len(body) >= goalReviewArtifactCap {
		t.Fatalf("normalized payload did not fit below the non-truncating seam: trimmed=%v body=%d", trimmed, len(body))
	}
	parsed, parsedDigest, err := externalSourceSnapshotEnvelopeFromText(body)
	if err != nil || parsedDigest != digest || len(parsed.Snapshots) != 12 {
		t.Fatalf("compiled snapshot parse mismatch: digest=%s/%s rows=%d err=%v", digest, parsedDigest, len(parsed.Snapshots), err)
	}
	app := newIsolatedKanbanBoardApp(t)
	artifact, _, err := app.createOSArtifactWithMetadata("workflow", "Source snapshot", body, scoutParticipantName, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	plan := goalPlan{Subtasks: []goalSubtask{{ID: "source_snapshot", Title: "Capture exact source text", Status: subtaskComplete, ArtifactID: artifact.ID}}}
	inputs := newGoalEngine(app).processStageInputs(&plan, ProcessStage{ID: "evidence_entailment", InputFrom: []string{"source_snapshot"}})
	if strings.Contains(inputs, "artifact truncated for review") {
		t.Fatal("max valid snapshot was truncated at the downstream stage-input seam")
	}
	inputEnvelope, inputDigest, err := externalSourceSnapshotEnvelopeFromText(inputs)
	if err != nil || inputDigest != digest || len(inputEnvelope.Snapshots) != 12 {
		t.Fatalf("stage-input snapshot roundtrip mismatch: digest=%s/%s rows=%d err=%v", digest, inputDigest, len(inputEnvelope.Snapshots), err)
	}
}
