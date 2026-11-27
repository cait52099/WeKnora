package domainengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// SemanticCheck is a deterministic same-value constraint between declared
// JSON inputs and outputs. Scientific constraints remain in the profile.
type SemanticCheck struct {
	OutputPointer string `json:"output_pointer"`
	Input         string `json:"input"`
	InputPointer  string `json:"input_pointer"`
}

func validateSemanticChecks(stage StageDefinition) error {
	if len(stage.SemanticChecks) > 32 {
		return errors.New("too many semantic checks")
	}
	for _, check := range stage.SemanticChecks {
		source, ok := stage.Inputs[check.Input]
		if !ok || source.Source == InputDocument || !validPointer(check.OutputPointer) || !validPointer(check.InputPointer) {
			return errors.New("semantic equality check must address a declared JSON input and the output")
		}
	}
	return nil
}
func validPointer(pointer string) bool {
	if len(pointer) > 512 || (pointer != "" && pointer[0] != '/') {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i == len(pointer) || (pointer[i] != '0' && pointer[i] != '1') {
				return false
			}
		}
	}
	return true
}

func checkSemantic(stage StageDefinition, output []byte, inputs map[string]string) bool {
	for _, rule := range stage.SemanticChecks {
		left, l := atPointer(output, rule.OutputPointer)
		right, r := atPointer([]byte(inputs[rule.Input]), rule.InputPointer)
		if !l || !r || !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

func atPointer(raw []byte, pointer string) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	if pointer == "" {
		return value, true
	}
	for _, part := range strings.Split(pointer[1:], "/") {
		part = unescapePointer(part)
		switch current := value.(type) {
		case map[string]any:
			next, ok := current[part]
			if !ok {
				return nil, false
			}
			value = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) || part != strconv.Itoa(index) {
				return nil, false
			}
			value = current[index]
		default:
			return nil, false
		}
	}
	return value, true
}
