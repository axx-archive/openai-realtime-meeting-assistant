// Command e10-evidence validates trust-rooted target registries and dual-signed
// E10 evidence packets. It is deliberately read-only and cannot capture media,
// call providers, mutate production, or qualify a release.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-realtime-meeting-assistant/internal/e10evidence"
)

func main() {
	mode := flag.String("mode", "", "registry, corpus, io-pilots, external-matrix, or qualification-result")
	inputPath := flag.String("input", "", "canonical compact JSON packet to validate")
	trustRootsPath := flag.String("trust-roots", "", "explicitly approved trust-root JSON")
	approvedTrustRootSHA256 := flag.String("approved-trust-root-sha256", "", "SHA-256 anchor copied from the separately approved release ledger")
	registryPath := flag.String("registry", "", "canonical target registry JSON (all packet modes)")
	registrySignaturePath := flag.String("registry-signature", "", "detached registry-owner Ed25519 signature in lowercase hex")
	registryPublicKeyPath := flag.String("registry-public-key", "", "registry-owner Ed25519 public key in lowercase hex")
	operatorSignaturePath := flag.String("operator-signature", "", "detached operator Ed25519 signature in lowercase hex")
	operatorPublicKeyPath := flag.String("operator-public-key", "", "operator Ed25519 public key in lowercase hex")
	reviewerSignaturePath := flag.String("reviewer-signature", "", "detached independent-review Ed25519 signature in lowercase hex")
	reviewerPublicKeyPath := flag.String("reviewer-public-key", "", "independent-review Ed25519 public key in lowercase hex")
	sourcePath := flag.String("source", "", "canonical signed source packet (qualification-result mode)")
	sourceKind := flag.String("source-kind", "", "corpus or io_pilots (qualification-result mode)")
	sourceOperatorSignaturePath := flag.String("source-operator-signature", "", "detached source operator signature in lowercase hex (qualification-result mode)")
	sourceOperatorPublicKeyPath := flag.String("source-operator-public-key", "", "source operator public key in lowercase hex (qualification-result mode)")
	sourceReviewerSignaturePath := flag.String("source-reviewer-signature", "", "detached source independent-review signature in lowercase hex (qualification-result mode)")
	sourceReviewerPublicKeyPath := flag.String("source-reviewer-public-key", "", "source independent-review public key in lowercase hex (qualification-result mode)")
	expectedReceiptPath := flag.String("expected-receipt", "", "optional previously emitted receipt that must exactly match a fresh verification")
	consumeLedgerPath := flag.String("consume-ledger", "", "optional private replay-ledger path used to consume the freshly verified receipt exactly once")
	flag.Parse()

	if strings.TrimSpace(*inputPath) == "" || strings.TrimSpace(*trustRootsPath) == "" || strings.TrimSpace(*approvedTrustRootSHA256) == "" {
		fatal(errors.New("-input, -trust-roots, and -approved-trust-root-sha256 are required"))
	}
	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		fatal(err)
	}
	roots, err := readTrustRoots(*trustRootsPath, *approvedTrustRootSHA256)
	if err != nil {
		fatal(err)
	}

	var receipt e10evidence.VerifiedValidationReceipt
	registrySignature, registrySignatureErr := readRequiredHex(*registrySignaturePath, "-registry-signature")
	if registrySignatureErr != nil {
		fatal(registrySignatureErr)
	}
	registryPublicKey, registryPublicKeyErr := readRequiredHex(*registryPublicKeyPath, "-registry-public-key")
	if registryPublicKeyErr != nil {
		fatal(registryPublicKeyErr)
	}
	if *mode == "registry" {
		_, verifiedReceipt, verifyErr := e10evidence.VerifyTargetRegistryReceipt(raw, registrySignature, registryPublicKey, roots)
		if verifyErr != nil {
			fatal(verifyErr)
		}
		receipt = verifiedReceipt
	} else {
		if strings.TrimSpace(*registryPath) == "" {
			fatal(errors.New("-registry is required for every evidence packet"))
		}
		registryRaw, readErr := os.ReadFile(*registryPath)
		if readErr != nil {
			fatal(readErr)
		}
		operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, signatureErr := readPacketSignatureSet(*operatorSignaturePath, *operatorPublicKeyPath, *reviewerSignaturePath, *reviewerPublicKeyPath)
		if signatureErr != nil {
			fatal(signatureErr)
		}

		switch *mode {
		case "corpus":
			_, verifiedReceipt, verifyErr := e10evidence.VerifyCorpusReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, roots)
			if verifyErr != nil {
				fatal(verifyErr)
			}
			receipt = verifiedReceipt
		case "io-pilots":
			_, verifiedReceipt, verifyErr := e10evidence.VerifyPilotReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, roots)
			if verifyErr != nil {
				fatal(verifyErr)
			}
			receipt = verifiedReceipt
		case "external-matrix":
			_, verifiedReceipt, verifyErr := e10evidence.VerifyMatrixReceipt(raw, registryRaw, registrySignature, registryPublicKey, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, roots)
			if verifyErr != nil {
				fatal(verifyErr)
			}
			receipt = verifiedReceipt
		case "qualification-result":
			if strings.TrimSpace(*sourcePath) == "" || (*sourceKind != "corpus" && *sourceKind != "io_pilots") {
				fatal(errors.New("-source and -source-kind=corpus|io_pilots are required for qualification-result mode"))
			}
			if strings.TrimSpace(*expectedReceiptPath) != "" || strings.TrimSpace(*consumeLedgerPath) != "" {
				fatal(errors.New("qualification-result mode emits an import bundle; -expected-receipt and -consume-ledger do not apply"))
			}
			sourceRaw, readErr := os.ReadFile(*sourcePath)
			if readErr != nil {
				fatal(readErr)
			}
			sourceOperatorSignature, sourceOperatorPublicKey, sourceReviewerSignature, sourceReviewerPublicKey, sourceSignatureErr := readPacketSignatureSet(*sourceOperatorSignaturePath, *sourceOperatorPublicKeyPath, *sourceReviewerSignaturePath, *sourceReviewerPublicKeyPath)
			if sourceSignatureErr != nil {
				fatal(sourceSignatureErr)
			}
			result, decodeErr := e10evidence.DecodeCanonicalStrict[e10evidence.QualificationResultPacket](raw)
			if decodeErr != nil {
				fatal(decodeErr)
			}
			bundleRaw, _, buildErr := e10evidence.BuildQualificationImportBundle(result.TenantID, *sourceKind, registryRaw, registrySignature, registryPublicKey, sourceRaw, sourceOperatorSignature, sourceOperatorPublicKey, sourceReviewerSignature, sourceReviewerPublicKey, raw, operatorSignature, operatorPublicKey, reviewerSignature, reviewerPublicKey, roots)
			if buildErr != nil {
				fatal(buildErr)
			}
			fmt.Print(string(bundleRaw))
			return
		default:
			fatal(errors.New("-mode must be registry, corpus, io-pilots, external-matrix, or qualification-result"))
		}
	}

	encoded, err := e10evidence.EncodeReceipt(receipt)
	if err != nil {
		fatal(err)
	}
	if strings.TrimSpace(*expectedReceiptPath) != "" {
		expected, readErr := os.ReadFile(*expectedReceiptPath)
		if readErr != nil {
			fatal(readErr)
		}
		if !bytes.Equal(expected, encoded) {
			fatal(errors.New("-expected-receipt does not exactly match the receipt derived from the original signed source envelope"))
		}
	}
	if strings.TrimSpace(*consumeLedgerPath) != "" {
		consumer, openErr := e10evidence.OpenValidationReceiptConsumer(*consumeLedgerPath)
		if openErr != nil {
			fatal(openErr)
		}
		if consumeErr := consumer.Consume(receipt); consumeErr != nil {
			fatal(consumeErr)
		}
	}
	// Do not append a newline: serialized receipts are exact-byte artifacts and
	// downstream re-verification intentionally rejects formatting drift.
	fmt.Print(string(encoded))
}

func readRequiredHex(path, flagName string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%s is required", flagName)
	}
	return readHex(path)
}

func readTrustRoots(path, approvedSHA256 string) (e10evidence.ApprovedTrustRoots, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return e10evidence.ApprovedTrustRoots{}, fmt.Errorf("trust roots: %w", err)
	}
	roots, err := e10evidence.LoadApprovedTrustRoots(raw, approvedSHA256)
	if err != nil {
		return e10evidence.ApprovedTrustRoots{}, fmt.Errorf("trust roots: %w", err)
	}
	return roots, nil
}

func readPacketSignatureSet(operatorSignaturePath, operatorPublicKeyPath, reviewerSignaturePath, reviewerPublicKeyPath string) ([]byte, []byte, []byte, []byte, error) {
	paths := []string{operatorSignaturePath, operatorPublicKeyPath, reviewerSignaturePath, reviewerPublicKeyPath}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, nil, nil, nil, errors.New("operator and independent-review signature/public-key files are required")
		}
	}
	values := make([][]byte, 4)
	for index, path := range paths {
		value, err := readHex(path)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		values[index] = value
	}
	return values[0], values[1], values[2], values[3], nil
}

func readHex(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, " \t\r\n") {
		return nil, errors.New("must contain one canonical lowercase hex value")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return nil, errors.New("must contain one canonical lowercase hex value")
	}
	return decoded, nil
}

func fatal(err error) {
	encoded, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
	fmt.Fprintln(os.Stderr, string(encoded))
	os.Exit(1)
}
