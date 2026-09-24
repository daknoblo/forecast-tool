package foundry

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryAggregateLimits(t *testing.T) {
	for _, mode := range []string{"pages", "cycle", "deployments", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c, _ := mockClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return accountResponse(testEndpoint), nil
				}
				next := fmt.Sprintf("?$skiptoken=%d", calls)
				value := []armDeployment{}
				padding := ""
				switch mode {
				case "cycle":
					next = "?api-version=" + apiVersion
				case "deployments":
					value = make([]armDeployment, maxDeployments+1)
					next = ""
				case "bytes":
					padding = strings.Repeat("x", 3<<20)
				}
				return jsonResponse(map[string]any{"value": value, "nextLink": next, "padding": padding}), nil
			})
			c.timeout = 10 * time.Second
			if _, err := c.Refresh(context.Background()); err == nil || c.endpoint != "" {
				t.Fatalf("aggregate %s limit not enforced: %v", mode, err)
			}
			switch mode {
			case "pages":
				if calls != maxPages+1 {
					t.Fatalf("page bound = %d, want %d", calls-1, maxPages)
				}
			case "cycle", "deployments":
				if calls != 2 {
					t.Fatalf("unexpected calls: %d", calls)
				}
			case "bytes":
				if calls != 7 {
					t.Fatalf("expected failure on sixth 3 MiB page: %d calls", calls)
				}
			}
		})
	}
}

func TestConcurrentRefreshSupersedesOldResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	c, _ := mockClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == testResource {
			close(started)
			<-release
			return accountResponse(testEndpoint), nil
		}
		return textResponse(200, `{"value":[]}`), nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := c.Refresh(context.Background())
		done <- err
	}()
	<-started
	if _, err := c.Refresh(nil); err == nil { //nolint:staticcheck // SA1012: Deliberately verify nil-context refresh prevents older discovery from restoring trust.
		t.Fatal("nil context refresh should invalidate and fail")
	}
	close(release)
	if err := <-done; err == nil || c.endpoint != "" {
		t.Fatal("old refresh restored invalidated endpoint")
	}
}

func TestIdentityTransportRejectsRedirectAndOversize(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if mode == "redirect" {
					resp := textResponse(http.StatusTemporaryRedirect, "secret")
					resp.Header.Set("Location", "https://evil.example/token")
					return resp, nil
				}
				return textResponse(200, strings.Repeat("s", maxResponseBytes+1)), nil
			})}
			transport := &identityTransport{client: client, tenant: testUUID}
			req, err := http.NewRequest(http.MethodPost, "https://login.microsoftonline.com/"+testUUID+"/oauth2/v2.0/token", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := transport.Do(req); err == nil || strings.Contains(err.Error(), "secret") || calls != 1 {
				t.Fatalf("unsafe identity response: %v, calls %d", err, calls)
			}
		})
	}
}

func TestOIDCMetadataCannotMoveClientSecret(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls != 1 {
			t.Fatal("malicious metadata caused a secret-bearing transport call")
		}
		return jsonResponse(map[string]any{
			"authorization_endpoint": "https://evil.example/authorize",
			"token_endpoint":         "https://evil.example/token",
			"issuer":                 "https://evil.example",
			"jwks_uri":               "https://evil.example/keys",
		}), nil
	})}
	credential, err := newCredential(testUUID, testUUID, "test-secret",
		&identityTransport{client: client, tenant: testUUID})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{credential: credential}
	if _, err := c.token(context.Background(), inferenceScope); err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("malicious OIDC metadata did not fail safely: %v", err)
	}
}
