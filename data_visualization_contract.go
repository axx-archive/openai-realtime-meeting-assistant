package main

import (
	"errors"
	"fmt"
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	dataVisualizationSchemaVersion   = "stride.data_visualization.v1"
	dataVisualizationCompilerVersion = "stride.data_visualization.compiler.v1"
	dataVisualizationMaxColumns      = 7
	dataVisualizationMaxSeries       = 6
	dataVisualizationMaxRows         = 200
	dataVisualizationMaxBarRows      = 40
	dataVisualizationMaxSourceBytes  = 128 * 1024
	dataVisualizationMaxOutputBytes  = 1024 * 1024
)

type DataVisualizationChartKind string

const (
	DataVisualizationBar     DataVisualizationChartKind = "bar"
	DataVisualizationLine    DataVisualizationChartKind = "line"
	DataVisualizationScatter DataVisualizationChartKind = "scatter"
)

type DataVisualizationColumnType string

const (
	DataVisualizationCategory DataVisualizationColumnType = "category"
	DataVisualizationNumber   DataVisualizationColumnType = "number"
)

type DataVisualizationColumn struct {
	ID    string                      `json:"id"`
	Label string                      `json:"label"`
	Type  DataVisualizationColumnType `json:"type"`
	Unit  string                      `json:"unit,omitempty"`
}

// DataVisualizationCell is deliberately discriminated. The compiler never
// infers a number from text, treats an empty string as missing, or accepts a
// nullable/partially typed value.
type DataVisualizationCell struct {
	Type   DataVisualizationColumnType `json:"type"`
	Text   string                      `json:"text,omitempty"`
	Number float64                     `json:"number,omitempty"`
}

type DataVisualizationTable struct {
	Columns []DataVisualizationColumn `json:"columns"`
	Rows    [][]DataVisualizationCell `json:"rows"`
}

type DataVisualizationSpec struct {
	Kind            DataVisualizationChartKind `json:"kind"`
	Title           string                     `json:"title"`
	XColumnID       string                     `json:"xColumnId"`
	SeriesColumnIDs []string                   `json:"seriesColumnIds"`
	Width           int                        `json:"width"`
	Height          int                        `json:"height"`
}

type DataVisualizationCompileRequest struct {
	Table                DataVisualizationTable `json:"table"`
	ExpectedSourceSHA256 string                 `json:"expectedSourceSha256"`
	Spec                 DataVisualizationSpec  `json:"spec"`
}

// DataVisualizationManifest intentionally contains no title, label, category,
// cell, SVG, HTML, URL, or other source body.
type DataVisualizationManifest struct {
	SchemaVersion   string `json:"schemaVersion"`
	CompilerVersion string `json:"compilerVersion"`
	Format          string `json:"format"`
	SourceSHA256    string `json:"sourceSha256"`
	SpecSHA256      string `json:"specSha256"`
	SVGSHA256       string `json:"svgSha256"`
	TableSHA256     string `json:"tableSha256"`
	ArtifactSHA256  string `json:"artifactSha256"`
	ManifestSHA256  string `json:"manifestSha256"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	ColumnCount     int    `json:"columnCount"`
	RowCount        int    `json:"rowCount"`
	SeriesCount     int    `json:"seriesCount"`
	PointCount      int    `json:"pointCount"`
}

type CompiledDataVisualization struct {
	SVG                 []byte
	AccessibleTableHTML []byte
	Manifest            DataVisualizationManifest
}

// dataVisualizationManifestBody is the explicit preimage for the manifest
// self-digest. ManifestSHA256 is excluded rather than implicitly zeroed, so an
// independent verifier can reproduce the identity without a self-reference.
type dataVisualizationManifestBody struct {
	SchemaVersion   string `json:"schemaVersion"`
	CompilerVersion string `json:"compilerVersion"`
	Format          string `json:"format"`
	SourceSHA256    string `json:"sourceSha256"`
	SpecSHA256      string `json:"specSha256"`
	SVGSHA256       string `json:"svgSha256"`
	TableSHA256     string `json:"tableSha256"`
	ArtifactSHA256  string `json:"artifactSha256"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	ColumnCount     int    `json:"columnCount"`
	RowCount        int    `json:"rowCount"`
	SeriesCount     int    `json:"seriesCount"`
	PointCount      int    `json:"pointCount"`
}

var dataVisualizationColumnIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func DataVisualizationSourceSHA256(table DataVisualizationTable) (string, error) {
	if err := validateDataVisualizationTable(table); err != nil {
		return "", err
	}
	raw, err := canonicalJSON(struct {
		Domain  string                 `json:"domain"`
		Version string                 `json:"version"`
		Table   DataVisualizationTable `json:"table"`
	}{Domain: "stride.data_visualization.source", Version: dataVisualizationSchemaVersion, Table: table})
	if err != nil {
		return "", fmt.Errorf("canonicalize visualization source: %w", err)
	}
	if len(raw) > dataVisualizationMaxSourceBytes {
		return "", errors.New("visualization source exceeds the byte limit")
	}
	return sha256Hex(raw), nil
}

func CompileDataVisualization(request DataVisualizationCompileRequest) (CompiledDataVisualization, error) {
	if err := validateDataVisualizationTable(request.Table); err != nil {
		return CompiledDataVisualization{}, err
	}
	if err := validateDataVisualizationSpec(request.Table, request.Spec); err != nil {
		return CompiledDataVisualization{}, err
	}
	sourceDigest, err := DataVisualizationSourceSHA256(request.Table)
	if err != nil {
		return CompiledDataVisualization{}, err
	}
	if !isNonZeroHexDigest(request.ExpectedSourceSHA256) || request.ExpectedSourceSHA256 != sourceDigest {
		return CompiledDataVisualization{}, errors.New("visualization source digest mismatch")
	}
	specRaw, err := canonicalJSON(struct {
		Domain  string                `json:"domain"`
		Version string                `json:"version"`
		Spec    DataVisualizationSpec `json:"spec"`
	}{Domain: "stride.data_visualization.spec", Version: dataVisualizationSchemaVersion, Spec: request.Spec})
	if err != nil {
		return CompiledDataVisualization{}, fmt.Errorf("canonicalize visualization spec: %w", err)
	}
	specDigest := sha256Hex(specRaw)

	svg, err := renderDataVisualizationSVG(request.Table, request.Spec, specDigest)
	if err != nil {
		return CompiledDataVisualization{}, err
	}
	tableHTML := renderDataVisualizationTable(request.Table, request.Spec)
	if len(svg)+len(tableHTML) > dataVisualizationMaxOutputBytes {
		return CompiledDataVisualization{}, errors.New("visualization output exceeds the byte limit")
	}
	svgDigest := sha256Hex(svg)
	tableDigest := sha256Hex(tableHTML)
	artifactDigest, err := dataVisualizationArtifactSHA256(sourceDigest, specDigest, svgDigest, tableDigest)
	if err != nil {
		return CompiledDataVisualization{}, err
	}
	manifest := DataVisualizationManifest{
		SchemaVersion: dataVisualizationSchemaVersion, CompilerVersion: dataVisualizationCompilerVersion,
		Format: "svg+accessible_html_table", SourceSHA256: sourceDigest, SpecSHA256: specDigest,
		SVGSHA256: svgDigest, TableSHA256: tableDigest, ArtifactSHA256: artifactDigest,
		Width: request.Spec.Width, Height: request.Spec.Height, ColumnCount: len(request.Table.Columns),
		RowCount: len(request.Table.Rows), SeriesCount: len(request.Spec.SeriesColumnIDs),
		PointCount: len(request.Table.Rows) * len(request.Spec.SeriesColumnIDs),
	}
	manifestRaw, err := canonicalJSON(dataVisualizationManifestDigestBody(manifest))
	if err != nil {
		return CompiledDataVisualization{}, err
	}
	manifest.ManifestSHA256 = sha256Hex(manifestRaw)
	if err := VerifyDataVisualizationManifest(manifest); err != nil {
		return CompiledDataVisualization{}, err
	}
	return CompiledDataVisualization{SVG: svg, AccessibleTableHTML: tableHTML, Manifest: manifest}, nil
}

func VerifyDataVisualizationManifest(manifest DataVisualizationManifest) error {
	for _, candidate := range []struct{ name, digest string }{
		{"source", manifest.SourceSHA256}, {"spec", manifest.SpecSHA256},
		{"svg", manifest.SVGSHA256}, {"table", manifest.TableSHA256},
		{"artifact", manifest.ArtifactSHA256}, {"manifest", manifest.ManifestSHA256},
	} {
		if !isNonZeroHexDigest(candidate.digest) {
			return fmt.Errorf("visualization manifest has an invalid %s digest", candidate.name)
		}
	}
	if manifest.SchemaVersion != dataVisualizationSchemaVersion || manifest.CompilerVersion != dataVisualizationCompilerVersion || manifest.Format != "svg+accessible_html_table" {
		return errors.New("visualization manifest version or format mismatch")
	}
	if manifest.Width < 320 || manifest.Width > 1600 || manifest.Height < 240 || manifest.Height > 1200 || manifest.ColumnCount < 2 || manifest.ColumnCount > dataVisualizationMaxColumns || manifest.RowCount < 1 || manifest.RowCount > dataVisualizationMaxRows || manifest.SeriesCount < 1 || manifest.SeriesCount > dataVisualizationMaxSeries || manifest.ColumnCount != manifest.SeriesCount+1 || manifest.PointCount != manifest.RowCount*manifest.SeriesCount {
		return errors.New("visualization manifest counts or dimensions are invalid")
	}
	artifactDigest, err := dataVisualizationArtifactSHA256(manifest.SourceSHA256, manifest.SpecSHA256, manifest.SVGSHA256, manifest.TableSHA256)
	if err != nil {
		return err
	}
	if artifactDigest != manifest.ArtifactSHA256 {
		return errors.New("visualization artifact digest mismatch")
	}
	raw, err := canonicalJSON(dataVisualizationManifestDigestBody(manifest))
	if err != nil {
		return err
	}
	if sha256Hex(raw) != manifest.ManifestSHA256 {
		return errors.New("visualization manifest digest mismatch")
	}
	return nil
}

func dataVisualizationArtifactSHA256(sourceDigest, specDigest, svgDigest, tableDigest string) (string, error) {
	for _, digest := range []string{sourceDigest, specDigest, svgDigest, tableDigest} {
		if !isNonZeroHexDigest(digest) {
			return "", errors.New("visualization artifact preimage has an invalid digest")
		}
	}
	raw, err := canonicalJSON(struct {
		Domain        string `json:"domain"`
		SVG           string `json:"svgSha256"`
		Table         string `json:"tableSha256"`
		Source        string `json:"sourceSha256"`
		Specification string `json:"specSha256"`
	}{Domain: "stride.data_visualization.artifact", SVG: svgDigest, Table: tableDigest, Source: sourceDigest, Specification: specDigest})
	if err != nil {
		return "", err
	}
	return sha256Hex(raw), nil
}

func dataVisualizationManifestDigestBody(manifest DataVisualizationManifest) dataVisualizationManifestBody {
	return dataVisualizationManifestBody{
		SchemaVersion: manifest.SchemaVersion, CompilerVersion: manifest.CompilerVersion, Format: manifest.Format,
		SourceSHA256: manifest.SourceSHA256, SpecSHA256: manifest.SpecSHA256, SVGSHA256: manifest.SVGSHA256,
		TableSHA256: manifest.TableSHA256, ArtifactSHA256: manifest.ArtifactSHA256,
		Width: manifest.Width, Height: manifest.Height, ColumnCount: manifest.ColumnCount, RowCount: manifest.RowCount,
		SeriesCount: manifest.SeriesCount, PointCount: manifest.PointCount,
	}
}

func validateDataVisualizationTable(table DataVisualizationTable) error {
	if len(table.Columns) < 2 || len(table.Columns) > dataVisualizationMaxColumns {
		return fmt.Errorf("visualization table requires 2-%d columns", dataVisualizationMaxColumns)
	}
	if len(table.Rows) == 0 || len(table.Rows) > dataVisualizationMaxRows {
		return fmt.Errorf("visualization table requires 1-%d rows", dataVisualizationMaxRows)
	}
	seen := map[string]bool{}
	totalText := 0
	for index, column := range table.Columns {
		if !dataVisualizationColumnIDPattern.MatchString(column.ID) || seen[column.ID] {
			return fmt.Errorf("visualization column %d has an invalid or duplicate id", index)
		}
		seen[column.ID] = true
		if column.Type != DataVisualizationCategory && column.Type != DataVisualizationNumber {
			return fmt.Errorf("visualization column %s has an unsupported type", column.ID)
		}
		if err := validateDataVisualizationText(column.Label, 80, false); err != nil {
			return fmt.Errorf("visualization column %s label: %w", column.ID, err)
		}
		if err := validateDataVisualizationText(column.Unit, 24, true); err != nil {
			return fmt.Errorf("visualization column %s unit: %w", column.ID, err)
		}
		totalText += utf8.RuneCountInString(column.Label) + utf8.RuneCountInString(column.Unit)
	}
	for rowIndex, row := range table.Rows {
		if len(row) != len(table.Columns) {
			return fmt.Errorf("visualization row %d is ragged", rowIndex)
		}
		for columnIndex, cell := range row {
			column := table.Columns[columnIndex]
			if cell.Type != column.Type {
				return fmt.Errorf("visualization cell %d:%d type mismatch", rowIndex, columnIndex)
			}
			switch column.Type {
			case DataVisualizationCategory:
				if cell.Number != 0 {
					return fmt.Errorf("visualization category cell %d:%d carries numeric data", rowIndex, columnIndex)
				}
				if err := validateDataVisualizationText(cell.Text, 120, false); err != nil {
					return fmt.Errorf("visualization category cell %d:%d: %w", rowIndex, columnIndex, err)
				}
				totalText += utf8.RuneCountInString(cell.Text)
			case DataVisualizationNumber:
				if cell.Text != "" || math.IsNaN(cell.Number) || math.IsInf(cell.Number, 0) || math.Abs(cell.Number) > 9e15 {
					return fmt.Errorf("visualization numeric cell %d:%d is invalid", rowIndex, columnIndex)
				}
			}
		}
	}
	if totalText > 20_000 {
		return errors.New("visualization source text exceeds the limit")
	}
	return nil
}

func validateDataVisualizationSpec(table DataVisualizationTable, spec DataVisualizationSpec) error {
	if spec.Kind != DataVisualizationBar && spec.Kind != DataVisualizationLine && spec.Kind != DataVisualizationScatter {
		return errors.New("unsupported visualization chart kind")
	}
	if err := validateDataVisualizationText(spec.Title, 120, false); err != nil {
		return fmt.Errorf("visualization title: %w", err)
	}
	if spec.Width < 320 || spec.Width > 1600 || spec.Height < 240 || spec.Height > 1200 {
		return errors.New("visualization dimensions are outside the supported bounds")
	}
	if !dataVisualizationColumnIDPattern.MatchString(spec.XColumnID) {
		return errors.New("visualization x axis is invalid")
	}
	if len(spec.SeriesColumnIDs) == 0 || len(spec.SeriesColumnIDs) > dataVisualizationMaxSeries {
		return fmt.Errorf("visualization requires 1-%d series", dataVisualizationMaxSeries)
	}
	if len(table.Columns) != len(spec.SeriesColumnIDs)+1 || table.Columns[0].ID != spec.XColumnID {
		return errors.New("visualization table must contain the x axis followed by exactly the selected series")
	}
	seriesSeen := map[string]bool{}
	for index, id := range spec.SeriesColumnIDs {
		if !dataVisualizationColumnIDPattern.MatchString(id) || id == spec.XColumnID || seriesSeen[id] {
			return errors.New("visualization series selection is invalid")
		}
		seriesSeen[id] = true
		if table.Columns[index+1].ID != id || table.Columns[index+1].Type != DataVisualizationNumber {
			return errors.New("visualization series must reference ordered numeric columns")
		}
	}
	switch spec.Kind {
	case DataVisualizationBar:
		if table.Columns[0].Type != DataVisualizationCategory || len(table.Rows) > dataVisualizationMaxBarRows {
			return fmt.Errorf("bar charts require a category x axis and at most %d rows", dataVisualizationMaxBarRows)
		}
		seen := map[string]bool{}
		for _, row := range table.Rows {
			category := row[0].Text
			if seen[category] {
				return errors.New("bar chart categories must be unique")
			}
			seen[category] = true
		}
	case DataVisualizationLine:
		if table.Columns[0].Type != DataVisualizationNumber {
			return errors.New("line charts require an explicit numeric x axis")
		}
		for index := 1; index < len(table.Rows); index++ {
			if !(table.Rows[index][0].Number > table.Rows[index-1][0].Number) {
				return errors.New("line chart x values must be strictly increasing")
			}
		}
	case DataVisualizationScatter:
		if table.Columns[0].Type != DataVisualizationNumber {
			return errors.New("scatter charts require a numeric x axis")
		}
	}
	return nil
}

func validateDataVisualizationText(value string, maxRunes int, allowEmpty bool) error {
	if !utf8.ValidString(value) {
		return errors.New("text is not valid UTF-8")
	}
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return errors.New("text is required")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return errors.New("text exceeds the length limit")
	}
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, "<>") || strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "javascript:") || strings.Contains(lower, "data:") {
		return errors.New("markup and external-resource text are unsupported")
	}
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return errors.New("text contains a forbidden control character")
		}
	}
	return nil
}

func isNonZeroHexDigest(value string) bool {
	if len(value) != 64 || !isHexDigest(value) {
		return false
	}
	return strings.Trim(value, "0") != ""
}

func renderDataVisualizationTable(table DataVisualizationTable, spec DataVisualizationSpec) []byte {
	var out strings.Builder
	out.WriteString(`<table data-stride-visualization="v1"><caption>`)
	out.WriteString(html.EscapeString(spec.Title))
	out.WriteString(`</caption><thead><tr>`)
	for _, column := range table.Columns {
		out.WriteString(`<th scope="col">`)
		out.WriteString(html.EscapeString(dataVisualizationColumnDisplay(column)))
		out.WriteString(`</th>`)
	}
	out.WriteString(`</tr></thead><tbody>`)
	for _, row := range table.Rows {
		out.WriteString(`<tr>`)
		for index, cell := range row {
			if index == 0 {
				out.WriteString(`<th scope="row">`)
			} else {
				out.WriteString(`<td>`)
			}
			if cell.Type == DataVisualizationCategory {
				out.WriteString(html.EscapeString(cell.Text))
			} else {
				out.WriteString(html.EscapeString(formatDataVisualizationNumber(cell.Number)))
			}
			if index == 0 {
				out.WriteString(`</th>`)
			} else {
				out.WriteString(`</td>`)
			}
		}
		out.WriteString(`</tr>`)
	}
	out.WriteString(`</tbody></table>`)
	return []byte(out.String())
}

func renderDataVisualizationSVG(table DataVisualizationTable, spec DataVisualizationSpec, specDigest string) ([]byte, error) {
	const left, right, top, bottom = 72.0, 36.0, 62.0, 70.0
	plotWidth := float64(spec.Width) - left - right
	plotHeight := float64(spec.Height) - top - bottom
	xMin, xMax, yMin, yMax := dataVisualizationDomains(table, spec)
	id := "dv-" + specDigest[:12]
	description := fmt.Sprintf("%s chart. X axis %s. %d series and %d data points.", spec.Kind, dataVisualizationColumnDisplay(table.Columns[0]), len(spec.SeriesColumnIDs), len(table.Rows)*len(spec.SeriesColumnIDs))
	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="%s-title %s-desc">`, spec.Width, spec.Height, spec.Width, spec.Height, id, id)
	fmt.Fprintf(&out, `<title id="%s-title">%s</title><desc id="%s-desc">%s</desc>`, id, html.EscapeString(spec.Title), id, html.EscapeString(description))
	out.WriteString(`<rect x="0" y="0" width="100%" height="100%" fill="#ffffff"/>`)
	fmt.Fprintf(&out, `<text x="%s" y="32" text-anchor="middle" font-family="system-ui, sans-serif" font-size="18" font-weight="600" fill="#111827">%s</text>`, formatDataVisualizationCoordinate(float64(spec.Width)/2), html.EscapeString(spec.Title))
	// Fixed five-tick axes avoid locale, font-measurement, or map-order input.
	for tick := 0; tick < 5; tick++ {
		ratio := float64(tick) / 4
		y := top + plotHeight*(1-ratio)
		value := yMin + (yMax-yMin)*ratio
		fmt.Fprintf(&out, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#e5e7eb" stroke-width="1"/>`, formatDataVisualizationCoordinate(left), formatDataVisualizationCoordinate(y), formatDataVisualizationCoordinate(left+plotWidth), formatDataVisualizationCoordinate(y))
		fmt.Fprintf(&out, `<text x="%s" y="%s" text-anchor="end" font-family="system-ui, sans-serif" font-size="11" fill="#374151">%s</text>`, formatDataVisualizationCoordinate(left-8), formatDataVisualizationCoordinate(y+4), html.EscapeString(formatDataVisualizationNumber(value)))
	}
	fmt.Fprintf(&out, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#374151" stroke-width="1.5"/>`, formatDataVisualizationCoordinate(left), formatDataVisualizationCoordinate(top+plotHeight), formatDataVisualizationCoordinate(left+plotWidth), formatDataVisualizationCoordinate(top+plotHeight))
	fmt.Fprintf(&out, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="#374151" stroke-width="1.5"/>`, formatDataVisualizationCoordinate(left), formatDataVisualizationCoordinate(top), formatDataVisualizationCoordinate(left), formatDataVisualizationCoordinate(top+plotHeight))

	palette := []string{"#155eef", "#b42318", "#067647", "#9333ea", "#b54708", "#0e7490"}
	dashes := []string{"none", "8 4", "3 3", "10 3 2 3", "2 5", "12 4"}
	xScale := func(value float64) float64 { return left + (value-xMin)/(xMax-xMin)*plotWidth }
	yScale := func(value float64) float64 { return top + (yMax-value)/(yMax-yMin)*plotHeight }

	switch spec.Kind {
	case DataVisualizationBar:
		groupWidth := plotWidth / float64(len(table.Rows))
		barWidth := groupWidth * 0.78 / float64(len(spec.SeriesColumnIDs))
		zeroY := yScale(0)
		for rowIndex, row := range table.Rows {
			for seriesIndex := range spec.SeriesColumnIDs {
				value := row[seriesIndex+1].Number
				if value == 0 {
					continue
				}
				x := left + float64(rowIndex)*groupWidth + groupWidth*0.11 + float64(seriesIndex)*barWidth
				y := yScale(value)
				height := math.Abs(zeroY - y)
				if height < 1 {
					height = 1
				}
				if value < 0 {
					y = zeroY
				}
				fmt.Fprintf(&out, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s" stroke="#111827" stroke-width="1" stroke-dasharray="%s"/>`, formatDataVisualizationCoordinate(x), formatDataVisualizationCoordinate(y), formatDataVisualizationCoordinate(barWidth), formatDataVisualizationCoordinate(height), palette[seriesIndex], dashes[seriesIndex])
			}
			fmt.Fprintf(&out, `<text x="%s" y="%s" text-anchor="middle" font-family="system-ui, sans-serif" font-size="10" fill="#374151">%s</text>`, formatDataVisualizationCoordinate(left+(float64(rowIndex)+0.5)*groupWidth), formatDataVisualizationCoordinate(top+plotHeight+18), html.EscapeString(row[0].Text))
		}
	case DataVisualizationLine:
		for seriesIndex := range spec.SeriesColumnIDs {
			var points strings.Builder
			for rowIndex, row := range table.Rows {
				if rowIndex > 0 {
					points.WriteByte(' ')
				}
				fmt.Fprintf(&points, "%s,%s", formatDataVisualizationCoordinate(xScale(row[0].Number)), formatDataVisualizationCoordinate(yScale(row[seriesIndex+1].Number)))
			}
			fmt.Fprintf(&out, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.5" stroke-dasharray="%s"/>`, points.String(), palette[seriesIndex], dashes[seriesIndex])
		}
	case DataVisualizationScatter:
		for seriesIndex := range spec.SeriesColumnIDs {
			for _, row := range table.Rows {
				x, y := xScale(row[0].Number), yScale(row[seriesIndex+1].Number)
				switch seriesIndex % 3 {
				case 0:
					fmt.Fprintf(&out, `<circle cx="%s" cy="%s" r="4" fill="%s" stroke="#111827"/>`, formatDataVisualizationCoordinate(x), formatDataVisualizationCoordinate(y), palette[seriesIndex])
				case 1:
					fmt.Fprintf(&out, `<rect x="%s" y="%s" width="8" height="8" fill="%s" stroke="#111827"/>`, formatDataVisualizationCoordinate(x-4), formatDataVisualizationCoordinate(y-4), palette[seriesIndex])
				case 2:
					fmt.Fprintf(&out, `<polygon points="%s,%s %s,%s %s,%s %s,%s" fill="%s" stroke="#111827"/>`, formatDataVisualizationCoordinate(x), formatDataVisualizationCoordinate(y-5), formatDataVisualizationCoordinate(x+5), formatDataVisualizationCoordinate(y), formatDataVisualizationCoordinate(x), formatDataVisualizationCoordinate(y+5), formatDataVisualizationCoordinate(x-5), formatDataVisualizationCoordinate(y), palette[seriesIndex])
				}
			}
		}
	}

	legendX := left
	for seriesIndex, column := range table.Columns[1:] {
		fmt.Fprintf(&out, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="3" stroke-dasharray="%s"/>`, formatDataVisualizationCoordinate(legendX), formatDataVisualizationCoordinate(float64(spec.Height)-24), formatDataVisualizationCoordinate(legendX+20), formatDataVisualizationCoordinate(float64(spec.Height)-24), palette[seriesIndex], dashes[seriesIndex])
		fmt.Fprintf(&out, `<text x="%s" y="%s" font-family="system-ui, sans-serif" font-size="11" fill="#111827">%s</text>`, formatDataVisualizationCoordinate(legendX+26), formatDataVisualizationCoordinate(float64(spec.Height)-20), html.EscapeString(dataVisualizationColumnDisplay(column)))
		legendX += 26 + float64(utf8.RuneCountInString(dataVisualizationColumnDisplay(column))*7) + 18
	}
	fmt.Fprintf(&out, `<text x="%s" y="%s" text-anchor="middle" font-family="system-ui, sans-serif" font-size="12" fill="#111827">%s</text>`, formatDataVisualizationCoordinate(left+plotWidth/2), formatDataVisualizationCoordinate(float64(spec.Height)-44), html.EscapeString(dataVisualizationColumnDisplay(table.Columns[0])))
	out.WriteString(`</svg>`)
	return []byte(out.String()), nil
}

func dataVisualizationDomains(table DataVisualizationTable, spec DataVisualizationSpec) (xMin, xMax, yMin, yMax float64) {
	xMin, xMax = 0, float64(len(table.Rows)-1)
	if spec.Kind != DataVisualizationBar {
		xMin, xMax = table.Rows[0][0].Number, table.Rows[0][0].Number
	}
	yMin, yMax = table.Rows[0][1].Number, table.Rows[0][1].Number
	for rowIndex, row := range table.Rows {
		if spec.Kind != DataVisualizationBar {
			xMin = math.Min(xMin, row[0].Number)
			xMax = math.Max(xMax, row[0].Number)
		} else if rowIndex == len(table.Rows)-1 {
			xMax = float64(len(table.Rows))
		}
		for seriesIndex := range spec.SeriesColumnIDs {
			yMin = math.Min(yMin, row[seriesIndex+1].Number)
			yMax = math.Max(yMax, row[seriesIndex+1].Number)
		}
	}
	if spec.Kind == DataVisualizationBar {
		yMin = math.Min(yMin, 0)
		yMax = math.Max(yMax, 0)
	}
	if xMin == xMax {
		xMin, xMax = xMin-1, xMax+1
	}
	if yMin == yMax {
		padding := math.Max(1, math.Abs(yMin)*0.05)
		yMin, yMax = yMin-padding, yMax+padding
	} else if spec.Kind != DataVisualizationBar {
		padding := (yMax - yMin) * 0.05
		yMin, yMax = yMin-padding, yMax+padding
	}
	return xMin, xMax, yMin, yMax
}

func dataVisualizationColumnDisplay(column DataVisualizationColumn) string {
	if column.Unit == "" {
		return column.Label
	}
	return column.Label + " (" + column.Unit + ")"
}

func formatDataVisualizationCoordinate(value float64) string {
	if math.Abs(value) < 0.0005 {
		value = 0
	}
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func formatDataVisualizationNumber(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}
