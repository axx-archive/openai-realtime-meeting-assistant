package main

import (
	"fmt"
	"strings"
	"testing"
)

func packagingGeneratedSceneTestText(id, text string, x, y, width, height int, extra string) string {
	return fmt.Sprintf(`<div data-deck-element="%s" data-deck-type="text"%s style="position:absolute;left:%dpx;top:%dpx;width:%dpx;height:%dpx;z-index:2;opacity:1;transform:rotate(0deg);font-size:48px;font-family:Arial;font-weight:700;color:#ffffff;text-align:left;line-height:1.05;letter-spacing:normal">%s</div>`, id, extra, x, y, width, height, text)
}

func packagingGeneratedSceneGoodHTML() string {
	return `<!doctype html><html><body><div id="stage">` +
		`<section class="pg on" data-deck-slide="cover" style="background:#101014">` +
		`<div data-deck-element="cover-bg" data-deck-type="shape" style="position:absolute;left:0px;top:0px;width:1920px;height:1080px;z-index:0;opacity:1;transform:rotate(0deg);background:#101014"></div>` +
		packagingGeneratedSceneTestText("cover-title", "Locked Cover", 120, 120, 1200, 180, "") +
		packagingGeneratedSceneTestText("cover-kicker", "A decisive opening", 120, 360, 900, 60, "") +
		`<div class="notes" hidden>Open with the decision.</div></section>` +
		`<section class="pg" data-deck-slide="proof" style="background:#1b3028">` +
		packagingGeneratedSceneTestText("proof-title", "Locked Proof", 120, 120, 800, 150, "") +
		packagingGeneratedSceneTestText("proof-body", "The market moved", 120, 330, 900, 200, "") +
		packagingGeneratedSceneTestText("proof-number", "42%", 1250, 300, 400, 300, "") +
		`<div class="notes" hidden>Land the proof.</div></section></div></body></html>`
}

func TestPackagingGeneratedSceneQualityGoodVariedScene(t *testing.T) {
	if err := validatePackagingGeneratedScene(nil, nil, packagingGeneratedSceneGoodHTML(), nil); err != nil {
		t.Fatalf("good generated scene: %v", err)
	}
}

func TestPackagingGeneratedSceneQualityGeometryAndMappingAdversaries(t *testing.T) {
	good := packagingGeneratedSceneGoodHTML()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "off canvas",
			source: strings.Replace(good, "left:0px;top:0px;width:1920px", "left:-1px;top:0px;width:1920px", 1),
			want:   "outside the 1920x1080 canvas",
		},
		{
			name:   "authored text outside safe zone",
			source: strings.Replace(good, "left:120px;top:120px;width:1200px", "left:80px;top:120px;width:1200px", 1),
			want:   "96px authored-text safe zone",
		},
		{
			name:   "text near collision",
			source: strings.Replace(good, "left:120px;top:360px;width:900px", "left:120px;top:320px;width:900px", 1),
			want:   "lack 24px breathing room",
		},
		{
			name: "one-sided overlap permission",
			source: strings.Replace(
				strings.Replace(good, `data-deck-element="cover-title" data-deck-type="text"`, `data-deck-element="cover-title" data-deck-type="text" data-deck-overlap="allow"`, 1),
				"left:120px;top:360px;width:900px", "left:120px;top:320px;width:900px", 1),
			want: "lack 24px breathing room",
		},
		{
			name:   "visibly empty text",
			source: strings.Replace(good, ">A decisive opening</div>", ">   </div>", 1),
			want:   "visibly empty",
		},
		{
			name:   "transparent text",
			source: strings.Replace(good, "color:#ffffff;text-align:left", "color:transparent;text-align:left", 1),
			want:   "visibly empty",
		},
		{
			name:   "hidden text",
			source: strings.Replace(good, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" hidden`, 1),
			want:   "visibly empty",
		},
		{
			name:   "missing slide mapping",
			source: strings.Replace(good, ` data-deck-slide="proof"`, "", 1),
			want:   "missing data-deck-slide",
		},
		{
			name:   "duplicate slide mapping",
			source: strings.Replace(good, `data-deck-slide="proof"`, `data-deck-slide="cover"`, 1),
			want:   "slide mapping \"cover\" is duplicated",
		},
		{
			name:   "missing element mapping",
			source: strings.Replace(good, ` data-deck-element="proof-body"`, "", 1),
			want:   "not a faithful native import",
		},
		{
			name:   "duplicate element mapping",
			source: strings.Replace(good, `data-deck-element="proof-body"`, `data-deck-element="cover-title"`, 1),
			want:   "element mapping \"cover-title\" is duplicated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePackagingGeneratedScene(nil, nil, test.source, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPackagingGeneratedSceneQualityPermitsOnlyMutualIntentionalOverlap(t *testing.T) {
	source := packagingGeneratedSceneGoodHTML()
	source = strings.Replace(source, `data-deck-element="cover-title" data-deck-type="text"`, `data-deck-element="cover-title" data-deck-type="text" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" data-deck-overlap="allow"`, 1)
	source = strings.Replace(source, "left:120px;top:360px;width:900px", "left:120px;top:320px;width:900px", 1)
	if err := validatePackagingGeneratedScene(nil, nil, source, nil); err != nil {
		t.Fatalf("mutually permitted intentional overlap: %v", err)
	}
}

func TestPackagingGeneratedSceneQualityAllowsOnlyExplicitBackgroundFurnitureOutsideSafeZone(t *testing.T) {
	source := packagingGeneratedSceneGoodHTML()
	source = strings.Replace(source, `data-deck-element="cover-kicker" data-deck-type="text"`, `data-deck-element="cover-kicker" data-deck-type="text" data-deck-furniture="background" aria-hidden="true"`, 1)
	source = strings.Replace(source, "left:120px;top:360px;width:900px", "left:0px;top:360px;width:900px", 1)
	if err := validatePackagingGeneratedScene(nil, nil, source, nil); err != nil {
		t.Fatalf("explicit background furniture: %v", err)
	}

	missingAria := strings.Replace(source, ` aria-hidden="true"`, "", 1)
	err := validatePackagingGeneratedScene(nil, nil, missingAria, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid background-furniture exception") {
		t.Fatalf("error=%v, want two-factor furniture rejection", err)
	}

	invalidValue := strings.Replace(source, `data-deck-furniture="background"`, `data-deck-furniture="headline"`, 1)
	err = validatePackagingGeneratedScene(nil, nil, invalidValue, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid background-furniture exception") {
		t.Fatalf("error=%v, want closed furniture vocabulary", err)
	}
}

func TestPackagingGeneratedSceneQualityBindsLockedCopy(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	copyBody := `{"slides":[{"slide_id":"cover","headline":"Locked Cover","visible_copy":"A decisive opening"},{"slide_id":"proof","headline":"Locked Proof","visible_copy":["The market moved","42%"]}]}`
	copyArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "locked copy", copyBody, scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{Subtasks: []goalSubtask{{ID: "write", ArtifactID: copyArtifact.ID}}}
	if err := validatePackagingGeneratedScene(app, plan, packagingGeneratedSceneGoodHTML(), nil); err != nil {
		t.Fatalf("scene matching locked copy: %v", err)
	}

	t.Run("missing locked slide", func(t *testing.T) {
		source := strings.Replace(packagingGeneratedSceneGoodHTML(), `<section class="pg" data-deck-slide="proof"`, `<section class="pg" data-deck-slide="other"`, 1)
		err := validatePackagingGeneratedScene(app, plan, source, nil)
		if err == nil || !strings.Contains(err.Error(), "locked deck_copy_v3 maps to \"proof\"") {
			t.Fatalf("error=%v, want locked slide mismatch", err)
		}
	})

	t.Run("locked visible copy changed", func(t *testing.T) {
		source := strings.Replace(packagingGeneratedSceneGoodHTML(), "The market moved", "A different assertion", 1)
		err := validatePackagingGeneratedScene(app, plan, source, nil)
		if err == nil || !strings.Contains(err.Error(), "drifted from locked copy") {
			t.Fatalf("error=%v, want locked copy drift", err)
		}
	})
}

func TestPackagingGeneratedSceneQualityBindsComparableLockedLayout(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	layoutBody := `{"slides":[{"slide_id":"cover","elements":[{"id":"cover-title","type":"text","x":120,"y":120,"width":1200,"height":180,"z":2,"opacity":1}]},{"slide_id":"proof","elements":[{"element_id":"proof-number","element_type":"text","x":"1250px","y":300,"width":400,"height":300}]}]}`
	layoutArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "locked layout", layoutBody, scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{Subtasks: []goalSubtask{{ID: "layout_plan", ArtifactID: layoutArtifact.ID}}}
	if err := validatePackagingGeneratedScene(app, plan, packagingGeneratedSceneGoodHTML(), nil); err != nil {
		t.Fatalf("scene matching comparable locked layout: %v", err)
	}

	source := strings.Replace(packagingGeneratedSceneGoodHTML(), "left:1250px;top:300px;width:400px", "left:1240px;top:300px;width:400px", 1)
	err = validatePackagingGeneratedScene(app, plan, source, nil)
	if err == nil || !strings.Contains(err.Error(), "locked layout requires 1250.00") {
		t.Fatalf("error=%v, want locked geometry drift", err)
	}
}

func TestPackagingGeneratedSceneQualityRunsBeforeDraftFiling(t *testing.T) {
	setupAuthTestEnv(t)
	app := newIsolatedKanbanBoardApp(t)
	bad := strings.Replace(packagingGeneratedSceneGoodHTML(), "left:0px;top:0px;width:1920px", "left:-1px;top:0px;width:1920px", 1)
	shipArtifact, _, err := app.createOSArtifactWithMetadata("artifacts", "ship deck", bad, scoutParticipantName, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan := &goalPlan{
		ProcessID: packagingStudioProcessID, ProcessVersion: 5,
		Subtasks: []goalSubtask{{ID: "ship_deck", ArtifactID: shipArtifact.ID, Status: subtaskComplete}},
	}
	_, _, err = compilePackagingStudioDraft(app, plan, "goal-preflight", ProcessStage{})
	if err == nil || !strings.Contains(err.Error(), "generated presentation scene preflight failed") || !strings.Contains(err.Error(), "outside the 1920x1080 canvas") {
		t.Fatalf("compile error=%v, want pre-filing generated scene rejection", err)
	}
	if _, found := app.studioShipDeliverableByContract("goal-preflight", packagingStudioDeckContract); found {
		t.Fatal("bad generated scene was filed before preflight")
	}
}

func TestPackagingGeneratedScenePreflightScope(t *testing.T) {
	if packagingGeneratedScenePreflightRequired(nil) || packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: "other", ProcessVersion: 5}) || packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: packagingStudioProcessID, ProcessVersion: 4}) {
		t.Fatal("generated-scene preflight escaped the Packaging Studio v5 boundary")
	}
	if !packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: packagingStudioProcessID, ProcessVersion: 5}) ||
		!packagingGeneratedScenePreflightRequired(&goalPlan{ProcessID: packagingStudioProcessID, ProcessImplementationRevision: "packaging_studio.runtime.v5"}) {
		t.Fatal("generated-scene preflight did not recognize Packaging Studio v5")
	}
}
