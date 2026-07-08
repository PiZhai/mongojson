package watchsync

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHubBroadcastsControlStateToRoomPeers(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(httpHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeRoom(w, r, "room-1")
	}))
	defer server.Close()

	first := dialTestClient(t, server.URL, "first")
	defer first.Close()
	second := dialTestClient(t, server.URL, "second")
	defer second.Close()

	readUntilState(t, first)
	readUntilState(t, second)

	payload := ControlMessage{
		Type:         messageTypeControl,
		ClientID:     "first",
		MediaID:      "demo.mp4:100",
		MediaName:    "demo.mp4",
		Paused:       false,
		Position:     12.5,
		PlaybackRate: 1.25,
	}
	if err := first.WriteJSON(payload); err != nil {
		t.Fatalf("write control: %v", err)
	}

	state := readUntilState(t, second)
	if state.ActorClientID != "first" {
		t.Fatalf("actor_client_id = %q, want first", state.ActorClientID)
	}
	if state.State == nil {
		t.Fatalf("expected state payload")
	}
	if state.State.MediaID != payload.MediaID || state.State.MediaName != payload.MediaName {
		t.Fatalf("state media = %q/%q, want %q/%q", state.State.MediaID, state.State.MediaName, payload.MediaID, payload.MediaName)
	}
	if state.State.Paused || state.State.Position != payload.Position || state.State.PlaybackRate != payload.PlaybackRate {
		t.Fatalf("state playback = paused:%v position:%v rate:%v", state.State.Paused, state.State.Position, state.State.PlaybackRate)
	}
	if state.State.Version != 1 {
		t.Fatalf("state version = %d, want 1", state.State.Version)
	}
}

type httpHandlerFunc func(http.ResponseWriter, *http.Request)

func (fn httpHandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fn(w, r)
}

func dialTestClient(t *testing.T, serverURL string, clientID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/?client_id=" + clientID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readUntilState(t *testing.T, conn *websocket.Conn) ServerMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var msg ServerMessage
		if err := conn.ReadJSON(&msg); err != nil {
			continue
		}
		if msg.Type == messageTypeState {
			return msg
		}
	}
	t.Fatalf("timed out waiting for state")
	return ServerMessage{}
}
