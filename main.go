/*
proxy-relay はプロキシへの接続を中継するプログラムで、ユーザー名やパスワードの入力を肩代わりします。

Introduction

このプログラムは別の場所にあるプロキシサーバーへの接続を補助するために使用します。

	[ブラウザ] -> [プロキシサーバー] -> [HTTPサーバ]

本来このような経路を辿るところを

	[ブラウザ] -> [proxy-relay] -> [プロキシサーバー] -> [HTTPサーバ]

上記のように介入することで、本来プロキシーサーバーに必要なユーザー名やパスワードの入力を省略したり、リバースプロキシとして動作し特定のポート番号への接続を特定のプロキシサーバへのリクエストに変形したりできます。

Usage

起動例は以下の通り。

	proxy-relay [flag]

flag には以下のようなものを指定できます。

	-c="config.toml"
		設定ファイルの場所を指定します。設定ファイルの記述に関しては後述します。
	-proxy_port=40000
		proxy-relay がプロキシーサーバとして待ち受けるポート番号です。
		-ports オプションとセットで動作します。
	-ports=4
		proxy-relay がいくつポートを確保するのかを指定します。
		例えば -port_port=40000 -ports=4 の時は 40000, 40001, 40002, 40003 で待ち受けます。
	-addr="localhost"
		proxy.pac に記載する際に必要になる、外部から見た時の proxy-relay が動作するアドレスを指定します。
		別のマシンからのアクセスを許容する場合には -addr="192.168.1.12" などに変更する必要があります。
	-bind="localhost"
		待ち受ける際にバインドするアドレスを指定します。
		別のマシンからのアクセスを許容する場合には -bind="" などに変更する必要があります。
	-v
		詳細なログを出力します。

Configuration

設定ファイル config.toml には、TOML ファイルの書式で動作に関わる様々な設定を記述できます。

proxy-relay の実行中にこのファイルが編集された場合などには約1秒後に自動的に設定が再読み込みされます。

	# 使用するプロキシの設定名です。
	# [proxies.xxxxxxx] の中から使用する設定をひとつ選びます。
	use_proxy = "example"

	# リバースプロキシの設定を行います。
	# 41000番ポートへの接続を www.example.com の22番ポートにしたい場合は "41000->www.example.com:22" のように記述します。
	# 例えば上記設定をした上で (-addr で指定したホスト):41000 に接続すると、
	# proxy-relay が use_proxy で指定したプロキシ設定の host:socks_port に SOCKSv5 で接続し、
	# そこで www.example.com:22 への接続を要求します。
	reverse = [
	  "41000->www.example.com:22",
	  "41001->images.example.com:22",
	]

	# プロキシ除外設定
	# 以下に記述されたドメインに対してはプロキシを経由せずに接続します。
	# この設定は proxy-relay が返すプロキシ自動構成スクリプト上で分岐するように出力されます。
	direct_hosts = [
	  "ajax.googleapis.com",
	  "ajax.aspnetcdn.com",
	  "netdna.bootstrapcdn.com",
	  "cdnjs.cloudflare.com",
	  "api.bootswatch.com",
	]

	# 接続先になるプロキシは以下のように設定します。
	# 現在の実装ではリバースプロキシと HTTP Connect メソッドの使用時に SOCKSv5 が使用されています。
	# それらを使用しない場合は socks_port を設定しなくても構いません。

	[proxies.example]
	host = "127.0.0.1"
	http_port = 1080
	socks_port = 1081
	username = "your-user-name"
	password = "hack-me"

*/
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
	flag.StringVar(&rl.bindAddress, "bind", "localhost", "server bind address")
	flag.BoolVar(&rl.verbose, "v", false, "verbose output")
	flag.Parse()

	if err := rl.reload(); err != nil {
		log.Fatalln("cannot open configuration file:", err)
	}

	if err := rl.watch(); err != nil {
		log.Fatalln(err)
	}
}
