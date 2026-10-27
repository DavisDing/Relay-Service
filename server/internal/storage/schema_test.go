package storage

import (
	"slices"
	"testing"
)

func TestSchemaHistoryOnlyInitializesContiguousVersions(t *testing.T) {
	expected := []string{"001_initial.sql", "002_retention_count_indexes.sql"}
	for _, test := range []struct {
		name    string
		applied []string
		want    []string
		invalid bool
	}{
		{name: "empty schema", want: expected},
		{name: "retry after committed initial SQL", applied: expected[:1], want: expected[1:]},
		{name: "complete schema", applied: expected},
		{name: "gap", applied: expected[1:], invalid: true},
		{name: "unknown version", applied: []string{"001_unknown.sql"}, invalid: true},
		{name: "newer database", applied: append(slices.Clone(expected), "003_future.sql"), invalid: true},
		{name: "duplicate", applied: []string{expected[0], expected[0]}, invalid: true},
		{name: "wrong order", applied: []string{expected[1], expected[0]}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := unappliedMigrations(expected, test.applied)
			if (err != nil) != test.invalid {
				t.Fatalf("unappliedMigrations error=%v; invalid=%v", err, test.invalid)
			}
			if err == nil && !slices.Equal(got, test.want) {
				t.Fatalf("pending=%v; want %v", got, test.want)
			}
		})
	}
}
