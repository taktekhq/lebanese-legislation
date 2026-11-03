package mcpserver

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// Tool arguments arrive as decoded JSON (numbers as json.Number). These
// helpers read them with the Worker's JavaScript coercions, so a tool
// accepts and refuses the same arguments.

type args map[string]any

// errArgType stands for the TypeError the Worker threw when a string
// method met another type (an internal error to the client).
var errArgType = errors.New("argument has the wrong type")

// numberArg is v as a JavaScript number when typeof v === "number".
func numberArg(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func truncf(f float64) float64 { return math.Trunc(f) }

// jsToNumber is Number(v) for a string or a number.
func jsToNumber(v any) float64 {
	if f, ok := numberArg(v); ok {
		return f
	}
	s, ok := v.(string)
	if !ok {
		return math.NaN()
	}
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// jsString is String(v) for a string or a number.
func jsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	}
	if f, ok := numberArg(v); ok {
		return jsNumberString(f)
	}
	return mustMarshal(v)
}

// strArg reads a string argument the Worker trimmed: absent or null is "".
// A number is read as its digits (the Worker threw a TypeError on it,
// which is no use to anyone); anything else is a type error.
func strArg(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	}
	if f, ok := numberArg(v); ok {
		return jsNumberString(f), nil
	}
	return "", errArgType
}

// idArg reads a positive id: bad is true for a missing, zero, negative or non-numeric
// falsy value (a tool error); otherwise id is the integer to look up, or 0
// when nothing can match (a fraction, a word), and shown is how the
// argument reads in a "not found" message.
func idArg(v any) (id int64, shown string, bad bool) {
	switch x := v.(type) {
	case nil, bool, map[string]any, []any:
		return 0, "", true
	case string:
		if x == "" {
			return 0, "", true
		}
		f := jsToNumber(x)
		if !math.IsNaN(f) && f <= 0 {
			return 0, "", true
		}
		if f == math.Trunc(f) && f > 0 && f < 1<<53 {
			return int64(f), x, false
		}
		return 0, x, false
	}
	f, ok := numberArg(v)
	if !ok || f == 0 || math.IsNaN(f) || f <= 0 {
		return 0, "", true
	}
	if f != math.Trunc(f) || f >= 1<<53 {
		return 0, jsNumberString(f), false
	}
	return int64(f), jsNumberString(f), false
}

// present is the Worker's `v !== undefined && v !== null && v !== ""`.
func present(v any) bool {
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok && s == "" {
		return false
	}
	return true
}

// truthy is JavaScript truthiness.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	if f, ok := numberArg(v); ok {
		return f != 0 && !math.IsNaN(f)
	}
	return true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
