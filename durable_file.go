package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrDurableReplaceAmbiguous means rename published the replacement, but the
// parent-directory fsync failed. Callers must not roll in-memory state back as
// if the old file were still authoritative; reload the visible file or poison
// the store until recovery establishes which generation is live.
var ErrDurableReplaceAmbiguous = errors.New("durable replacement outcome is ambiguous")

// writeFileAtomicallyDurable replaces path without exposing a partially
// written file. Success means both the new contents and directory entry were
// synced, so callers may treat nil as surviving a process or host restart.
func writeFileAtomicallyDurable(path string, data []byte, perm fs.FileMode) error {
	return writeFileAtomically(path, data, perm, true)
}

func writeFileAtomicallyBestEffort(path string, data []byte, perm fs.FileMode) error {
	return writeFileAtomically(path, data, perm, false)
}

func writeFileAtomicallyForCanonicalMode(path string, data []byte, perm fs.FileMode) error {
	if canonicalLegacyDurabilityRequired() {
		return writeFileAtomicallyDurable(path, data, perm)
	}
	return writeFileAtomicallyBestEffort(path, data, perm)
}

func writeFileAtomically(path string, data []byte, perm fs.FileMode, durable bool) error {
	return canonicalFenceFileMutation(path, data, func() error {
		return writeFileAtomicallyUnfenced(path, data, perm, durable)
	})
}

func writeFileAtomicallyUnfenced(path string, data []byte, perm fs.FileMode, durable bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	// The .tmp- prefix is also the backup walker's explicit transient-file
	// convention, so an in-flight replacement is never copied as user data.
	temp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := writeAll(temp, data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if durable {
		if err := temp.Sync(); err != nil {
			_ = temp.Close()
			return fmt.Errorf("sync temp file: %w", err)
		}
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	cleanup = false
	if durable {
		if err := syncDirectoryForAtomicWrite(dir); err != nil {
			return fmt.Errorf("%w: sync parent directory: %v", ErrDurableReplaceAmbiguous, err)
		}
	}
	return nil
}

// appendFileDurably appends one complete record and fsyncs it before returning.
func appendFileDurably(path string, data []byte, perm fs.FileMode) error {
	return appendFile(path, data, perm, true)
}

func appendFileBestEffort(path string, data []byte, perm fs.FileMode) error {
	return appendFile(path, data, perm, false)
}

func appendFile(path string, data []byte, perm fs.FileMode, durable bool) error {
	return canonicalFenceAppendMutation(path, data, func() error {
		return appendFileUnfenced(path, data, perm, durable)
	})
}

func appendFileUnfenced(path string, data []byte, perm fs.FileMode, durable bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("open append file: %w", err)
	}
	if err := writeAll(file, data); err != nil {
		_ = file.Close()
		return fmt.Errorf("append file: %w", err)
	}
	if durable {
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return fmt.Errorf("sync append file: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close append file: %w", err)
	}
	if created && durable {
		if err := syncDirectoryForAtomicWrite(dir); err != nil {
			return fmt.Errorf("sync parent directory: %w", err)
		}
	}
	return nil
}

// canonicalLegacyDurabilityRequired keeps the pre-W1 hot path unchanged while
// canonical capture is off. Shadow/required modes may publish a commit marker
// only after the legacy source has this stronger durability guarantee.
func canonicalLegacyDurabilityRequired() bool {
	mode, err := canonicalModeFromEnvironment()
	if err != nil {
		return false
	}
	return mode == "shadow" || mode == "required"
}

func canonicalModeFromEnvironment() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("BONFIRE_CANONICAL_MODE")))
	if mode == "" {
		mode = "off"
	}
	switch mode {
	case "off", "shadow", "required":
		return mode, nil
	default:
		return "", fmt.Errorf("invalid BONFIRE_CANONICAL_MODE %q: want off, shadow, or required", mode)
	}
}

func validateCanonicalModeConfig() error {
	_, err := canonicalModeFromEnvironment()
	return err
}

type fileWriter interface {
	Write([]byte) (int, error)
}

func writeAll(writer fileWriter, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return fmt.Errorf("short write")
		}
		data = data[written:]
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

var syncDirectoryForAtomicWrite = syncDirectory
