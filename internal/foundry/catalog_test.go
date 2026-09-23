package foundry

import "testing"

func TestChatProfiles(t *testing.T) {
	for _, tt := range []struct {
		model, format, state, sku string
		caps                      map[string]string
		chat, reasoning           bool
	}{
		{"gpt-4o", "OpenAI", "Succeeded", "GlobalStandard", nil, true, false},
		{"gpt-4.1-mini-2025-04-14", "OpenAI", "Succeeded", "", nil, true, false},
		{"o3-mini", "OpenAI", "Succeeded", "", nil, true, true},
		{"gpt-5", "OpenAI", "Succeeded", "", nil, true, true},
		{"gpt-5-chat", "OpenAI", "Succeeded", "", nil, true, false},
		{"model-router", "OpenAI", "Succeeded", "", nil, true, true},
		{"gpt-future", "OpenAI", "Succeeded", "", nil, false, false},
		{"gpt-future", "OpenAI", "Succeeded", "", map[string]string{"chatCompletions": "true"}, true, true},
		{"gpt-4o", "OpenAI", "Creating", "", nil, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "GlobalBatch", nil, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"batch_only": "true"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"batch_only": "?"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"responses-only": "true"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"chat-completion": "false"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"chatCompletion": "true", "chat_completions": "false"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"chatCompletions": "1"}, false, false},
		{"gpt-4o", "OpenAI", "Succeeded", "", map[string]string{"inferenceProtocol": "anthropic"}, false, false},
		{"gpt-4o", "Anthropic", "Succeeded", "", nil, false, false},
		{"gpt-4o", "", "Succeeded", "", nil, false, false},
	} {
		t.Run(tt.model+"/"+tt.sku+"/"+tt.state, func(t *testing.T) {
			d := Deployment{Name: "arbitrary-alias", ModelName: tt.model, ModelFormat: tt.format,
				ProvisioningState: tt.state, SKU: tt.sku, Capabilities: tt.caps}
			if got := d.SupportsChat(); got != tt.chat {
				t.Fatalf("SupportsChat = %v, want %v", got, tt.chat)
			}
			if tt.chat && d.reasoningSafe() != tt.reasoning {
				t.Fatalf("reasoning = %v, want %v", d.reasoningSafe(), tt.reasoning)
			}
		})
	}
	for _, model := range []string{"text-embedding-3-small", "gpt-image-1", "dall-e-3",
		"gpt-4o-audio-preview", "gpt-4o-realtime-preview", "gpt-4o-transcribe", "whisper",
		"tts-1", "sora", "gpt-5-pro", "o3-pro", "gpt-5-codex", "codex-mini",
		"computer-use-preview", "claude-sonnet", "gpt-35-turbo-instruct", "gpt-responses-only"} {
		t.Run("excluded/"+model, func(t *testing.T) {
			d := Deployment{ModelName: model, ModelFormat: "OpenAI", ProvisioningState: "Succeeded",
				Capabilities: map[string]string{"chatCompletions": "true"}}
			if d.SupportsChat() {
				t.Fatal("excluded model accepted even with chat metadata")
			}
		})
	}
}

func TestAliasesAndFind(t *testing.T) {
	plain := Deployment{Name: "o3", ModelName: "gpt-4o", ModelFormat: "OpenAI", ProvisioningState: "Succeeded"}
	reasoning := Deployment{Name: "gpt-4o", ModelName: "o3-2025-04-16", ModelFormat: "OpenAI", ProvisioningState: "Succeeded"}
	unknown := Deployment{Name: "gpt-4o", ModelName: "unknown", ModelFormat: "OpenAI", ProvisioningState: "Succeeded"}
	if plain.reasoningSafe() || !reasoning.reasoningSafe() || unknown.SupportsChat() {
		t.Fatal("alias affected model classification")
	}
	s := Snapshot{Deployments: []Deployment{plain, reasoning}}
	if got, ok := s.Find("o3"); !ok || got.ModelName != "gpt-4o" {
		t.Fatal("Find did not match deployment name")
	}
	if _, ok := s.Find("O3"); ok {
		t.Fatal("Find should use exact deployment names")
	}
	if _, ok := s.Find("missing"); ok {
		t.Fatal("found missing deployment")
	}
}
