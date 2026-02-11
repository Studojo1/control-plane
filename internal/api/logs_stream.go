package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/studojo/control-plane/internal/k8s"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for now - in production, validate against allowed origins
		return true
	},
}

// HandleStreamLogs streams logs from Kubernetes pods via WebSocket
func (h *DevHandler) HandleStreamLogs(w http.ResponseWriter, r *http.Request) {
	if h.K8sClient == nil {
		http.Error(w, "K8s client not available", http.StatusServiceUnavailable)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("failed to upgrade to websocket", "error", err)
		return
	}
	defer conn.Close()

	service := r.URL.Query().Get("service")
	pod := r.URL.Query().Get("pod")
	follow := r.URL.Query().Get("follow") == "true"
	tailLines := r.URL.Query().Get("tail")
	if tailLines == "" {
		tailLines = "100"
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Stream logs
	go func() {
		defer cancel()
		for {
			// Read message from client (for closing connection)
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Warn("websocket read error", "error", err)
				}
				return
			}
		}
	}()

	// Get pod logs
	logStream, err := h.K8sClient.StreamLogs(ctx, service, pod, tailLines, follow)
	if err != nil {
		slog.Error("failed to stream logs", "error", err, "service", service, "pod", pod)
		conn.WriteJSON(map[string]string{"error": fmt.Sprintf("Failed to stream logs: %v", err)})
		return
	}
	defer logStream.Close()

	// Stream logs to WebSocket
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := logStream.Read(buf)
			if err != nil {
				if err == io.EOF {
					if !follow {
						return
					}
					// Continue reading if following
					time.Sleep(100 * time.Millisecond)
					continue
				}
				slog.Warn("log stream read error", "error", err)
				return
			}

			if n > 0 {
				if err := conn.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
					slog.Warn("websocket write error", "error", err)
					return
				}
			}
		}
	}
}

