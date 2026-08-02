// e10-openai-probe is a deliberately narrow, explicit paid-probe command.
// It does not start the application, source .env files, or print credentials.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10probe"
)

func main() {
	if len(os.Args) < 2 {
		fatal(usage())
	}
	var err error
	switch os.Args[1] {
	case "access":
		err = access(os.Args[2:])
	case "transcribe-file":
		err = transcribe(os.Args[2:])
	case "transcribe-realtime":
		err = transcribeRealtime(os.Args[2:])
	case "responses":
		err = responses(os.Args[2:])
	case "scout-realtime":
		err = scoutRealtime(os.Args[2:])
	default:
		fatal(usage())
	}
	if err != nil {
		fatal(err.Error())
	}
}

func usage() string {
	return "usage: e10-openai-probe <access|transcribe-file|transcribe-realtime|responses|scout-realtime> [flags]"
}

func common(fs *flag.FlagSet) (*string, *string, *string, *string, *string, *bool) {
	candidate := fs.String("candidate-digest", "", "required SHA-256 digest of the candidate git/tree manifest")
	candidateManifest := fs.String("candidate-manifest", "", "required regular non-symlink candidate manifest")
	receiptDir := fs.String("receipt-dir", "", "required new absolute private receipt directory")
	ack := fs.String("acknowledge-paid-probe", "", "required explicit acknowledgement nonce (stored only as a digest)")
	expectedProject := fs.String("expected-project-sha256", "", "SHA-256 of OPENAI_PROJECT_ID; required when that env value is set")
	allowProjectKey := fs.Bool("allow-project-scoped-key", false, "explicitly permit an sk-proj credential when its raw project ID is unavailable")
	return candidate, candidateManifest, receiptDir, ack, expectedProject, allowProjectKey
}

func access(args []string) error {
	fs := flag.NewFlagSet("access", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate, candidateManifest, receiptDir, ack, expectedProject, allowProjectKey := common(fs)
	model := fs.String("model", "", "required allowlisted model name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	receipt, err := e10probe.RunAccess(context.Background(), e10probe.Config{
		CandidateDigest: *candidate, CandidateManifestPath: *candidateManifest, ReceiptDir: *receiptDir, Acknowledgement: *ack,
		APIKey: os.Getenv("OPENAI_API_KEY"), Project: openAIProject(), ExpectedProjectSHA256: *expectedProject, AllowProjectScopedKey: *allowProjectKey, Model: *model,
	})
	printReceipt(receipt.Probe, receipt.Outcome, receipt.Schema, *receiptDir)
	return err
}

func transcribe(args []string) error {
	fs := flag.NewFlagSet("transcribe-file", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate, candidateManifest, receiptDir, ack, expectedProject, allowProjectKey := common(fs)
	audio := fs.String("audio-file", "", "required existing synthetic RIFF/WAVE PCM fixture (maximum 10 seconds)")
	fixtureDigest := fs.String("expected-fixture-sha256", "", "required SHA-256 of the approved fixture bytes")
	reference := fs.String("reference-file", "", "required regular non-symlink UTF-8 reference file")
	referenceDigest := fs.String("expected-reference-sha256", "", "required SHA-256 of the approved reference bytes")
	maxUSD := fs.Float64("max-usd", 0, "required maximum spend admission; hard cap is 0.05")
	if err := fs.Parse(args); err != nil {
		return err
	}
	receipt, err := e10probe.RunTranscribeFile(context.Background(), e10probe.Config{
		CandidateDigest: *candidate, CandidateManifestPath: *candidateManifest, ReceiptDir: *receiptDir, Acknowledgement: *ack,
		APIKey: os.Getenv("OPENAI_API_KEY"), Project: openAIProject(), ExpectedProjectSHA256: *expectedProject, AllowProjectScopedKey: *allowProjectKey, Model: e10probe.TranscribeModel,
		AudioPath: *audio, ExpectedFixtureSHA256: *fixtureDigest, ReferencePath: *reference, ExpectedReferenceSHA256: *referenceDigest, MaxUSD: *maxUSD,
	})
	printReceipt(receipt.Probe, receipt.Outcome, receipt.Schema, *receiptDir)
	return err
}

func transcribeRealtime(args []string) error {
	fs := flag.NewFlagSet("transcribe-realtime", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate, candidateManifest, receiptDir, ack, expectedProject, allowProjectKey := common(fs)
	audio := fs.String("audio-file", "", "required existing synthetic RIFF/WAVE 24 kHz mono PCM fixture (maximum 10 seconds)")
	fixtureDigest := fs.String("expected-fixture-sha256", "", "required SHA-256 of the approved fixture bytes")
	reference := fs.String("reference-file", "", "required regular non-symlink UTF-8 reference file")
	referenceDigest := fs.String("expected-reference-sha256", "", "required SHA-256 of the approved reference bytes")
	segmentID := fs.String("segment-id", "", "required unique URL-safe application segment identifier")
	maxUSD := fs.Float64("max-usd", 0, "required maximum spend admission; hard cap is 0.05")
	if err := fs.Parse(args); err != nil {
		return err
	}
	receipt, err := e10probe.RunRealtimeTranscription(context.Background(), e10probe.RealtimeTranscribeConfig{
		Config: e10probe.Config{
			CandidateDigest: *candidate, CandidateManifestPath: *candidateManifest, ReceiptDir: *receiptDir, Acknowledgement: *ack,
			APIKey: os.Getenv("OPENAI_API_KEY"), Project: openAIProject(), ExpectedProjectSHA256: *expectedProject, AllowProjectScopedKey: *allowProjectKey, Model: e10probe.TranscribeModel,
			AudioPath: *audio, ExpectedFixtureSHA256: *fixtureDigest, ReferencePath: *reference, ExpectedReferenceSHA256: *referenceDigest, MaxUSD: *maxUSD,
		},
		SegmentID: *segmentID,
	})
	printReceipt(receipt.Probe, receipt.Outcome, receipt.Schema, *receiptDir)
	return err
}

func responses(args []string) error {
	fs := flag.NewFlagSet("responses", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate, candidateManifest, receiptDir, ack, expectedProject, allowProjectKey := common(fs)
	model := fs.String("model", "", "required allowlisted GPT-5.6 model name")
	maxUSD := fs.Float64("max-usd", 0, "required maximum spend admission; model-specific hard cap applies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	receipt, err := e10probe.RunResponses(context.Background(), e10probe.Config{
		CandidateDigest: *candidate, CandidateManifestPath: *candidateManifest, ReceiptDir: *receiptDir, Acknowledgement: *ack,
		APIKey: os.Getenv("OPENAI_API_KEY"), Project: openAIProject(), ExpectedProjectSHA256: *expectedProject, AllowProjectScopedKey: *allowProjectKey, Model: *model, MaxUSD: *maxUSD,
	})
	printReceipt("responses", receipt.Outcome, receipt.Schema, *receiptDir)
	return err
}

func scoutRealtime(args []string) error {
	fs := flag.NewFlagSet("scout-realtime", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	candidate, candidateManifest, receiptDir, ack, expectedProject, allowProjectKey := common(fs)
	interactionID := fs.String("interaction-id", "", "required unique URL-safe synthetic interaction identifier")
	maxUSD := fs.Float64("max-usd", 0, "required maximum spend admission; hard cap is 0.05")
	if err := fs.Parse(args); err != nil {
		return err
	}
	receipt, err := e10probe.RunRealtimeScout(context.Background(), e10probe.RealtimeScoutConfig{
		Config: e10probe.Config{
			CandidateDigest: *candidate, CandidateManifestPath: *candidateManifest, ReceiptDir: *receiptDir, Acknowledgement: *ack,
			APIKey: os.Getenv("OPENAI_API_KEY"), Project: openAIProject(), ExpectedProjectSHA256: *expectedProject, AllowProjectScopedKey: *allowProjectKey,
			Model: e10probe.ScoutRealtimeModel, MaxUSD: *maxUSD,
		},
		InteractionID: *interactionID,
	})
	printReceipt(receipt.Probe, receipt.Outcome, receipt.Schema, *receiptDir)
	return err
}

func printReceipt(probe, outcome, schema, dir string) {
	if schema != "" {
		fmt.Printf("probe=%s outcome=%s receipt=%s/receipt.json\n", probe, outcome, dir)
	}
}

func fatal(message string) { fmt.Fprintln(os.Stderr, "e10-openai-probe:", message); os.Exit(1) }

func openAIProject() string {
	return os.Getenv("OPENAI_PROJECT_ID")
}
