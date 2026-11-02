package oauth

import (
	"context"
	"sync"
	"time"

	"github.com/nycu-ucr/oauth2"

	"github.com/nycu-ucr/openapi"
	"github.com/nycu-ucr/openapi/models"
	"github.com/nycu-ucr/openapi/nrf/AccessToken"
)

// A token is issued to a particular NF for a particular target and service.
type tokenCacheKey struct {
	nfType   models.NrfNfManagementNfType
	targetNF models.NrfNfManagementNfType
	nfID     string
	nrfURI   string
	scope    string
}

type cachedToken struct {
	response  models.NrfAccessTokenAccessTokenRsp
	expiresAt time.Time
}

var tokenMap sync.Map
var clientMap sync.Map

func GetTokenCtx(
	nfType, targetNF models.NrfNfManagementNfType,
	nfId, nrfUri, scope string,
) (context.Context, *models.ProblemDetails, error) {
	tok, pd, err := sendAccTokenReq(nfType, targetNF, nfId, nrfUri, scope)
	if err != nil {
		return nil, pd, err
	}
	return context.WithValue(context.Background(),
		openapi.ContextOAuth2, tok), pd, nil
}

func sendAccTokenReq(
	nfType, targetNF models.NrfNfManagementNfType,
	nfId, nrfUri, scope string,
) (oauth2.TokenSource, *models.ProblemDetails, error) {
	key := tokenCacheKey{nfType, targetNF, nfId, nrfUri, scope}
	if val, ok := tokenMap.Load(key); ok {
		cached := val.(cachedToken)
		if time.Now().Before(cached.expiresAt) {
			return oauth2.StaticTokenSource(&oauth2.Token{
				AccessToken: cached.response.AccessToken,
				TokenType:   cached.response.TokenType,
				Expiry:      cached.expiresAt,
			}), nil, nil
		}
	}

	var client *AccessToken.APIClient
	if val, ok := clientMap.Load(nrfUri); ok {
		client = val.(*AccessToken.APIClient)
	} else {
		configuration := AccessToken.NewConfiguration()
		configuration.SetBasePath(nrfUri)
		newClient := AccessToken.NewAPIClient(configuration)
		val, _ := clientMap.LoadOrStore(nrfUri, newClient)
		client = val.(*AccessToken.APIClient)
	}

	req := &AccessToken.AccessTokenRequestRequest{}
	req.SetGrantType("client_credentials")
	req.SetNfInstanceId(nfId)
	req.SetNfType(nfType)
	req.SetTargetNfType(targetNF)
	req.SetScope(scope)

	requestStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := client.AccessTokenRequestApi.AccessTokenRequest(
		ctx, req)
	if err != nil {
		return nil, nil, err
	}

	response := res.NrfAccessTokenAccessTokenRsp
	// expires_in is a lifetime in seconds, not a Unix timestamp.
	expiresAt := requestStart.Add(time.Duration(response.ExpiresIn) * time.Second)
	if response.ExpiresIn > 0 {
		tokenMap.Store(key, cachedToken{response: response, expiresAt: expiresAt})
	}
	return oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: response.AccessToken,
		TokenType:   response.TokenType,
		Expiry:      expiresAt,
	}), nil, nil
}
