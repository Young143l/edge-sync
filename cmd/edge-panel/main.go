// edge-panel：面板 HTTP server（Go 版，替代 TS server）。
// 状态类请求转发内核 IPC；下载流直读磁盘（不过内核）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const version = "0.1.0"

func main() {
	configPath := flag.String("c", "etc/panel.json", "path to panel.json")
	flag.Parse()

	cfg, err := loadOrCreatePanelConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.Socket == "" {
		log.Fatalf(`panel.json missing "socket" (kernel unix socket path)`)
	}
	if cfg.DataDir == "" {
		log.Fatalf(`panel.json missing "dataDir"`)
	}

	srv := newPanelServer(cfg)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port),
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Printf("[panel] shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		close(done)
	}()

	log.Printf("[panel] serving on http://%s:%d (socket: %s)", cfg.Bind, cfg.Port, cfg.Socket)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
	<-done
	log.Printf("[panel] bye")
}
