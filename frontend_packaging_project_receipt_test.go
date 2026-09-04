package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// projectReceiptArrowBody returns the body of an arrow function, brace-matched
// from the first `{` after the signature. The picker's `apply` is a closure
// inside chooseArtifactProject, so it cannot be reached by a top-level scan.
func projectReceiptArrowBody(source, signature string) string {
	start := strings.Index(source, signature)
	if start < 0 {
		return ""
	}
	rest := source[start+len(signature):]
	open := strings.Index(rest, "{")
	if open < 0 {
		return ""
	}
	depth := 0
	for index := open; index < len(rest); index++ {
		switch rest[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[open : index+1]
			}
		}
	}
	return ""
}

// TestIndexProjectTagToastReportsTheFilingTheServerActuallyDid runs the project
// picker's own apply() against the three receipts POST
// /assistant/artifacts/project answers with.
//
// Tagging and Drive filing are two different outcomes. artifact_projects.go
// fences the Drive half twice — a private deliverable never mints
// `Projects/<name>`, and a folder this member did not create is never written
// into — so those tags answer 200 with project set but folderId "" and
// saved/moved false (pinned server-side by
// TestArtifactProjectTagFencesPrivateCodenamesAndOtherMembersFolders). Reading
// only `project` made the toast claim "Filed under Projects/<name>" for every
// one of them, sending people to a Drive folder that holds no row for their
// file. folderId is the filing proof, so folderId drives the copy.
func TestIndexProjectTagToastReportsTheFilingTheServerActuallyDid(t *testing.T) {
	raw, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	picker := projectReceiptArrowBody(html, "async function chooseArtifactProject(artifactId, trigger, options = {})")
	if picker == "" {
		t.Fatal("chooseArtifactProject missing")
	}
	apply := projectReceiptArrowBody(picker, "const apply = async name =>")
	if apply == "" {
		t.Fatal("the project picker's apply() closure missing")
	}
	if !strings.Contains(apply, "payload?.folderId") {
		t.Fatalf("apply() must read the server's filing receipt, not just the project name:\n%s", apply)
	}

	script := `const toasts=[];
const showToast=toast=>toasts.push(toast);
let opened=null;
const openDriveFolder=(id,name)=>{opened={id,name}};
const appShell={dataset:{tool:'files'}};
const loadStudioProjects=()=>{};
const artifactId='os-artifact-1';
const options={};
let receipt=null;
const setArtifactProject=async()=>{if(receipt instanceof Error)throw receipt;return receipt};
const apply=async name=>` + apply + `;
(async()=>{
  const run=async(payload,name)=>{toasts.length=0;opened=null;receipt=payload;await apply(name);return {toast:toasts[toasts.length-1],opened}};
  const out={};
  out.filed=await run({ok:true,project:'Northstar',folderId:'folder-northstar',fileId:'os-artifact-1',saved:true,moved:true},'Northstar');
  out.tagOnly=await run({ok:true,project:'Northstar',folderId:'',fileId:'',saved:false,moved:false},'Northstar');
  out.privateTag=await run({ok:true,project:'Chimera',folderId:'',fileId:'',saved:false,moved:false},'Chimera');
  out.removed=await run({ok:true,project:'',folderId:'',fileId:'',saved:false,moved:false},'');
  console.log(JSON.stringify(out));
})().catch(error=>{console.error(error);process.exit(1)});`

	output, err := exec.Command("node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("project receipt harness: %v\n%s", err, output)
	}
	var result map[string]struct {
		Toast struct {
			Text   string `json:"text"`
			Kind   string `json:"kind"`
			Action *struct {
				Label string `json:"label"`
			} `json:"action"`
		} `json:"toast"`
		Opened *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"opened"`
	}
	line := strings.TrimSpace(string(output))
	if index := strings.LastIndex(line, "\n"); index >= 0 {
		line = strings.TrimSpace(line[index+1:])
	}
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("decode harness output: %v\n%s", err, output)
	}

	filed := result["filed"]
	if filed.Toast.Text != "Filed under Projects/Northstar" || filed.Toast.Kind != "done" || filed.Toast.Action == nil {
		t.Fatalf("a real filing lost its receipt or its Open folder action: %+v", filed.Toast)
	}

	// The two fenced cases are tagged but NOT filed. Neither may claim a Drive
	// filing, and neither may offer to open a folder this member cannot use.
	for _, name := range []string{"tagOnly", "privateTag"} {
		toast := result[name].Toast
		if strings.Contains(toast.Text, "Filed under") {
			t.Errorf("%s claimed a Drive filing the server refused: %q", name, toast.Text)
		}
		if !strings.Contains(toast.Text, "not filed in Drive") {
			t.Errorf("%s must say the tag landed but the Drive filing did not: %q", name, toast.Text)
		}
		if toast.Kind == "done" {
			t.Errorf("%s reported a completed filing: kind=%q", name, toast.Kind)
		}
		if toast.Action != nil {
			t.Errorf("%s offered %q for a folder that was never written", name, toast.Action.Label)
		}
	}
	if text := result["tagOnly"].Toast.Text; !strings.Contains(text, "Northstar") {
		t.Errorf("the degraded toast must still name the project it tagged: %q", text)
	}
	if text := result["privateTag"].Toast.Text; !strings.Contains(text, "Chimera") {
		t.Errorf("the degraded toast must still name the project it tagged: %q", text)
	}

	removed := result["removed"].Toast
	if removed.Text != "Removed from its project" || removed.Kind != "done" || removed.Action != nil {
		t.Fatalf("clearing a project must still read as a completed removal: %+v", removed)
	}
}
