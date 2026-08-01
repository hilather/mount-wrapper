package state

import (
	"embed"
	"fmt"
	"strings"
)

// CurrentSchemaVersion is the latest forward-only schema version (parity with Python).
const CurrentSchemaVersion = 6

//go:embed migrations/*.sql
var migrationsFS embed.FS

var migrationFiles = map[int]string{
	1: "migrations/001_initial.sql",
	2: "migrations/002_index_duration.sql",
	3: "migrations/003_mount_duration.sql",
	4: "migrations/004_converting_status.sql",
	5: "migrations/005_convert_source_size.sql",
	6: "migrations/006_convert_duration.sql",
}

// MigrationSQL returns the SQL text for a single migration version.
func MigrationSQL(version int) (string, error) {
	filename, ok := migrationFiles[version]
	if !ok {
		return "", fmt.Errorf("unknown schema migration version %d", version)
	}
	b, err := migrationsFS.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MigrationsNeeded returns ordered migration versions to apply from fromVersion
// (exclusive) up to toVersion (inclusive). Defaults to CurrentSchemaVersion when
// toVersion is 0 or negative is not used — pass CurrentSchemaVersion explicitly.
func MigrationsNeeded(fromVersion, toVersion int) ([]int, error) {
	if fromVersion < 0 {
		return nil, fmt.Errorf("invalid from_version %d", fromVersion)
	}
	if toVersion < fromVersion {
		return nil, fmt.Errorf("cannot migrate downward from %d to %d", fromVersion, toVersion)
	}
	var out []int
	for v := fromVersion + 1; v <= toVersion; v++ {
		if _, ok := migrationFiles[v]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// splitSQLStatements splits a migration script into individual statements.
// Handles line comments (-- ...) and ignores empty statements.
func splitSQLStatements(script string) []string {
	// Strip full-line comments and keep multi-line SQL intact.
	var cleaned strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleaned.WriteString(line)
		cleaned.WriteByte('\n')
	}
	parts := strings.Split(cleaned.String(), ";")
	var stmts []string
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}
