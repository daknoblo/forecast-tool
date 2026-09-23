package foundry

import (
	"context"
	"net/http"
	"testing"
)

func TestCompatiblePublisherChatProfiles(t *testing.T) {
	for _, tt := range []struct {
		model, format string
	}{
		{"DeepSeek-R1", "DeepSeek"},
		{"deepseek-v3.2", "DeepSeek"},
		{"Mistral-Large-2411", "Mistral"},
		{"mistral-small-2503", "MistralAI"},
		{"Llama-3.3-70B-Instruct", "Meta"},
		{"Phi-4-mini-instruct", "Microsoft"},
		{"command-r-plus", "Cohere"},
		{"grok-3-mini", "xAI"},
	} {
		t.Run(tt.model, func(t *testing.T) {
			d := Deployment{Name: "gpt-4o", ModelName: tt.model, ModelFormat: tt.format, ProvisioningState: "Succeeded"}
			if !d.SupportsChat() || !d.reasoningSafe() {
				t.Fatal("reference-backed chat profile should be supported with conservative request options")
			}
			d.ModelFormat = "OpenAI"
			if d.SupportsChat() {
				t.Fatal("known model accepted with a mismatched publisher")
			}
		})
	}
	for model, format := range compatibleChatProfiles {
		t.Run("reference/"+model, func(t *testing.T) {
			d := Deployment{ModelName: model, ModelFormat: format, ProvisioningState: "Succeeded"}
			if !d.SupportsChat() {
				t.Fatal("reference chat profile rejected")
			}
			d.Capabilities = map[string]string{"chatCompletions": "false"}
			if d.SupportsChat() {
				t.Fatal("profile overrode explicitly disabled chat")
			}
			d.Capabilities = map[string]string{"protocol": "anthropic"}
			if d.SupportsChat() {
				t.Fatal("profile overrode an incompatible protocol")
			}
		})
	}
}

func TestCompatiblePublisherMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, model, format string
		caps                map[string]string
		want                bool
	}{
		{"chat alone insufficient", "future-chat", "DeepSeek", map[string]string{"chatCompletions": "true"}, false},
		{"protocol alone insufficient", "future-chat", "Mistral", map[string]string{"protocol": "openai"}, false},
		{"both required", "future-chat", "NewPublisher", map[string]string{"chatCompletions": "true", "protocol": "openai-v1"}, true},
		{"compatible spelling", "future-chat", "Meta", map[string]string{"chat_completion": "true", "inference_protocol": "openai-compatible"}, true},
		{"conflicting protocols", "future-chat", "Meta", map[string]string{"chatCompletions": "true", "protocol": "openai", "inferenceProtocol": "responses"}, false},
		{"invalid chat", "future-chat", "Meta", map[string]string{"chatCompletions": "yes", "protocol": "openai"}, false},
		{"empty publisher", "future-chat", "", map[string]string{"chatCompletions": "true", "protocol": "openai"}, false},
		{"Anthropic blocked", "future-chat", "Anthropic", map[string]string{"chatCompletions": "true", "protocol": "openai"}, false},
		{"Anthropic native blocked", "future-chat", "Anthropic-native", map[string]string{"chatCompletions": "true", "protocol": "openai"}, false},
		{"Claude blocked", "claude-future", "NewPublisher", map[string]string{"chatCompletions": "true", "protocol": "openai"}, false},
		{"known publisher mismatch", "gpt-4o", "Meta", map[string]string{"chatCompletions": "true", "protocol": "openai"}, false},
		{"responses-only blocked", "future-chat", "DeepSeek", map[string]string{"chatCompletions": "true", "protocol": "openai", "responsesOnly": "true"}, false},
		{"batch-only blocked", "future-chat", "Meta", map[string]string{"chatCompletions": "true", "protocol": "openai", "batchOnly": "true"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := Deployment{Name: "gpt-4o", ModelName: tt.model, ModelFormat: tt.format,
				ProvisioningState: "Succeeded", Capabilities: tt.caps}
			if got := d.SupportsChat(); got != tt.want {
				t.Fatalf("SupportsChat = %v, want %v", got, tt.want)
			}
			if tt.want && !d.reasoningSafe() {
				t.Fatal("unfamiliar model must use conservative request options, regardless of alias")
			}
		})
	}
	for _, model := range []string{"embed-v4", "future-image", "future-audio", "future-realtime",
		"future-transcribe", "future-batch", "gpt-5-pro", "gpt-5-codex"} {
		d := Deployment{ModelName: model, ModelFormat: "NewPublisher", ProvisioningState: "Succeeded",
			Capabilities: map[string]string{"chatCompletions": "true", "protocol": "openai"}}
		if d.SupportsChat() {
			t.Errorf("unsupported model bypassed exclusions through metadata: %s", model)
		}
	}
}

func TestCompatiblePublisherDiscovery(t *testing.T) {
	known := deployment("plain-alias", "DeepSeek-R1-2025-01-20")
	known.Properties.Model.Format = "DeepSeek"
	advertised := deployment("gpt-4o", "future-chat")
	advertised.Properties.Model.Format = "NewPublisher"
	advertised.Properties.Capabilities = map[string]string{"chatCompletions": "true", "protocol": "openai"}
	unsupported := deployment("deepseek-r1", "unknown")
	unsupported.Properties.Model.Format = "DeepSeek"
	batch := deployment("batch-alias", "mistral-large")
	batch.Properties.Model.Format = "MistralAI"
	batch.SKU.Name = "GlobalBatch"
	c, _ := mockClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == testResource {
			return accountResponse("https://test-account.services.ai.azure.com"), nil
		}
		return jsonResponse(map[string]any{"value": []armDeployment{known, advertised, unsupported, batch}}), nil
	})
	s, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Deployments) != 2 || s.Endpoint != "https://test-account.services.ai.azure.com/openai/v1" {
		t.Fatalf("unexpected compatible discovery: %+v", s)
	}
	for _, name := range []string{"plain-alias", "gpt-4o"} {
		if d, ok := s.Find(name); !ok || !d.Reasoning || !d.SupportsChat() {
			t.Errorf("missing compatible deployment or conservative request options: %s", name)
		}
	}
}
