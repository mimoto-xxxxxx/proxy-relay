package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"text/template"
)

const proxyPacTemplate = `
var direct_hosts = {{.DirectHosts}};
var proxies = {{.Proxies}};

function FindProxyForURL(url, host) {
  if (host in direct_hosts) {
    return "DIRECT";
  }

  shuffle(proxies);
  return "PROXY "+proxies.join(";");
}

function shuffle(a) {
    var i = a.length;
    while(i){
        var j = Math.floor(Math.random()*i);
        var t = a[--i];
        a[i] = a[j];
        a[j] = t;
    }
}
`

// renderError は proxy.pac を返す際にエラーが起きて意図していない場所に繋がってしまわないよう嘘のプロキシを返す。
func renderErrorPac(w http.ResponseWriter) {
	w.Write([]byte(`/* Proxy.pac loading error. */ function FindProxyForURL(url, host) { return 'PROXY 0.0.0.0:8888'; }`))
}

// marshalJSONString は v を JSON に Marshal して文字列として返す。
func marshalJSONString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// proxyPac はプロクシ自動設定ファイルを返答する。
func (rl *relay) serveProxyPac(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")

	tpl, err := template.New("").Parse(proxyPacTemplate)
	if err != nil {
		log.Println("ProxyPac:", err)
		renderErrorPac(w)
		return
	}

	var proxies []string
	for i := rl.port; i < rl.port+rl.numPorts; i++ {
		proxies = append(proxies, fmt.Sprintf("%s:%d", rl.address, i))
	}
	err = tpl.Execute(w, map[string]interface{}{
		"Proxies":     marshalJSONString(proxies),
		"DirectHosts": marshalJSONString(rl.cfg.DirectHosts),
	})
	if err != nil {
		log.Println("ProxyPac:", err)
		renderErrorPac(w)
		return
	}
}
