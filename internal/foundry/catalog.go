// Package foundry discovers synchronous OpenAI-compatible chat deployments in
// Azure Foundry using an explicit Entra client-secret identity. It does not send
// inference requests.
package foundry

import (
	"strings"
	"time"
)

type Identity struct {
	ResourceID   string
	TenantID     string
	ClientID     string
	ClientSecret string `json:"-"`
}

type Snapshot struct {
	ResourceID  string
	Endpoint    string
	Deployments []Deployment
	RefreshedAt time.Time
}

type Deployment struct {
	Name              string
	ModelName         string
	ModelFormat       string
	ProvisioningState string
	Reasoning         bool
	ModelVersion      string
	SKU               string
	Capabilities      map[string]string
}

func (s Snapshot) Find(name string) (Deployment, bool) {
	for _, d := range s.Deployments {
		if d.Name == name {
			return d, true
		}
	}
	return Deployment{}, false
}

// OpenAI profiles are deliberately finite. Aliases never identify a model or
// its request options.
var chatProfiles = map[string]bool{
	"gpt-35-turbo": false, "gpt-3.5-turbo": false,
	"gpt-4": false, "gpt-4-32k": false, "gpt-4-turbo": false,
	"gpt-4-turbo-preview": false, "gpt-4o": false, "gpt-4o-mini": false,
	"gpt-4.1": false, "gpt-4.1-mini": false, "gpt-4.1-nano": false,
	"gpt-4.5-preview": false,
	"gpt-5":           true, "gpt-5-mini": true, "gpt-5-nano": true,
	"gpt-5-chat": false, "gpt-5-chat-latest": false,
	"gpt-5.1": true, "gpt-5.1-chat": false,
	"gpt-5.2": true, "gpt-5.2-chat": false,
	"o1": true, "o1-mini": true, "o1-preview": true,
	"o3": true, "o3-mini": true, "o4-mini": true,
	"model-router": true,
}

// These publisher/model pairs have chat profiles in ai-ui's Foundry catalog.
// They establish chat compatibility, not support for sampling parameters.
var compatibleChatProfiles = map[string]string{
	"deepseek-r1":                            "deepseek",
	"deepseek-r1-0528":                       "deepseek",
	"deepseek-v3":                            "deepseek",
	"deepseek-v3-0324":                       "deepseek",
	"deepseek-v3.1":                          "deepseek",
	"deepseek-v3.2":                          "deepseek",
	"phi-4":                                  "microsoft",
	"phi-4-mini-instruct":                    "microsoft",
	"phi-4-reasoning":                        "microsoft",
	"phi-4-mini-reasoning":                   "microsoft",
	"phi-4-multimodal-instruct":              "microsoft",
	"phi-3.5-mini-instruct":                  "microsoft",
	"phi-3.5-moe-instruct":                   "microsoft",
	"phi-3.5-vision-instruct":                "microsoft",
	"meta-llama-3.1-8b-instruct":             "meta",
	"meta-llama-3.1-70b-instruct":            "meta",
	"meta-llama-3.1-405b-instruct":           "meta",
	"llama-3.3-70b-instruct":                 "meta",
	"llama-3.2-11b-vision-instruct":          "meta",
	"llama-3.2-90b-vision-instruct":          "meta",
	"llama-4-scout-17b-16e-instruct":         "meta",
	"llama-4-maverick-17b-128e-instruct-fp8": "meta",
	"mistral-large":                          "mistral",
	"mistral-large-2407":                     "mistral",
	"mistral-large-2411":                     "mistral",
	"mistral-small":                          "mistral",
	"mistral-small-2503":                     "mistral",
	"mistral-medium-2505":                    "mistral",
	"mistral-nemo":                           "mistral",
	"codestral-2501":                         "mistral",
	"pixtral-large-2411":                     "mistral",
	"command-r":                              "cohere",
	"command-r-plus":                         "cohere",
	"command-r-08-2024":                      "cohere",
	"command-r-plus-08-2024":                 "cohere",
	"command-a-03-2025":                      "cohere",
	"cohere-command-r":                       "cohere",
	"cohere-command-r-plus":                  "cohere",
	"cohere-command-a":                       "cohere",
	"grok-3":                                 "xai",
	"grok-3-mini":                            "xai",
	"grok-4":                                 "xai",
	"grok-4.3":                               "xai",
	"grok-4-fast-reasoning":                  "xai",
	"grok-4-fast-non-reasoning":              "xai",
}

func canonicalModel(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) > 11 && name[len(name)-11] == '-' {
		if _, err := time.Parse("2006-01-02", name[len(name)-10:]); err == nil {
			return name[:len(name)-11]
		}
	}
	return name
}

func capabilityKey(s string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(s)))
}

func (d Deployment) capability(names ...string) (present, enabled, valid bool) {
	enabled, valid = true, true
	for key, value := range d.Capabilities {
		for _, name := range names {
			if capabilityKey(key) != name {
				continue
			}
			present = true
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true":
			case "false":
				enabled = false
			default:
				enabled, valid = false, false
			}
		}
	}
	return present, present && enabled, present && valid
}

func (d Deployment) SupportsChat() bool {
	format := capabilityKey(d.ModelFormat)
	if format == "mistralai" {
		format = "mistral"
	}
	if !strings.EqualFold(d.ProvisioningState, "Succeeded") ||
		format == "" || strings.Contains(format, "anthropic") ||
		strings.Contains(strings.ToLower(d.SKU), "batch") {
		return false
	}
	for _, key := range []string{"batchonly", "responsesonly"} {
		if present, enabled, valid := d.capability(key); present && (!valid || enabled) {
			return false
		}
	}
	compatibleProtocol := false
	for key, value := range d.Capabilities {
		if k := capabilityKey(key); k == "protocol" || k == "inferenceprotocol" {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "openai", "openai-compatible", "openai-v1", "openai_v1":
				compatibleProtocol = true
			default:
				return false
			}
		}
	}
	model := canonicalModel(d.ModelName)
	if model == "" || strings.HasSuffix(model, "-pro") ||
		strings.Contains(model, "codex") || model == "computer-use-preview" ||
		model == "gpt-35-turbo-instruct" || model == "gpt-3.5-turbo-instruct" {
		return false
	}
	for _, excluded := range []string{"embedding", "embed", "image", "dall-e", "audio",
		"realtime", "transcri", "whisper", "tts", "sora", "batch", "claude", "responses"} {
		if strings.Contains(model+"-"+strings.ToLower(d.ModelVersion), excluded) {
			return false
		}
	}
	present, enabled, valid := d.capability("chatcompletion", "chatcompletions")
	if present && (!valid || !enabled) {
		return false
	}
	if _, known := chatProfiles[model]; known {
		return format == "openai"
	}
	if expected, known := compatibleChatProfiles[model]; known {
		return format == expected
	}
	// Unlike a known publisher/model pair, an unfamiliar non-OpenAI model must
	// explicitly advertise both the protocol and the chat operation.
	return present && enabled && valid && (format == "openai" || compatibleProtocol)
}

func (d Deployment) reasoningSafe() bool {
	reasoning, known := chatProfiles[canonicalModel(d.ModelName)]
	// Non-OpenAI and metadata-only profiles have unknown sampling constraints.
	return !known || reasoning
}
