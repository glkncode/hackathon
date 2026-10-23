package httptransport

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

func NewServer(address string, handler http.Handler, log *zap.Logger) *http.Server {
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		ErrorLog: zap.NewStdLog(log.Named("http"))}
}

// Serve does not own the pool/logger. It finishes shutdown before returning so
// main can safely release resources still used by in-flight handlers.
func Serve(ctx context.Context, server *http.Server, log *zap.Logger, certFile, keyFile string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if (certFile == "") != (keyFile == "") {
		return errors.New("HTTP: TLS certificate and key must be specified together")
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		if certFile != "" {
			result <- server.ServeTLS(listener, certFile, keyFile)
		} else {
			result <- server.Serve(listener)
		}
	}()
	log.Info("HTTP server starting", zap.String("address", listener.Addr().String()), zap.Bool("tls", certFile != ""))
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		serveErr := <-result
		if !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		log.Info("HTTP server stopped")
		return err
	}
}
