package service

import (
	"context"
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/domainengine"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestDomainCaptureDisabledUsesOriginalContext(t *testing.T) {
	service := &knowledgeService{}
	ctx := context.Background()
	doc := &types.Knowledge{FilePath: "source", FileHash: "hash"}
	actual, capture := service.beginDomainCapture(ctx, types.DocumentProcessPayload{FilePath: "source"}, doc, 1)
	if actual != ctx || capture != nil {
		t.Fatal("disabled domain engine changed the native parse")
	}
	request := &types.ReadRequest{FileContent: []byte("synthetic original")}
	recordDomainParserInput(actual, request, "")
	if string(request.FileContent) != "synthetic original" {
		t.Fatal("disabled capture modified the parser input")
	}
}

func TestDomainCapturePreservesFullTextAndCoverage(t *testing.T) {
	capture := &domainengine.ParseSnapshot{}
	ctx := context.WithValue(context.Background(), domainCaptureKey{}, capture)
	original := []byte("original\x00")
	recordDomainParserInput(ctx, &types.ReadRequest{FileContent: original, FileType: "md",
		ParserEngineOverrides: map[string]string{"fixture_key": "not persisted"}}, "")
	const paragraph = "中😀 文档完整解析后的合成试验正文，用于验证来源位置。 Synthetic document body."
	parsed := paragraph + "\r\n\r\n| A | B |\r\n| - | - |\r\n| x | y |"
	normalized := paragraph + "\n\n| A | B |\n| - | - |\n| x | y |"
	end := utf8.RuneCountInString(paragraph)
	blocks := []types.SourceBlock{{Start: 0, End: end, Locator: types.SourceLocator{
		Type: types.SourceLocatorPDF, Page: 1,
	}}}
	setDomainParseOutput(capture, parsed, blocks, &types.ReadResult{MarkdownContent: normalized}, nil)
	coverage := capture.Coverage
	if len(capture.ParserConfigSHA256) != 64 || string(capture.Original) != string(original) ||
		capture.ParsedMarkdown != parsed || capture.NormalizedMarkdown != normalized ||
		coverage.Body != domainengine.Observed || coverage.Tables != domainengine.Observed ||
		coverage.SourcePositions != domainengine.Observed || coverage.Captions != domainengine.Unknown ||
		coverage.Supplements != domainengine.Unknown || coverage.ParseCompleteness != domainengine.Unknown ||
		coverage.ModelInput != domainengine.NotEvaluated || coverage.ExtractionQuality != domainengine.NotEvaluated {
		t.Fatalf("incomplete or overstated snapshot: %+v", coverage)
	}
	var coordinates struct {
		Parsed     []types.SourceBlock
		Normalized []types.SourceBlock
	}
	if err := json.Unmarshal(capture.SourceMap, &coordinates); err != nil {
		t.Fatal(err)
	}
	// Remapping associates the paragraph and its two normalized line breaks.
	if len(coordinates.Parsed) != 1 || len(coordinates.Normalized) != 1 ||
		coordinates.Parsed[0].End != end || coordinates.Normalized[0].Start != 0 ||
		coordinates.Normalized[0].End != end+2 || coordinates.Normalized[0].Locator.Page != 1 {
		t.Fatalf("rune coordinates changed or were lost: %+v", coordinates)
	}
}
