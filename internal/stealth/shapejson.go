package stealth

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// A shape read from JSON: a profile's or a config's. The members are exactly
// those of the page; an unknown one, or one of the wrong type, refuses the
// shape. A number must be a bare, non-negative whole number: a quoted number
// and a fraction are both refused. It ends at Resolve, so a shape read from
// JSON obeys every rule a shape resolved from code does.

type jsonUp struct {
	Method   *string         `json:"method"`
	In       *string         `json:"in"`
	Name     *string         `json:"name"`
	Limit    json.RawMessage `json:"limit"`
	Inflight json.RawMessage `json:"inflight"`
	Gather   json.RawMessage `json:"gather"`
}

type jsonDown struct {
	Mode     *string         `json:"mode"`
	Gather   json.RawMessage `json:"gather"`
	Life     json.RawMessage `json:"life"`
	Fallback *bool           `json:"fallback"`
}

type jsonShape struct {
	Up   *jsonUp   `json:"up"`
	Down *jsonDown `json:"down"`
}

// uintRaw reads a bare non-negative integer literal, or ok=false. nil raw is
// a member that was not written, which is kept as a nil *uint64.
func uintRaw(raw json.RawMessage) (*uint64, bool) {
	if raw == nil {
		return nil, true
	}
	v, err := strconv.ParseUint(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil {
		return nil, false
	}
	return &v, true
}

// pairRaw reads an array of exactly two bare non-negative integers, or
// ok=false. nil raw is a member that was not written.
func pairRaw(raw json.RawMessage) (*[2]uint64, bool) {
	if raw == nil {
		return nil, true
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 2 {
		return nil, false
	}
	var out [2]uint64
	for i, it := range items {
		v, err := strconv.ParseUint(string(bytes.TrimSpace(it)), 10, 64)
		if err != nil {
			return nil, false
		}
		out[i] = v
	}
	return &out, true
}

// ParseShape reads a shape from the text of a JSON object, as strictly as a
// profile is read: an unknown member, a wrong type or a non-integer number
// refuses it.
func ParseShape(text []byte) (Shape, error) {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.DisallowUnknownFields()
	var js jsonShape
	if err := dec.Decode(&js); err != nil {
		return Shape{}, errNoShape
	}
	if dec.More() {
		return Shape{}, errNoShape
	}
	if js.Up == nil || js.Down == nil || js.Up.Method == nil || js.Up.In == nil || js.Down.Mode == nil {
		return Shape{}, errNoShape
	}
	limit, ok1 := uintRaw(js.Up.Limit)
	inflight, ok2 := uintRaw(js.Up.Inflight)
	upGather, ok3 := pairRaw(js.Up.Gather)
	downGather, ok4 := pairRaw(js.Down.Gather)
	life, ok5 := pairRaw(js.Down.Life)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return Shape{}, errNoShape
	}
	return Resolve(Given{
		Method: *js.Up.Method, Place: *js.Up.In, Name: js.Up.Name,
		Limit: limit, Inflight: inflight, Gather: upGather,
		Mode: *js.Down.Mode, DownGather: downGather, Life: life, Fallback: js.Down.Fallback,
	})
}

// ShapeObject is the shape as a profile writes it: an object of exactly the
// page's members, the defaults left out so a profile stays small. It round
// trips through ParseShape.
func (s Shape) ShapeObject() map[string]any {
	up := map[string]any{"method": s.Method, "in": s.Place.Word()}
	if s.Name != "" {
		up["name"] = s.Name
	}
	down := map[string]any{"mode": s.Mode.Word()}
	return map[string]any{"up": up, "down": down}
}
