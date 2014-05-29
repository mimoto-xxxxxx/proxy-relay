package main

import (
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	ttemplate "text/template"
	"time"

	"code.google.com/p/go.net/proxy"
	"github.com/BurntSushi/toml"
	"github.com/howeyc/fsnotify"
)

// Config は設定情報を管理するための構造体。
type Config struct {
	UseProxy   string `toml:"use_proxy"`
	Reverse    []string
	ReverseMap []ReverseMap
	Proxies    map[string]*ProxyConfig
}

// ReverseMap は Config の Reverse に "40000->example.com:80" のようにテキストで保存された情報を
// プログラムから扱いやすい形に分解して保存するために使用する構造体。
type ReverseMap struct {
	Port      int
	ConnectTo string
}

// ProxyConfig はプロキシひとつひとつの設定情報を管理するための構造体。
type ProxyConfig struct {
	Host      string
	HTTPPort  int `toml:"http_port"`
	SOCKSPort int `toml:"socks_port"`
	Username  string
	Password  string
}

// Server はリレーサーバ。ひとつのポートを Listen してリバースプロキシか HTTP プロキシとして振る舞う。
type Server struct {
	Logger    *log.Logger
	listener  net.Listener
	connectTo string
	proxy     *ProxyConfig
	closed    bool
}

// Conn は Server が Accept した通信の続きを担い、リバースプロキシとして振る舞うために使用される。
type Conn struct {
	server *Server
	rwc    net.Conn
	Data   interface{}
}

// New は新しい Server を作成する。
// connectTo に "example.com:80" のような情報を渡すとリバースプロキシ、空文字列だと HTTP プロキシ。
// 実際に使用するプロキシ設定は ProxyConfig で指定する。
func New(connectTo string, proxy *ProxyConfig) *Server {
	return &Server{
		Logger:    log.New(os.Stderr, "", log.LstdFlags),
		connectTo: connectTo,
		proxy:     proxy,
	}
}

// ListenAndServe は addr で Listen して通信の待受状態に入る。
// Listen が成功したかどうかを errch を通じて返し、Serve の結果は Logger を経由して出力する。
func (srv *Server) ListenAndServe(addr string, errch chan<- error) {
	l, err := net.Listen("tcp", addr)
	errch <- err
	if err != nil {
		return
	}

	if srv.connectTo != "" {
		err = srv.ServeSOCKS(l)
	} else {
		err = srv.ServeHTTP(l)
	}
	if err != nil {
		if oe, ok := err.(*net.OpError); !ok || oe.Err.Error() != "use of closed network connection" {
			srv.Logger.Println("ListenAndServe:", err)
		}
	}
}

// ServeHTTP は HTTP プロキシとして l を処理する。
func (srv *Server) ServeHTTP(l net.Listener) error {
	defer l.Close()
	srv.listener = l

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
	sv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if srv.closed {
			// Listen を Close した後でも持続的接続によって生きているクライアントがあると
			// 処理が引き続き飛んでくる可能性があるため、接続を切りつつ同じ URL へリダイレクトを試みる
			w.Header().Set("Connection", "close")
			http.Redirect(w, r, r.URL.String(), http.StatusSeeOther)
			return
		}

		if r.Method == "CONNECT" {
			srv.ServeHTTPConnect(w, r)
			return
		}

		// プロキシなしのダイレクト接続か
		// http://proxy/ としてアクセスしてきた場合
		if !r.URL.IsAbs() || r.URL.Host == "proxy" {
			w.Header().Set("Connection", "close")
			switch r.URL.Path {
			case "/":
				srv.ServeStat(w, r)
			case "/reload":
				srv.Reload(w, r)
			case "/proxy.pac":
				srv.ProxyPac(w, r)
			default:
				http.NotFound(w, r)
			}
			return
		}
		rp.ServeHTTP(w, r)
	})}
	return sv.Serve(l)
}

// ServeStat はプロキシサーバの設定情報を Web ページとして返す。
func (srv *Server) ServeStat(w http.ResponseWriter, r *http.Request) {
	tpl, err := template.ParseFiles(*proxyHtml)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = tpl.Execute(w, map[string]interface{}{
		"Proxy":   srv.proxy,
		"Config":  config,
		"IPAddress": *address,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// Reload はプロキシサーバの設定情報を更新する。
func (srv *Server) Reload(w http.ResponseWriter, r *http.Request) {
	if err := reload(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// renderError は proxy.pac を返す際にエラーが起きて意図していない場所に繋がってしまわないよう嘘のプロキシを返す。
func renderError(err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	w.Write([]byte(`/* Proxy.pac loading error. */ function FindProxyForURL(url, host) { return 'PROXY 0.0.0.0:8888'; }`))
}

// ProxyPac はプロクシ自動設定ファイルを返答する。
func (srv *Server) ProxyPac(w http.ResponseWriter, r *http.Request) {
	tpl, err := ttemplate.ParseFiles(*proxyPac)
	if err != nil {
		log.Println("ProxyPac:", err)
		renderError(err, w)
		return
	}
	//	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	err = tpl.Execute(w, map[string]interface{}{
		"IPAddress": *address,
		"BasePort":  *proxyPort,
		"NumPorts":  *proxyPorts,
	})
	if err != nil {
		log.Println("ProxyPac:", err)
		renderError(err, w)
		return
	}
}

// HTTP の Connect メソッドの実装。Connect のリクエストだがコードを使いまわすため先は SOCKS プロキシで繋ぐ。
func (srv *Server) ServeHTTPConnect(w http.ResponseWriter, r *http.Request) {
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

// ServeSOCKS はリバースプロキシとして l を処理する。
func (srv *Server) ServeSOCKS(l net.Listener) error {
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
			srv.Logger.Printf("relay: Server.newConn: %v", err)
			continue
		}
		go c.serve()
	}
}

// Close は Listen を終了する。
func (srv *Server) Close() error {
	srv.closed = true
	return srv.listener.Close()
}

// newConn はサーバが Accept したクライアントに対応するインスタンスを用意する。
func (srv *Server) newConn(c net.Conn) (*Conn, error) {
	conn := &Conn{
		server: srv,
		rwc:    c,
	}
	return conn, nil
}

// serve はリバースプロキシとしてクライアントを処理する。
func (c *Conn) serve() {
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

// connectSOCKS は pc の設定を元に SOCKS プロキシを経由して host に接続し、通信が完了するまで待つ。
// 接続に成功する前にエラーが発生した場合は connected が false になる。
func connectSOCKS(c net.Conn, host string, pc *ProxyConfig, intro []byte) (connected bool, err error) {
	var auth *proxy.Auth
	if pc.Username != "" || pc.Password != "" {
		auth = &proxy.Auth{
			User:     pc.Username,
			Password: pc.Password,
		}
	}
	var d proxy.Dialer
	d, err = proxy.SOCKS5("tcp", pc.Host+":"+strconv.Itoa(pc.SOCKSPort), auth, &net.Dialer{})
	if err != nil {
		return
	}

	var conn net.Conn
	conn, err = d.Dial("tcp", host)
	if err != nil {
		return
	}

	if intro != nil {
		if _, err = c.Write(intro); err != nil {
			return
		}
	}

	connected = true

	var wg sync.WaitGroup
	var e1, e2 error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, e1 = io.Copy(c, conn)
	}()
	go func() {
		defer wg.Done()
		_, e2 = io.Copy(conn, c)
	}()
	wg.Wait()

	if e1 != nil && e2 != nil {
		err = fmt.Errorf("two errors:\n%v\n%v\n", e1, e2)
		return
	}
	if e1 != nil {
		err = e1
		return
	}
	if e2 != nil {
		err = e2
		return
	}
	return
}

// close は接続を閉じる。
func (c *Conn) close() {
	if c.rwc != nil {
		c.rwc.Close()
		c.rwc = nil
	}
}

// reload は設定情報を再読み込みする。
func reload() error {
	var cfg Config
	if _, err := toml.DecodeFile(*configFilename, &cfg); err != nil {
		return err
	}

	pxy, ok := cfg.Proxies[cfg.UseProxy]
	if !ok {
		return fmt.Errorf("proxy setting not found: %s", cfg.UseProxy)
	}

	for _, mapping := range cfg.Reverse {
		kv := strings.SplitN(mapping, "->", 2)
		if len(kv) != 2 {
			return fmt.Errorf("could not parse mapping setting: %s", mapping)
		}

		p, err := strconv.Atoi(kv[0])
		if err != nil {
			return fmt.Errorf("invalid portnumber: %v", err)
		}
		cfg.ReverseMap = append(cfg.ReverseMap, ReverseMap{Port: p, ConnectTo: kv[1]})
	}

	for _, srv := range servers {
		if err := srv.Close(); err != nil {
			return fmt.Errorf("could not close server: %v", err)
		}
	}

	// wait
	runtime.Gosched()

	var srvs []*Server

	//HTTP プロキシの構築
	listenErr := make(chan error)
	for i := *proxyPort; i < *proxyPort+*proxyPorts; i++ {
		srv := New("", pxy)
		go srv.ListenAndServe(":"+strconv.Itoa(i), listenErr)
		if err := <-listenErr; err != nil {
			return fmt.Errorf("could not listen: %v", err)
		}
		srvs = append(srvs, srv)
	}

	// SOCKS リバースプロキシの構築
	for _, m := range cfg.ReverseMap {
		srv := New(m.ConnectTo, pxy)
		go srv.ListenAndServe(":"+strconv.Itoa(m.Port), listenErr)
		if err := <-listenErr; err != nil {
			return fmt.Errorf("could not listen: %v", err)
		}
		srvs = append(srvs, srv)
	}

	config = &cfg
	servers = srvs

	if *verbose {
		log.Println("configuration reloaded.")
	}
	return nil
}

// watch は設定ファイルの変更を監視し検出したら設定を再読み込みする。
func watch() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	// 何か変更があった時は設定の再作成を1秒後にスケジューリングする。
	// 既にスケジューリングされている場合は t が入れ替わるため、結局一番最後のもののみが使用される。
	var t <-chan time.Time

	done := make(chan error)
	go func() {
		for {
			select {
			case ev := <-w.Event:
				if *verbose {
					log.Println("receive fsnotify event:", ev.Name)
				}
				t = time.After(time.Second)
			case err := <-w.Error:
				done <- err
				return
			case <-t:
				if err := reload(); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	if err = w.Watch(path.Dir(*configFilename)); err != nil {
		return err
	}
	defer w.Close()
	return <-done
}

var (
	proxyPac       = flag.String("pac", "proxy.pac", "proxy.pac template file")
	proxyHtml      = flag.String("html", "proxy.html", "proxy.html template file")
	configFilename = flag.String("c", "config.toml", "configuration filename")
	proxyPort      = flag.Int("proxy_port", 40000, "proxy port number")
	proxyPorts     = flag.Int("ports", 4, "listening ports")
	address        = flag.String("addr", "localhost", "myself address")
	verbose        = flag.Bool("v", false, "verbose output")
)

var (
	servers []*Server
	config  *Config
)

func main() {
	flag.Parse()
	if err := reload(); err != nil {
		log.Fatalln("cannot open configuration file:", err)
	}
	if err := watch(); err != nil {
		log.Fatalln(err)
	}
}
