package ginadapter_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
	"github.com/getaviato/aviato-go/ginadapter"
	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

func TestMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("aviato-gin-adapter-test-secret!!"))
	plugin := aviato.MustNew(aviato.Options{Secret: secret, BasePath: "/aviato"}).
		Segment("customers", "vip", func(context.Context, *aviato.Invocation) (aviato.SegmentResult, error) {
			return aviato.SegmentFilter(map[string]any{"plan": "team"}), nil
		})
	router := gin.New()
	router.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	ginadapter.Mount(router, plugin)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	signed, err := aviato.NewSigningClient(secret)
	require.NoError(t, err)
	client := pluginv1connect.NewPluginServiceClient(signed, server.URL+"/aviato", connect.WithProtoJSON())
	response, err := client.GetManifest(context.Background(), connect.NewRequest(&pluginv1.GetManifestRequest{AgentProtocolVersion: 1}))
	require.NoError(t, err)
	assert.Equal(t, "vip", response.Msg.GetManifest().GetSegments()[0].GetName())

	unsigned := pluginv1connect.NewPluginServiceClient(http.DefaultClient, server.URL+"/aviato", connect.WithProtoJSON())
	_, err = unsigned.GetManifest(context.Background(), connect.NewRequest(&pluginv1.GetManifestRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	health, err := http.Get(server.URL + "/health")
	require.NoError(t, err)
	health.Body.Close()
	assert.Equal(t, http.StatusOK, health.StatusCode, "other routes keep working")
}
