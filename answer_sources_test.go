package main

import "testing"

func sourceMessage(id, author, text string) scoutChatMessageRecord {
	return scoutChatMessageRecord{ID: id, Role: "user", Text: text, AuthorName: author}
}

// The whole point of a source chip is that it is PROVABLE. A chip that merely
// says "this message was in my context window" is the unearned authority the
// design forbids — context is not evidence.
func TestAnswerSourcesCiteAQuotedMessage(t *testing.T) {
	messages := []scoutChatMessageRecord{
		sourceMessage("1", "Dana", "We settled on tiered pricing with a usage ceiling for launch"),
		sourceMessage("2", "Tim", "The office is closed on Monday"),
	}
	sources := groundAnswerInMessages(
		"You settled on tiered pricing with a usage ceiling, per Dana.",
		messages,
		4,
	)
	if len(sources) != 1 || sources[0].MessageID != "1" {
		t.Fatalf("sources = %+v, want only message 1", sources)
	}
	if sources[0].Author != "Dana" {
		t.Fatalf("author = %q, want Dana", sources[0].Author)
	}
}

// An answer grounded in nothing must render with NO chips. The design is
// explicit: "an answer with no sources renders as an answer with no sources,
// visibly, rather than borrowing unearned authority."
func TestAnswerSourcesAreEmptyWhenNothingIsQuoted(t *testing.T) {
	messages := []scoutChatMessageRecord{
		sourceMessage("1", "Dana", "The office is closed on Monday"),
	}
	sources := groundAnswerInMessages("I do not have enough to answer that.", messages, 4)
	if len(sources) != 0 {
		t.Fatalf("sources = %+v, want none", sources)
	}
}

// Stopword runs are not evidence. "and we are going to be" appearing in both
// texts proves nothing, and citing on it would make every answer cite
// everything — which is the same as citing nothing.
func TestAnswerSourcesIgnoreStopwordRuns(t *testing.T) {
	messages := []scoutChatMessageRecord{
		sourceMessage("1", "Dana", "and we are going to be there in the morning"),
	}
	sources := groundAnswerInMessages("and we are going to be fine about it", messages, 4)
	if len(sources) != 0 {
		t.Fatalf("sources = %+v, want none — the overlap is all stopwords", sources)
	}
}

func TestAnswerSourcesIgnoreScoutsOwnMessages(t *testing.T) {
	messages := []scoutChatMessageRecord{
		{ID: "1", Role: "scout", Text: "tiered pricing with a usage ceiling was agreed"},
	}
	sources := groundAnswerInMessages(
		"tiered pricing with a usage ceiling was agreed earlier",
		messages,
		4,
	)
	// Scout citing itself is circular — it proves the model is consistent, not
	// that the claim is grounded in what the team said.
	if len(sources) != 0 {
		t.Fatalf("sources = %+v, want none — Scout must not cite itself", sources)
	}
}

func TestAnswerSourcesMatchDespitePunctuationAndCase(t *testing.T) {
	messages := []scoutChatMessageRecord{
		sourceMessage("1", "Dana", "Ship the tiered pricing model on Tuesday!"),
	}
	sources := groundAnswerInMessages("They ship the TIERED PRICING MODEL on Tuesday.", messages, 4)
	if len(sources) != 1 {
		t.Fatalf("sources = %+v, want 1", sources)
	}
}

// Several messages can genuinely ground one answer, but a wall of chips is
// unreadable — the cap keeps the strongest.
func TestAnswerSourcesAreCappedAndOrderedByStrength(t *testing.T) {
	messages := []scoutChatMessageRecord{
		sourceMessage("1", "Dana", "tiered pricing model launches Tuesday"),
		sourceMessage("2", "Tim", "the migration plan covers every existing customer account"),
		sourceMessage("3", "Erick", "support runbook needs a rewrite before launch day"),
	}
	answer := "tiered pricing model launches Tuesday, and the migration plan covers " +
		"every existing customer account, plus support runbook needs a rewrite before launch day"
	sources := groundAnswerInMessages(answer, messages, 2)
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want the cap of 2", len(sources))
	}
	// The longest provable overlap ranks first.
	if sources[0].MessageID != "2" {
		t.Fatalf("first source = %s, want message 2 (longest overlap)", sources[0].MessageID)
	}
}

func TestAnswerSourcesCarryAQuotedExcerpt(t *testing.T) {
	messages := []scoutChatMessageRecord{
		sourceMessage("1", "Dana", "We settled on tiered pricing with a usage ceiling for launch"),
	}
	sources := groundAnswerInMessages(
		"You settled on tiered pricing with a usage ceiling.",
		messages,
		4,
	)
	if len(sources) != 1 {
		t.Fatalf("sources = %+v", sources)
	}
	// The chip shows WHY it was cited — the matched phrase, so a reader can
	// check the citation without opening the message.
	if sources[0].Quote == "" {
		t.Fatal("source carries no quoted excerpt")
	}
}

func TestAnswerSourcesHandleEmptyInput(t *testing.T) {
	if got := groundAnswerInMessages("", []scoutChatMessageRecord{sourceMessage("1", "D", "hello there friend")}, 4); len(got) != 0 {
		t.Fatalf("sources = %+v, want none for an empty answer", got)
	}
	if got := groundAnswerInMessages("something", nil, 4); len(got) != 0 {
		t.Fatalf("sources = %+v, want none for no messages", got)
	}
}
