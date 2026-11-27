// Package domainengine implements the domain configuration and artifact contracts.
package domainengine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxProfileBytes = 1 << 20
	maxResultBytes  = 4 << 20
	draft2020URI    = "https://json-schema.org/draft/2020-12/schema"
	schemaResource  = "https://domain.invalid/output"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,95}$`)

// Profile is an editable document. Publish stores an immutable revision of it.
type Profile struct {
	ID              string                     `json:"id"`
	Revision        string                     `json:"revision"`
	Name            string                     `json:"name"`
	Stages          []StageDefinition          `json:"stages"`
	Classifications map[string]json.RawMessage `json:"classifications,omitempty"`
	Mappings        map[string]json.RawMessage `json:"mappings,omitempty"`
}

type StageDefinition struct {
	ID       string                `json:"id"`
	Optional bool                  `json:"optional,omitempty"`
	Inputs   map[string]StageInput `json:"inputs"`
	Prompt   string                `json:"prompt"`
	Schema   json.RawMessage       `json:"schema"`
	ModelRef string                `json:"model_ref"`
}

type InputSource string

const (
	InputDocument InputSource = "document"
	InputCoverage InputSource = "coverage"
	InputOutput   InputSource = "stage_output"
)

type StageInput struct {
	Source  InputSource `json:"source"`
	StageID string      `json:"stage_id,omitempty"`
}

// DraftGenerator is reserved for a future assisted editing workflow. A
// generated draft must pass the same import and explicit publish flow.
type DraftGenerator interface {
	GenerateDraft(ctx context.Context, description string) ([]byte, error)
}

type CompiledProfile struct {
	profile Profile
	raw     []byte
	schemas map[string]*jsonschema.Schema
}

func (p *CompiledProfile) Fingerprint() string { return digest(p.raw) }

// Definition returns an independent copy of the editable configuration.
func (p *CompiledProfile) Definition() (Profile, error) {
	var out Profile
	err := json.Unmarshal(p.raw, &out)
	return out, err
}

// ImportProfile validates a bounded JSON manifest without filesystem or network access.
func ImportProfile(r io.Reader) (*CompiledProfile, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxProfileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxProfileBytes {
		return nil, errors.New("domain profile exceeds size limit")
	}
	if err := checkJSON(raw); err != nil {
		return nil, err
	}

	var profile Profile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&profile); err != nil {
		return nil, err
	}
	if !identifier.MatchString(profile.ID) || !identifier.MatchString(profile.Revision) ||
		strings.TrimSpace(profile.Name) == "" || len(profile.Stages) == 0 || len(profile.Stages) > 16 {
		return nil, errors.New("invalid profile identity, name or stage count")
	}
	compiled := &CompiledProfile{profile: profile, raw: raw, schemas: make(map[string]*jsonschema.Schema)}
	previous := make(map[string]StageDefinition)
	for _, stage := range profile.Stages {
		if err := validateStage(stage, previous); err != nil {
			return nil, fmt.Errorf("stage %q: %w", stage.ID, err)
		}
		schema, err := compileSchema(stage.Schema)
		if err != nil {
			return nil, fmt.Errorf("stage %q schema: %w", stage.ID, err)
		}
		compiled.schemas[stage.ID] = schema
		previous[stage.ID] = stage
	}
	return compiled, nil
}

func validateStage(stage StageDefinition, previous map[string]StageDefinition) error {
	_, duplicate := previous[stage.ID]
	if duplicate || !identifier.MatchString(stage.ID) || !identifier.MatchString(stage.ModelRef) ||
		strings.TrimSpace(stage.Prompt) == "" || len(stage.Inputs) == 0 || len(stage.Inputs) > 32 {
		return errors.New("invalid stage identity, model, prompt or inputs")
	}
	for name, input := range stage.Inputs {
		if !identifier.MatchString(name) {
			return errors.New("invalid input name")
		}
		switch input.Source {
		case InputDocument, InputCoverage:
			if input.StageID != "" {
				return fmt.Errorf("input %q must not name a stage", name)
			}
		case InputOutput:
			dependency, found := previous[input.StageID]
			if !found || (dependency.Optional && !stage.Optional) {
				return fmt.Errorf("input %q needs an available earlier stage", name)
			}
		default:
			return fmt.Errorf("input %q has an unknown source", name)
		}
	}
	return nil
}

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}

func compileSchema(raw []byte) (*jsonschema.Schema, error) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if document["$schema"] != draft2020URI {
		return nil, errors.New("explicit JSON Schema Draft 2020-12 is required")
	}
	if err := checkSchemaPolicy(document); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(offlineLoader{})
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, err
	}
	return compiler.Compile(schemaResource)
}

func (p *CompiledProfile) ValidateOutput(stageID string, raw []byte) error {
	schema, found := p.schemas[stageID]
	if !found {
		return errors.New("unknown stage")
	}
	if len(raw) > maxResultBytes {
		return errors.New("stage output exceeds size limit")
	}
	if err := checkJSON(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return err
	}
	return schema.Validate(value)
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// StageFingerprints returns each stage's configuration hash, including the
// configuration of its declared upstream stages. Callers add runtime input
// artifact hashes and resolved model configuration when scheduling a run.
func (p *CompiledProfile) StageFingerprints() (map[string]string, error) {
	hashes := make(map[string]string, len(p.profile.Stages))
	for _, stage := range p.profile.Stages {
		dependencies := make(map[string]string)
		for _, input := range stage.Inputs {
			if input.Source == InputOutput {
				dependencies[input.StageID] = hashes[input.StageID]
			}
		}
		raw, err := json.Marshal(struct {
			Stage        StageDefinition
			Dependencies map[string]string
		}{stage, dependencies})
		if err != nil {
			return nil, err
		}
		hashes[stage.ID] = digest(raw)
	}
	return hashes, nil
}
