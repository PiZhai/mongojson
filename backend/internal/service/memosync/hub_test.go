package memosync

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHubBroadcastsDocumentRevision(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeDocument(w, r, "memo-1")
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial memo sync: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ready Event
	if err := conn.ReadJSON(&ready); err != nil || ready.Type != EventReady {
		t.Fatalf("read ready event: %#v, %v", ready, err)
	}

	hub.Publish(Event{Type: EventDocumentUpdated, DocumentID: "memo-1", Revision: 7, ActorClientID: "peer-1"})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var event Event
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("read memo event: %v", err)
	}
	if event.Type != EventDocumentUpdated || event.DocumentID != "memo-1" || event.Revision != 7 || event.ActorClientID != "peer-1" {
		t.Fatalf("unexpected event: %#v", event)
	}
}
