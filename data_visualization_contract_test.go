package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"math"
	"strings"
	"sync"
	"testing"
)

func dataVisualizationFixture(kind DataVisualizationChartKind) DataVisualizationCompileRequest {
	xType := DataVisualizationNumber
	xValues := []DataVisualizationCell{
		{Type: DataVisualizationNumber, Number: 1},
		{Type: DataVisualizationNumber, Number: 2},
		{Type: DataVisualizationNumber, Number: 3},
	}
	if kind == DataVisualizationBar {
		xType = DataVisualizationCategory
		xValues = []DataVisualizationCell{
			{Type: DataVisualizationCategory, Text: `Secret Q1 & "one"`},
			{Type: DataVisualizationCategory, Text: "Secret Q2"},
			{Type: DataVisualizationCategory, Text: "Secret Q3"},
		}
	}
	table := DataVisualizationTable{
		Columns: []DataVisualizationColumn{
			{ID: "period", Label: "Period", Type: xType},
			{ID: "revenue", Label: "Revenue", Type: DataVisualizationNumber, Unit: "USD"},
			{ID: "cost", Label: "Cost", Type: DataVisualizationNumber, Unit: "USD"},
		},
		Rows: [][]DataVisualizationCell{
			{xValues[0], {Type: DataVisualizationNumber, Number: 12.5}, {Type: DataVisualizationNumber, Number: -3}},
			{xValues[1], {Type: DataVisualizationNumber, Number: 0}, {Type: DataVisualizationNumber, Number: 8.25}},
			{xValues[2], {Type: DataVisualizationNumber, Number: 17}, {Type: DataVisualizationNumber, Number: 17}},
		},
	}
	digest, err := DataVisualizationSourceSHA256(table)
	if err != nil {
		panic(err)
	}
	return DataVisualizationCompileRequest{
		Table: table, ExpectedSourceSHA256: digest,
		Spec: DataVisualizationSpec{Kind: kind, Title: "Secret Operating Results", XColumnID: "period", SeriesColumnIDs: []string{"revenue", "cost"}, Width: 800, Height: 480},
	}
}

func TestDataVisualizationCompilerIsDeterministicAccessibleAndBodyFree(t *testing.T) {
	for _, kind := range []DataVisualizationChartKind{DataVisualizationBar, DataVisualizationLine, DataVisualizationScatter} {
		t.Run(string(kind), func(t *testing.T) {
			request := dataVisualizationFixture(kind)
			first, err := CompileDataVisualization(request)
			if err != nil {
				t.Fatal(err)
			}
			second, err := CompileDataVisualization(request)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first.SVG, second.SVG) || !bytes.Equal(first.AccessibleTableHTML, second.AccessibleTableHTML) || first.Manifest != second.Manifest {
				t.Fatal("identical visualization input was not byte deterministic")
			}
			if first.Manifest.ManifestSHA256 == "" || first.Manifest.ArtifactSHA256 == "" || first.Manifest.SourceSHA256 != request.ExpectedSourceSHA256 {
				t.Fatalf("incomplete visualization identity: %+v", first.Manifest)
			}
			if err := VerifyDataVisualizationManifest(first.Manifest); err != nil {
				t.Fatalf("compiled manifest does not independently verify: %v", err)
			}
			preimage, err := canonicalJSON(dataVisualizationManifestDigestBody(first.Manifest))
			if err != nil || sha256Hex(preimage) != first.Manifest.ManifestSHA256 {
				t.Fatalf("manifest self-digest preimage mismatch: digest=%s err=%v", first.Manifest.ManifestSHA256, err)
			}
			tampered := first.Manifest
			tampered.RowCount++
			if err := VerifyDataVisualizationManifest(tampered); err == nil {
				t.Fatal("tampered visualization manifest verified")
			}
			resignedArtifact := first.Manifest
			resignedArtifact.ArtifactSHA256 = strings.Repeat("f", 64)
			resignDataVisualizationManifest(t, &resignedArtifact)
			if err := VerifyDataVisualizationManifest(resignedArtifact); err == nil {
				t.Fatal("re-signed manifest with an impossible artifact identity verified")
			}
			resignedCounts := first.Manifest
			resignedCounts.ColumnCount++
			resignDataVisualizationManifest(t, &resignedCounts)
			if err := VerifyDataVisualizationManifest(resignedCounts); err == nil {
				t.Fatal("re-signed manifest with impossible column/series counts verified")
			}
			if first.Manifest.PointCount != 6 || first.Manifest.RowCount != 3 || first.Manifest.SeriesCount != 2 {
				t.Fatalf("incorrect body-free counts: %+v", first.Manifest)
			}
			if err := xml.Unmarshal(first.SVG, new(any)); err != nil {
				t.Fatalf("SVG is not XML: %v", err)
			}
			svg := string(first.SVG)
			if !strings.Contains(svg, `role="img"`) || !strings.Contains(svg, "aria-labelledby=") || !strings.Contains(svg, "<title ") || !strings.Contains(svg, "<desc ") {
				t.Fatalf("SVG lacks accessible naming: %s", svg)
			}
			requireDataVisualizationPassiveSVG(t, svg)
			if !strings.Contains(string(first.AccessibleTableHTML), `<caption>Secret Operating Results</caption>`) || !strings.Contains(string(first.AccessibleTableHTML), `<th scope="col">`) || !strings.Contains(string(first.AccessibleTableHTML), `<th scope="row">`) {
				t.Fatal("accessible table lacks caption or header semantics")
			}
			manifestRaw, err := json.Marshal(first.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{"Secret Operating Results", "Secret Q1", "Revenue", "Period", "<svg", "<table"} {
				if bytes.Contains(manifestRaw, []byte(body)) {
					t.Fatalf("body-free manifest leaked %q: %s", body, manifestRaw)
				}
			}
		})
	}
}

func TestDataVisualizationBarZeroHasNoFalseGeometry(t *testing.T) {
	request := dataVisualizationFixture(DataVisualizationBar)
	result, err := CompileDataVisualization(request)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has six source points and one exact zero. The only other rect
	// is the white chart background, so five data bars plus one background are
	// expected. An exact zero must not be promoted to a visible one-pixel bar.
	if got := strings.Count(string(result.SVG), "<rect "); got != 6 {
		t.Fatalf("bar SVG rect count=%d, want background plus five nonzero bars", got)
	}
	allZero := request
	allZero.Table.Rows = allZero.Table.Rows[:1]
	allZero.Table.Rows[0][1].Number = 0
	allZero.Table.Rows[0][2].Number = 0
	allZero = refreshVisualizationDigest(allZero)
	zeroResult, err := CompileDataVisualization(allZero)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(zeroResult.SVG), "<rect "); got != 1 {
		t.Fatalf("all-zero chart emitted %d rects, want background only", got)
	}
}

func TestDataVisualizationCompilerIsConcurrentAndIdentityBound(t *testing.T) {
	request := dataVisualizationFixture(DataVisualizationLine)
	want, err := CompileDataVisualization(request)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for index := 0; index < 32; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, compileErr := CompileDataVisualization(request)
			if compileErr != nil {
				errs <- compileErr.Error()
				return
			}
			if !bytes.Equal(got.SVG, want.SVG) || got.Manifest != want.Manifest {
				errs <- "concurrent compiler output drift"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for errText := range errs {
		t.Error(errText)
	}

	changed := request
	changed.Table.Rows = cloneDataVisualizationRows(request.Table.Rows)
	changed.Table.Rows[1][1].Number = 0.25
	changed.ExpectedSourceSHA256, err = DataVisualizationSourceSHA256(changed.Table)
	if err != nil {
		t.Fatal(err)
	}
	changedResult, err := CompileDataVisualization(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedResult.Manifest.SourceSHA256 == want.Manifest.SourceSHA256 || changedResult.Manifest.ArtifactSHA256 == want.Manifest.ArtifactSHA256 {
		t.Fatal("source cell mutation did not change source and artifact identity")
	}

	specChanged := request
	specChanged.Spec.Title = "Changed title"
	specResult, err := CompileDataVisualization(specChanged)
	if err != nil {
		t.Fatal(err)
	}
	if specResult.Manifest.SourceSHA256 != want.Manifest.SourceSHA256 || specResult.Manifest.SpecSHA256 == want.Manifest.SpecSHA256 || specResult.Manifest.ArtifactSHA256 == want.Manifest.ArtifactSHA256 {
		t.Fatal("spec mutation was not isolated to spec and artifact identity")
	}

	positiveZero := request.Table
	positiveZero.Rows = cloneDataVisualizationRows(request.Table.Rows)
	negativeZero := request.Table
	negativeZero.Rows = cloneDataVisualizationRows(request.Table.Rows)
	positiveZero.Rows[1][1].Number = 0
	negativeZero.Rows[1][1].Number = math.Copysign(0, -1)
	positiveDigest, err := DataVisualizationSourceSHA256(positiveZero)
	if err != nil {
		t.Fatal(err)
	}
	negativeDigest, err := DataVisualizationSourceSHA256(negativeZero)
	if err != nil {
		t.Fatal(err)
	}
	if positiveDigest != negativeDigest {
		t.Fatal("RFC 8785 negative-zero normalization changed visualization identity")
	}
}

func TestDataVisualizationCompilerFailsClosed(t *testing.T) {
	valid := dataVisualizationFixture(DataVisualizationLine)
	tests := map[string]func(DataVisualizationCompileRequest) DataVisualizationCompileRequest{
		"source digest mismatch": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.ExpectedSourceSHA256 = strings.Repeat("a", 64)
			return r
		},
		"zero source digest": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.ExpectedSourceSHA256 = strings.Repeat("0", 64)
			return r
		},
		"unsupported chart":  func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest { r.Spec.Kind = "pie"; return r },
		"invalid dimensions": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest { r.Spec.Width = 200; return r },
		"ambiguous series": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Spec.SeriesColumnIDs = []string{"revenue", "revenue"}
			return r
		},
		"missing axis": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Spec.XColumnID = "missing"
			return r
		},
		"duplicate column": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Columns[1].ID = r.Table.Columns[0].ID
			return refreshVisualizationDigest(r)
		},
		"ragged row": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Rows[0] = r.Table.Rows[0][:2]
			return r
		},
		"cell type mismatch": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Rows[0][1].Type = DataVisualizationCategory
			return r
		},
		"numeric text": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Rows[0][1].Text = "12"
			return r
		},
		"not finite": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Rows[0][1].Number = math.NaN()
			return r
		},
		"unsafe integral": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Rows[0][1].Number = float64(uint64(1) << 54)
			return r
		},
		"non increasing line x": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Table.Rows[1][0].Number = 1
			return refreshVisualizationDigest(r)
		},
		"invalid utf8": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Spec.Title = string([]byte{0xff})
			return r
		},
		"control character": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Spec.Title = "bad\u202e title"
			return r
		},
		"markup": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Spec.Title = `</title><script>alert(1)</script>`
			return r
		},
		"external URL": func(r DataVisualizationCompileRequest) DataVisualizationCompileRequest {
			r.Spec.Title = "https://example.com/chart"
			return r
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := dataVisualizationFixture(DataVisualizationLine)
			request.Table.Columns = append([]DataVisualizationColumn(nil), request.Table.Columns...)
			request.Table.Rows = cloneDataVisualizationRows(request.Table.Rows)
			request.Spec.SeriesColumnIDs = append([]string(nil), request.Spec.SeriesColumnIDs...)
			request = mutate(request)
			if result, err := CompileDataVisualization(request); err == nil || len(result.SVG) != 0 || len(result.AccessibleTableHTML) != 0 || result.Manifest.ManifestSHA256 != "" {
				t.Fatalf("invalid visualization produced output=%+v err=%v", result.Manifest, err)
			}
		})
	}

	bar := dataVisualizationFixture(DataVisualizationBar)
	bar.Table.Rows[1][0].Text = bar.Table.Rows[0][0].Text
	bar = refreshVisualizationDigest(bar)
	if _, err := CompileDataVisualization(bar); err == nil {
		t.Fatal("duplicate bar category was accepted")
	}

	oversize := valid
	oversize.Table.Rows = make([][]DataVisualizationCell, dataVisualizationMaxRows+1)
	for index := range oversize.Table.Rows {
		oversize.Table.Rows[index] = cloneDataVisualizationRows(valid.Table.Rows)[0]
	}
	if _, err := CompileDataVisualization(oversize); err == nil {
		t.Fatal("oversized visualization source was accepted")
	}
}

func TestDataVisualizationDegenerateDomainsRemainFinite(t *testing.T) {
	for _, kind := range []DataVisualizationChartKind{DataVisualizationBar, DataVisualizationLine, DataVisualizationScatter} {
		request := dataVisualizationFixture(kind)
		request.Table.Rows = request.Table.Rows[:1]
		for index := 1; index < len(request.Table.Rows[0]); index++ {
			request.Table.Rows[0][index].Number = 0
		}
		request = refreshVisualizationDigest(request)
		result, err := CompileDataVisualization(request)
		if err != nil {
			t.Fatalf("%s degenerate domain: %v", kind, err)
		}
		if bytes.Contains(result.SVG, []byte("NaN")) || bytes.Contains(result.SVG, []byte("Inf")) {
			t.Fatalf("%s emitted a non-finite coordinate", kind)
		}
	}
}

func FuzzCompileDataVisualizationNeverEmitsActiveSVG(f *testing.F) {
	f.Add("Quarter & one", "Safe title")
	f.Add(`</text><script>alert(1)</script>`, "Unsafe title")
	f.Add("https://example.com", "URL-like category")
	f.Fuzz(func(t *testing.T, category, title string) {
		request := dataVisualizationFixture(DataVisualizationBar)
		request.Table.Rows = request.Table.Rows[:1]
		request.Table.Rows[0][0].Text = category
		request.Spec.Title = title
		digest, err := DataVisualizationSourceSHA256(request.Table)
		if err != nil {
			return
		}
		request.ExpectedSourceSHA256 = digest
		result, err := CompileDataVisualization(request)
		if err != nil {
			return
		}
		if err := xml.Unmarshal(result.SVG, new(any)); err != nil {
			t.Fatalf("successful compiler output is not XML: %v", err)
		}
		requireDataVisualizationPassiveSVG(t, string(result.SVG))
	})
}

func requireDataVisualizationPassiveSVG(t *testing.T, svg string) {
	t.Helper()
	lower := strings.ToLower(svg)
	for _, forbidden := range []string{"<!doctype", "<!entity", "<?", "<script", "<style", "<foreignobject", "<image", "<use", "<a ", "<iframe", "<object", "<embed", "<animate", "<set", "href=", "xlink", "onload", "onclick", "url("} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("SVG contains forbidden active construct %q: %s", forbidden, svg)
		}
	}
}

func refreshVisualizationDigest(request DataVisualizationCompileRequest) DataVisualizationCompileRequest {
	digest, err := DataVisualizationSourceSHA256(request.Table)
	if err == nil {
		request.ExpectedSourceSHA256 = digest
	}
	return request
}

func cloneDataVisualizationRows(rows [][]DataVisualizationCell) [][]DataVisualizationCell {
	cloned := make([][]DataVisualizationCell, len(rows))
	for index := range rows {
		cloned[index] = append([]DataVisualizationCell(nil), rows[index]...)
	}
	return cloned
}

func resignDataVisualizationManifest(t *testing.T, manifest *DataVisualizationManifest) {
	t.Helper()
	raw, err := canonicalJSON(dataVisualizationManifestDigestBody(*manifest))
	if err != nil {
		t.Fatal(err)
	}
	manifest.ManifestSHA256 = sha256Hex(raw)
}
