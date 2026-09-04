package business

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestBusinessActionLostAckAndCrossBusinessKey(t *testing.T) {
	s, _, _ := testStore(t)
	ctx := context.Background()
	f := makeFixture(t, s)
	in := BusinessAction{IdempotencyKey: uuid.NewString(), BusinessID: f.result.Business.ID, ExpectedRevision: f.result.Business.Revision, Action: "pause"}
	first, err := s.UpdateBusinessAction(ctx, f.scope, in)
	if err != nil || first.Status != "paused" {
		t.Fatalf("pause: %+v %v", first, err)
	}
	retry, err := s.UpdateBusinessAction(ctx, f.scope, in)
	if err != nil || retry != first {
		t.Fatalf("lost-ack retry: %+v %v", retry, err)
	}
	in.BusinessID = "biz_some_other_business"
	if _, err = s.UpdateBusinessAction(ctx, f.scope, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-business replay: %v", err)
	}
	in = BusinessAction{IdempotencyKey: uuid.NewString(), BusinessID: first.ID, ExpectedRevision: first.Revision, Action: "resume"}
	resumed, err := s.UpdateBusinessAction(ctx, f.scope, in)
	if err != nil || resumed.Status != "active" {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	in = BusinessAction{IdempotencyKey: uuid.NewString(), BusinessID: first.ID, ExpectedRevision: first.Revision, Action: "update_policy", Leadership: "human_ceo", AuthorityPreset: "advise"}
	if _, err = s.UpdateBusinessAction(ctx, f.scope, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale policy: %v", err)
	}
}
