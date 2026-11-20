package geetest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"
)

type member struct {
	key   string
	value any
}

type object []member

func (o object) get(key string) (any, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

func (o *object) set(key string, value any) {
	for i := range *o {
		if (*o)[i].key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key: key, value: value})
}

func obj(fields ...any) object {
	if len(fields)%2 != 0 {
		panic("obj requires key/value pairs")
	}
	o := make(object, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			panic("obj key is not a string")
		}
		o = append(o, member{key: key, value: fields[i+1]})
	}
	return o
}

func parseOrderedJSON(input string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(input))
	dec.UseNumber()
	v, err := decodeOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("geetest: trailing JSON value")
		}
		return nil, fmt.Errorf("geetest: trailing JSON data: %w", err)
	}
	return v, nil
}

func decodeOrderedValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := object{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("geetest: JSON object key is not a string")
				}
				value, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(key, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			a := []any{}
			for dec.More() {
				value, err := decodeOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return a, nil
		default:
			return nil, fmt.Errorf("geetest: unexpected JSON delimiter %q", t)
		}
	case string:
		return t, nil
	case json.Number:
		return t, nil
	case bool:
		return t, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("geetest: unsupported JSON token %T", tok)
	}
}

func marshalPythonJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writePythonJSON(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writePythonJSON(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case object:
		b.WriteByte('{')
		for i, m := range t {
			if i != 0 {
				b.WriteByte(',')
			}
			writePythonString(b, m.key)
			b.WriteByte(':')
			if err := writePythonJSON(b, m.value); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case *object:
		if t == nil {
			b.WriteString("null")
			return nil
		}
		return writePythonJSON(b, *t)
	case []any:
		b.WriteByte('[')
		for i, value := range t {
			if i != 0 {
				b.WriteByte(',')
			}
			if err := writePythonJSON(b, value); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case string:
		writePythonString(b, t)
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		s, err := canonicalPythonNumber(t)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case int:
		b.WriteString(strconv.Itoa(t))
	case int8:
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case int16:
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case int32:
		b.WriteString(strconv.FormatInt(int64(t), 10))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case uint:
		b.WriteString(strconv.FormatUint(uint64(t), 10))
	case uint8:
		b.WriteString(strconv.FormatUint(uint64(t), 10))
	case uint16:
		b.WriteString(strconv.FormatUint(uint64(t), 10))
	case uint32:
		b.WriteString(strconv.FormatUint(uint64(t), 10))
	case uint64:
		b.WriteString(strconv.FormatUint(t, 10))
	case float64:
		b.WriteString(formatPythonFloat(t))
	case float32:
		b.WriteString(formatPythonFloat(float64(t)))
	default:
		return fmt.Errorf("geetest: cannot JSON encode %T", v)
	}
	return nil
}

func canonicalPythonNumber(n json.Number) (string, error) {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		var i big.Int
		if _, ok := i.SetString(s, 10); !ok {
			return "", fmt.Errorf("geetest: invalid JSON integer %q", s)
		}
		return i.String(), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "", fmt.Errorf("geetest: invalid JSON number %q: %w", s, err)
	}
	return formatPythonFloat(f), nil
}

func formatPythonFloat(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	abs := math.Abs(f)
	if abs != 0 && (abs >= 1e16 || abs < 1e-4) {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		e := strings.LastIndexByte(s, 'e')
		mantissa, exponent := s[:e], s[e+1:]
		sign := ""
		if exponent[0] == '+' || exponent[0] == '-' {
			sign, exponent = exponent[:1], exponent[1:]
		}
		if len(exponent) < 2 {
			exponent = strings.Repeat("0", 2-len(exponent)) + exponent
		}
		return mantissa + "e" + sign + exponent
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

func writePythonString(b *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '':
			b.WriteString(`\b`)
		case '':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '':
			b.WriteString(`\r`)
		case '	':
			b.WriteString(`\t`)
		default:
			if r >= 0x20 && r <= 0x7e {
				b.WriteRune(r)
				continue
			}
			if r <= 0xffff {
				b.WriteString("\\u")
				for shift := 12; shift >= 0; shift -= 4 {
					b.WriteByte(hex[(r>>shift)&0xf])
				}
				continue
			}
			hi, lo := utf16.EncodeRune(r)
			for _, u := range []rune{hi, lo} {
				b.WriteString("\\u")
				for shift := 12; shift >= 0; shift -= 4 {
					b.WriteByte(hex[(u>>shift)&0xf])
				}
			}
		}
	}
	b.WriteByte('"')
}

func stripJSONP(text string, prefixRunes int) (string, error) {
	r := []rune(text)
	if prefixRunes < 0 || len(r) <= prefixRunes {
		return "", fmt.Errorf("geetest: short JSONP response: %d runes", len(r))
	}
	return string(r[prefixRunes : len(r)-1]), nil
}

func parseDataObject(text string, prefixRunes int) (object, error) {
	inner, err := stripJSONP(text, prefixRunes)
	if err != nil {
		return nil, err
	}
	v, err := parseOrderedJSON(inner)
	if err != nil {
		return nil, fmt.Errorf("geetest: parse JSONP: %w", err)
	}
	root, ok := v.(object)
	if !ok {
		return nil, fmt.Errorf("geetest: JSONP root is %T, not object", v)
	}
	dataValue, ok := root.get("data")
	if !ok {
		return nil, fmt.Errorf("geetest: response has no data field")
	}
	data, ok := dataValue.(object)
	if !ok {
		return nil, fmt.Errorf("geetest: response data is %T, not object", dataValue)
	}
	return data, nil
}
