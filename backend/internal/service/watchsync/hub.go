package watchsync

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	messageTypeControl   = "control"
	messageTypeState     = "state"
	messageTypePeerCount = "peer_count"
	messageTypeError     = "error"

	defaultPlaybackRate = 1
	maxMessageBytes     = 8 << 10
	writeWait           = 5 * time.Second
	pongWait            = 60 * time.Second
	pingPeriod          = 45 * time.Second
)

type PlaybackState struct {
	RoomID       string  `json:"room_id"`
	MediaID      string  `json:"media_id"`
	MediaName    string  `json:"media_name"`
	Paused       bool    `json:"paused"`
	Position     float64 `json:"position"`
	PlaybackRate float64 `json:"playback_rate"`
	ServerTime   int64   `json:"server_time"`
	Version      int64   `json:"version"`
}

type ControlMessage struct {
	Type         string  `json:"type"`
	ClientID     string  `json:"client_id"`
	MediaID      string  `json:"media_id"`
	MediaName    string  `json:"media_name"`
	Paused       bool    `json:"paused"`
	Position     float64 `json:"position"`
	PlaybackRate float64 `json:"playback_rate"`
	BaseVersion  int64   `json:"base_version"`
}

type ServerMessage struct {
	Type          string         `json:"type"`
	State         *PlaybackState `json:"state,omitempty"`
	PeerCount     int            `json:"peer_count,omitempty"`
	ActorClientID string         `json:"actor_client_id,omitempty"`
	Message       string         `json:"message,omitempty"`
}

type Hub struct {
	mu       sync.Mutex
	rooms    map[string]*room
	upgrader websocket.Upgrader
}

type room struct {
	id      string
	state   PlaybackState
	clients map[*client]struct{}
}

type client struct {
	id   string
	room *room
	conn *websocket.Conn
	send chan ServerMessage
}

func NewHub() *Hub {
	return &Hub{
		rooms: map[string]*room{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(*http.Request) bool {
				return true
			},
		},
	}
}

func (h *Hub) ServeRoom(w http.ResponseWriter, r *http.Request, roomID string) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		http.Error(w, "room id is required", http.StatusBadRequest)
		return
	}

	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if clientID == "" {
		http.Error(w, "client_id is required", http.StatusBadRequest)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	c := &client{
		id:   clientID,
		conn: conn,
		send: make(chan ServerMessage, 16),
	}

	state, peerCount := h.register(roomID, c)
	c.send <- ServerMessage{Type: messageTypeState, State: &state}
	h.broadcastPeerCount(c.room, peerCount)

	go c.writePump()
	c.readPump(h)
}

func (h *Hub) register(roomID string, c *client) (PlaybackState, int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	r := h.rooms[roomID]
	if r == nil {
		r = &room{
			id: roomID,
			state: PlaybackState{
				RoomID:       roomID,
				Paused:       true,
				PlaybackRate: defaultPlaybackRate,
				ServerTime:   nowMillis(),
			},
			clients: map[*client]struct{}{},
		}
		h.rooms[roomID] = r
	}

	c.room = r
	r.clients[c] = struct{}{}
	return r.state, len(r.clients)
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if c.room == nil {
		return
	}

	r := c.room
	delete(r.clients, c)
	close(c.send)

	if len(r.clients) == 0 {
		delete(h.rooms, r.id)
		return
	}

	h.broadcastLocked(r, ServerMessage{Type: messageTypePeerCount, PeerCount: len(r.clients)})
}

func (h *Hub) applyControl(c *client, msg ControlMessage) (PlaybackState, error) {
	if c.room == nil {
		return PlaybackState{}, errors.New("client is not registered in a room")
	}
	if msg.Type != "" && msg.Type != messageTypeControl {
		return PlaybackState{}, errors.New("unsupported message type")
	}
	if msg.PlaybackRate <= 0 || msg.PlaybackRate > 16 {
		return PlaybackState{}, errors.New("playback_rate must be between 0 and 16")
	}
	if msg.Position < 0 {
		msg.Position = 0
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	state := c.room.state
	state.MediaID = strings.TrimSpace(msg.MediaID)
	state.MediaName = strings.TrimSpace(msg.MediaName)
	state.Paused = msg.Paused
	state.Position = msg.Position
	state.PlaybackRate = msg.PlaybackRate
	state.ServerTime = nowMillis()
	state.Version++
	c.room.state = state

	h.broadcastLocked(c.room, ServerMessage{
		Type:          messageTypeState,
		State:         &state,
		ActorClientID: c.id,
	})
	return state, nil
}

func (h *Hub) broadcastPeerCount(r *room, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.broadcastLocked(r, ServerMessage{Type: messageTypePeerCount, PeerCount: count})
}

func (h *Hub) broadcastLocked(r *room, msg ServerMessage) {
	for c := range r.clients {
		select {
		case c.send <- msg:
		default:
		}
	}
}

func (c *client) readPump(h *Hub) {
	defer func() {
		h.unregister(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		var msg ControlMessage
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.ClientID != "" && msg.ClientID != c.id {
			c.send <- ServerMessage{Type: messageTypeError, Message: "client_id does not match connection"}
			continue
		}
		if _, err := h.applyControl(c, msg); err != nil {
			c.send <- ServerMessage{Type: messageTypeError, Message: err.Error()}
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			payload, err := json.Marshal(msg)
			if err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func normalizeRoomID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			continue
		}
		return ""
	}
	return value
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
