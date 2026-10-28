package contracts

import (
	"fmt"
	"time"
)

// Time is an instant on the wire: an ISO-8601 UTC string with millisecond precision, the shape
// JavaScript's Date#toISOString produces and the frontend's ISODate expects. Its zero value is
// "absent" and is dropped by `omitzero` tags.
type Time struct{ time.Time }

const timeLayout = "2006-01-02T15:04:05.000Z"

// At converts t to the wire representation (UTC, truncated to milliseconds).
func At(t time.Time) Time { return Time{t.UTC().Truncate(time.Millisecond)} }

// Now is At(time.Now()).
func Now() Time { return At(time.Now()) }

func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.UTC().Format(timeLayout) + `"`), nil
}

func (t *Time) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*t = Time{}
		return nil
	}
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return fmt.Errorf("contracts: time must be a JSON string, got %s", b)
	}
	parsed, err := time.Parse(time.RFC3339, string(b[1:len(b)-1]))
	if err != nil {
		return fmt.Errorf("contracts: invalid time: %w", err)
	}
	*t = At(parsed)
	return nil
}
