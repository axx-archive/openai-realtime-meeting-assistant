package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	ventureWorkbookToolID       = "economics_waterfall"
	ventureWorkbookContract     = "venture_workbook_v1"
	ventureWorkbookMime         = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	ventureWorkbookPreviewKey   = "workbookPreview"
	ventureWorkbookPolicyLocked = "private_no_publish_no_send"
)

type ventureWorkbookSheetPreview struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
}

type ventureWorkbookPreview struct {
	Version      int                           `json:"version"`
	FileName     string                        `json:"fileName"`
	Mime         string                        `json:"mime"`
	SheetCount   int                           `json:"sheetCount"`
	FormulaCount int                           `json:"formulaCount"`
	InputPolicy  string                        `json:"inputPolicy"`
	Sheets       []ventureWorkbookSheetPreview `json:"sheets"`
}

type ventureWorkbookCell struct {
	Ref     string
	Value   string
	Formula string
	Style   int
	Number  bool
}

type ventureWorkbookSheet struct {
	Name    string
	Purpose string
	Rows    [][]ventureWorkbookCell
}

type ventureWorkbookOperationContextKey struct{}

var ventureWorkbookBeforeChatCommitProbe func() error

func normalizeVentureWorkbookOperationID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 16 || len(value) > 96 {
		return "", fmt.Errorf("operationId must be 16-96 letters, numbers, or hyphens")
	}
	for _, character := range value {
		if character != '-' && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return "", fmt.Errorf("operationId must be 16-96 letters, numbers, or hyphens")
		}
	}
	return value, nil
}

func ventureWorkbookOperationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(ventureWorkbookOperationContextKey{}).(string)
	return strings.TrimSpace(value)
}

func ventureWorkbookContext(operationID string) context.Context {
	return context.WithValue(context.Background(), ventureWorkbookOperationContextKey{}, strings.TrimSpace(operationID))
}

func ventureWorkbookSourceMessageID(threadID, userEmail, operationID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(threadID) + "\x00" + normalizeAccountEmail(userEmail) + "\x00" + strings.TrimSpace(operationID) + "\x00" + ventureWorkbookContract))
	return "scout-chat-message-workbook-" + hex.EncodeToString(digest[:16])
}

func ventureWorkbookDefinition(objective string) ([]ventureWorkbookSheet, int) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		objective = "Untitled venture model"
	}
	if len([]rune(objective)) > 240 {
		objective = string([]rune(objective)[:240])
	}
	sheets := []ventureWorkbookSheet{
		{Name: "Read Me", Purpose: "Scope, editing guidance, and the original request", Rows: [][]ventureWorkbookCell{
			{{Ref: "A1", Value: "Venture workbook", Style: 1}},
			{{Ref: "A3", Value: "Request", Style: 2}, {Ref: "B3", Value: objective}},
			{{Ref: "A5", Value: "How to use", Style: 2}},
			{{Ref: "A6", Value: "Enter only known values in blue input cells. Blank inputs are intentional; Scout inferred no financial facts."}},
			{{Ref: "A7", Value: "Review formulas and source every material assumption before using this workbook for a decision."}},
		}},
		{Name: "Assumptions", Purpose: "Editable operating and financing inputs", Rows: [][]ventureWorkbookCell{
			{{Ref: "A1", Value: "Assumption", Style: 1}, {Ref: "B1", Value: "Value", Style: 1}, {Ref: "C1", Value: "Unit", Style: 1}, {Ref: "D1", Value: "Source / note", Style: 1}},
			{{Ref: "A2", Value: "Starting monthly revenue"}, {Ref: "B2", Style: 3}, {Ref: "C2", Value: "currency"}},
			{{Ref: "A3", Value: "Monthly growth"}, {Ref: "B3", Style: 4}, {Ref: "C3", Value: "%"}},
			{{Ref: "A4", Value: "Gross margin"}, {Ref: "B4", Style: 4}, {Ref: "C4", Value: "%"}},
			{{Ref: "A5", Value: "Monthly operating expense"}, {Ref: "B5", Style: 3}, {Ref: "C5", Value: "currency"}},
			{{Ref: "A6", Value: "Opening cash"}, {Ref: "B6", Style: 3}, {Ref: "C6", Value: "currency"}},
			{{Ref: "A7", Value: "Pre-money valuation"}, {Ref: "B7", Style: 3}, {Ref: "C7", Value: "currency"}},
			{{Ref: "A8", Value: "New investment"}, {Ref: "B8", Style: 3}, {Ref: "C8", Value: "currency"}},
		}},
		{Name: "Operating Model", Purpose: "Twelve-month formula-driven revenue, margin, burn, and cash view"},
		{Name: "Ownership", Purpose: "Illustrative financing ownership waterfall from user-entered values", Rows: [][]ventureWorkbookCell{
			{{Ref: "A1", Value: "Stakeholder", Style: 1}, {Ref: "B1", Value: "Pre-financing %", Style: 1}, {Ref: "C1", Value: "Post-financing %", Style: 1}},
			{{Ref: "A2", Value: "Existing holders"}, {Ref: "B2", Value: "1", Number: true, Style: 4}, {Ref: "C2", Formula: "IFERROR(B2*Assumptions!B7/(Assumptions!B7+Assumptions!B8),0)", Style: 4}},
			{{Ref: "A3", Value: "New investors"}, {Ref: "C3", Formula: "IFERROR(Assumptions!B8/(Assumptions!B7+Assumptions!B8),0)", Style: 4}},
			{{Ref: "A5", Value: "Check", Style: 2}, {Ref: "C5", Formula: "SUM(C2:C3)", Style: 4}},
		}},
		{Name: "Sources", Purpose: "Evidence register for every material assumption", Rows: [][]ventureWorkbookCell{
			{{Ref: "A1", Value: "Assumption", Style: 1}, {Ref: "B1", Value: "Source URL / document", Style: 1}, {Ref: "C1", Value: "As-of date", Style: 1}, {Ref: "D1", Value: "Verification status", Style: 1}},
			{{Ref: "A2", Value: "Add one row per material input"}, {Ref: "D2", Value: "unverified"}},
		}},
	}
	formulaCount := 0
	model := &sheets[2]
	model.Rows = append(model.Rows, []ventureWorkbookCell{{Ref: "A1", Value: "Month", Style: 1}, {Ref: "B1", Value: "Revenue", Style: 1}, {Ref: "C1", Value: "Gross profit", Style: 1}, {Ref: "D1", Value: "Operating expense", Style: 1}, {Ref: "E1", Value: "Net cash flow", Style: 1}, {Ref: "F1", Value: "Ending cash", Style: 1}})
	for month := 1; month <= 12; month++ {
		row := month + 1
		revenue := "Assumptions!B2"
		if month > 1 {
			revenue = fmt.Sprintf("B%d*(1+Assumptions!B3)", row-1)
		}
		cash := "Assumptions!B6+E2"
		if month > 1 {
			cash = fmt.Sprintf("F%d+E%d", row-1, row)
		}
		model.Rows = append(model.Rows, []ventureWorkbookCell{
			{Ref: fmt.Sprintf("A%d", row), Value: strconv.Itoa(month), Number: true},
			{Ref: fmt.Sprintf("B%d", row), Formula: revenue, Style: 3},
			{Ref: fmt.Sprintf("C%d", row), Formula: fmt.Sprintf("B%d*Assumptions!B4", row), Style: 3},
			{Ref: fmt.Sprintf("D%d", row), Formula: "Assumptions!B5", Style: 3},
			{Ref: fmt.Sprintf("E%d", row), Formula: fmt.Sprintf("C%d-D%d", row, row), Style: 3},
			{Ref: fmt.Sprintf("F%d", row), Formula: cash, Style: 3},
		})
		formulaCount += 5
	}
	formulaCount += 3
	return sheets, formulaCount
}

func xmlText(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

func ventureWorkbookSheetXML(sheet ventureWorkbookSheet) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"/></sheetViews><sheetFormatPr defaultRowHeight="15"/><cols><col min="1" max="1" width="28" customWidth="1"/><col min="2" max="6" width="20" customWidth="1"/></cols><sheetData>`)
	for rowIndex, row := range sheet.Rows {
		body.WriteString(`<row r="` + strconv.Itoa(rowIndex+1) + `">`)
		for _, cell := range row {
			style := ""
			if cell.Style > 0 {
				style = ` s="` + strconv.Itoa(cell.Style) + `"`
			}
			switch {
			case cell.Formula != "":
				body.WriteString(`<c r="` + cell.Ref + `"` + style + `><f>` + xmlText(cell.Formula) + `</f><v>0</v></c>`)
			case cell.Number:
				body.WriteString(`<c r="` + cell.Ref + `"` + style + `><v>` + xmlText(cell.Value) + `</v></c>`)
			case cell.Value == "":
				body.WriteString(`<c r="` + cell.Ref + `"` + style + `/>`)
			default:
				body.WriteString(`<c r="` + cell.Ref + `" t="inlineStr"` + style + `><is><t xml:space="preserve">` + xmlText(cell.Value) + `</t></is></c>`)
			}
		}
		body.WriteString(`</row>`)
	}
	body.WriteString(`</sheetData><sheetProtection sheet="0"/><pageMargins left="0.7" right="0.7" top="0.75" bottom="0.75" header="0.3" footer="0.3"/></worksheet>`)
	return body.String()
}

func buildVentureWorkbookXLSX(objective string) ([]byte, ventureWorkbookPreview, error) {
	sheets, formulaCount := ventureWorkbookDefinition(objective)
	preview := ventureWorkbookPreview{Version: 1, FileName: "venture-workbook.xlsx", Mime: ventureWorkbookMime, SheetCount: len(sheets), FormulaCount: formulaCount, InputPolicy: "blank_user_inputs_no_inferred_financial_facts"}
	for _, sheet := range sheets {
		preview.Sheets = append(preview.Sheets, ventureWorkbookSheetPreview{Name: sheet.Name, Purpose: sheet.Purpose})
	}
	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="$#,##0;[Red]-$#,##0"/></numFmts><fonts count="3"><font><sz val="11"/><name val="Aptos"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Aptos"/></font><font><b/><sz val="11"/><name val="Aptos"/></font></fonts><fills count="4"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF17324D"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFDDEBFF"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="5"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/><xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/><xf numFmtId="164" fontId="0" fillId="3" borderId="0" xfId="0" applyNumberFormat="1" applyFill="1"/><xf numFmtId="10" fontId="0" fillId="3" borderId="0" xfId="0" applyNumberFormat="1" applyFill="1"/></cellXfs></styleSheet>`},
	}
	var workbook, rels, contentOverrides strings.Builder
	workbook.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for index, sheet := range sheets {
		id := index + 1
		workbook.WriteString(`<sheet name="` + xmlText(sheet.Name) + `" sheetId="` + strconv.Itoa(id) + `" r:id="rId` + strconv.Itoa(id) + `"/>`)
		rels.WriteString(`<Relationship Id="rId` + strconv.Itoa(id) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet` + strconv.Itoa(id) + `.xml"/>`)
		contentOverrides.WriteString(`<Override PartName="/xl/worksheets/sheet` + strconv.Itoa(id) + `.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`)
		files = append(files, struct{ name, body string }{"xl/worksheets/sheet" + strconv.Itoa(id) + ".xml", ventureWorkbookSheetXML(sheet)})
	}
	workbook.WriteString(`</sheets><calcPr calcId="191029" fullCalcOnLoad="1"/></workbook>`)
	rels.WriteString(`<Relationship Id="rId` + strconv.Itoa(len(sheets)+1) + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`)
	files[0].body += contentOverrides.String() + `</Types>`
	files = append(files, struct{ name, body string }{"xl/workbook.xml", workbook.String()}, struct{ name, body string }{"xl/_rels/workbook.xml.rels", rels.String()})

	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	fixedTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetModTime(fixedTime)
		header.SetMode(0o644)
		part, err := writer.CreateHeader(header)
		if err != nil {
			return nil, preview, err
		}
		if _, err := part.Write([]byte(file.body)); err != nil {
			return nil, preview, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, preview, err
	}
	return output.Bytes(), preview, nil
}

func ventureWorkbookArtifactID(threadID, sourceMessageID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(sourceMessageID) + "\x00" + ventureWorkbookContract))
	return "os-artifact-workbook-" + hex.EncodeToString(digest[:12])
}

func ventureWorkbookPreviewBody(objective string, preview ventureWorkbookPreview) string {
	lines := []string{"# Venture workbook", "", "Workbook ready for **" + strings.TrimSpace(objective) + "**.", "", "## Workbook contents"}
	for _, sheet := range preview.Sheets {
		lines = append(lines, "- **"+sheet.Name+"** — "+sheet.Purpose)
	}
	return strings.Join(append(lines, "", "Inputs are intentionally blank. No financial facts, sources, or investment conclusions were inferred.", "Save to Drive to download the actual `.xlsx` workbook."), "\n")
}

func (app *kanbanBoardApp) createPrivateVentureWorkbook(threadID string, sourceMessageID string, objective string, user *userAccount) (scoutAgentThread, error) {
	return app.createPrivateVentureWorkbookForOperation(threadID, sourceMessageID, objective, user, conversationTurnOperation{})
}

func (app *kanbanBoardApp) createPrivateVentureWorkbookForOperation(threadID string, sourceMessageID string, objective string, user *userAccount, operation conversationTurnOperation) (scoutAgentThread, error) {
	if app == nil || app.memory == nil || user == nil || normalizeAccountEmail(user.Email) == "" {
		return scoutAgentThread{}, fmt.Errorf("workbook generation is unavailable")
	}
	artifactID := ventureWorkbookArtifactID(threadID, sourceMessageID)
	if existing, found := app.osArtifactByID(artifactID); found {
		if artifactType(existing) != artifactTypeWorkbook || existing.Metadata["artifactContract"] != ventureWorkbookContract || existing.Metadata["sourceMessageId"] != strings.TrimSpace(sourceMessageID) || existing.Metadata["originSurface"] != "chat:"+strings.TrimSpace(threadID) || normalizeAccountEmail(existing.Metadata["ownerEmail"]) != normalizeAccountEmail(user.Email) || strings.TrimSpace(existing.Metadata["query"]) != strings.TrimSpace(objective) {
			return scoutAgentThread{}, fmt.Errorf("workbook operation conflicts with an existing artifact")
		}
		if operation.ID != "" && (existing.Metadata["operationId"] != operation.ID || existing.Metadata["operationBodyDigest"] != operation.BodyDigest) {
			return scoutAgentThread{}, fmt.Errorf("workbook operation conflicts with its conversation binding")
		}
		return scoutAgentThread{ID: "agent-thread-workbook-" + strings.TrimPrefix(artifactID, "os-artifact-"), Mode: "artifacts", Query: strings.TrimSpace(objective), Status: artifactStatusComplete, Artifact: existing, Actions: app.osAssistantActions(objective, "artifacts", existing)}, nil
	}
	workbook, preview, err := buildVentureWorkbookXLSX(objective)
	if err != nil {
		return scoutAgentThread{}, fmt.Errorf("build workbook: %w", err)
	}
	ref, err := putBlob(workbook, ventureWorkbookMime)
	if err != nil {
		return scoutAgentThread{}, fmt.Errorf("store workbook: %w", err)
	}
	assets, _ := json.Marshal([]artifactAsset{{Ref: ref, Mime: ventureWorkbookMime, Name: preview.FileName, Kind: "export"}})
	previewJSON, _ := json.Marshal(preview)
	metadata := map[string]string{
		"type": artifactTypeWorkbook, "mode": "artifacts", "status": artifactStatusComplete, "published": "false", "publicationPolicy": ventureWorkbookPolicyLocked,
		"source": "scout_thread", "sourceMessageId": strings.TrimSpace(sourceMessageID), "originSurface": "chat:" + strings.TrimSpace(threadID), "requestedBy": normalizeAccountEmail(user.Email), "ownerEmail": normalizeAccountEmail(user.Email), "visibility": "private",
		"toolTemplate": ventureWorkbookToolID, "artifactContract": ventureWorkbookContract, "reviewGate": "passed", "progressPercent": "100", "providerCalls": "0", "generationMode": "deterministic_local_workbook",
		"threadId": "agent-thread-workbook-" + strings.TrimPrefix(artifactID, "os-artifact-"), "threadQuery": strings.TrimSpace(objective), "threadStatus": artifactStatusComplete,
		artifactAssetsMetadataKey: string(assets), ventureWorkbookPreviewKey: string(previewJSON), "driveFileName": preview.FileName,
	}
	if operation.ID != "" {
		metadata["operationId"] = operation.ID
		metadata["operationBodyDigest"] = operation.BodyDigest
		metadata["originId"] = strings.TrimSpace(threadID)
	}
	artifact, _, _, err := app.createOSArtifactWithIDAndMetadataAcknowledged(artifactID, "artifacts", objective, ventureWorkbookPreviewBody(objective, preview), user.Name, metadata)
	if err != nil {
		return scoutAgentThread{}, err
	}
	return scoutAgentThread{ID: "agent-thread-workbook-" + strings.TrimPrefix(artifactID, "os-artifact-"), Mode: "artifacts", Query: strings.TrimSpace(objective), Status: artifactStatusComplete, Artifact: artifact, Actions: app.osAssistantActions(objective, "artifacts", artifact)}, nil
}

func (app *kanbanBoardApp) replayPrivateVentureWorkbook(ctx context.Context, thread scoutChatThreadRecord, sourceMessageID string, objective string, user *userAccount) (map[string]any, error) {
	messageIndex := scoutChatMessageIndex(thread, sourceMessageID)
	if messageIndex < 0 || strings.TrimSpace(thread.Messages[messageIndex].Text) != objective || normalizeAccountEmail(thread.Messages[messageIndex].AuthorEmail) != normalizeAccountEmail(user.Email) {
		return nil, fmt.Errorf("workbook operation conflicts with an existing message")
	}
	artifactID := ventureWorkbookArtifactID(thread.ID, sourceMessageID)
	artifact, allowed := authorizedArtifactForActions(ctx, user, artifactID, ACLReadContent)
	if !allowed || artifactType(artifact) != artifactTypeWorkbook || artifact.Metadata["sourceMessageId"] != sourceMessageID || artifact.Metadata["originSurface"] != "chat:"+thread.ID {
		return nil, fmt.Errorf("workbook operation is unavailable")
	}
	var answer scoutChatMessageRecord
	for _, message := range thread.Messages {
		if message.Thread != nil && message.CausedByMessageID == sourceMessageID && message.Thread.ArtifactID == artifactID {
			answer = message
			break
		}
	}
	if answer.ID == "" || answer.Thread == nil || answer.Thread.Status != artifactStatusComplete {
		return nil, fmt.Errorf("workbook operation is incomplete")
	}
	agentThread := scoutAgentThread{ID: answer.Thread.ID, Mode: "artifacts", Query: objective, Status: artifactStatusComplete, Artifact: artifact, Actions: app.osAssistantActions(objective, "artifacts", artifact)}
	return map[string]any{"ok": true, "query": objective, "answer": answer, "thread": thread, "agentThread": agentThread, "artifact": artifact, "actions": agentThread.Actions, "providerCalls": 0, "providerExecutionFenced": true, "executionBridge": "deterministic_private_venture_workbook_v1", "replayed": true}, nil
}

func artifactPublicationDisabled(entry meetingMemoryEntry) bool {
	return strings.EqualFold(strings.TrimSpace(entry.Metadata["publicationPolicy"]), ventureWorkbookPolicyLocked)
}
