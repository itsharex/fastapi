package chat

import (
	credentials "cloud.google.com/go/iam/credentials/apiv1"
	"cloud.google.com/go/iam/credentials/apiv1/credentialspb"
	"context"
	"fmt"
	"github.com/gogf/gf/v2/encoding/gjson"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"
	"github.com/gogf/gf/v2/os/grpool"
	"github.com/gogf/gf/v2/os/gtime"
	"github.com/gogf/gf/v2/text/gstr"
	"github.com/iimeta/fastapi/internal/config"
	"github.com/iimeta/fastapi/internal/consts"
	"github.com/iimeta/fastapi/internal/model"
	"github.com/iimeta/fastapi/internal/service"
	"github.com/iimeta/fastapi/utility/cache"
	"github.com/iimeta/fastapi/utility/crypto"
	"github.com/iimeta/fastapi/utility/logger"
	"github.com/iimeta/fastapi/utility/redis"
	"github.com/iimeta/fastapi/utility/util"
	"google.golang.org/api/option"
	"time"
)

var gcpCache = cache.New() // [key]Token

func getGcpToken(ctx context.Context, key *model.Key, proxyURL string) string {

	now := gtime.TimestampMilli()
	defer func() {
		logger.Debugf(ctx, "getGcpToken time: %d", gtime.TimestampMilli()-now)
	}()

	if gcpTokenCacheValue := gcpCache.GetVal(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key)); gcpTokenCacheValue != nil {
		return gcpTokenCacheValue.(string)
	}

	reply, err := redis.GetStr(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key))
	if err == nil && reply != "" {

		if expiresIn, err := redis.TTL(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key)); err != nil {
			logger.Errorf(ctx, "getGcpToken key: %s, error: %v", key.Key, err)
		} else {
			if err = gcpCache.Set(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key), reply, time.Second*time.Duration(expiresIn-60)); err != nil {
				logger.Errorf(ctx, "getGcpToken key: %s, error: %v", key.Key, err)
			}
		}

		return reply
	}

	result := gstr.Split(key.Key, "|")

	data := g.Map{
		"client_id":     result[1],
		"client_secret": result[2],
		"refresh_token": result[3],
		"grant_type":    "refresh_token",
	}

	getGcpTokenRes := new(model.GetGcpTokenRes)
	if err = util.HttpPost(ctx, config.Cfg.Gcp.GetTokenUrl, nil, data, &getGcpTokenRes, proxyURL); err != nil {
		logger.Errorf(ctx, "getGcpToken key: %s, error: %v", key.Key, err)
		return ""
	}

	if getGcpTokenRes.Error != "" {
		logger.Errorf(ctx, "getGcpToken key: %s, getGcpTokenRes.Error: %s", key.Key, getGcpTokenRes.Error)
		if err = grpool.Add(gctx.NeverDone(ctx), func(ctx context.Context) {
			service.Key().DisabledModelKey(ctx, key)
		}); err != nil {
			logger.Error(ctx, err)
		}
		return ""
	}

	if err = gcpCache.Set(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key), getGcpTokenRes.AccessToken, time.Second*time.Duration(getGcpTokenRes.ExpiresIn-60)); err != nil {
		logger.Errorf(ctx, "getGcpToken key: %s, error: %v", key.Key, err)
	}

	if err = redis.SetEX(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key), getGcpTokenRes.AccessToken, getGcpTokenRes.ExpiresIn-60); err != nil {
		logger.Errorf(ctx, "getGcpToken key: %s, error: %v", key.Key, err)
	}

	return getGcpTokenRes.AccessToken
}

type ApplicationDefaultCredentials struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id"`
	AuthURI                 string `json:"auth_uri"`
	TokenURI                string `json:"token_uri"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
	ClientX509CertURL       string `json:"client_x509_cert_url"`
	UniverseDomain          string `json:"universe_domain"`
}

func getGcpTokenNew(ctx context.Context, key *model.Key, proxyURL string) (string, error) {

	now := gtime.TimestampMilli()
	defer func() {
		logger.Debugf(ctx, "getGcpTokenNew time: %d", gtime.TimestampMilli()-now)
	}()

	if gcpTokenCacheValue := gcpCache.GetVal(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, crypto.SM3(key.Key))); gcpTokenCacheValue != nil {
		return gcpTokenCacheValue.(string), nil
	}

	reply, err := redis.GetStr(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, crypto.SM3(key.Key)))
	if err == nil && reply != "" {

		if expiresIn, err := redis.TTL(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, crypto.SM3(key.Key))); err != nil {
			logger.Errorf(ctx, "getGcpTokenNew key: %s, error: %v", key.Key, err)
		} else {
			if err = gcpCache.Set(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, crypto.SM3(key.Key)), reply, time.Second*time.Duration(expiresIn-60)); err != nil {
				logger.Errorf(ctx, "getGcpTokenNew key: %s, error: %v", key.Key, err)
			}
		}

		return reply, nil
	}

	result := gstr.Split(key.Key, "|")

	adc := &ApplicationDefaultCredentials{}
	if err := gjson.Unmarshal([]byte(result[1]), adc); err != nil {
		logger.Errorf(ctx, "getGcpTokenNew gjson.Unmarshal key: %s, error: %v", key.Key, err)
		return "", err
	}

	client, err := credentials.NewIamCredentialsClient(ctx, option.WithCredentialsJSON([]byte(result[1])))
	if err != nil {
		logger.Errorf(ctx, "getGcpTokenNew NewIamCredentialsClient key: %s, error: %v", key.Key, err)
		return "", err
	}

	defer func() {
		if err = client.Close(); err != nil {
			logger.Error(ctx, err)
		}
	}()

	request := &credentialspb.GenerateAccessTokenRequest{
		Name:  fmt.Sprintf("projects/-/serviceAccounts/%s", adc.ClientEmail),
		Scope: []string{"https://www.googleapis.com/auth/cloud-platform"},
	}

	response, err := client.GenerateAccessToken(ctx, request)
	if err != nil {
		logger.Errorf(ctx, "getGcpTokenNew GenerateAccessToken key: %s, error: %v", key.Key, err)
		return "", err
	}

	if err = gcpCache.Set(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key), response.AccessToken, time.Second*time.Duration(response.ExpireTime.Seconds-60)); err != nil {
		logger.Errorf(ctx, "getGcpTokenNew key: %s, error: %v", key.Key, err)
	}

	if err = redis.SetEX(ctx, fmt.Sprintf(consts.GCP_TOKEN_KEY, key.Key), response.AccessToken, response.ExpireTime.Seconds-60); err != nil {
		logger.Errorf(ctx, "getGcpTokenNew key: %s, error: %v", key.Key, err)
	}

	return response.AccessToken, nil
}
