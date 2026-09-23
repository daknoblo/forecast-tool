package foundry

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const testUUID = "11111111-2222-3333-4444-555555555555"
const testResource = "/subscriptions/" + testUUID + "/resourceGroups/test-group/providers/Microsoft.CognitiveServices/accounts/test-account"
const testEndpoint = "https://test-account.openai.azure.com/openai/v1"

func TestNewIdentity(t *testing.T) {
	valid := Identity{ResourceID: testResource, TenantID: testUUID, ClientID: testUUID, ClientSecret: "test-secret"}
	c, err := New(valid)
	if err != nil || c == nil || c.endpoint != "" {
		t.Fatalf("New = %v, %v", c, err)
	}
	for name, change := range map[string]func(*Identity){
		"resource absent": func(i *Identity) { i.ResourceID = "" },
		"resource wrong":  func(i *Identity) { i.ResourceID += "/deployments/x" },
		"resource query":  func(i *Identity) { i.ResourceID += "?secret" },
		"subscription":    func(i *Identity) { i.ResourceID = strings.Replace(testResource, testUUID, "not-a-uuid", 1) },
		"encoded segment": func(i *Identity) { i.ResourceID = strings.Replace(testResource, "test-group", "%2e%2e", 1) },
		"account suffix":  func(i *Identity) { i.ResourceID += ".evil" },
		"tenant absent":   func(i *Identity) { i.TenantID = "" },
		"tenant path":     func(i *Identity) { i.TenantID = "../other" },
		"client absent":   func(i *Identity) { i.ClientID = "" },
		"secret absent":   func(i *Identity) { i.ClientSecret = " \n" },
	} {
		t.Run(name, func(t *testing.T) {
			i := valid
			change(&i)
			if _, err := New(i); err == nil || strings.Contains(err.Error(), valid.ClientSecret) {
				t.Fatalf("unsafe identity error: %v", err)
			}
		})
	}
	data, err := json.Marshal(valid)
	if err != nil || strings.Contains(string(data), valid.ClientSecret) {
		t.Fatal("identity JSON leaked secret")
	}
}

func TestEndpointValidation(t *testing.T) {
	for _, raw := range []string{"https://test-account.openai.azure.com", testEndpoint, testEndpoint + "/",
		"https://test-account.services.ai.azure.com/", "https://TEST-ACCOUNT.openai.azure.com/openai/v1"} {
		if got, ok := normalizeEndpoint(raw, "test-account"); !ok || !strings.HasSuffix(got, "/openai/v1") {
			t.Errorf("valid endpoint rejected: %s", raw)
		}
	}
	for _, raw := range []string{
		"http://test-account.openai.azure.com", "https://other.openai.azure.com",
		"https://test-account.openai.azure.com.evil", "https://test-account.openai.azure.com:443",
		"https://test-account.openai.azure.com:444", "https://test-account.openai.azure.com.",
		"https://user:secret@test-account.openai.azure.com", testEndpoint + "?api-version=x",
		testEndpoint + "?", testEndpoint + "#", testEndpoint + "#fragment",
		"https://test-account.openai.azure.com//openai/v1", testEndpoint + "/../v1",
		"https://test-account.openai.azure.com/%6fpenai/v1", " " + testEndpoint,
		"https://test-account.cognitiveservices.azure.com", "https://evil.example/openai/v1",
	} {
		if _, ok := normalizeEndpoint(raw, "test-account"); ok {
			t.Errorf("unsafe endpoint accepted: %s", raw)
		}
	}
	p := accountProperties{Endpoint: "https://evil.example", Endpoints: map[string]string{"v1": testEndpoint}}
	if got, err := selectEndpoint(p, "test-account"); err != nil || got != testEndpoint {
		t.Fatalf("safe alternate metadata: %q, %v", got, err)
	}
	p.Endpoints["other"] = "https://test-account.services.ai.azure.com"
	if _, err := selectEndpoint(p, "test-account"); err == nil {
		t.Fatal("ambiguous metadata accepted")
	}
}

func TestVerifiedCustomSubdomain(t *testing.T) {
	const customEndpoint = "https://custom-inference.services.ai.azure.com/openai/v1"
	for _, tt := range []struct {
		name, accountID, subdomain, endpoint string
		valid                                bool
	}{
		{"verified different name", testResource, "custom-inference", customEndpoint, true},
		{"verified root", testResource, "CUSTOM-INFERENCE", "https://custom-inference.openai.azure.com/", true},
		{"account name still allowed", testResource, "custom-inference", testEndpoint, true},
		{"foreign account response", testResource + "-other", "custom-inference", customEndpoint, false},
		{"mismatched Azure resource", testResource, "custom-inference", "https://hostile.openai.azure.com", false},
		{"unverified subdomain", testResource, "", customEndpoint, false},
		{"arbitrary host", testResource, "custom-inference", "https://custom-inference.example/openai/v1", false},
		{"explicit port", testResource, "custom-inference", "https://custom-inference.openai.azure.com:443", false},
		{"nondefault port", testResource, "custom-inference", "https://custom-inference.openai.azure.com:444", false},
		{"domain injection", testResource, "custom-inference.openai.azure.com", customEndpoint, false},
		{"encoded subdomain", testResource, "%63ustom-inference", customEndpoint, false},
		{"whitespace subdomain", testResource, " custom-inference", customEndpoint, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c, scopes := mockClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return jsonResponse(map[string]any{"id": tt.accountID, "properties": accountProperties{
						Endpoint: tt.endpoint, CustomSubDomainName: tt.subdomain,
					}}), nil
				}
				return jsonResponse(map[string]any{"value": []armDeployment{deployment("chat", "gpt-4o")}}), nil
			})
			s, err := c.Refresh(context.Background())
			if !tt.valid {
				if err == nil || s.Endpoint != "" || calls != 1 {
					t.Fatalf("unsafe custom subdomain discovery: %+v, %v, calls %d", s, err, calls)
				}
				if err := c.Authorize(request(t, customEndpoint)); err == nil || len(*scopes) != 1 {
					t.Fatal("unverified custom subdomain acquired an inference token")
				}
				return
			}
			want := strings.TrimRight(tt.endpoint, "/")
			if !strings.HasSuffix(want, "/openai/v1") {
				want += "/openai/v1"
			}
			if err != nil || s.Endpoint != want || calls != 2 {
				t.Fatalf("valid custom subdomain discovery: %+v, %v, calls %d", s, err, calls)
			}
			if err := c.Authorize(request(t, s.Endpoint)); err != nil {
				t.Fatalf("verified custom endpoint rejected: %v", err)
			}
			if err := c.Authorize(request(t, "https://hostile.openai.azure.com/openai/v1")); err == nil || len(*scopes) != 3 {
				t.Fatal("hostile alternate host acquired an inference token")
			}
		})
	}
	p := accountProperties{CustomSubDomainName: "custom-inference", Endpoints: map[string]string{"v1": customEndpoint}}
	if got, err := selectEndpoint(p, "test-account"); err != nil || got != customEndpoint {
		t.Fatalf("custom subdomain in endpoint map rejected: %q, %v", got, err)
	}
}

func TestPaginationTargets(t *testing.T) {
	c := &Client{resourceID: testResource}
	current := c.resourceURL(testResource + "/deployments")
	for _, raw := range []string{"?$skiptoken=abc", testResource + "/deployments?$skiptoken=abc",
		current + "&$skiptoken=abc"} {
		if got, err := c.paginationURL(current, raw); err != nil || !strings.Contains(got, "api-version="+apiVersion) {
			t.Errorf("valid pagination rejected: %s, %v", raw, err)
		}
	}
	for _, raw := range []string{
		"https://evil.example" + testResource + "/deployments",
		"http://management.azure.com" + testResource + "/deployments",
		"https://management.azure.com:443" + testResource + "/deployments",
		"https://management.azure.com:444" + testResource + "/deployments",
		"https://user:secret@management.azure.com" + testResource + "/deployments",
		"//evil.example" + testResource + "/deployments",
		"?api-version=bad", "?api-version=" + apiVersion + "&api-version=" + apiVersion,
		"?API-Version=" + apiVersion, "?bad=%zz", "?x=1&x=2", current + "#",
		strings.Replace(current, "test-account", "other", 1),
		testResource + "/deployments/../deployments", testResource + "/%64eployments",
		testResource + "/deployments/", strings.Repeat("x", 16385),
	} {
		if _, err := c.paginationURL(current, raw); err == nil {
			t.Errorf("hostile pagination accepted: %s", raw)
		}
	}
}
