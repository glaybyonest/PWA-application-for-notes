package realtime

import (
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 16 * 1024
)

type TaskHandler func(TaskPayload)

// WebSocketHandler abstracts the websocket endpoint for the HTTP server layer.
type WebSocketHandler interface {
	ServeWS(http.ResponseWriter, *http.Request)
}

// Hub manages all active websocket clients and broadcasts task events.
type Hub struct {
	logger     *slog.Logger
	onTask     TaskHandler
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan Envelope
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	upgrader   websocket.Upgrader
}

// Client represents a single websocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan Envelope
}

// NewHub creates and starts a websocket hub.
func NewHub(logger *slog.Logger, onTask TaskHandler) *Hub {
	if logger == nil {
		logger = slog.Default()
	}

	hub := &Hub{
		logger:     logger,
		onTask:     onTask,
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Envelope, 32),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     sameOriginOnly,
		},
	}

	go hub.run()
	return hub
}

func sameOriginOnly(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return parsed.Host == r.Host
}

func (h *Hub) run() {
	defer close(h.done)

	for {
		select {
		case client := <-h.register:
			h.clients[client] = struct{}{}
			h.logger.Info("websocket client connected", "clients", len(h.clients))
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.logger.Info("websocket client disconnected", "clients", len(h.clients))
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					delete(h.clients, client)
					close(client.send)
				}
			}
		case <-h.stop:
			for client := range h.clients {
				close(client.send)
				if client.conn != nil {
					_ = client.conn.Close()
				}
				delete(h.clients, client)
			}
			return
		}
	}
}

// Broadcast emits an envelope to all active websocket clients.
func (h *Hub) Broadcast(message Envelope) {
	select {
	case h.broadcast <- message:
	case <-h.done:
	}
}

// ServeWS upgrades an HTTP request and attaches it to the hub.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade websocket", "error", err)
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan Envelope, 16),
	}

	h.registerClient(client)
	go client.writePump()
	go client.readPump()
}

func (h *Hub) registerClient(client *Client) {
	select {
	case h.register <- client:
	case <-h.done:
	}
}

func (h *Hub) unregisterClient(client *Client) {
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

// Shutdown closes all websocket clients and stops the hub loop.
func (h *Hub) Shutdown() {
	h.stopOnce.Do(func() {
		close(h.stop)
		<-h.done
	})
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregisterClient(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		var message Envelope
		if err := c.conn.ReadJSON(&message); err != nil {
			c.hub.logger.Debug("websocket read finished", "error", err)
			break
		}

		if message.Type != "newTask" {
			c.hub.logger.Warn("unknown websocket event type", "type", message.Type)
			continue
		}

		if !message.Payload.Valid() {
			c.hub.logger.Warn("ignored invalid task payload")
			continue
		}

		if c.hub.onTask != nil {
			c.hub.onTask(message.Payload)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteJSON(message); err != nil {
				c.hub.logger.Debug("websocket write failed", "error", err)
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
