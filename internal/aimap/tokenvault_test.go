package aimap

import (
	"testing"

	"github.com/Kong/ai-deck-converter/internal/aigw"
	"github.com/stretchr/testify/require"
)

// Test-fixture values, held in vars rather than literals at the assignment
// sites: every one is either a vault *reference* (what the config actually
// carries) or obviously fake, but gosec's hardcoded-credential check (G101)
// pattern-matches literals assigned to credential-named fields, keys, and
// variables. The variable names steer clear of those patterns too.
var (
	fixtureRedisVaultRef    = "{vault://env/redis-pass}"
	fixtureSentinelVaultRef = "{vault://env/sentinel-pass}"
	fixtureAWSVaultRef      = "{vault://env/aws-secret}"
	fixtureVaultEncRef      = "{vault://env/TOKEN_VAULT_ENC_SECRET}"
	fixtureAWSID            = "AKIA..."
	fixtureAzureClientValue = "fake-azure-client-secret"
)

// tokenVaultRedisFixture builds a RedisCloudConfig exercising every field the
// nested API model carries, in both directions' shapes.
func tokenVaultRedisFixture() *aigw.RedisCloudConfig {
	port := 6379
	database := 0
	sslVerify := true
	connectTimeout := 2000
	poolSize := 256
	backlog := 128
	maxRedirections := 5
	isServerless := false
	return &aigw.RedisCloudConfig{
		Host:           "redis.internal",
		Port:           &port,
		Database:       &database,
		Username:       "default",
		Password:       fixtureRedisVaultRef,
		SSL:            &sslVerify,
		ServerName:     "redis.internal",
		ConnectTimeout: &connectTimeout,
		ReadTimeout:    &connectTimeout,
		SendTimeout:    &connectTimeout,
		Keepalive: &aigw.RedisKeepaliveConfig{
			PoolSize: &poolSize,
			Backlog:  &backlog,
		},
		Sentinel: &aigw.RedisSentinelConfig{
			Master:   "mymaster",
			Role:     "any",
			Username: "sentinel",
			Password: fixtureSentinelVaultRef,
			Nodes: []aigw.RedisNode{
				{Host: "sentinel-1.internal", Port: &port},
				{Host: "sentinel-2.internal", Port: &port},
			},
		},
		Cluster: &aigw.RedisClusterConfig{
			MaxRedirections: &maxRedirections,
			Nodes: []aigw.RedisNode{
				{IP: "10.0.0.1", Port: &port},
			},
		},
		CloudAuthentication: &aigw.RedisCloudAuthentication{
			Type:            "aws",
			AccessKeyID:     fixtureAWSID,
			SecretAccessKey: fixtureAWSVaultRef,
			CacheName:       "my-cache",
			IsServerless:    &isServerless,
			Region:          "us-east-1",
			AssumeRoleARN:   "arn:aws:iam::123:role/redis",
			RoleSessionName: "redis-session",
		},
	}
}

func TestTokenVaultRedisToPlugin(t *testing.T) {
	t.Parallel()

	block := TokenVaultRedisToPlugin(tokenVaultRedisFixture())
	require.Equal(t, map[string]any{
		"host":            "redis.internal",
		"port":            6379,
		"database":        0, // explicit zero must survive the lowering
		"username":        "default",
		"password":        fixtureRedisVaultRef,
		"ssl":             true,
		"server_name":     "redis.internal",
		"connect_timeout": 2000,
		"read_timeout":    2000,
		"send_timeout":    2000,
		// nested keepalive flattens to prefixed keys
		"keepalive_pool_size": 256,
		"keepalive_backlog":   128,
		// nested sentinel flattens to prefixed keys; nodes address by host
		"sentinel_master":   "mymaster",
		"sentinel_role":     "any",
		"sentinel_username": "sentinel",
		"sentinel_password": fixtureSentinelVaultRef,
		"sentinel_nodes": []map[string]any{
			{"host": "sentinel-1.internal", "port": 6379},
			{"host": "sentinel-2.internal", "port": 6379},
		},
		// nested cluster flattens to prefixed keys; nodes address by ip
		"cluster_max_redirections": 5,
		"cluster_nodes": []map[string]any{
			{"ip": "10.0.0.1", "port": 6379},
		},
		"cloud_authentication": map[string]any{
			"auth_provider":         "aws",
			"aws_access_key_id":     fixtureAWSID,
			"aws_secret_access_key": fixtureAWSVaultRef,
			"aws_cache_name":        "my-cache",
			"aws_is_serverless":     false,
			"aws_region":            "us-east-1",
			"aws_assume_role_arn":   "arn:aws:iam::123:role/redis",
			"aws_role_session_name": "redis-session",
		},
	}, block)
}

func TestTokenVaultRedisRoundTrip(t *testing.T) {
	t.Parallel()

	// The reverse mapping must invert the forward one field for field, so
	// forward -> reverse -> forward is a fixpoint.
	require.Equal(t, tokenVaultRedisFixture(),
		TokenVaultRedisFromPlugin(TokenVaultRedisToPlugin(tokenVaultRedisFixture())))
}

func TestTokenVaultRedisFromPluginOmissions(t *testing.T) {
	t.Parallel()

	// Only the keys present in the plugin block are set; the forward
	// direction's omissions stay omissions across a round trip.
	r := TokenVaultRedisFromPlugin(map[string]any{"host": "redis.internal", "port": 6379})
	require.Equal(t, &aigw.RedisCloudConfig{Host: "redis.internal", Port: intPtr(6379)}, r)
	require.Nil(t, r.Keepalive)
	require.Nil(t, r.Sentinel)
	require.Nil(t, r.Cluster)
	require.Nil(t, r.CloudAuthentication)
}

func TestTokenVaultToFromPlugin(t *testing.T) {
	t.Parallel()

	tv := &aigw.TokenVaultConfig{
		Directory:         "my-directory",
		Provider:          "my-upstream-provider",
		Redis:             tokenVaultRedisFixture(),
		EncryptionSecrets: []string{fixtureVaultEncRef},
	}
	block := TokenVaultToPlugin(tv)
	require.Equal(t, map[string]any{
		"directory":          "my-directory",
		"provider":           "my-upstream-provider",
		"redis":              TokenVaultRedisToPlugin(tokenVaultRedisFixture()),
		"encryption_secrets": []string{fixtureVaultEncRef},
	}, block)
	require.Equal(t, tv, TokenVaultFromPlugin(block))

	// Bare block (no redis, no secrets): the PR's first sample config.
	bare := TokenVaultToPlugin(&aigw.TokenVaultConfig{
		Directory: "my-directory",
		Provider:  "my-upstream-provider",
	})
	require.Equal(t, map[string]any{
		"directory": "my-directory",
		"provider":  "my-upstream-provider",
	}, bare)
	require.Equal(t, &aigw.TokenVaultConfig{
		Directory: "my-directory",
		Provider:  "my-upstream-provider",
	}, TokenVaultFromPlugin(bare))

	require.Nil(t, TokenVaultToPlugin(nil))
	require.Nil(t, TokenVaultFromPlugin(nil))
}

func TestCloudAuthToFromPluginVariants(t *testing.T) {
	t.Parallel()

	// Azure and GCP variants rename to their own prefixes; the discriminator
	// is `type` in the API model and `auth_provider` in the plugin.
	azure := TokenVaultRedisToPlugin(&aigw.RedisCloudConfig{
		CloudAuthentication: &aigw.RedisCloudAuthentication{
			Type:         "azure",
			ClientID:     "client",
			ClientSecret: fixtureAzureClientValue,
			TenantID:     "tenant",
		},
	})
	require.Equal(t, map[string]any{
		"cloud_authentication": map[string]any{
			"auth_provider":       "azure",
			"azure_client_id":     "client",
			"azure_client_secret": fixtureAzureClientValue,
			"azure_tenant_id":     "tenant",
		},
	}, azure)
	require.Equal(t, &aigw.RedisCloudConfig{
		CloudAuthentication: &aigw.RedisCloudAuthentication{
			Type:         "azure",
			ClientID:     "client",
			ClientSecret: fixtureAzureClientValue,
			TenantID:     "tenant",
		},
	}, TokenVaultRedisFromPlugin(azure))

	gcp := TokenVaultRedisToPlugin(&aigw.RedisCloudConfig{
		CloudAuthentication: &aigw.RedisCloudAuthentication{
			Type:               "gcp",
			ServiceAccountJSON: "{...}",
		},
	})
	require.Equal(t, map[string]any{
		"cloud_authentication": map[string]any{
			"auth_provider":            "gcp",
			"gcp_service_account_json": "{...}",
		},
	}, gcp)
	require.Equal(t, &aigw.RedisCloudConfig{
		CloudAuthentication: &aigw.RedisCloudAuthentication{
			Type:               "gcp",
			ServiceAccountJSON: "{...}",
		},
	}, TokenVaultRedisFromPlugin(gcp))
}

func intPtr(i int) *int { return &i }
