// Package websocketapi provides WebSocket API for real-time message broadcasting.
package websocketapi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512

	// Maximum number of clients
	maxClients = 1000

	// Send channel buffer size
	sendBufferSize = 256
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   1024,
	WriteBufferSize:  1024,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: Implement proper origin check for production
		// For now, allow same origin only in production
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Allow requests without Origin header (non-browser clients)
		}
		// You should validate against your allowed origins
		return true
	},
}

// Hub maintains the set of active clients and broadcasts messages to the clients.
type Hub struct {
	// Registered clients.
	clients map[string]*Client

	// Inbound messages from the clients (not used currently, but ready for future).
	broadcast chan []byte

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	// Mutex for clients map
	mu sync.RWMutex

	// Logger
	logger logger.Writer

	// Context for shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan interface{}

	// Client ID
	id string

	// Context for client lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHub creates a new Hub.
func NewHub(parent logger.Writer) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[string]*Client),
		logger:     parent,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Run starts the hub's main loop.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if len(h.clients) >= maxClients {
				h.mu.Unlock()
				h.Log(logger.Warn, "max clients reached, rejecting new connection")
				client.conn.Close()
				continue
			}
			h.clients[client.id] = client
			h.mu.Unlock()
			h.Log(logger.Info, "websocket client connected: %s (total: %d)", client.id, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client.id]; ok {
				delete(h.clients, client.id)
				close(client.send)
				h.Log(logger.Info, "websocket client disconnected: %s (total: %d)", client.id, len(h.clients))
			}
			h.mu.Unlock()

		case <-h.ctx.Done():
			h.Log(logger.Info, "websocket hub shutting down")
			return
		}
	}
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(message interface{}) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, client := range h.clients {
		select {
		case client.send <- message:
			// Message sent successfully
		default:
			// Send channel is full, client is slow or stuck
			h.Log(logger.Warn, "client %s send buffer full, skipping message", id)
		}
	}
}

// Close shuts down the hub.
func (h *Hub) Close() {
	h.cancel()

	h.mu.Lock()
	defer h.mu.Unlock()

	// Close all client connections
	for _, client := range h.clients {
		client.cancel()
		client.conn.Close()
	}
	h.clients = make(map[string]*Client)
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Log implements logger.Writer.
func (h *Hub) Log(level logger.Level, format string, args ...interface{}) {
	h.logger.Log(level, "[websocket] "+format, args...)
}

// readPump pumps messages from the websocket connection to the hub.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			// Read messages from client (currently we just discard them)
			// In the future, you can process client messages here
			_, _, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					c.hub.Log(logger.Warn, "websocket error for client %s: %v", c.id, err)
				}
				return
			}
		}
	}
}

// writePump pumps messages from the hub to the websocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteJSON(message); err != nil {
				c.hub.Log(logger.Warn, "write error for client %s: %v", c.id, err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}

// ServeWS handles websocket requests from the peer.
func ServeWS(hub *Hub, c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		hub.Log(logger.Error, "websocket upgrade failed: %v", err)
		return
	}

	ctx, cancel := context.WithCancel(hub.ctx)
	client := &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan interface{}, sendBufferSize),
		id:     uuid.New().String(),
		ctx:    ctx,
		cancel: cancel,
	}

	hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
