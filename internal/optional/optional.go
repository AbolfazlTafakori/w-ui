// Package optional is a field an edit may leave alone, clear or change, and
// the JSON that tells the three apart: absent, null, or a value.
package optional

import (
	"encoding/json"
	"time"
)

// Time is a time that can be absent, null or set.
type Time struct {
	Set   bool
	Value *time.Time
}

// UnmarshalJSON records that the field was present; null leaves Value nil.
func (o *Time) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var t time.Time
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	o.Value = &t
	return nil
}

// MarshalJSON writes the value, or null.
func (o Time) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

// At is the Time for a given time; Clear is one that removes the date.
func At(t time.Time) Time { return Time{Set: true, Value: &t} }
func Clear() Time         { return Time{Set: true} }
