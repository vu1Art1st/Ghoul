package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestFindingEvidenceImageContent(t *testing.T) {
	s, finding, request := trafficEvidenceServer(t)
	raw, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := s.m.pg.AddFindingEvidence(t.Context(), finding.FindingID, nil, "test", "screenshot API", db.FindingEvidence{
		Kind:    "screenshot",
		Title:   "IDOR proof",
		Content: "data:image/png;base64," + onePixelPNG,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := request(http.MethodGet, fmt.Sprintf("/api/exploration/findings/%d/evidence/%d/content", finding.FindingID, item.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("content status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type=%q", got)
	}
	if !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatal("served screenshot bytes differ from stored image")
	}
	w = request(http.MethodGet, fmt.Sprintf("/api/exploration/findings/%d/evidence", finding.FindingID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", w.Code, w.Body.String())
	}
	var timeline struct {
		Generic []db.FindingEvidence `json:"generic"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &timeline); err != nil {
		t.Fatal(err)
	}
	if len(timeline.Generic) == 0 || timeline.Generic[0].Content != "" {
		t.Fatal("timeline should not inline screenshot base64")
	}
	w = request(http.MethodGet, fmt.Sprintf("/api/exploration/findings/%d/evidence/%d/content", finding.FindingID+9999, item.ID), "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-finding evidence read status=%d, want 404", w.Code)
	}
}
