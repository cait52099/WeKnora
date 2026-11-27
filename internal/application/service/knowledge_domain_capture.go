package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/domainengine"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

type domainCaptureKey struct{}

func (s *knowledgeService) beginDomainCapture(ctx context.Context, payload types.DocumentProcessPayload,
	knowledge *types.Knowledge, attempt int,
) (context.Context, *domainengine.ParseSnapshot) {
	if s.domainStore == nil || attempt < 1 || payload.FileURL != "" || payload.URL != "" ||
		payload.FilePath == "" || payload.FilePath != knowledge.FilePath ||
		knowledge.FileHash == "" || len(payload.Passages) > 0 {
		return ctx, nil
	}
	scope := domainengine.Scope{TenantID: knowledge.TenantID, KBID: knowledge.KnowledgeBaseID}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	binding, err := s.domainStore.ActiveBinding(lookupCtx, scope)
	if err != nil {
		logger.Warnf(ctx, "domain capture binding lookup failed for %s", knowledge.ID)
		return ctx, nil
	}
	if binding == nil {
		return ctx, nil
	}
	capture := &domainengine.ParseSnapshot{
		Binding: *binding, DocumentID: knowledge.ID, Attempt: attempt,
		FilePath: knowledge.FilePath, NativeFileHash: knowledge.FileHash,
	}
	return context.WithValue(ctx, domainCaptureKey{}, capture), capture
}

func recordDomainParserInput(ctx context.Context, req *types.ReadRequest, asrModel string) {
	capture, _ := ctx.Value(domainCaptureKey{}).(*domainengine.ParseSnapshot)
	if capture == nil || len(req.FileContent) == 0 || len(req.FileContent) > domainengine.MaxOriginalBytes {
		return
	}
	capture.Original = req.FileContent
	// Keep only the fingerprint. Override values can include parser credentials.
	config, _ := json.Marshal(struct {
		Engine, FileType, ASRModel string
		Overrides                  map[string]string
	}{req.ParserEngine, req.FileType, asrModel, req.ParserEngineOverrides})
	hash := sha256.Sum256(config)
	capture.ParserConfigSHA256 = hex.EncodeToString(hash[:])
}

func (s *knowledgeService) finishDomainCapture(ctx context.Context, capture *domainengine.ParseSnapshot,
	parsed string, blocks []types.SourceBlock, result *types.ReadResult, images []docparser.StoredImage,
) {
	if len(capture.Original) == 0 || result == nil {
		logger.Warnf(ctx, "domain capture unavailable for %s: missing or oversized input", capture.DocumentID)
		return
	}
	setDomainParseOutput(capture, parsed, blocks, result, images)
	saveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.domainStore.SaveSnapshot(saveCtx, *capture); err != nil {
		switch {
		case errors.Is(err, domainengine.ErrStaleSource):
			logger.Infof(ctx, "domain capture skipped for stale source %s", capture.DocumentID)
		case errors.Is(err, domainengine.ErrConflict):
			logger.Warnf(ctx, "domain capture conflicts with existing attempt for %s", capture.DocumentID)
		default:
			logger.Warnf(ctx, "domain capture could not persist for %s", capture.DocumentID)
		}
	}
}

func setDomainParseOutput(capture *domainengine.ParseSnapshot, parsed string, blocks []types.SourceBlock,
	result *types.ReadResult, images []docparser.StoredImage,
) {
	capture.ParsedMarkdown = parsed
	capture.NormalizedMarkdown = result.MarkdownContent
	type imagePosition struct {
		Reference string               `json:"reference"`
		Locators  types.SourceLocators `json:"locators"`
	}
	type parserImage struct {
		Filename string               `json:"filename"`
		MimeType string               `json:"media_type"`
		Locators types.SourceLocators `json:"locators"`
	}
	parserImages := make([]parserImage, 0, len(result.ImageRefs))
	hasParserImagePositions := false
	for _, image := range result.ImageRefs {
		parserImages = append(parserImages, parserImage{image.Filename, image.MimeType, image.SourceLocators})
		hasParserImagePositions = hasParserImagePositions || len(image.SourceLocators) > 0
	}
	positions := make([]imagePosition, 0, len(images))
	for _, image := range images {
		positions = append(positions, imagePosition{image.ServingURL, image.SourceLocators})
	}
	normalizedBlocks := sourceloc.RemapBlocks(blocks, parsed, result.MarkdownContent)
	// Source maps carry separate rune coordinates for the two text revisions.
	capture.SourceMap, _ = json.Marshal(struct {
		Parsed       []types.SourceBlock `json:"parsed"`
		Normalized   []types.SourceBlock `json:"normalized"`
		Images       []imagePosition     `json:"images"`
		ParserImages []parserImage       `json:"parser_images"`
	}{blocks, normalizedBlocks, positions, parserImages})
	capture.Coverage = domainCoverage(result.MarkdownContent, len(result.ImageRefs)+len(images) > 0,
		len(blocks) > 0 || hasParserImagePositions || domainHasImagePositions(images))
}

func domainHasImagePositions(images []docparser.StoredImage) bool {
	for _, image := range images {
		if len(image.SourceLocators) > 0 {
			return true
		}
	}
	return false
}

func domainCoverage(markdown string, images, positions bool) domainengine.DocumentCoverageReport {
	report := domainengine.DocumentCoverageReport{
		Body: domainengine.Unknown, Tables: domainengine.Unknown, Images: domainengine.Unknown,
		Captions: domainengine.Unknown, Supplements: domainengine.Unknown, SourcePositions: domainengine.Unknown,
		ParseCompleteness: domainengine.Unknown, ModelInput: domainengine.NotEvaluated,
		ExtractionQuality: domainengine.NotEvaluated,
	}
	// Bound the optional AST inspection without truncating the stored text.
	// Signals without other parser evidence remain unknown above this limit.
	if len(markdown) <= 4<<20 && strings.TrimSpace(markdown) != "" {
		source := []byte(markdown)
		root := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(source))
		_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			switch n := node.(type) {
			case *ast.Text:
				if domainBodyText(n) && strings.TrimSpace(string(n.Segment.Value(source))) != "" {
					report.Body = domainengine.Observed
				}
			case *extensionast.Table:
				report.Tables = domainengine.Observed
			case *ast.Image:
				images = true
			case *ast.HTMLBlock:
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(n.Lines().Value(source)))), "<table") {
					report.Tables = domainengine.Observed
				}
			}
			return ast.WalkContinue, nil
		})
	}
	if images {
		report.Images = domainengine.Observed
	}
	if positions {
		report.SourcePositions = domainengine.Observed
	}
	return report
}

func domainBodyText(node ast.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.(type) {
		case *extensionast.Table, *ast.Image:
			return false
		}
	}
	return true
}
