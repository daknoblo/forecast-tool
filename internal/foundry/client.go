package foundry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	apiVersion       = "2024-10-01"
	armScope         = "https://management.azure.com/.default"
	inferenceScope   = "https://cognitiveservices.azure.com/.default"
	discoveryTimeout = 45 * time.Second
	tokenTimeout     = 20 * time.Second
	maxResponseBytes = 4 << 20
	maxCatalogBytes  = 16 << 20
	maxPages         = 100
	maxDeployments   = 10000
)

// Client retains one SDK credential (and its token cache). Do not copy a Client.
type Client struct {
	resourceID string
	credential azcore.TokenCredential
	httpClient *http.Client
	timeout    time.Duration

	mu         sync.RWMutex
	endpoint   string
	generation uint64
}

func New(identity Identity) (*Client, error) {
	id := strings.TrimSuffix(strings.TrimSpace(identity.ResourceID), "/")
	if err := validateResourceID(id); err != nil {
		return nil, err
	}
	tenant, client := strings.TrimSpace(identity.TenantID), strings.TrimSpace(identity.ClientID)
	if !validUUID(tenant) || !validUUID(client) || strings.TrimSpace(identity.ClientSecret) == "" {
		return nil, errors.New("Azure erfordert eine explizite Mandanten-ID, Client-ID (jeweils UUID) und ein Clientgeheimnis.")
	}
	httpClient := newHTTPClient()
	credential, err := newCredential(tenant, client, identity.ClientSecret, &identityTransport{client: httpClient, tenant: tenant})
	if err != nil {
		return nil, errors.New("Die Azure-Clientgeheimnis-Identitaet ist ungueltig.")
	}
	return &Client{resourceID: id, credential: credential, httpClient: httpClient, timeout: discoveryTimeout}, nil
}

func newCredential(tenant, client, secret string, transport policy.Transporter) (*azidentity.ClientSecretCredential, error) {
	return azidentity.NewClientSecretCredential(tenant, client, secret, &azidentity.ClientSecretCredentialOptions{
		DisableInstanceDiscovery: true,
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.AzurePublic, Transport: transport,
			Retry: policy.RetryOptions{MaxRetries: 2, TryTimeout: 10 * time.Second,
				RetryDelay: time.Second, MaxRetryDelay: 4 * time.Second},
		},
	})
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: refuseRedirect,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 10, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// The SDK consumes OIDC metadata too. Pin its transport so metadata cannot move
// the client secret to another origin or tenant, and bound SDK response bodies.
type identityTransport struct {
	client *http.Client
	tenant string
}

func (t *identityTransport) Do(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("Das Azure-Anmeldeziel ist nicht zugelassen.")
	}
	validationURL := *req.URL
	// MSAL appends an empty query marker to its OIDC discovery URL.
	validationURL.ForceQuery = false
	if !cleanURL(&validationURL) || req.URL.Host != "login.microsoftonline.com" ||
		(req.Host != "" && req.Host != req.URL.Host) ||
		(req.URL.Path != "/"+t.tenant+"/v2.0/.well-known/openid-configuration" &&
			req.URL.Path != "/"+t.tenant+"/oauth2/v2.0/token") {
		return nil, errors.New("Das Azure-Anmeldeziel ist nicht zugelassen.")
	}
	client := *t.client
	client.CheckRedirect = refuseRedirect
	resp, err := client.Do(req)
	if err != nil {
		return nil, safeError(req.Context(), err, "Azure-Anmeldung")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, errors.New("Azure-Anmeldeweiterleitungen sind nicht zugelassen.")
	}
	body, err := readResponse(req.Context(), resp, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// Refresh invalidates earlier discovery immediately, even if refresh fails.
// A concurrent newer refresh supersedes this call rather than restoring stale trust.
func (c *Client) Refresh(ctx context.Context) (Snapshot, error) {
	if c == nil {
		return Snapshot{}, errors.New("Der Azure-Client ist nicht initialisiert.")
	}
	c.mu.Lock()
	c.generation++
	generation := c.generation
	c.endpoint = ""
	c.mu.Unlock()
	if ctx == nil || c.credential == nil || c.httpClient == nil {
		return Snapshot{}, errors.New("Azure-Ermittlung erfordert einen initialisierten Client und Kontext.")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	budget := int64(maxCatalogBytes)
	var account struct {
		ID         string            `json:"id"`
		Properties accountProperties `json:"properties"`
	}
	if err := c.getJSON(ctx, c.resourceURL(c.resourceID), &account, &budget); err != nil {
		return Snapshot{}, err
	}
	if !strings.EqualFold(account.ID, c.resourceID) {
		return Snapshot{}, errors.New("Die Azure-Antwort gehoert nicht zur konfigurierten Ressourcen-ID.")
	}
	parts := strings.Split(c.resourceID, "/")
	endpoint, err := selectEndpoint(account.Properties, parts[len(parts)-1])
	if err != nil {
		return Snapshot{}, err
	}
	deployments := make([]Deployment, 0)
	seenPages, seenNames := make(map[string]bool), make(map[string]bool)
	total := 0
	next := c.resourceURL(c.resourceID + "/deployments")
	for page := 0; next != ""; page++ {
		if page >= maxPages || seenPages[next] {
			return Snapshot{}, errors.New("Die Azure-Bereitstellungsliste ist zu lang oder enthaelt wiederholte Seiten.")
		}
		seenPages[next] = true
		var response struct {
			Value    *[]armDeployment `json:"value"`
			NextLink string           `json:"nextLink"`
		}
		if err := c.getJSON(ctx, next, &response, &budget); err != nil {
			return Snapshot{}, err
		}
		if response.Value == nil {
			return Snapshot{}, errors.New("Die Azure-Antwort enthaelt keine Bereitstellungsliste.")
		}
		total += len(*response.Value)
		if total > maxDeployments {
			return Snapshot{}, errors.New("Die Azure-Bereitstellungsliste ueberschreitet die Mengengrenze.")
		}
		for _, raw := range *response.Value {
			nameKey := strings.ToLower(raw.Name)
			if !validSegment(raw.Name) || seenNames[nameKey] ||
				!strings.EqualFold(raw.ID, c.resourceID+"/deployments/"+raw.Name) {
				return Snapshot{}, errors.New("Die Azure-Bereitstellung hat eine ungueltige, fremde oder doppelte ID.")
			}
			seenNames[nameKey] = true
			d := Deployment{
				Name: raw.Name, ModelName: raw.Properties.Model.Name, ModelFormat: raw.Properties.Model.Format,
				ModelVersion: raw.Properties.Model.Version, ProvisioningState: raw.Properties.ProvisioningState,
				SKU: raw.SKU.Name, Capabilities: raw.Properties.Capabilities,
			}
			if d.SupportsChat() {
				d.Reasoning = d.reasoningSafe()
				deployments = append(deployments, d)
			}
		}
		if response.NextLink == "" {
			next = ""
		} else {
			next, err = c.paginationURL(next, response.NextLink)
			if err != nil {
				return Snapshot{}, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, safeError(ctx, err, "Azure-Ermittlung")
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].Name < deployments[j].Name })
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return Snapshot{}, errors.New("Die Azure-Ermittlung wurde durch eine neuere Aktualisierung ersetzt.")
	}
	c.endpoint = endpoint
	return Snapshot{ResourceID: c.resourceID, Endpoint: endpoint, Deployments: deployments, RefreshedAt: time.Now().UTC()}, nil
}

type armDeployment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SKU  struct {
		Name string `json:"name"`
	} `json:"sku"`
	Properties struct {
		Model struct {
			Name    string `json:"name"`
			Format  string `json:"format"`
			Version string `json:"version"`
		} `json:"model"`
		ProvisioningState string            `json:"provisioningState"`
		Capabilities      map[string]string `json:"capabilities"`
	} `json:"properties"`
}

// Authorize only signs POSTs to the last verified chat-completions endpoint.
// Callers must not mutate the destination afterwards and must refuse redirects
// on their inference HTTP client; this hook does not own that transport.
func (c *Client) Authorize(req *http.Request) error {
	if req == nil {
		return errors.New("Die Azure-Autorisierung erfordert eine HTTP-Anfrage.")
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for key := range req.Header {
		if strings.EqualFold(key, "api-key") || strings.EqualFold(key, "Authorization") {
			delete(req.Header, key)
		}
	}
	if c == nil || c.credential == nil {
		return errors.New("Der Azure-Client ist nicht initialisiert.")
	}
	c.mu.RLock()
	endpoint, generation := c.endpoint, c.generation
	c.mu.RUnlock()
	if endpoint == "" || !cleanURL(req.URL) || req.URL.RawQuery != "" ||
		req.URL.String() != endpoint+"/chat/completions" ||
		(req.Host != "" && req.Host != req.URL.Host) ||
		req.Method != http.MethodPost || req.RequestURI != "" {
		return errors.New("Das Azure-Chat-Ziel ist nicht durch eine erfolgreiche Ermittlung verifiziert.")
	}
	token, err := c.token(req.Context(), inferenceScope)
	if err != nil {
		return err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.generation != generation || c.endpoint != endpoint {
		return errors.New("Die Azure-Ermittlung hat sich waehrend der Autorisierung geaendert.")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (c *Client) token(ctx context.Context, scope string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return "", safeError(ctx, ctx.Err(), "Azure-Anmeldung")
	}
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return "", safeError(ctx, err, "Azure-Anmeldung")
	}
	if ctx.Err() != nil {
		return "", safeError(ctx, ctx.Err(), "Azure-Anmeldung")
	}
	if token.Token == "" || strings.ContainsAny(token.Token, "\r\n") || !token.ExpiresOn.After(time.Now()) {
		return "", errors.New("Azure hat kein gueltiges Zugriffstoken geliefert.")
	}
	return token.Token, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target any, budget *int64) error {
	token, err := c.token(ctx, armScope)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("Die Azure-Abfrage konnte nicht erstellt werden.")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := *c.httpClient
	client.CheckRedirect = refuseRedirect
	resp, err := client.Do(req)
	if err != nil {
		return safeError(ctx, err, "Azure-Abfrage")
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return fmt.Errorf("Die Azure-Abfrage ist fehlgeschlagen (HTTP %d).", resp.StatusCode)
	}
	body, err := readResponse(ctx, resp, *budget)
	if err != nil {
		return err
	}
	*budget -= int64(len(body))
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("Die Azure-Antwort hat kein gueltiges JSON-Datenformat.")
	}
	return nil
}

func readResponse(ctx context.Context, resp *http.Response, budget int64) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	limit := min(int64(maxResponseBytes), budget)
	if resp.ContentLength > limit {
		return nil, errors.New("Die Azure-Antwort ueberschreitet die erlaubte Groesse.")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, safeError(ctx, err, "Lesen der Azure-Antwort")
	}
	if int64(len(body)) > limit {
		return nil, errors.New("Die Azure-Antwort ueberschreitet die erlaubte Groesse.")
	}
	return body, nil
}

type interruptedError struct{ cause error }

func (e interruptedError) Error() string {
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return "Die Azure-Anfrage hat das Zeitlimit ueberschritten."
	}
	return "Die Azure-Anfrage wurde abgebrochen."
}
func (e interruptedError) Unwrap() error { return e.cause }

func safeError(ctx context.Context, err error, action string) error {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(ctx.Err(), cause) || errors.Is(err, cause) {
			return interruptedError{cause: cause}
		}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return errors.New(action + ": Das Zeitlimit wurde ueberschritten.")
	}
	return errors.New(action + " fehlgeschlagen; bitte Identitaet, Berechtigungen und Verbindung pruefen.")
}
