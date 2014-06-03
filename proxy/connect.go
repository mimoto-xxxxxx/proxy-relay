// package Proxy は HTTP フォワードプロキシリレーサーバと SOCKS v5 プロトコルによるリバースプロキシサーバの実装。
package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/mimoto-xxxxxx/proxy-relay/config"

	"code.google.com/p/go.net/proxy"
)

// connectSOCKS は pc の設定を元に SOCKS プロキシを経由して host に接続し、通信が完了するまで待つ。
// 接続に成功する前にエラーが発生した場合は connected が false になる。
func connectSOCKS(c net.Conn, host string, pc *config.Proxy, intro []byte) (connected bool, err error) {
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
