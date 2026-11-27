package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Tencent/WeKnora/internal/domainengine"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type DomainModels struct{ Service interfaces.ModelService }

func (m DomainModels) Pin(ctx context.Context, tenant uint64, id string) (domainengine.ModelPin, error) {
	row, err := m.Service.GetModelByID(types.WithExecutionTenant(ctx, tenant), id)
	if err != nil {
		return domainengine.ModelPin{}, err
	}
	if row.Type != types.ModelTypeKnowledgeQA || row.Status != types.ModelStatusActive {
		return domainengine.ModelPin{}, errors.New("active chat model required")
	}
	params := row.Parameters
	params.APIKey, params.AppSecret = "", ""
	payload, err := json.Marshal(struct {
		Name       string
		Source     types.ModelSource
		Parameters types.ModelParameters
	}{row.Name, row.Source, params})
	if err != nil {
		return domainengine.ModelPin{}, err
	}
	sum := sha256.Sum256(payload)
	window := params.ContextWindow
	if window <= 0 {
		window = 8192
	}
	out := params.MaxOutputTokens
	if out <= 0 {
		out = 2048
	}
	if out >= window {
		out = window / 2
	}
	if out < 1 {
		return domainengine.ModelPin{}, errors.New("insufficient model context")
	}
	return domainengine.ModelPin{Fingerprint: hex.EncodeToString(sum[:]), InputBudget: window - out, OutputBudget: out, ContextWindow: window}, nil
}

func (m DomainModels) Generate(ctx context.Context, tenant uint64, id string, pin domainengine.ModelPin, system, user string) (domainengine.ModelResult, error) {
	ctx = types.WithExecutionTenant(ctx, tenant)
	before, err := m.Pin(ctx, tenant, id)
	if ctx.Err() != nil {
		return domainengine.ModelResult{}, ctx.Err()
	}
	if err != nil || before.Fingerprint != pin.Fingerprint || pin.OutputBudget > before.OutputBudget {
		return domainengine.ModelResult{}, domainengine.ErrModelChanged
	}
	provider, err := m.Service.GetChatModel(ctx, id)
	if err != nil {
		return domainengine.ModelResult{}, err
	}
	after, err := m.Pin(ctx, tenant, id)
	if ctx.Err() != nil {
		return domainengine.ModelResult{}, ctx.Err()
	}
	if err != nil || after != before {
		return domainengine.ModelResult{}, domainengine.ErrModelChanged
	}
	resp, err := provider.Chat(ctx, []chat.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}, &chat.ChatOptions{
		MaxTokens: pin.OutputBudget,
	})
	if err != nil {
		var httpErr *api.HTTPError
		if errors.As(err, &httpErr) {
			switch {
			case httpErr.StatusCode == 429:
				return domainengine.ModelResult{Called: true}, &domainengine.ProviderFailure{Code: "provider_rate_limited", RetryAfter: httpErr.RetryAfter()}
			case httpErr.StatusCode == 401 || httpErr.StatusCode == 403:
				return domainengine.ModelResult{Called: true}, &domainengine.ProviderFailure{Code: "provider_auth_rejected", Permanent: true}
			}
		}
		return domainengine.ModelResult{Called: true}, err
	}

	if resp == nil {
		return domainengine.ModelResult{Called: true}, errors.New("empty domain model response")
	}
	return domainengine.ModelResult{Called: true, Text: resp.Content, FinishReason: resp.FinishReason, ToolAttempt: len(resp.ToolCalls) > 0,
		PromptTokens: resp.Usage.PromptTokens, CompletionTokens: resp.Usage.CompletionTokens}, nil
}
