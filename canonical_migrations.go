package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// canonicalMigrationFiles is deliberately embedded into the application so a
// running binary and its migration checksum cannot drift from one another.
//
//go:embed migrations/*.sql
var canonicalMigrationFiles embed.FS

type canonicalMigration struct {
	Version int64
	Name    string
	SQL     string
	SHA256  [sha256.Size]byte
}

func loadCanonicalMigrations() ([]canonicalMigration, error) {
	entries, err := fs.ReadDir(canonicalMigrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read canonical migrations: %w", err)
	}
	migrations := make([]canonicalMigration, 0, len(entries))
	seenVersions := make(map[int64]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("canonical migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("canonical migration %q has invalid version", entry.Name())
		}
		if prior := seenVersions[version]; prior != "" {
			return nil, fmt.Errorf("canonical migrations %q and %q share version %d", prior, entry.Name(), version)
		}
		raw, err := canonicalMigrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read canonical migration %q: %w", entry.Name(), err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, fmt.Errorf("canonical migration %q is empty", entry.Name())
		}
		seenVersions[version] = entry.Name()
		migrations = append(migrations, canonicalMigration{
			Version: version,
			Name:    entry.Name(),
			SQL:     string(raw),
			SHA256:  sha256.Sum256(raw),
		})
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}
