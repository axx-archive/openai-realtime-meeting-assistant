package main

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

type dissentGoldenVector struct {
	Name         string `json:"name"`
	Input        any    `json:"input"`
	Canonical    string `json:"canonical"`
	CanonicalHex string `json:"canonicalHex"`
	SHA256       string `json:"sha256"`
	HMAC         string `json:"hmac"`
}

type dissentGoldenFile struct {
	Secret          string                `json:"secret"`
	SourceSha256    map[string]string     `json:"sourceSha256"`
	UndefinedThrows bool                  `json:"undefinedThrows"`
	Vectors         []dissentGoldenVector `json:"vectors"`
}

// dissentExpandGoldenInput expands the three tagged forms the fixture uses
// for values plain JSON cannot carry into Go unchanged — the same expansion
// testdata/dissent-golden.mjs applies before handing the value to the real
// TypeScript:
//
//	{"__number":"-0"}            negative zero. JSON.stringify(-0) is "0", so a
//	                             literal -0 in the file round-trips to +0 and
//	                             the vector asserts nothing about the sign.
//	{"__stringHex":"61eda08062"} a string holding a LONE SURROGATE, as WTF-8
//	                             bytes. Go's encoding/json rewrites a \ud800
//	                             escape to U+FFFD; JSON.parse keeps the code
//	                             unit, so the escape form cannot be used here.
//	{"__keysHex":{"eda080":1}}   an object whose KEYS are such strings. The
//	                             value tags cannot express one, so without this
//	                             no vector carries a non-ASCII object key and
//	                             the key SORT — the half of canonicalJson that
//	                             fixes byte order, and with it every planId —
//	                             has no cross-language evidence.
func dissentExpandGoldenInput(t *testing.T, value any) any {
	t.Helper()
	switch typed := value.(type) {
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, dissentExpandGoldenInput(t, item))
		}
		return out
	case map[string]any:
		if len(typed) == 1 {
			if literal, ok := typed["__number"].(string); ok {
				number, err := strconv.ParseFloat(literal, 64)
				if err != nil {
					t.Fatalf("__number %q: %v", literal, err)
				}
				return number
			}
			if encoded, ok := typed["__stringHex"].(string); ok {
				decoded, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatalf("__stringHex %q: %v", encoded, err)
				}
				return string(decoded)
			}
			if entries, ok := typed["__keysHex"].(map[string]any); ok {
				out := map[string]any{}
				for encoded, item := range entries {
					decoded, err := hex.DecodeString(encoded)
					if err != nil {
						t.Fatalf("__keysHex %q: %v", encoded, err)
					}
					out[string(decoded)] = dissentExpandGoldenInput(t, item)
				}
				return out
			}
		}
		out := map[string]any{}
		for key, item := range typed {
			out[key] = dissentExpandGoldenInput(t, item)
		}
		return out
	}
	return value
}

func loadDissentGolden(t *testing.T) dissentGoldenFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/dissent_canonical_golden.json")
	if err != nil {
		t.Fatalf("golden vectors missing (regenerate with testdata/dissent-golden.mjs): %v", err)
	}
	var golden dissentGoldenFile
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Vectors) < 25 {
		t.Fatalf("golden vectors=%d, want at least 25", len(golden.Vectors))
	}
	// The fixtures are only evidence if they can be re-derived: the generator
	// is committed next to them and records the digest of the TypeScript it
	// ran, so a drifted source is detectable instead of silently green.
	if golden.SourceSha256["src/crypto.ts"] == "" {
		t.Fatal("golden file must record the sha256 of the TypeScript it was generated from")
	}
	return golden
}

// Every vector produced by the real TypeScript must replay byte-for-byte.
func TestDissentCanonicalGoldenVectorsReplay(t *testing.T) {
	golden := loadDissentGolden(t)
	for _, vector := range golden.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			canonical, err := dissentCanonicalJSON(dissentExpandGoldenInput(t, vector.Input))
			if err != nil {
				t.Fatalf("canonical: %v", err)
			}
			// Canonicalization must be a function of the value alone. Go
			// randomizes map iteration, so a key comparator that reports two
			// distinct keys equal leaves sort.SliceStable emitting whichever
			// order the runtime happened to hand it — a canonicalizer that
			// hashes the same value two ways run to run. One pass could pick
			// the right order by luck; a repeat cannot.
			for repeat := 0; repeat < 16; repeat++ {
				again, err := dissentCanonicalJSON(dissentExpandGoldenInput(t, vector.Input))
				if err != nil || again != canonical {
					t.Fatalf("canonicalization is not deterministic:\n got %q\nwant %q (err %v)", again, canonical, err)
				}
			}
			if got := hex.EncodeToString([]byte(canonical)); got != vector.CanonicalHex {
				t.Fatalf("canonical bytes differ:\n got %q\nwant %q", canonical, vector.Canonical)
			}
			if got := dissentSha256Hex(canonical); got != vector.SHA256 {
				t.Fatalf("sha256=%s, want %s", got, vector.SHA256)
			}
			if got := dissentHMACSha256Base64URL(golden.Secret, canonical); got != vector.HMAC {
				t.Fatalf("hmac=%s, want %s", got, vector.HMAC)
			}
			if len(vector.HMAC) != 43 {
				t.Fatalf("hmac length=%d, want 43 unpadded base64url chars", len(vector.HMAC))
			}
			if !dissentVerifyHMAC(golden.Secret, canonical, vector.HMAC) {
				t.Fatal("verifyHMAC rejected the golden digest")
			}
			if dissentVerifyHMAC(golden.Secret+"x", canonical, vector.HMAC) || dissentVerifyHMAC(golden.Secret, canonical+" ", vector.HMAC) {
				t.Fatal("verifyHMAC accepted a tampered secret/value")
			}
			tampered := []byte(vector.HMAC)
			tampered[0] ^= 0x01
			if dissentVerifyHMAC(golden.Secret, canonical, string(tampered)) || dissentVerifyHMAC(golden.Secret, canonical, vector.HMAC[:42]) {
				t.Fatal("verifyHMAC accepted a tampered/short digest")
			}
		})
	}
}

// Go has no undefined; the slot the TS throw occupies is "unsupported type".
func TestDissentCanonicalRejectsUnsupportedValues(t *testing.T) {
	golden := loadDissentGolden(t)
	if !golden.UndefinedThrows {
		t.Fatal("the TS reference must throw on undefined")
	}
	if _, err := dissentCanonicalJSON(map[string]any{"a": struct{}{}}); err == nil {
		t.Fatal("unsupported value must be rejected")
	}
	if _, err := dissentCanonicalJSON([]any{make(chan int)}); err == nil {
		t.Fatal("unsupported array element must be rejected")
	}
}

// The ECMAScript number formatter, pinned on the edges the golden file
// cannot express as JSON input (NaN/Infinity) plus the exponent thresholds.
func TestDissentFormatNumberMatchesECMAScript(t *testing.T) {
	cases := map[float64]string{
		0: "0", 1: "1", -1: "-1", 1e21: "1e+21", 1e20: "100000000000000000000", 123456789012345680000: "123456789012345680000",
		1e-7: "1e-7", 0.000001: "0.000001", 0.1: "0.1", 1.5e-9: "1.5e-9", 1.25e22: "1.25e+22", 0.5: "0.5", 100: "100",
		9007199254740991: "9007199254740991", 1.7976931348623157e308: "1.7976931348623157e+308", 5e-324: "5e-324",
	}
	for value, want := range cases {
		if got := dissentFormatNumber(value); got != want {
			t.Errorf("format(%v)=%q, want %q", value, got, want)
		}
	}
	if got := dissentFormatNumber(-0.0); got != "0" {
		t.Errorf("format(-0)=%q, want 0", got)
	}
	for _, special := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := dissentFormatNumber(special); got != "null" {
			t.Errorf("format(%v)=%q, want null", special, got)
		}
	}
	if dissentCompareUTF16("😀", "\uFFFF") >= 0 || dissentCompareUTF16("😀", "\uE000") >= 0 || dissentCompareUTF16("\uE000", "\uFFFF") >= 0 {
		t.Fatal("key order must follow UTF-16 code units, not UTF-8 bytes")
	}
	// -0 is the reason the golden file tags it: JSON cannot carry a negative
	// zero, so the sign is pinned here as well as by the tagged
	// "negative_zero" vector the generator now expands.
	if got := dissentFormatNumber(math.Copysign(0, -1)); got != "0" {
		t.Errorf("format(copysign -0)=%q, want 0", got)
	}
}

// A lone surrogate is the one JS string with no Go equivalent. Well-formed
// JSON.stringify re-emits it as a \udXXX escape (node: JSON.stringify("a\ud800b")
// is "a\\ud800b"), so the canonical bytes — and therefore the contractSha256
// and planId taken over them — must carry the escape, not the U+FFFD
// substitution Go's decoders reach for.
func TestDissentCanonicalLoneSurrogateMatchesJSONStringify(t *testing.T) {
	for _, testCase := range []struct{ value, want string }{
		{"a\xed\xa0\x80b", `"a\ud800b"`},               // U+D800 alone
		{"\xed\xbf\xbf", `"\udfff"`},                   // U+DFFF alone
		{"\xed\xa0\xbd\xed\xb8\x80", "\"\U0001F600\""}, // a PAIR that arrived as two WTF-8 runs
		{"\U0001F600", "\"\U0001F600\""},               // a real astral rune still goes out raw
	} {
		got, err := dissentCanonicalJSON(testCase.value)
		if err != nil {
			t.Fatalf("canonical(% x): %v", testCase.value, err)
		}
		if got != testCase.want {
			t.Errorf("canonical(% x)=%s, want %s", testCase.value, got, testCase.want)
		}
	}
	// Object KEYS take the same path as values.
	got, err := dissentCanonicalJSON(map[string]any{"a\xed\xa0\x80b": 1})
	if err != nil || got != `{"a\ud800b":1}` {
		t.Fatalf("canonical key=%s err=%v, want {\"a\\ud800b\":1}", got, err)
	}
	// …and so does the key SORT. []rune mangles each WTF-8 surrogate byte to
	// U+FFFD, so these three DISTINCT keys all collapse to [FFFD FFFD FFFD]:
	// a comparator built on it calls them equal, sort.SliceStable then keeps
	// Go's randomized map order, and the same map canonicalizes — and
	// therefore hashes — differently run to run. Node's Object.keys sort puts
	// them D800 < DFFF < FFFD.
	for repeat := 0; repeat < 64; repeat++ {
		got, err := dissentCanonicalJSON(map[string]any{"\xed\xa0\x80": 1, "\xed\xbf\xbf": 2, "\uFFFD": 3})
		if err != nil || got != "{\"\\ud800\":1,\"\\udfff\":2,\"\uFFFD\":3}" {
			t.Fatalf("surrogate key order=%q err=%v, want {\"\\ud800\":1,\"\\udfff\":2,\"\uFFFD\":3}", got, err)
		}
	}
	// A lone surrogate is ONE UTF-16 code unit, so it sorts before any key
	// that merely starts with the same unit, and String.length counts it once.
	if dissentCompareUTF16("\xed\xa0\x80", "\uFFFD") >= 0 || dissentCompareUTF16("\xed\xa0\x80", "\xed\xbf\xbf") >= 0 {
		t.Fatal("lone surrogate keys must order by their real UTF-16 code unit")
	}
	if dissentCompareUTF16("\xed\xa0\x80", "\xed\xa0\x80b") >= 0 || dissentCompareUTF16("\xed\xa0\x80", "\xed\xa0\x80") != 0 {
		t.Fatal("a lone surrogate must be a one-unit prefix of a longer key")
	}
	for value, want := range map[string]int{"\xed\xa0\x80": 1, "a\xed\xa0\x80b": 3, "\U0001F600": 2, "\xed\xa0\xbd\xed\xb8\x80": 2, "abc": 3, "é": 1} {
		if got := dissentStringLength(value); got != want {
			t.Errorf("String.length(% x)=%d, want %d", value, got, want)
		}
	}
	// Anything that is neither UTF-8 nor WTF-8 has no JS counterpart at all,
	// so it is refused rather than hashed as some other string.
	if _, err := dissentCanonicalJSON("bad\xffbyte"); err == nil {
		t.Fatal("invalid UTF-8 must be refused, not silently replaced")
	}
}

// Go's encoding/json rewrites an unpaired \ud800 escape to U+FFFD while
// JSON.parse keeps the code unit, so the same JSON TEXT would hash
// differently on the two sides. dissentDecodeJSON refuses those documents;
// everything else decodes exactly as JSON.parse does.
func TestDissentDecodeJSONRejectsLoneSurrogateEscapes(t *testing.T) {
	for _, text := range []string{
		`{"metadata":{"k":"a\ud800b"}}`,
		`"\udfff"`,
		`["\ud83d"]`,
		`{"k":"\\\ud800"}`, // a literal backslash then the escape: still unpaired
	} {
		if _, err := dissentDecodeJSON(text); err == nil {
			t.Fatalf("decode(%s) must refuse the unpaired surrogate escape", text)
		}
		// Proof the guard is load-bearing: the stdlib silently substitutes.
		var loose any
		if err := json.Unmarshal([]byte(text), &loose); err != nil {
			t.Fatalf("stdlib should still parse %s: %v", text, err)
		}
	}
	for _, text := range []string{
		`{"k":"\ud83d\ude00"}`, // a proper pair is fine
		`{"k":"\\u0041"}`,      // a literal backslash-u, not an escape
		`{"k":"plain","n":[1,-0,1e21]}`,
	} {
		if _, err := dissentDecodeJSON(text); err != nil {
			t.Fatalf("decode(%s): %v", text, err)
		}
	}
	// The surviving pair canonicalizes to the raw astral rune, as in JS.
	value, err := dissentDecodeJSON(`{"k":"\ud83d\ude00"}`)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := dissentCanonicalJSON(value)
	if err != nil || !strings.Contains(canonical, "\U0001F600") {
		t.Fatalf("canonical=%q err=%v, want the astral rune raw", canonical, err)
	}
}
