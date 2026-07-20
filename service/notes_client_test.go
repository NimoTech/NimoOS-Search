package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotesClient_Query_SendsUserAndFiltersArchived(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/parser/notes/query", r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"hits":[{"note_id":"n1","chunk_no":0,"text":"t","type":"note","status":"curated","updated_at":9,"score":0.7}]}`))
	}))
	defer srv.Close()
	c := NewNotesClient(NewBaseURLSource("", srv.URL), 5)
	hits, err := c.Query(context.Background(), "nas", 6, "42")
	require.NoError(t, err)
	require.Equal(t, "42", gotBody["user_id"])
	require.EqualValues(t, 6, gotBody["top_k"])
	require.ElementsMatch(t, []any{"draft", "curated"}, gotBody["statuses"])
	require.Len(t, hits, 1)
	require.Equal(t, "n1", hits[0].NoteID)
}

func TestNotesClient_Query_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := NewNotesClient(NewBaseURLSource("", srv.URL), 5)
	_, err := c.Query(context.Background(), "q", 3, "1")
	require.Error(t, err)
}

func TestNotesClient_ZeroTimeoutClampedToDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":[]}`))
	}))
	defer srv.Close()
	c := NewNotesClient(NewBaseURLSource("", srv.URL), 0)
	hits, err := c.Query(context.Background(), "q", 3, "1")
	require.NoError(t, err)
	require.Empty(t, hits)
}
