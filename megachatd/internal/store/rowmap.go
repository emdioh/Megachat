package store

import (
	"context"
	"database/sql"
	"strconv"
)

// rowMap is a dynamically typed result row (column -> nil|int64|float64|
// string), the Go counterpart of the sqlite3.Row/dict values the Python
// dedup code merges field by field.
type rowMap map[string]any

func scanRowMaps(rows *sql.Rows) ([]rowMap, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []rowMap
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(rowMap, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func queryRowMaps(ctx context.Context, ex execer, q string, args ...any) ([]rowMap, error) {
	rows, err := ex.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanRowMaps(rows)
}

func (r rowMap) str(col string) *string {
	switch v := r[col].(type) {
	case nil:
		return nil
	case string:
		return &v
	case int64:
		s := strconv.FormatInt(v, 10)
		return &s
	case float64:
		s := strconv.FormatFloat(v, 'g', -1, 64)
		return &s
	}
	return nil
}

func (r rowMap) int(col string) int64 { return asInt(r[col]) }

// isMissing is Python's “value is None or value == ""“.
func isMissing(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && s == ""
}

// asInt is models._as_int (None/non-numeric -> 0).
func asInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// truthyString is Python truthiness of a text column value.
func truthyString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
