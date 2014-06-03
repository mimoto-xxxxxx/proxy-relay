package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"time"

	"github.com/mimoto-xxxxxx/proxy-relay/config"
	"github.com/mimoto-xxxxxx/proxy-relay/proxy"

	"github.com/howeyc/fsnotify"
)

type relay struct {
	running     []io.Closer
	cfg         *config.Config
	toml        string
	port        int
	numPorts    int
	address     string
	bindAddress string
	verbose     bool
}

func (rl *relay) Close() error {
	if rl.running == nil {
		return nil
	}
	for _, srv := range rl.running {
		srv.Close()
	}
	return nil
}

// reload は設定情報を再読み込みする。
func (rl *relay) reload() error {
	var err error
	rl.cfg, err = config.New(rl.toml)
	if err != nil {
		return err
	}

	if err = rl.Close(); err != nil {
		return err
	}

	var srvs []io.Closer

	//HTTP プロキシの構築
	listenErr := make(chan error)
	for i := rl.port; i < rl.port+rl.numPorts; i++ {

		mux := http.NewServeMux()
		mux.HandleFunc("/", rl.serveStat)
		mux.HandleFunc("/reload", rl.serveReload)
		mux.HandleFunc("/proxy.pac", rl.serveProxyPac)

		srv := proxy.NewHTTP(rl.cfg.Proxy)
		srv.Handler = mux
		go srv.ListenAndServe(fmt.Sprintf("%s:%d", rl.bindAddress, i), listenErr)
		if err := <-listenErr; err != nil {
			return fmt.Errorf("could not listen: %v", err)
		}
		srvs = append(srvs, srv)
	}

	// SOCKS リバースプロキシの構築
	for port, connectTo := range rl.cfg.ReverseMap {
		srv := proxy.NewSOCKS(connectTo, rl.cfg.Proxy)
		go srv.ListenAndServe(fmt.Sprintf("%s:%d", rl.bindAddress, port), listenErr)
		if err := <-listenErr; err != nil {
			return fmt.Errorf("could not listen: %v", err)
		}
		srvs = append(srvs, srv)
	}

	rl.running = srvs

	if rl.verbose {
		log.Println("configuration reloaded.")
	}
	return nil
}

// watch は設定ファイルの変更を監視し検出したら rl.reload する。
// 短い周期でたくさん変更された場合は1回にまとめる。
func (rl *relay) watch() error {
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
				if rl.verbose {
					log.Println("receive fsnotify event:", ev.Name)
				}
				t = time.After(time.Second)
			case err := <-w.Error:
				done <- err
				return
			case <-t:
			if err := rl.reload(); err != nil {
				log.Println(err)
			}
			}
		}
	}()
	if err = w.Watch(path.Dir(rl.toml)); err != nil {
		return err
	}
	defer w.Close()
	return <-done
}

func main() {
	rl := &relay{}

	flag.StringVar(&rl.toml, "c", "config.toml", "configuration filename")
	flag.IntVar(&rl.port, "proxy_port", 40000, "proxy port number")
	flag.IntVar(&rl.numPorts, "ports", 4, "listening ports")
	flag.StringVar(&rl.address, "addr", "localhost", "myself address")
	flag.StringVar(&rl.bindAddress, "bind", "", "server bind address")
	flag.BoolVar(&rl.verbose, "v", false, "verbose output")
	flag.Parse()

	if err := rl.reload(); err != nil {
		log.Fatalln("cannot open configuration file:", err)
	}

	if err := rl.watch(); err != nil {
		log.Fatalln(err)
	}
}
