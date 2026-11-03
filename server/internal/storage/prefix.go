package storage

import (
	"unicode/utf8"
)

// Exclusive end of the UTF-8/BINARY (PostgreSQL C) prefix range. Increment a
// Unicode scalar rather than an arbitrary byte so PostgreSQL gets valid text.
// An all-MaxRune prefix has no finite upper bound.
func recordPrefixEnd(prefix string) (string, bool) {
	runes := []rune(prefix)
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] < utf8.MaxRune {
			runes[i]++
			if runes[i] == 0xD800 {
				runes[i] = 0xE000 // skip surrogate code points
			}
			return string(runes[:i+1]), true
		}
	}
	return "", false
}

// Both SQLite Scan and Count use the same parameter-bound range predicate.
func sqliteRecordPrefix(query string, args []any, prefix string) (string, []any) {
	if prefix == "" {
		return query, args
	}
	if !utf8.ValidString(prefix) {
		// Preserve SQLite's legacy handling of malformed text inputs.
		return query + " AND substr(record_key,1,length(?))=?", append(args, prefix, prefix)
	}
	query += " AND record_key >= ?"
	args = append(args, prefix)
	if end, ok := recordPrefixEnd(prefix); ok {
		query += " AND record_key < ?"
		args = append(args, end)
	}
	return query, args
}
