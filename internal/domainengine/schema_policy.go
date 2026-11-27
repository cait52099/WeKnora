package domainengine

import (
	"errors"
	"fmt"
	"strings"
)

const maxSchemaExpansion = 8192

// checkSchemaPolicy bounds expansion of the executable schema graph. Only
// local JSON Pointer references are supported in this import format.
func checkSchemaPolicy(root map[string]any) error {
	visiting := make(map[string]bool)
	costs := make(map[string]int)
	var visit func(string, any) (int, error)
	visit = func(path string, value any) (int, error) {
		if visiting[path] {
			return 0, errors.New("recursive schemas are not supported")
		}
		if cost, found := costs[path]; found {
			return cost, nil
		}
		visiting[path] = true
		defer delete(visiting, path)
		cost := 1
		add := func(childPath string, child any) error {
			childCost, err := visit(childPath, child)
			if err != nil {
				return err
			}
			cost += childCost
			if cost > maxSchemaExpansion {
				return errors.New("schema expansion exceeds limit")
			}
			return nil
		}
		if object, ok := value.(map[string]any); ok {
			if draft, exists := object["$schema"]; exists && (path != "#" || draft != draft2020URI) {
				return 0, errors.New("nested or alternate schema dialect is not supported")
			}
			for _, keyword := range []string{"$dynamicRef", "$recursiveRef", "$anchor", "$dynamicAnchor", "dependencies"} {
				if _, exists := object[keyword]; exists {
					return 0, fmt.Errorf("unsupported schema keyword %s", keyword)
				}
			}
			if _, exists := object["$id"]; exists && path != "#" {
				return 0, errors.New("nested schema identifiers are not supported")
			}
			if ref, exists := object["$ref"]; exists {
				target, ok := ref.(string)
				if !ok || !strings.HasPrefix(target, "#/$defs/") || strings.ContainsAny(target[1:], "%#\t\r\n ") {
					return 0, errors.New("schema references must point to local definitions")
				}
				// References name one top-level definition. This canonical
				// pointer form keeps graph identity and cycle detection stable.
				name := strings.TrimPrefix(target, "#/$defs/")
				if name == "" || strings.Contains(name, "/") {
					return 0, errors.New("schema reference must name a top-level definition")
				}
				definitions, _ := root["$defs"].(map[string]any)
				definition, found := definitions[unescapePointer(name)]
				if !found || target != "#/$defs/"+escapePointer(unescapePointer(name)) {
					return 0, errors.New("unresolved local schema definition")
				}
				if err := add(target, definition); err != nil {
					return 0, err
				}
			}
			for _, key := range []string{"properties", "patternProperties", "dependentSchemas", "$defs"} {
				children, _ := object[key].(map[string]any)
				for name, child := range children {
					if err := add(path+"/"+key+"/"+escapePointer(name), child); err != nil {
						return 0, err
					}
				}
			}
			for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
				children, _ := object[key].([]any)
				for index, child := range children {
					if err := add(fmt.Sprintf("%s/%s/%d", path, key, index), child); err != nil {
						return 0, err
					}
				}
			}
			for _, key := range []string{"items", "contains", "additionalProperties", "propertyNames",
				"unevaluatedItems", "unevaluatedProperties", "not", "if", "then", "else", "contentSchema"} {
				if child, found := object[key]; found {
					if err := add(path+"/"+key, child); err != nil {
						return 0, err
					}
				}
			}
		}
		costs[path] = cost
		return cost, nil
	}
	_, err := visit("#", root)
	return err
}

func escapePointer(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

func unescapePointer(s string) string {
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
}
