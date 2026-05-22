package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterAtGateway_PostsExpectedRoutes(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	err := RegisterAtGateway(srv.URL, "http://127.0.0.1:12345", "/v1/search")
	require.NoError(t, err)
	require.Equal(t, "/v1/search", got["prefix"])
	require.Equal(t, "http://127.0.0.1:12345", got["target"])
}
