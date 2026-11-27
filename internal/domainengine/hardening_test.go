package domainengine

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"
	"time"
)

func TestLongModelBudgetAndEstimator(t *testing.T) {
	budget, err := (ModelPin{ContextWindow: 196608, OutputBudget: 32768}).ForStage(StageDefinition{InputTokens: 125000, OutputTokens: 24576})
	must(t, err)
	if budget.InputBudget != 125000 || budget.OutputBudget != 24576 {
		t.Fatalf("wrong budget: %+v", budget)
	}
	if _, err = budget.ForStage(StageDefinition{InputTokens: 180000, OutputTokens: 24576}); err == nil {
		t.Fatal("overcommitted context")
	}
	payload := bytes.Repeat([]byte(" This is one synthetic sentence."), 4000)
	tokens, method := estimateInput(payload, "extract complete text")
	if method != "cl100k_base_plus_25_percent" || tokens >= len(payload) || !fitsInput(payload, "extract complete text", budget) {
		t.Fatalf("%s %d", method, tokens)
	}
}

func syntheticPackage(t *testing.T) ([]byte, map[string][]byte) {
	t.Helper()
	var word bytes.Buffer
	docx := zip.NewWriter(&word)
	xml, err := docx.Create("word/document.xml")
	must(t, err)
	_, err = xml.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Mock semantic prompt</w:t></w:r></w:p></w:body></w:document>`))
	must(t, err)
	must(t, docx.Close())
	input := map[string][]byte{"manifest.json": fixture(t, "automotive").Manifest(), "original/flow1.txt": []byte("fixture prompt"),
		"original/flow2.docx": word.Bytes(), "original/flow1.json": []byte(`{"observations":[]}`), "original/flow2.json": []byte(`{"unknown":null}`)}
	var raw bytes.Buffer
	bundle := zip.NewWriter(&raw)
	for name, content := range input {
		file, err := bundle.Create(name)
		must(t, err)
		_, err = file.Write(content)
		must(t, err)
	}
	must(t, bundle.Close())
	return raw.Bytes(), input
}

func TestProfilePackageOriginalHashes(t *testing.T) {
	raw, input := syntheticPackage(t)
	p, err := ImportPackage(raw)
	must(t, err)
	if p.SHA256 != digest(raw) || !bytes.Equal(p.archive, raw) || p.DOCXText != "Mock semantic prompt\n" {
		t.Fatal("package provenance was lost")
	}
	for name, content := range input {
		if digest(content) != p.Files[name] {
			t.Fatalf("wrong digest on %s", name)
		}
	}
}

type longModel struct {
	timeout time.Duration
	budget  int
}

func (m *longModel) Pin(context.Context, uint64, string) (ModelPin, error) { return ModelPin{}, nil }
func (m *longModel) Generate(ctx context.Context, _ uint64, _ string, pin ModelPin, _ string, _ string) (ModelResult, error) {
	deadline, _ := ctx.Deadline()
	m.timeout = time.Until(deadline)
	m.budget = pin.OutputBudget
	return ModelResult{Called: true, Text: `{"vehicle_platforms":[]}`, FinishReason: "stop"}, nil
}
func TestStageAppliesLongTimeoutAndOutput(t *testing.T) {
	p := fixture(t, "automotive")
	stage := p.profile.Stages[0]
	stage.OutputTokens = 24576
	stage.InputTokens = 120000
	stage.TimeoutSeconds = 1200
	model := new(longModel)
	w := &Worker{Models: model}
	out := w.execute(context.Background(), 1, p, stage, ModelPin{ContextWindow: 196608, OutputBudget: 32768},
		map[string]string{"source": "synthetic manuscript"}, 0, nil)
	if out.valid == nil || model.budget != 24576 || model.timeout < 19*time.Minute || model.timeout > 20*time.Minute {
		t.Fatalf("long policy not applied: %+v / %+v", model, out)
	}
}
