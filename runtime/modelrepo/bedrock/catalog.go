package bedrock

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

const inferenceTypeInferenceProfile types.InferenceType = "INFERENCE_PROFILE"

type catalogProvider struct {
	spec       modelrepo.BackendSpec
	httpClient *http.Client
	tracker    libtracker.ActivityTracker
}

func init() {
	modelrepo.RegisterCatalogProvider("bedrock", func(spec modelrepo.BackendSpec, opts modelrepo.CatalogOptions) (modelrepo.CatalogProvider, error) {
		return &catalogProvider{spec: spec, httpClient: opts.HTTPClient, tracker: opts.Tracker}, nil
	})
	modelrepo.RegisterThinkDeclarationRefusal("bedrock", thinkDeclarationRefusal())
}

func (p *catalogProvider) Type() string { return "bedrock" }

func (p *catalogProvider) ListModels(ctx context.Context) ([]modelrepo.ObservedModel, error) {
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	cfg, err := loadAWSConfig(ctx, regionFromURL(p.spec.BaseURL), p.spec.APIKey, p.httpClient)
	if err != nil {
		return nil, err
	}

	client := bedrock.NewFromConfig(cfg)

	output, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to list foundation models: %w", err)
	}

	profileIDs, _ := listInferenceProfileIDs(ctx, client)

	models := []modelrepo.ObservedModel{}
	for _, summary := range output.ModelSummaries {
		invokeID, ok := resolveInvocableModelID(aws.ToString(summary.ModelId), summary.InferenceTypesSupported, profileIDs)
		if !ok {
			continue
		}
		models = append(models, observedFromSummary(summary, invokeID))
	}

	return modelrepo.WithoutUnavailableModels(p.AvailabilityScope(), models), nil
}

func (p *catalogProvider) AvailabilityScope() string {
	return availabilityScope(regionFromURL(p.spec.BaseURL), p.spec.APIKey)
}

func listInferenceProfileIDs(ctx context.Context, client *bedrock.Client) ([]string, error) {
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListInferenceProfiles(ctx, &bedrock.ListInferenceProfilesInput{
			NextToken:  nextToken,
			TypeEquals: types.InferenceProfileTypeSystemDefined,
		})
		if err != nil {
			return nil, err
		}
		for _, s := range out.InferenceProfileSummaries {
			if id := aws.ToString(s.InferenceProfileId); id != "" {
				ids = append(ids, id)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			return ids, nil
		}
		nextToken = out.NextToken
	}
}

func resolveInvocableModelID(modelID string, inferenceTypes []types.InferenceType, profileIDs []string) (string, bool) {
	var onDemand, viaProfile bool
	for _, t := range inferenceTypes {
		switch t {
		case types.InferenceTypeOnDemand:
			onDemand = true
		case inferenceTypeInferenceProfile:
			viaProfile = true
		}
	}
	if onDemand {
		return modelID, true
	}
	if !viaProfile {
		return "", false
	}
	suffix := "." + modelID
	var matches []string
	for _, pid := range profileIDs {
		if strings.HasSuffix(pid, suffix) {
			matches = append(matches, pid)
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		gi := strings.HasPrefix(matches[i], "global.")
		gj := strings.HasPrefix(matches[j], "global.")
		return !gi && gj
	})
	return matches[0], true
}

func observedFromSummary(summary types.FoundationModelSummary, invokeID string) modelrepo.ObservedModel {
	inputs := bedrockModalities(summary.InputModalities)
	outputs := bedrockModalities(summary.OutputModalities)
	text := inputs[types.ModelModalityText] && outputs[types.ModelModalityText]
	embedding := inputs[types.ModelModalityEmbedding] || outputs[types.ModelModalityEmbedding]
	conversational := text && !embedding

	meta := map[string]string{}
	if name := displayNameFor(aws.ToString(summary.ModelName), aws.ToString(summary.ModelId), invokeID); name != "" {
		meta[modelrepo.ObservedDisplayNameMetaKey] = name
	}

	return modelrepo.ObservedModel{
		Name: invokeID,
		CapabilityConfig: modelrepo.CapabilityConfig{
			CanChat:   conversational,
			CanStream: conversational && aws.ToBool(summary.ResponseStreamingSupported),
			CanPrompt: conversational,
			CanVision: inputs[types.ModelModalityImage],
		},
		Meta: meta,
	}
}

func displayNameFor(vendorName, baseID, invokeID string) string {
	if vendorName == "" || invokeID == baseID {
		return vendorName
	}
	prefix := strings.TrimSuffix(invokeID, "."+baseID)
	if prefix == invokeID {
		return vendorName
	}
	return fmt.Sprintf("%s (cross-region inference profile, %s)", vendorName, prefix)
}

func bedrockModalities(modalities []types.ModelModality) map[types.ModelModality]bool {
	set := make(map[types.ModelModality]bool, len(modalities))
	for _, modality := range modalities {
		set[types.ModelModality(strings.ToUpper(string(modality)))] = true
	}
	return set
}

func (p *catalogProvider) ProviderFor(model modelrepo.ObservedModel) modelrepo.Provider {
	return NewBedrockProvider(
		regionFromURL(p.spec.BaseURL),
		p.spec.APIKey,
		model.Name,
		model.CapabilityConfig,
		p.httpClient,
		p.tracker,
	)
}
