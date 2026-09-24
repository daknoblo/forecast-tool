package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type credentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f credentialFunc) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return f(ctx, options)
}

func mockToken() azcore.AccessToken {
	return azcore.AccessToken{Token: "mock-bearer-secret", ExpiresOn: time.Now().Add(time.Hour)}
}

func jsonResponse(v any) *http.Response {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return textResponse(http.StatusOK, string(b))
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}

func accountResponse(endpoint string) *http.Response {
	return jsonResponse(map[string]any{"id": testResource, "properties": map[string]any{"endpoint": endpoint}})
}

func deployment(name, model string) armDeployment {
	var d armDeployment
	d.ID, d.Name = testResource+"/deployments/"+name, name
	d.Properties.Model.Name, d.Properties.Model.Format = model, "OpenAI"
	d.Properties.ProvisioningState = "Succeeded"
	d.SKU.Name = "Standard"
	return d
}

func mockClient(t *testing.T, transport roundTripFunc) (*Client, *[]string) {
	t.Helper()
	scopes := new([]string)
	var mu sync.Mutex
	c := &Client{
		resourceID: testResource, timeout: time.Second,
		httpClient: &http.Client{Transport: transport, Timeout: time.Second},
		credential: credentialFunc(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			if len(options.Scopes) != 1 {
				t.Error("expected a single token scope")
			}
			mu.Lock()
			*scopes = append(*scopes, options.Scopes...)
			mu.Unlock()
			return mockToken(), nil
		}),
	}
	return c, scopes
}

func request(t *testing.T, endpoint string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint+"/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestDiscoveryPaginationAndAuthorization(t *testing.T) {
	calls := 0
	c, scopes := mockClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "management.azure.com" ||
			r.URL.Query().Get("api-version") != apiVersion ||
			r.Header.Get("Authorization") != "Bearer "+mockToken().Token || r.Header.Get("api-key") != "" {
			t.Errorf("incorrect ARM request: %s", r.URL)
		}
		switch calls {
		case 1:
			return accountResponse(testEndpoint), nil
		case 2:
			return jsonResponse(map[string]any{"value": []armDeployment{
				deployment("z-plain", "gpt-4o"), deployment("hidden", "text-embedding-3-small"),
			}, "nextLink": "?$skiptoken=page2"}), nil
		case 3:
			return jsonResponse(map[string]any{"value": []armDeployment{
				deployment("a-router", "model-router"), deployment("ordinary-alias", "o3"),
			}}), nil
		default:
			t.Fatal("unexpected network call")
			return nil, nil
		}
	})
	before := request(t, testEndpoint)
	before.Header.Set("api-key", "remove-me")
	if err := c.Authorize(before); err == nil || len(*scopes) != 0 || before.Header.Get("api-key") != "" {
		t.Fatal("authorization before discovery was not fail-closed")
	}
	snapshot, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Endpoint != testEndpoint || snapshot.ResourceID != testResource || snapshot.RefreshedAt.IsZero() ||
		len(snapshot.Deployments) != 3 || snapshot.Deployments[0].Name != "a-router" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if d, ok := snapshot.Find("ordinary-alias"); !ok || !d.Reasoning {
		t.Fatal("canonical model reasoning was not preserved")
	}
	req := request(t, snapshot.Endpoint)
	req.Header["API-KEY"] = []string{"old-secret"}
	req.Header["authorization"] = []string{"old-token"}
	if err := c.Authorize(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer "+mockToken().Token || len(req.Header) != 1 {
		t.Fatalf("wrong authorization headers: %v", req.Header)
	}
	want := []string{armScope, armScope, armScope, inferenceScope}
	if fmt.Sprint(*scopes) != fmt.Sprint(want) {
		t.Fatalf("token scopes: %v", *scopes)
	}
}

func TestAuthorizeDestinationGuard(t *testing.T) {
	c, scopes := mockClient(t, nil)
	c.endpoint = testEndpoint
	for name, mutate := range map[string]func(*http.Request){
		"host":           func(r *http.Request) { r.URL.Host = "evil.example" },
		"scheme":         func(r *http.Request) { r.URL.Scheme = "http" },
		"port":           func(r *http.Request) { r.URL.Host += ":443" },
		"userinfo":       func(r *http.Request) { r.URL.User = url.UserPassword("user", "secret") },
		"path":           func(r *http.Request) { r.URL.Path = "/openai/v1/responses" },
		"trailing slash": func(r *http.Request) { r.URL.Path += "/" },
		"escaped path":   func(r *http.Request) { r.URL.RawPath = "/openai/v1/%63hat/completions" },
		"query":          func(r *http.Request) { r.URL.RawQuery = "api-version=x" },
		"empty query":    func(r *http.Request) { r.URL.ForceQuery = true },
		"fragment":       func(r *http.Request) { r.URL.Fragment = "x" },
		"host header":    func(r *http.Request) { r.Host = "other.example" },
		"method":         func(r *http.Request) { r.Method = http.MethodGet },
		"request uri":    func(r *http.Request) { r.RequestURI = "/other" },
		"nil URL":        func(r *http.Request) { r.URL = nil },
	} {
		t.Run(name, func(t *testing.T) {
			req := request(t, testEndpoint)
			req.Header.Set("Authorization", "old")
			req.Header.Set("api-key", "old")
			mutate(req)
			if err := c.Authorize(req); err == nil || len(req.Header) != 0 || len(*scopes) != 0 {
				t.Fatalf("unsafe destination signed: %v", err)
			}
		})
	}
	if err := c.Authorize(nil); err == nil {
		t.Fatal("nil request accepted")
	}
}

func TestDiscoveryFailsClosed(t *testing.T) {
	for name, response := range map[string]func() *http.Response{
		"foreign deployment": func() *http.Response {
			d := deployment("x", "gpt-4o")
			d.ID = strings.Replace(d.ID, "test-account", "foreign", 1)
			return jsonResponse(map[string]any{"value": []armDeployment{d}})
		},
		"missing deployment ID": func() *http.Response {
			d := deployment("x", "gpt-4o")
			d.ID = ""
			return jsonResponse(map[string]any{"value": []armDeployment{d}})
		},
		"duplicate": func() *http.Response {
			return jsonResponse(map[string]any{"value": []armDeployment{deployment("x", "gpt-4o"), deployment("X", "o3")}})
		},
		"invalid name": func() *http.Response {
			return jsonResponse(map[string]any{"value": []armDeployment{deployment("../x", "gpt-4o")}})
		},
		"missing array": func() *http.Response { return textResponse(200, `{}`) },
		"null array":    func() *http.Response { return textResponse(200, `{"value":null}`) },
		"malformed":     func() *http.Response { return textResponse(200, `secret-invalid-json`) },
		"wrong type":    func() *http.Response { return textResponse(200, `{"value":"secret"}`) },
		"hostile nextLink": func() *http.Response {
			return jsonResponse(map[string]any{"value": []armDeployment{}, "nextLink": "https://evil.example/secret"})
		},
		"oversize": func() *http.Response { return textResponse(200, strings.Repeat("s", maxResponseBytes+1)) },
		"status":   func() *http.Response { return textResponse(403, "mock-bearer-secret test-secret") },
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			c, scopes := mockClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return accountResponse(testEndpoint), nil
				}
				return response(), nil
			})
			c.endpoint = testEndpoint
			s, err := c.Refresh(context.Background())
			if err == nil || s.Endpoint != "" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe refresh result: %+v %v", s, err)
			}
			if calls != 2 || len(*scopes) != 2 {
				t.Fatalf("unexpected token/network call after failed discovery: %d %d", calls, len(*scopes))
			}
			if err := c.Authorize(request(t, testEndpoint)); err == nil || len(*scopes) != 2 {
				t.Fatal("failed refresh retained trust")
			}
		})
	}
}

func TestAccountValidationAndEmptyCatalog(t *testing.T) {
	for _, endpoint := range []string{testEndpoint, "https://other.openai.azure.com", "https://evil.example/openai/v1"} {
		calls := 0
		c, _ := mockClient(t, func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return accountResponse(endpoint), nil
			}
			return textResponse(200, `{"value":[]}`), nil
		})
		s, err := c.Refresh(context.Background())
		if endpoint == testEndpoint {
			if err != nil || s.Deployments == nil || len(s.Deployments) != 0 {
				t.Fatalf("empty valid catalog: %+v, %v", s, err)
			}
		} else if err == nil || calls != 1 {
			t.Fatal("hostile endpoint was not rejected before deployment request")
		}
	}
	c, _ := mockClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(map[string]any{"id": testResource + "-other", "properties": map[string]string{"endpoint": testEndpoint}}), nil
	})
	if _, err := c.Refresh(context.Background()); err == nil {
		t.Fatal("foreign account accepted")
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		c, _ := mockClient(t, func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, fmt.Errorf("sensitive details: %w", r.Context().Err())
		})
		c.timeout = 10 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		if cancelFirst {
			cancel()
		}
		_, err := c.Refresh(ctx)
		cancel()
		want := context.DeadlineExceeded
		if cancelFirst {
			want = context.Canceled
		}
		if !errors.Is(err, want) || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("wrong safe context error: %v", err)
		}
	}
	c, _ := mockClient(t, nil)
	c.endpoint = testEndpoint
	c.credential = credentialFunc(func(ctx context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
		<-ctx.Done()
		return azcore.AccessToken{}, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.Authorize(request(t, testEndpoint).WithContext(ctx)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("authorization did not honor caller deadline: %v", err)
	}
}

func TestTokenFailureSanitized(t *testing.T) {
	for _, result := range []struct {
		token azcore.AccessToken
		err   error
	}{
		{err: errors.New("test-secret mock-bearer-secret raw body")},
		{token: azcore.AccessToken{}},
		{token: azcore.AccessToken{Token: "expired-secret", ExpiresOn: time.Now().Add(-time.Second)}},
		{token: azcore.AccessToken{Token: "header\r\nsecret", ExpiresOn: time.Now().Add(time.Hour)}},
	} {
		c, _ := mockClient(t, nil)
		c.endpoint = testEndpoint
		c.credential = credentialFunc(func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return result.token, result.err
		})
		req := request(t, testEndpoint)
		if err := c.Authorize(req); err == nil || strings.Contains(err.Error(), "secret") || len(req.Header) != 0 {
			t.Fatalf("unsafe token failure: %v", err)
		}
	}
}

func TestRefreshDuringTokenAcquisition(t *testing.T) {
	c, _ := mockClient(t, nil)
	c.endpoint = testEndpoint
	c.credential = credentialFunc(func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
		_, _ = c.Refresh(nil) //nolint:staticcheck // SA1012: Deliberately verify nil-context refresh invalidates in-flight authorization.
		return mockToken(), nil
	})
	if err := c.Authorize(request(t, testEndpoint)); err == nil {
		t.Fatal("authorization survived invalidating refresh")
	}
}

func TestResponseLimitAndClose(t *testing.T) {
	body := &trackedBody{Reader: strings.NewReader("12345")}
	resp := &http.Response{Body: body, ContentLength: -1}
	if _, err := readResponse(context.Background(), resp, 4); err == nil || !body.closed {
		t.Fatal("budget/close not enforced")
	}
	body = &trackedBody{Reader: strings.NewReader("1234")}
	resp = &http.Response{Body: body, ContentLength: 4}
	if data, err := readResponse(context.Background(), resp, 4); err != nil || string(data) != "1234" || !body.closed {
		t.Fatal("exact budget should succeed and close")
	}
	body = &trackedBody{Reader: strings.NewReader("12345")}
	resp = &http.Response{Body: body, ContentLength: 5}
	if _, err := readResponse(context.Background(), resp, 4); err == nil || !body.closed || body.Len() != 5 {
		t.Fatal("declared oversize should be rejected before reading")
	}
}

type trackedBody struct {
	*strings.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestDiscoveryRefusesRedirects(t *testing.T) {
	var destinationCalls int
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, _ := mockClient(t, nil)
	c.httpClient = source.Client()
	// getJSON's transport behavior is tested against loopback TLS, not Azure.
	budget := int64(maxCatalogBytes)
	err := c.getJSON(context.Background(), source.URL, new(any), &budget)
	if err == nil || destinationCalls != 0 {
		t.Fatalf("redirect followed: %v, %d", err, destinationCalls)
	}
}

func TestSDKCredentialCacheAndIdentityTransport(t *testing.T) {
	tokenCalls := map[string]int{}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			base := "https://login.microsoftonline.com/" + testUUID
			return jsonResponse(map[string]any{
				"authorization_endpoint": base + "/oauth2/v2.0/authorize",
				"token_endpoint":         base + "/oauth2/v2.0/token",
				"issuer":                 base + "/v2.0", "jwks_uri": base + "/discovery/v2.0/keys",
			}), nil
		}

		if r.URL.Path != "/"+testUUID+"/oauth2/v2.0/token" {
			t.Fatalf("unexpected SDK URL: %s", r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_secret") != "test-secret" || r.Form.Get("grant_type") != "client_credentials" {
			t.Fatal("SDK did not use explicit client-secret credentials")
		}
		scope := strings.TrimSuffix(r.Form.Get("scope"), " openid offline_access profile")
		tokenCalls[scope]++
		return jsonResponse(map[string]any{"access_token": "sdk-cached-token", "token_type": "Bearer", "expires_in": 3600}), nil
	})}
	transport := &identityTransport{client: httpClient, tenant: testUUID}
	credential, err := newCredential(testUUID, testUUID, "test-secret", transport)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{credential: credential}
	for range 2 {
		for _, scope := range []string{armScope, inferenceScope} {
			if _, err := credential.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{scope}}); err != nil {
				t.Fatalf("mock SDK acquisition: %v", err)
			}
			if token, err := c.token(context.Background(), scope); err != nil || token != "sdk-cached-token" {
				t.Fatalf("SDK acquisition failed: %q, %v", token, err)
			}
		}
	}
	if tokenCalls[armScope] != 1 || tokenCalls[inferenceScope] != 1 || len(tokenCalls) != 2 {
		t.Fatalf("SDK credential did not cache independently by scope: %v", tokenCalls)
	}
	for _, target := range []string{"https://evil.example/" + testUUID + "/oauth2/v2.0/token",
		"https://login.microsoftonline.com:444/" + testUUID + "/oauth2/v2.0/token",
		"https://login.microsoftonline.com/other/oauth2/v2.0/token"} {
		req, err := http.NewRequest(http.MethodPost, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transport.Do(req); err == nil {
			t.Fatalf("identity transport accepted %s", target)
		}
	}
}
