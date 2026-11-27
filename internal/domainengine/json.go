package domainengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

func checkJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := checkValue(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func checkValue(dec *json.Decoder, depth int) error {
	if depth > 48 {
		return errors.New("JSON nesting exceeds limit")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if number, ok := tok.(json.Number); ok {
		raw := string(number)
		if len(raw) > 1024 {
			return errors.New("JSON number exceeds size limit")
		}
		if index := strings.IndexAny(raw, "eE"); index >= 0 {
			exponent, err := strconv.Atoi(raw[index+1:])
			if err != nil || exponent < -1024 || exponent > 1024 {
				return errors.New("JSON exponent exceeds limit")
			}
		}
	}
	delimiter, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	names := make(map[string]struct{})
	for dec.More() {
		if delimiter == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if _, exists := names[name]; exists {
				return errors.New("duplicate JSON object key")
			}
			names[name] = struct{}{}
		}
		if err := checkValue(dec, depth+1); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}
