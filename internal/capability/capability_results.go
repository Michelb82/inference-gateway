package capability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// OutputSchema deliberately supports only a documented JSON Schema subset.
// Unsupported keywords are rejected while decoding a manifest.
type OutputSchema struct {
	Type       string                    `json:"type"`
	Required   []string                  `json:"required,omitempty"`
	Properties map[string]PropertySchema `json:"properties,omitempty"`
}
type PropertySchema struct {
	Type      string   `json:"type"`
	MinLength *int     `json:"minLength,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
}

func strictDecode(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one schema object")
	}
	return nil
}
func rejectNullSchemaKeywords(data []byte) error {
	var keywords map[string]json.RawMessage
	if err := json.Unmarshal(data, &keywords); err != nil {
		return err
	}
	for key, value := range keywords {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("schema keyword %s must not be null", key)
		}
	}
	return nil
}
func (s *OutputSchema) UnmarshalJSON(data []byte) error {
	if err := rejectNullSchemaKeywords(data); err != nil {
		return err
	}
	type plain OutputSchema
	var value plain
	if err := strictDecode(data, &value); err != nil {
		return err
	}
	*s = OutputSchema(value)
	return nil
}
func (s *PropertySchema) UnmarshalJSON(data []byte) error {
	if err := rejectNullSchemaKeywords(data); err != nil {
		return err
	}
	type plain PropertySchema
	var value plain
	if err := strictDecode(data, &value); err != nil {
		return err
	}
	*s = PropertySchema(value)
	return nil
}
func (s *OutputSchema) clone() *OutputSchema {
	if s == nil {
		return nil
	}
	out := *s
	out.Required = append([]string(nil), s.Required...)
	if s.Properties != nil {
		out.Properties = make(map[string]PropertySchema, len(s.Properties))
	}
	for key, property := range s.Properties {
		if property.MinLength != nil {
			v := *property.MinLength
			property.MinLength = &v
		}
		if property.Minimum != nil {
			v := *property.Minimum
			property.Minimum = &v
		}
		if property.Maximum != nil {
			v := *property.Maximum
			property.Maximum = &v
		}
		out.Properties[key] = property
	}
	return &out
}
func (s *OutputSchema) validate() error {
	if s == nil {
		return nil
	}
	if s.Type != "object" {
		return fmt.Errorf("type must be object")
	}
	seen := map[string]bool{}
	for _, key := range s.Required {
		if strings.TrimSpace(key) == "" || seen[key] {
			return fmt.Errorf("invalid or duplicate required field %q", key)
		}
		seen[key] = true
		if _, ok := s.Properties[key]; !ok {
			return fmt.Errorf("required field %q must be declared in properties", key)
		}
	}
	for _, key := range s.keys() {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("property name must not be blank")
		}
		p := s.Properties[key]
		switch p.Type {
		case "string", "number", "integer", "boolean", "null":
		default:
			return fmt.Errorf("property %s has unsupported type %q", key, p.Type)
		}
		if p.MinLength != nil && (p.Type != "string" || *p.MinLength < 0) {
			return fmt.Errorf("property %s minLength requires a string and a nonnegative integer", key)
		}
		if (p.Minimum != nil || p.Maximum != nil) && p.Type != "number" && p.Type != "integer" {
			return fmt.Errorf("property %s numeric bounds require number or integer", key)
		}
		for _, bound := range []*float64{p.Minimum, p.Maximum} {
			if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
				return fmt.Errorf("property %s has nonfinite numeric bounds", key)
			}
		}
		if p.Minimum != nil && p.Maximum != nil && *p.Minimum > *p.Maximum {
			return fmt.Errorf("property %s minimum exceeds maximum", key)
		}
	}
	return nil
}
func (s *OutputSchema) keys() []string {
	keys := make([]string, 0, len(s.Properties))
	for key := range s.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ParseRawJSON(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
		if newline := strings.IndexByte(raw, '\n'); newline >= 0 && strings.EqualFold(strings.TrimSpace(raw[:newline]), "json") {
			raw = raw[newline+1:]
		}
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("invalid JSON model output: %w", err)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("empty JSON model output")
	}
	return parsed, nil
}

// ParseResult validates declared properties and preserves extra result fields.
func ParseResult(def Definition, raw string) (map[string]any, error) {
	parsed, err := ParseRawJSON(raw)
	if err != nil {
		return nil, err
	}
	s := def.OutputSchema
	if s == nil {
		return parsed, nil
	}
	for _, key := range s.Required {
		if _, ok := parsed[key]; !ok {
			return nil, fmt.Errorf("result missing field %s", key)
		}
	}
	var keys []string
	if def.plan != nil {
		keys = def.plan.outputFields
	} else {
		keys = s.keys()
	}
	for _, key := range keys {
		value, exists := parsed[key]
		if !exists {
			continue
		}
		p := s.Properties[key]
		valid := false
		switch p.Type {
		case "string":
			text, ok := value.(string)
			valid = ok
			if valid && p.MinLength != nil {
				valid = utf8.RuneCountInString(text) >= *p.MinLength
			}
		case "number", "integer":
			number, ok := value.(float64)
			valid = ok
			if valid && p.Type == "integer" {
				valid = math.Trunc(number) == number
			}
			if valid && p.Minimum != nil {
				valid = number >= *p.Minimum
			}
			if valid && p.Maximum != nil {
				valid = number <= *p.Maximum
			}
		case "boolean":
			_, valid = value.(bool)
		case "null":
			valid = value == nil
		}
		if !valid {
			return nil, fmt.Errorf("result field %s does not match output_schema", key)
		}
	}
	return parsed, nil
}
