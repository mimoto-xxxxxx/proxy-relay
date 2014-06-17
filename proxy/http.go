package proxy

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/mimoto-xxxxxx/proxy-relay/config"
)

// HTTP はひとつのポートを Listen して HTTP プロキシとして振る舞う。
type HTTP struct {
	Logger   *log.Logger
	Handler  http.Handler // 任意の Web アクセス用
	listener net.Listener
	server   *http.Server
	sig      chan struct{}
	proxy    *config.Proxy
}

// New は新しい HTTP プロキシサーバを作成する。
// 実際に使用するプロキシ設定は proxy で指定する。
func NewHTTP(proxy *config.Proxy) *HTTP {
	return &HTTP{
		Logger: log.New(os.Stderr, "", log.LstdFlags),
		proxy:  proxy,
	}
}

// Close は Listen していたポートを開放して処理を返し、goroutine 経由でクライアント接続も閉じていく。
func (srv *HTTP) Close() error {
	err := srv.listener.Close()
	// 維持しているコネクションも閉じる
	srv.sig <- struct{}{}
	<-srv.sig
	return err
}

// ListenAndServe は addr で Listen して通信の待受状態に入る。
// Listen が成功したかどうかを errch を通じて返し、Serve の結果は Logger を経由して出力する。
func (srv *HTTP) ListenAndServe(addr string, errch chan<- error) {
	l, err := net.Listen("tcp", addr)
	errch <- err
	if err != nil {
		return
	}

	if err = srv.serveHTTP(l); err != nil {
		if oe, ok := err.(*net.OpError); !ok || oe.Err.Error() != "use of closed network connection" {
			srv.Logger.Println("ListenAndServe:", err)
		}
	}
}

// ServeHTTP は HTTP プロキシとして l を処理する。
func (srv *HTTP) serveHTTP(l net.Listener) error {
	defer l.Close()
	srv.listener = l
	srv.sig = make(chan struct{})

	tp := &http.Transport{Proxy: http.ProxyURL(&url.URL{
		Host: srv.proxy.Host + ":" + strconv.Itoa(srv.proxy.HTTPPort),
		User: url.UserPassword(srv.proxy.Username, srv.proxy.Password),
	})}
	rp := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.Header.Add("X-Real-IP", r.RemoteAddr)
		},
		Transport: tp,
	}
	srv.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "CONNECT" {
			srv.serveHTTPConnect(w, r)
			return
		}

		// プロキシなしのダイレクト接続か
		// http://proxy/ としてアクセスしてきた場合
		if (!r.URL.IsAbs() || r.URL.Host == "proxy") && srv.Handler != nil {
			srv.Handler.ServeHTTP(w, r)
			return
		}

		rp.ServeHTTP(w, r)
	})}
	return run(srv.server, l, 1*time.Second, srv.sig)
}

// Based on github.com/stretchr/graceful Copyright (c) 2014 Stretchr, Inc.
func run(server *http.Server, listener net.Listener, timeout time.Duration, c chan struct{}) error {
	// Track connection state
	add := make(chan net.Conn)
	remove := make(chan net.Conn)
	server.ConnState = func(conn net.Conn, state http.ConnState) {
		switch state {
		case http.StateActive:
			add <- conn
		case http.StateClosed, http.StateIdle:
			remove <- conn
		}
	}

	// Manage open connections
	stop := make(chan chan struct{})
	kill := make(chan struct{})
	go func() {
		var done chan struct{}
		connections := map[net.Conn]struct{}{}
		for {
			select {
			case conn := <-add:
				connections[conn] = struct{}{}
			case conn := <-remove:
				delete(connections, conn)
				if done != nil && len(connections) == 0 {
					done <- struct{}{}
					return
				}
			case done = <-stop:
				if len(connections) == 0 {
					done <- struct{}{}
					return
				}
			case <-kill:
				for k := range connections {
					k.Close()
				}
				return
			}
		}
	}()

	ended := make(chan struct{})
	go func() {
		<-c
		server.SetKeepAlivesEnabled(false)
		listener.Close()
		ended <- struct{}{}
	}()

	// Serve with graceful listener
	err := server.Serve(listener)

	// Request done notification
	done := make(chan struct{})
	stop <- done

	if timeout > 0 {
		select {
		case <-done:
		case <-time.After(timeout):
			kill <- struct{}{}
		}
	} else {
		<-done
	}
	c <- <-ended
	return err
}

// HTTP の Connect メソッドの実装。Connect のリクエストだがコードを使いまわすため先は SOCKS プロキシで繋ぐ。
func (srv *HTTP) serveHTTPConnect(w http.ResponseWriter, r *http.Request) {
	hij, ok := w.(http.Hijacker)
	if !ok {
		panic("does not support hijacking!")
	}
	c, _, err := hij.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		srv.Logger.Println("could not hijack:", err)
		return
	}

	defer func() {
		if err := recover(); err != nil {
			const size = 4096
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			srv.Logger.Printf("panic serving %v: %v\n%s", c.RemoteAddr(), err, buf)
		}
		c.Close()
	}()

	connected, err := connectSOCKS(c, r.URL.Host, srv.proxy, []byte("HTTP/1.0 200 OK\r\n\r\n"))
	if err != nil {
		if !connected {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		srv.Logger.Println("connectSOCKS:", err)
	}
}
