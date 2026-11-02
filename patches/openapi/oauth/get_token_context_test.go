package oauth

import (
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gonethttp "github.com/nycu-ucr/gonet/http"
	"github.com/nycu-ucr/oauth2"
	"github.com/nycu-ucr/openapi"
	"github.com/nycu-ucr/openapi/models"
	"github.com/nycu-ucr/openapi/nrf/AccessToken"
)

type tokenTransport struct {
	t        *testing.T
	requests *int32
}

func (transport tokenTransport) RoundTrip(request *gonethttp.Request) (*gonethttp.Response, error) {
	if request.Method != "POST" || request.URL.Path != "/oauth2/token" {
		transport.t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
	}
	n := atomic.AddInt32(transport.requests, 1)
	body, err := json.Marshal(models.NrfAccessTokenAccessTokenRsp{
		AccessToken: "token-" + string(rune('0'+n)),
		TokenType:   "Bearer",
		ExpiresIn:   1000,
	})
	if err != nil {
		return nil, err
	}
	return &gonethttp.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Header:     gonethttp.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    request,
	}, nil
}

func TestTokenCacheUsesLifetimeAndRequestIdentity(t *testing.T) {
	var requests int32
	nrfURI := "http://nrf.test"
	configuration := AccessToken.NewConfiguration()
	configuration.SetBasePath(nrfURI)
	configuration.SetHTTPClient(&gonethttp.Client{
		Transport: tokenTransport{t: t, requests: &requests},
	})
	clientMap.Store(nrfURI, AccessToken.NewAPIClient(configuration))

	get := func(nfID string, target models.NrfNfManagementNfType) *oauth2.Token {
		t.Helper()
		ctx, _, err := GetTokenCtx(models.NrfNfManagementNfType_AMF, target,
			nfID, nrfURI, "nsmf-pdusession")
		if err != nil {
			t.Fatalf("GetTokenCtx: %v", err)
		}
		source, ok := ctx.Value(openapi.ContextOAuth2).(oauth2.TokenSource)
		if !ok {
			t.Fatal("missing OAuth token source")
		}
		token, err := source.Token()
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		return token
	}

	first := get("amf-one", models.NrfNfManagementNfType_SMF)
	if first.Expiry.Before(time.Now().Add(900 * time.Second)) {
		t.Fatalf("expires_in was not converted to an absolute expiry: %v", first.Expiry)
	}
	second := get("amf-one", models.NrfNfManagementNfType_SMF)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("same request identity made %d NRF requests, want 1", got)
	}
	if first.AccessToken != second.AccessToken {
		t.Fatalf("cache returned different tokens: %q and %q", first.AccessToken, second.AccessToken)
	}

	get("amf-one", models.NrfNfManagementNfType_UDM)
	get("amf-two", models.NrfNfManagementNfType_SMF)
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("distinct request identities made %d NRF requests, want 3", got)
	}

	key := tokenCacheKey{models.NrfNfManagementNfType_AMF,
		models.NrfNfManagementNfType_SMF, "amf-one", nrfURI, "nsmf-pdusession"}
	tokenMap.Store(key, cachedToken{
		response:  models.NrfAccessTokenAccessTokenRsp{AccessToken: first.AccessToken},
		expiresAt: time.Now().Add(-time.Second),
	})
	refreshed := get("amf-one", models.NrfNfManagementNfType_SMF)
	if got := atomic.LoadInt32(&requests); got != 4 {
		t.Fatalf("expired token made %d NRF requests, want 4", got)
	}
	if refreshed.AccessToken == first.AccessToken {
		t.Fatal("expired token was reused")
	}
}
