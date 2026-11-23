package scanner

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// decodeStrict decodes exactly one JSON object, rejecting unknown keys and
// trailing data.
func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return classifyJSONError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the finding object")
	}
	return nil
}

// classifyJSONError maps an encoding/json error to a fixed reason, because the
// library's own text can quote scanner input.
func classifyJSONError(err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return errors.New("not a finding object: invalid JSON")
	case errors.As(err, &typ):
		return errors.New("not a finding object: wrong value type")
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return errors.New("not a finding object: unknown key")
	}
	return errors.New("not a finding object: malformed")
}
