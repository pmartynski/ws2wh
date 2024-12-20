// Package server provides the HTTP and WebSocket server implementation for ws2wh
package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"github.com/pmartynski/ws2wh/backend"
	"github.com/pmartynski/ws2wh/frontend"
	"github.com/pmartynski/ws2wh/session"
)

// Server handles WebSocket connections and forwards messages to a configured backend
type Server struct {
	DefaultBackend backend.Backend
	frontendAddr   string
	backendUrl     string
	sessions       map[string]*session.Session
	echoStack      *echo.Echo
}

// CreateServer initializes a new Server instance with the given configuration
//
// Parameters:
//   - frontendAddr: The address and port to listen on (e.g. ":3000")
//   - websocketPath: The path where WebSocket connections will be upgraded (e.g. "/ws")
//   - backendUrl: The URL where backend messages will be sent
//   - replyPathPrefix: The prefix for reply endpoints (e.g. "/reply")
//
// Returns a configured Server instance ready to be started
func CreateServer(
	frontendAddr string,
	websocketPath string,
	backendUrl string,
	replyPathPrefix string) *Server {

	s := Server{
		frontendAddr: frontendAddr,
		backendUrl:   backendUrl,
		sessions:     make(map[string]*session.Session, 100),
	}
	s.DefaultBackend = backend.CreateBackend(backendUrl)

	es := echo.New()
	es.Use(middleware.Logger())
	es.Logger.SetLevel(log.DEBUG)
	es.HideBanner = true

	// should we recover from panic?
	es.Use(middleware.Recover())

	replyPath := fmt.Sprintf("%s/:id", strings.TrimRight(replyPathPrefix, "/"))
	es.GET(websocketPath, s.handle)
	es.POST(replyPath, s.send)

	s.echoStack = es
	fmt.Printf("⇨ backend action: POST %s\n", backendUrl)
	fmt.Printf("⇨ websocket upgrade path: %s\n", websocketPath)

	return &s
}

// Start begins listening for connections on the configured address
func (s *Server) Start() {
	log.SetLevel(log.DEBUG)
	e := s.echoStack
	e.Logger.Info(e.Start(s.frontendAddr))
}

// Stop gracefully shuts down the server
func (s *Server) Stop() {
	s.echoStack.Shutdown(context.Background())
}

func (s *Server) handle(c echo.Context) error {
	id := uuid.NewString()
	handler := frontend.NewWsHandler()

	s.sessions[id] = session.NewSession(session.SessionParams{
		Id:           id,
		Backend:      s.DefaultBackend,
		ReplyChannel: fmt.Sprintf("%s://%s/reply/%s", c.Scheme(), c.Request().Host, id),
		Connection:   handler,
	})

	defer delete(s.sessions, id)

	go s.sessions[id].Receive()
	handler.Handle(c.Response().Writer, c.Request(), c.Response().Header())
	return nil
}

func (s *Server) send(c echo.Context) error {
	id := c.Param("id")
	var body []byte
	body, _ = io.ReadAll(c.Request().Body)
	session := s.sessions[id]

	if session == nil {
		c.JSON(http.StatusNotFound, SessionResponse{Success: false, Message: "NOT_FOUND"})
	}

	if len(body) > 0 {
		session.Send(body)
	}

	if c.Request().Header.Get(backend.CommandHeader) == backend.TerminateSessionCommand {
		err := session.Close()

		if err != nil {
			c.Logger().Error(err)
		}
	}

	return c.JSON(http.StatusOK, SessionResponse{Success: true})
}

// SessionResponse represents the JSON response format for session-related operations
type SessionResponse struct {
	Success bool        `json:"success"`
	Message interface{} `json:"message,omitempty"`
}
