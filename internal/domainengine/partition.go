package domainengine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ArrayPartition is an explicit profile opt-in. Each contiguous document
// partition must produce the same single-array object. The engine appends
// records in source order. Stages consuming prior JSON cannot opt in.
type ArrayPartition struct {
	ArrayKey string `json:"array_key"`
	MaxParts int    `json:"max_parts"`
}
type StageCoverage struct {
	Mode           string `json:"mode"`
	TotalBytes     int    `json:"total_bytes"`
	CompletedBytes int    `json:"completed_bytes"`
	Parts          int    `json:"parts"`
	CompletedParts int    `json:"completed_parts"`
}

func validatePartition(stage StageDefinition) error {
	spec := stage.Partition
	if spec == nil {
		return nil
	}
	if !identifier.MatchString(spec.ArrayKey) || spec.MaxParts < 2 || spec.MaxParts > 8 || len(stage.Inputs) != 1 || len(stage.SemanticChecks) > 0 {
		return errors.New("array partition requires one document input, an array key and 2–8 parts")
	}
	for _, input := range stage.Inputs {
		if input.Source != InputDocument {
			return errors.New("only document inputs may be partitioned")
		}
	}
	return nil
}

func fitsInput(body []byte, system string, pin ModelPin) bool {
	return len(body)+len(system)+256 <= pin.InputBudget
}

// documentParts preserves every byte, including paragraph separators.
func documentParts(source, inputKey, system string, pin ModelPin, limit int) ([]string, bool) {
	paragraphs := strings.SplitAfterSeq(source, "\n\n")
	parts := make([]string, 0, limit)
	var current string
	for paragraph := range paragraphs {
		proposed := current + paragraph
		raw, _ := json.Marshal(map[string]string{inputKey: proposed})
		if fitsInput(raw, system, pin) {
			current = proposed
			continue
		}
		if current == "" || len(parts)+1 >= limit {
			return nil, false
		}
		parts = append(parts, current)
		raw, _ = json.Marshal(map[string]string{inputKey: paragraph})
		if !fitsInput(raw, system, pin) {
			return nil, false
		}
		current = paragraph
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts, len(parts) > 1
}

func (w *Worker) executePartition(ctx context.Context, tenant uint64, p *CompiledProfile, stage StageDefinition, pin ModelPin,
	instructions string, input map[string]string, retries int, authorize authorizeModelCall) stageOutcome {
	var key, source string
	for key, source = range input {
	}
	spec := stage.Partition
	instructions += "\nThis input is one contiguous partition of the document. Extract covered records only. Return a JSON object with one array property named " + spec.ArrayKey + "."
	parts, ok := documentParts(source, key, instructions, pin, spec.MaxParts)
	if !ok {
		return stageOutcome{state: "needs_review", code: "cannot_partition_complete_input", coverage: StageCoverage{Mode: "not_sent", TotalBytes: len(source)}}
	}
	coverage := StageCoverage{Mode: "paragraph_partition", TotalBytes: len(source), Parts: len(parts)}
	rawResponses := make([]string, 0, len(parts))
	merged := make([]json.RawMessage, 0)
	total := ModelResult{}
	var size int
	for _, part := range parts {
		body, _ := json.Marshal(map[string]string{key: part})
		validated := w.callModel(ctx, tenant, p, stage, pin, instructions, body, map[string]string{key: part}, retries, authorize)
		total.PromptTokens += validated.result.PromptTokens
		total.CompletionTokens += validated.result.CompletionTokens
		rawResponses = append(rawResponses, validated.result.Text)
		size += len(validated.result.Text)
		if size > maxResultBytes/2 {
			return stageOutcome{state: "needs_review", code: "partition_output_exceeds_limit", result: total, coverage: coverage}
		}
		raw, _ := json.Marshal(rawResponses)
		total.Text = string(raw)
		if validated.valid == nil {
			validated.result, validated.coverage = total, coverage
			return validated
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(validated.valid, &object); err != nil || len(object) != 1 || len(object[spec.ArrayKey]) == 0 || object[spec.ArrayKey][0] != '[' {
			return stageOutcome{state: "needs_review", code: "partition_result_contract", result: total, coverage: coverage}
		}
		var records []json.RawMessage
		if err := json.Unmarshal(object[spec.ArrayKey], &records); err != nil {
			return stageOutcome{state: "needs_review", code: "partition_result_contract", result: total, coverage: coverage}
		}
		merged = append(merged, records...)
		coverage.CompletedParts++
		coverage.CompletedBytes += len(part)
	}
	valid, err := json.Marshal(map[string][]json.RawMessage{spec.ArrayKey: merged})
	if err != nil || p.ValidateOutput(stage.ID, valid) != nil {
		return stageOutcome{state: "needs_review", code: "merged_output_invalid", result: total, coverage: coverage}
	}
	return stageOutcome{state: "queued", result: total, valid: valid, coverage: coverage}
}
