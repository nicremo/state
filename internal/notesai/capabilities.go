package notesai

import (
	"context"
	"sync"
	"time"

	"github.com/nicremo/state/internal/state"
)

// Capabilities is what the app may offer right now. The app never builds in
// a limit of its own: the photo picker's maximum comes from here.
type Capabilities struct {
	AIAvailable bool   `json:"ai_available"`
	Reason      string `json:"reason,omitempty"`
	Consent     bool   `json:"consent"`
	Agent       struct {
		Model     string `json:"model"`
		Available bool   `json:"available"`
		ToolCalls bool   `json:"tool_calls"`
	} `json:"agent"`
	Vision struct {
		Available     bool   `json:"available"`
		MaxImages     int    `json:"max_images"`
		MaxImageBytes int64  `json:"max_image_bytes"`
		MaxTotalBytes int64  `json:"max_total_bytes"`
		Model         string `json:"model"`
		LimitSource   string `json:"limit_source"`
	} `json:"vision"`
	Audio struct {
		Available       bool   `json:"available"`
		MaxSegmentBytes int64  `json:"max_segment_bytes"`
		MaxSegments     int    `json:"max_segments"`
		SegmentSeconds  int    `json:"segment_seconds"`
		Model           string `json:"model"`
	} `json:"audio"`
}

const (
	ReasonNotConfigured       = "not_configured"
	ReasonCatalogUnavailable  = "catalog_unavailable"
	ReasonAgentModelMissing   = "agent_model_unavailable"
	ReasonAgentNeedsToolCalls = "agent_model_without_tools"
	// Tokens kept free for instructions, context notes and the answer.
	contextReserveTokens = 32000
	// A conservative budget per image; providers count 1000 to 1600.
	tokensPerImage = 2000
)

// ResolveCapabilities combines the live model catalogue with the server's
// own limits. OpenRouter publishes modalities and context length, not an
// image count, so the image limit is the smaller of the server policy and
// what the context window leaves room for; it is never "unlimited".
func ResolveCapabilities(models []Model, sttModels []Model, settings state.NoteAISettings, keyConfigured bool, policy state.NoteMediaPolicy) Capabilities {
	var capabilities Capabilities
	capabilities.Consent = settings.Consent
	capabilities.Agent.Model = settings.AgentModel
	capabilities.Vision.Model = settings.AgentModel
	capabilities.Audio.Model = settings.TranscriptionModel
	capabilities.Vision.MaxImageBytes = policy.MaxImageBytes
	capabilities.Vision.MaxTotalBytes = policy.MaxTotalImageBytes
	capabilities.Audio.MaxSegmentBytes = policy.MaxAudioBytes
	capabilities.Audio.MaxSegments = policy.MaxAudioSegments
	capabilities.Audio.SegmentSeconds = policy.AudioSegmentSecs

	if !keyConfigured {
		capabilities.Reason = ReasonNotConfigured
		return capabilities
	}
	if models == nil {
		capabilities.Reason = ReasonCatalogUnavailable
		return capabilities
	}
	agent, found := findModel(models, settings.AgentModel)
	if !found {
		capabilities.Reason = ReasonAgentModelMissing
		return capabilities
	}
	capabilities.Agent.ToolCalls = agent.Supports("tools")
	if !capabilities.Agent.ToolCalls {
		capabilities.Reason = ReasonAgentNeedsToolCalls
		return capabilities
	}
	capabilities.Agent.Available = true
	capabilities.AIAvailable = true

	if agent.Accepts("image") {
		limit := policy.MaxImages
		capabilities.Vision.LimitSource = "server_policy"
		if agent.ContextLength > 0 {
			byContext := (agent.ContextLength - contextReserveTokens) / tokensPerImage
			if byContext < limit {
				limit = byContext
				capabilities.Vision.LimitSource = "model_context"
			}
		}
		if limit > 0 {
			capabilities.Vision.Available = true
			capabilities.Vision.MaxImages = limit
		}
	}
	if stt, found := findModel(sttModels, settings.TranscriptionModel); found && contains(stt.Architecture.OutputModalities, "transcription") {
		capabilities.Audio.Available = true
	}
	return capabilities
}

func findModel(models []Model, id string) (Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

// Gateway owns the OpenRouter client and a cached model catalogue, refreshed
// periodically so a model that loses a capability is switched off safely.
type Gateway struct {
	client  *Client
	policy  state.NoteMediaPolicy
	refresh time.Duration

	mu        sync.Mutex
	models    []Model
	sttModels []Model
	fetchedAt time.Time
	settings  state.NoteAISettings
}

func NewGateway(client *Client, policy state.NoteMediaPolicy) *Gateway {
	return &Gateway{client: client, policy: policy, refresh: 6 * time.Hour}
}

func (gateway *Gateway) Client() *Client {
	return gateway.client
}

// Configured reports whether the server has an OpenRouter key.
func (gateway *Gateway) Configured() bool {
	return gateway != nil && gateway.client.Configured()
}

// Capabilities refreshes the catalogue when it is stale and resolves what
// the current settings allow. A failed refresh keeps the last catalogue.
func (gateway *Gateway) Capabilities(ctx context.Context, settings state.NoteAISettings) Capabilities {
	gateway.mu.Lock()
	stale := gateway.models == nil || time.Since(gateway.fetchedAt) > gateway.refresh
	gateway.mu.Unlock()
	if stale && gateway.Configured() {
		gateway.Refresh(ctx)
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.settings = settings
	return ResolveCapabilities(gateway.models, gateway.sttModels, settings, gateway.Configured(), gateway.policy)
}

// Refresh loads both catalogues.
func (gateway *Gateway) Refresh(ctx context.Context) {
	fetchContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	models, err := gateway.client.Models(fetchContext)
	if err != nil {
		return
	}
	sttModels, err := gateway.client.TranscriptionModels(fetchContext)
	if err != nil {
		return
	}
	gateway.mu.Lock()
	gateway.models, gateway.sttModels, gateway.fetchedAt = models, sttModels, time.Now()
	gateway.mu.Unlock()
}

// Model returns a catalogue entry, for pricing estimates.
func (gateway *Gateway) Model(id string) (Model, bool) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return findModel(gateway.models, id)
}

// MediaPolicy is the upload policy the state service enforces: the server
// limits, with the image count lowered to what the model allows.
func (gateway *Gateway) MediaPolicy() state.NoteMediaPolicy {
	policy := gateway.policy
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.models == nil {
		return policy
	}
	capabilities := ResolveCapabilities(gateway.models, gateway.sttModels, gateway.effectiveSettings(), true, gateway.policy)
	if capabilities.Vision.Available {
		policy.MaxImages = capabilities.Vision.MaxImages
	} else if capabilities.Agent.Available {
		policy.MaxImages = 0
	}
	return policy
}

func (gateway *Gateway) effectiveSettings() state.NoteAISettings {
	settings := gateway.settings
	if settings.AgentModel == "" {
		settings.AgentModel = state.DefaultNotesAgentModel
	}
	if settings.TranscriptionModel == "" {
		settings.TranscriptionModel = state.DefaultTranscriptionModel
	}
	return settings
}
