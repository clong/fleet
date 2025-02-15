package cached_mysql

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/fleetdm/fleet/v4/server/datastore/redis"
	"github.com/fleetdm/fleet/v4/server/fleet"
	"github.com/fleetdm/fleet/v4/server/mock"
	"github.com/fleetdm/fleet/v4/server/ptr"
	redigo "github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPool is basically repeated in every package that uses redis
// I tried to move this to a datastoretest package, but there's an import loop with redis
// so I decided to copy and past for now
func newPool(t *testing.T, cluster bool) fleet.RedisPool {
	if cluster && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		t.Skipf("docker networking limitations prevent running redis cluster tests on %s", runtime.GOOS)
	}

	if _, ok := os.LookupEnv("REDIS_TEST"); ok {
		var (
			addr     = "127.0.0.1:"
			password = ""
			database = 0
			useTLS   = false
			port     = "6379"
		)
		if cluster {
			port = "7001"
		}
		addr += port

		pool, err := redis.NewRedisPool(redis.PoolConfig{
			Server:      addr,
			Password:    password,
			Database:    database,
			UseTLS:      useTLS,
			ConnTimeout: 5 * time.Second,
			KeepAlive:   10 * time.Second,
		})
		require.NoError(t, err)
		conn := pool.Get()
		defer conn.Close()
		_, err = conn.Do("PING")
		require.Nil(t, err)
		return pool
	}
	return nil
}

func TestCachedAppConfig(t *testing.T) {
	pool := newPool(t, false)
	conn := pool.Get()
	_, err := conn.Do("DEL", CacheKeyAppConfig)
	require.NoError(t, err)

	mockedDS := new(mock.Store)
	ds := New(mockedDS, pool)

	var appConfigSet *fleet.AppConfig
	mockedDS.NewAppConfigFunc = func(ctx context.Context, info *fleet.AppConfig) (*fleet.AppConfig, error) {
		appConfigSet = info
		return info, nil
	}
	mockedDS.AppConfigFunc = func(ctx context.Context) (*fleet.AppConfig, error) {
		return appConfigSet, err
	}
	mockedDS.SaveAppConfigFunc = func(ctx context.Context, info *fleet.AppConfig) error {
		appConfigSet = info
		return nil
	}
	_, err = ds.NewAppConfig(context.Background(), &fleet.AppConfig{
		HostSettings: fleet.HostSettings{
			AdditionalQueries: ptr.RawMessage(json.RawMessage(`"TestCachedAppConfig"`)),
		},
	})
	require.NoError(t, err)

	t.Run("NewAppConfig", func(t *testing.T) {
		data, err := redigo.Bytes(conn.Do("GET", CacheKeyAppConfig))
		require.NoError(t, err)

		require.NotEmpty(t, data)
		newAc := &fleet.AppConfig{}
		require.NoError(t, json.Unmarshal(data, &newAc))
		require.NotNil(t, newAc.HostSettings.AdditionalQueries)
		assert.Equal(t, json.RawMessage(`"TestCachedAppConfig"`), *newAc.HostSettings.AdditionalQueries)
	})

	t.Run("AppConfig", func(t *testing.T) {
		require.False(t, mockedDS.AppConfigFuncInvoked)
		ac, err := ds.AppConfig(context.Background())
		require.NoError(t, err)
		require.False(t, mockedDS.AppConfigFuncInvoked)

		require.Equal(t, ptr.RawMessage(json.RawMessage(`"TestCachedAppConfig"`)), ac.HostSettings.AdditionalQueries)
	})

	t.Run("AppConfig uses DS if redis fails", func(t *testing.T) {
		_, err = conn.Do("DEL", CacheKeyAppConfig)
		require.NoError(t, err)
		ac, err := ds.AppConfig(context.Background())
		require.NoError(t, err)
		require.True(t, mockedDS.AppConfigFuncInvoked)

		require.Equal(t, ptr.RawMessage(json.RawMessage(`"TestCachedAppConfig"`)), ac.HostSettings.AdditionalQueries)
	})

	t.Run("SaveAppConfig", func(t *testing.T) {
		require.NoError(t, ds.SaveAppConfig(context.Background(), &fleet.AppConfig{
			HostSettings: fleet.HostSettings{
				AdditionalQueries: ptr.RawMessage(json.RawMessage(`"NewSAVED"`)),
			},
		}))

		data, err := redigo.Bytes(conn.Do("GET", CacheKeyAppConfig))
		require.NoError(t, err)

		require.NotEmpty(t, data)
		newAc := &fleet.AppConfig{}
		require.NoError(t, json.Unmarshal(data, &newAc))
		require.NotNil(t, newAc.HostSettings.AdditionalQueries)
		assert.Equal(t, json.RawMessage(`"NewSAVED"`), *newAc.HostSettings.AdditionalQueries)

		ac, err := ds.AppConfig(context.Background())
		require.NoError(t, err)
		require.NotNil(t, ac.HostSettings.AdditionalQueries)
		assert.Equal(t, json.RawMessage(`"NewSAVED"`), *ac.HostSettings.AdditionalQueries)
	})

	t.Run("AuthenticateHost skips cache if disabled", func(t *testing.T) {
		_, err = conn.Do("DEL", CacheKeyAppConfig)
		require.NoError(t, err)

		mockedDS.AppConfigFunc = func(ctx context.Context) (*fleet.AppConfig, error) {
			return &fleet.AppConfig{}, nil
		}
		mockedDS.AuthenticateHostFunc = func(ctx context.Context, nodeKey string) (*fleet.Host, error) {
			return &fleet.Host{ID: 999}, nil
		}
		_, err = ds.AuthenticateHost(context.Background(), "1234")
		require.NoError(t, err)
		require.True(t, mockedDS.AuthenticateHostFuncInvoked)
		mockedDS.AuthenticateHostFuncInvoked = false

		_, err = ds.AuthenticateHost(context.Background(), "1234")
		require.NoError(t, err)
		require.True(t, mockedDS.AuthenticateHostFuncInvoked)
		mockedDS.AuthenticateHostFuncInvoked = false
	})
}
