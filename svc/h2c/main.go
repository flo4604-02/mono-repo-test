package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"
	pingv1 "github.com/unkeyed/mono-repo-test/gen/ping/v1"
	"github.com/unkeyed/mono-repo-test/gen/ping/v1/pingv1connect"
	"github.com/unkeyed/mono-repo-test/pkg/shared"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3457"
	}

	var ready atomic.Bool
	go func() {
		time.Sleep(2 * time.Second)
		ready.Store(true)
		log.Println("h2c: ready to serve traffic")
	}()

	var inflight atomic.Int64

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("h2c: received %s — shutting down", s)
		deadline := time.After(10 * time.Second)
		for inflight.Load() > 0 {
			select {
			case <-deadline:
				log.Printf("h2c: shutdown deadline reached with %d in-flight", inflight.Load())
				os.Exit(1)
			default:
				time.Sleep(100 * time.Millisecond)
			}
		}
		log.Println("h2c: clean shutdown")
		os.Exit(0)
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Add(-1)

		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "h2c",
			Status:  "ok",
			Port:    port,
			Message: fmt.Sprintf("proto=%s | in-flight: %d", r.Proto, inflight.Load()),
		})
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			shared.JSON(w, http.StatusServiceUnavailable, shared.Response{
				Service: "h2c",
				Status:  "not_ready",
				Port:    port,
				Message: "still starting up",
			})
			return
		}
		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "h2c",
			Status:  "healthy",
			Port:    port,
			Message: fmt.Sprintf("proto=%s", r.Proto),
		})
	})

	mux.HandleFunc("POST /healthz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			shared.JSON(w, http.StatusServiceUnavailable, shared.Response{
				Service: "h2c",
				Status:  "not_ready",
				Port:    port,
			})
			return
		}
		shared.JSON(w, http.StatusOK, shared.Response{
			Service: "h2c",
			Status:  "healthy",
			Port:    port,
		})
	})

	// Connect-RPC service — registered on a separate mux to avoid pattern conflicts
	connectPath, connectHandler := pingv1connect.NewPingServiceHandler(&pingServer{port: port})

	// Route Connect-RPC paths to the connect handler, everything else to the main mux
	root := http.NewServeMux()
	root.Handle(connectPath, connectHandler)
	root.Handle("/", mux)

	h2cHandler := h2c.NewHandler(root, &http2.Server{})

	log.Printf("h2c: listening on :%s (HTTP/1.1 + h2c + Connect-RPC)", port)
	if err := http.ListenAndServe(":"+port, h2cHandler); err != nil {
		log.Fatal(err)
	}
}

type pingServer struct {
	pingv1connect.UnimplementedPingServiceHandler
	port string
}

func (s *pingServer) Ping(ctx context.Context, req *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	log.Printf("h2c: Connect-RPC Ping called with message=%q", req.Msg.Message)
	return connect.NewResponse(&pingv1.PingResponse{
		Message:  fmt.Sprintf("pong: %s", req.Msg.Message),
		Protocol: "connect-rpc over h2c",
	}), nil
}

func (s *pingServer) Count(ctx context.Context, req *connect.Request[pingv1.CountRequest], stream *connect.ServerStream[pingv1.CountResponse]) error {
	count := req.Msg.Count
	if count <= 0 {
		count = 5
	}
	log.Printf("h2c: Connect-RPC Count called with count=%d", count)

	for i := int32(1); i <= count; i++ {
		if err := stream.Send(&pingv1.CountResponse{
			Number:   i,
			Protocol: "connect-rpc server-stream over h2c",
		}); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
	return nil
}
