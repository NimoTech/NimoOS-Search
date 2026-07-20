package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// NoteHit mirrors Parser's /v1/parser/notes/query hit payload.
type NoteHit struct {
	NoteID    string  `json:"note_id"`
	ChunkNo   int     `json:"chunk_no"`
	Text      string  `json:"text"`
	Type      string  `json:"type"`
	Status    string  `json:"status"`
	UpdatedAt int64   `json:"updated_at"`
	Score     float64 `json:"score"`
}

// NotesClient queries knowledge notes through Parser, which owns the
// embed step and the user_id hard-isolation filter (single point of truth).
type NotesClient struct {
	src  *BaseURLSource
	http *http.Client
}

func NewNotesClient(src *BaseURLSource, timeoutSec int) *NotesClient {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	return &NotesClient{src: src,
		http: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}}
}

func (c *NotesClient) Query(ctx context.Context, query string, topK int, userID string) ([]NoteHit, error) {
	body, _ := json.Marshal(map[string]any{
		"user_id": userID,
		"query":   query,
		"top_k":   topK,
		// archived notes are excluded from search by design (spec §8).
		"statuses": []string{"draft", "curated"},
	})
	resp, err := doWithRediscover(c.http, c.src, func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/v1/parser/notes/query", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("notes query: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Hits []NoteHit `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Hits, nil
}
