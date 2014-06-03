package proxy

import (
	"log"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/mimoto-xxxxxx/proxy-relay/config"
)

// SOCKS は SOCKS v5 プロトコルを利用したリバースプロキシサーバ。
type SOCKS struct {
	Logger    *log.Logger
	listener  net.Listener
	connectTo string
	proxy     *config.Proxy
	closed    bool
}

// conn は SOCKS が Accept した通信の続きを担い、リバースプロキシとして振る舞うために使用される。
type conn struct {
	server *SOCKS
	rwc    net.Conn
	Data   interface{}
}

// New は新しい SOCKS を作成する。connectTo には "example.com:80" のような情報を渡す。
// 実際に使用するプロキシ設定は proxy で指定する。
func NewSOCKS(connectTo string, proxy *config.Proxy) *SOCKS {
	return &SOCKS{
		Logger:    log.New(os.Stderr, "", log.LstdFlags),
		connectTo: connectTo,
		proxy:     proxy,
	}
}

// ListenAndServe は addr で Listen して通信の待受状態に入る。
// Listen が成功したかどうかを errch を通じて返し、Serve の結果は Logger を経由して出力する。
func (srv *SOCKS) ListenAndServe(addr string, errch chan<- error) {
	l, err := net.Listen("tcp", addr)
	errch <- err
	if err != nil {
		return
	}

	if err = srv.serveSOCKS(l); err != nil {
		if oe, ok := err.(*net.OpError); !ok || oe.Err.Error() != "use of closed network connection" {
			srv.Logger.Println("ListenAndServe:", err)
		}
	}
}

// ServeSOCKS はリバースプロキシとして l を処理する。
func (srv *SOCKS) serveSOCKS(l net.Listener) error {
	defer l.Close()
	srv.listener = l
	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		rw, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				srv.Logger.Printf("relay: Accept error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		c, err := srv.newConn(rw)
		if err != nil {
			srv.Logger.Printf("relay: SOCKS.newConn: %v", err)
			continue
		}
		go c.serve()
	}
}

// Close は Listen を終了する。
func (srv *SOCKS) Close() error {
	srv.closed = true
	return srv.listener.Close()
}

// newConn はサーバが Accept したクライアントに対応するインスタンスを用意する。
func (srv *SOCKS) newConn(c net.Conn) (*conn, error) {
	conn := &conn{
		server: srv,
		rwc:    c,
	}
	return conn, nil
}

// serve はリバースプロキシとしてクライアントを処理する。
func (c *conn) serve() {
	defer func() {
		if err := recover(); err != nil {
			const size = 4096
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			c.server.Logger.Printf("panic serving %v: %v\n%s", c.rwc.RemoteAddr(), err, buf)
		}
		c.close()
	}()

	if _, err := connectSOCKS(c.rwc, c.server.connectTo, c.server.proxy, nil); err != nil {
		c.server.Logger.Println(err)
		return
	}
}

// close は接続を閉じる。
func (c *conn) close() {
	if c.rwc != nil {
		c.rwc.Close()
		c.rwc = nil
	}
}
