package memosync

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	EventReady           = "ready"
	EventDocumentUpdated = "document_updated"
	EventDocumentDeleted = "document_deleted"
	EventNotesUpdated    = "notes_updated"

	writeWait  = 5 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 45 * time.Second
)

type Event struct {
	Type          string `json:"type"`
	DocumentID    string `json:"document_id"`
	Revision      int64  `json:"revision,omitempty"`
	ActorClientID string `json:"actor_client_id,omitempty"`
}

type Hub struct {
	mu       sync.Mutex
	rooms    map[string]map[*client]struct{}
	upgrader websocket.Upgrader
}

type client struct {
	documentID string
	conn       *websocket.Conn
	send       chan Event
}

func NewHub() *Hub {
	return &Hub{
		rooms: map[string]map[*client]struct{}{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

func (h *Hub) ServeDocument(w http.ResponseWriter, r *http.Request, documentID string) {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" || len(documentID) > 128 {
		http.Error(w, "document id is required", http.StatusBadRequest)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{documentID: documentID, conn: conn, send: make(chan Event, 16)}
	h.register(c)
	c.send <- Event{Type: EventReady, DocumentID: documentID}
	go c.writePump()
	c.readPump(h)
}

func (h *Hub) Publish(event Event) {
	event.DocumentID = strings.TrimSpace(event.DocumentID)
	if event.DocumentID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.rooms[event.DocumentID] {
		select {
		case c.send <- event:
		default:
		}
	}
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[c.documentID]
	if room == nil {
		room = map[*client]struct{}{}
		h.rooms[c.documentID] = room
	}
	room[c] = struct{}{}
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[c.documentID]
	if room == nil {
		return
	}
	delete(room, c)
	close(c.send)
	if len(room) == 0 {
		delete(h.rooms, c.documentID)
	}
}

func (c *client) readPump(h *Hub) {
	defer func() {
		h.unregister(c)
		_ = c.conn.Close()
	}()
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetReadLimit(1024)
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
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
		case event, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			payload, err := json.Marshal(event)
			if err != nil || c.conn.WriteMessage(websocket.TextMessage, payload) != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}
