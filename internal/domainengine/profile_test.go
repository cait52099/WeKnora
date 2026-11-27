package domainengine

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) *CompiledProfile {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + "-synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := ImportProfile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func importDefinition(t *testing.T, p Profile) (*CompiledProfile, error) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return ImportProfile(bytes.NewReader(raw))
}

func TestSyntheticDomainsUseSameCompiler(t *testing.T) {
	for _, tc := range []struct{ name, extract, analyze string }{
		{"cosmetic", `{"formula_groups":[{"group_code":"A","mixing_temperature_c":23}]}`,
			`{"study_observations":[{"source_code":"A","label":"synthetic observation"}]}`},
		{"automotive", `{"vehicle_platforms":[{"platform_code":"B","cycle_count":9007199254740993}]}`,
			`{"failure_modes":[{"source_code":"B","label":"synthetic fault"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := fixture(t, tc.name)
			definition, err := profile.Definition()
			if err != nil {
				t.Fatal(err)
			}
			if len(definition.Stages[1].Inputs) != 1 ||
				definition.Stages[1].Inputs["extraction"] != (StageInput{Source: InputOutput, StageID: "extract"}) {
				t.Fatal("analysis would receive inputs other than complete extraction")
			}
			for stage, output := range map[string]string{"extract": tc.extract, "analyze": tc.analyze} {
				if err := profile.ValidateOutput(stage, []byte(output)); err != nil {
					t.Fatal(err)
				}
				for _, bad := range []string{`{}`, `{"x":1,"x":2}`, output + "{}", "1e999999999"} {
					if err := profile.ValidateOutput(stage, []byte(bad)); err == nil {
						t.Errorf("accepted invalid %s output", stage)
					}
				}
			}
		})
	}
}

func TestStagesAndDependencyFingerprints(t *testing.T) {
	p := fixture(t, "automotive")
	definition, _ := p.Definition()
	hash, _ := p.StageFingerprints()

	definition.Mappings["records"] = json.RawMessage(`[]`)
	recompiled, err := importDefinition(t, definition)
	if err != nil {
		t.Fatal(err)
	}
	mappingHash, _ := recompiled.StageFingerprints()
	if hash["extract"] != mappingHash["extract"] || hash["analyze"] != mappingHash["analyze"] {
		t.Fatal("mapping-only edit invalidated model stages")
	}
	definition.Stages[0].Prompt += " Edited."
	recompiled, err = importDefinition(t, definition)
	if err != nil {
		t.Fatal(err)
	}
	edited, _ := recompiled.StageFingerprints()
	if hash["extract"] == edited["extract"] || hash["analyze"] == edited["analyze"] {
		t.Fatal("upstream edit did not invalidate downstream configuration")
	}
	extra := definition.Stages[0]
	extra.ID, extra.Optional = "source-audit", true
	definition.Stages = append(definition.Stages, extra)
	if _, err := importDefinition(t, definition); err != nil {
		t.Fatalf("independent optional stage: %v", err)
	}
	definition.Stages[1].Inputs["missing"] = StageInput{Source: InputOutput, StageID: extra.ID}
	if _, err := importDefinition(t, definition); err == nil {
		t.Fatal("accepted a dependency on a later stage")
	}
}

func TestOfflineSchemaPolicy(t *testing.T) {
	const head = `{"$schema":"https://json-schema.org/draft/2020-12/schema",`
	for name, schema := range map[string]string{
		"external":    head + `"$ref":"https://example.invalid/schema"}`,
		"filesystem":  head + `"$ref":"file:///etc/passwd"}`,
		"recursive":   head + `"$ref":"#/$defs/node","$defs":{"node":{"items":{"$ref":"#/$defs/node"}}}}`,
		"wrong draft": `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
		"example":     `{"title":"A sample report","data":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := compileSchema([]byte(schema)); err == nil {
				t.Fatal("accepted disallowed schema")
			}
		})
	}
	if _, err := compileSchema([]byte(head + `"$ref":"#/$defs/id","$defs":{"id":{"type":"string"}}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportProfile(strings.NewReader(strings.Repeat(" ", maxProfileBytes+1))); err == nil {
		t.Fatal("accepted oversized manifest")
	}
	if _, err := ImportProfile(strings.NewReader(`{"id":"a","id":"b"}`)); err == nil {
		t.Fatal("accepted duplicate manifest key")
	}
}
