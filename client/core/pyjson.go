package core

// Ordered JSON read/write that reproduces Python's
// json.load + json.dump(data, ensure_ascii=False, indent=2) + "\n"
// byte-for-byte for the shapes the install scripts touch, so the client's
// file diff matches /i and /u (TRD §11.3 M2/M5).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type jsonObject struct {
	keys []string
	vals map[string]any
}

func newObject() *jsonObject { return &jsonObject{vals: map[string]any{}} }

func (o *jsonObject) get(k string) (any, bool) { v, ok := o.vals[k]; return v, ok }

// set keeps first-insertion position (Python dict semantics, duplicate keys: last value wins).
func (o *jsonObject) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *jsonObject) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *jsonObject) len() int { return len(o.keys) }

func parseJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := newObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("bad key")
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
		return nil, errors.New("bad delim")
	default:
		return t, nil // string, json.Number, bool, nil
	}
}

func dumpJSON(v any) []byte {
	var b strings.Builder
	writeValue(&b, v, 0)
	b.WriteString("\n")
	return []byte(b.String())
}

func writeValue(b *strings.Builder, v any, depth int) {
	ind := func(d int) { b.WriteString("\n" + strings.Repeat("  ", d)) }
	switch t := v.(type) {
	case *jsonObject:
		if t.len() == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{")
		for i, k := range t.keys {
			if i > 0 {
				b.WriteString(",")
			}
			ind(depth + 1)
			writeString(b, k)
			b.WriteString(": ")
			writeValue(b, t.vals[k], depth+1)
		}
		ind(depth)
		b.WriteString("}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[")
		for i, e := range t {
			if i > 0 {
				b.WriteString(",")
			}
			ind(depth + 1)
			writeValue(b, e, depth+1)
		}
		ind(depth)
		b.WriteString("]")
	case string:
		writeString(b, t)
	case json.Number:
		b.WriteString(t.String()) // exotic float spellings (1e5, 1.50) may differ from Python repr
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		panic(fmt.Sprintf("unexpected json type %T", v))
	}
}

// writeString mirrors Python's ensure_ascii=False escaping.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
